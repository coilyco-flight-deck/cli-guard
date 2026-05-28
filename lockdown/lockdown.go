// Package lockdown writes a per-repo Claude Code settings file that
// enforces an allowlist-inversion for the wrapper binary supplied by a
package lockdown

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// validateShellSyntax pipes the script through `sh -n`, which parses
// without executing. Used to guard hook generation: a malformed script
func validateShellSyntax(body string) error {
	cmd := exec.Command("sh", "-n")
	cmd.Stdin = strings.NewReader(body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

//go:embed defaults.yaml
var defaultsYAML []byte

// Defaults is the parsed allow / deny list pair that BuildPlan writes
// into the target settings file. Loaded from defaults.yaml via
type Defaults struct {
	Allow []string `yaml:"allow" json:"-"`
	Deny  []string `yaml:"deny" json:"-"`
}

// Settings is the subset of Claude Code settings we manipulate
// directly.
type Settings struct {
	Permissions Permissions `json:"permissions"`
}

// Permissions is the on-disk shape of the Claude Code settings
// permissions block (allow / deny rule strings).
type Permissions struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// LoadDefaults parses the embedded canonical allow/deny lists.
func LoadDefaults() (*Defaults, error) {
	var d Defaults
	if err := yaml.Unmarshal(defaultsYAML, &d); err != nil {
		return nil, fmt.Errorf("lockdown: parse embedded defaults: %w", err)
	}
	return &d, nil
}

// Plan describes what lockdown would (or did) write. Rendered as JSON
// for the caller to display or persist.
type Plan struct {
	TargetPath string          // the .claude/settings*.json path
	Existed    bool            // did TargetPath exist before?
	Before     json.RawMessage // original file contents, if any
	After      json.RawMessage // file contents that would be (or were) written
}

// BuildPlan computes what the target settings file should look like
// after applying the defaults. Does not touch disk. Routes through the
func BuildPlan(targetPath string, d *Defaults, drv *Driver) (*Plan, error) {
	if err := drv.Validate(); err != nil {
		return nil, err
	}
	plan := &Plan{TargetPath: targetPath}

	raw, err := os.ReadFile(targetPath)
	switch {
	case err == nil:
		plan.Existed = true
		plan.Before = append(json.RawMessage(nil), raw...)
	case os.IsNotExist(err):
		// Nothing to load. Fresh bootstrap.
	default:
		return nil, fmt.Errorf("lockdown: read %s: %w", targetPath, err)
	}

	after, err := drv.BuildSettings(raw, d, drv)
	if err != nil {
		return nil, err
	}
	plan.After = after
	return plan, nil
}

// claudeCodeBuildSettings is the default ClaudeCode driver's
// BuildSettings implementation. Takes the existing file bytes (nil for
func claudeCodeBuildSettings(raw []byte, d *Defaults, drv *Driver) ([]byte, error) {
	canonicalPerms := map[string]any{
		"allow": uniqueSorted(append([]string(nil), d.Allow...)),
		"deny":  uniqueSorted(append([]string(nil), d.Deny...)),
	}
	canonicalBashHook := map[string]any{
		"matcher": "Bash",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": drv.HookSettingsPath(),
			},
		},
	}

	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("lockdown: parse existing settings: %w", err)
		}
	}
	if out == nil {
		out = map[string]any{}
	}
	out["permissions"] = canonicalPerms
	out["hooks"] = mergeBashHook(out["hooks"], canonicalBashHook)

	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("lockdown: marshal: %w", err)
	}
	encoded = append(encoded, '\n')
	return encoded, nil
}

// mergeBashHook returns a hooks-shaped map with the canonical Bash
// matcher entry installed under PreToolUse. Other PreToolUse matchers
func mergeBashHook(existing any, canonicalBash map[string]any) map[string]any {
	out := map[string]any{}
	if m, ok := existing.(map[string]any); ok {
		for k, v := range m {
			out[k] = v
		}
	}
	pre, ok := out["PreToolUse"].([]any)
	if !ok {
		out["PreToolUse"] = []any{canonicalBash}
		return out
	}
	for i, entry := range pre {
		em, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if matcher, _ := em["matcher"].(string); matcher == "Bash" {
			pre[i] = canonicalBash
			out["PreToolUse"] = pre
			return out
		}
	}
	out["PreToolUse"] = append(pre, canonicalBash)
	return out
}

// Write applies the plan to disk. Caller should have shown the plan
// first and confirmed.
func Write(plan *Plan) error {
	if err := os.MkdirAll(filepath.Dir(plan.TargetPath), 0o750); err != nil {
		return fmt.Errorf("lockdown: mkdir: %w", err)
	}
	return os.WriteFile(plan.TargetPath, plan.After, 0o600)
}

// HookPath returns the absolute path of the generated PreToolUse hook
// script. It sits next to settings.json under the driver's settings
func HookPath(settingsPath string, drv *Driver) string {
	return filepath.Join(filepath.Dir(settingsPath), drv.HookFilename)
}

// WriteHook renders and writes the PreToolUse hook script with 0755
// perms. Validates the generated script with `sh -n` before writing -
func WriteHook(settingsPath string, d *Defaults, drv *Driver) (string, bool, error) {
	if err := drv.Validate(); err != nil {
		return "", false, err
	}
	body, err := drv.RenderHookScript(d, drv)
	if err != nil {
		return "", false, err
	}
	if err := validateShellSyntax(body); err != nil {
		return "", false, fmt.Errorf("lockdown: generated hook failed sh -n: %w", err)
	}
	hookPath := HookPath(settingsPath, drv)
	existed := false
	if _, statErr := os.Stat(hookPath); statErr == nil {
		existed = true
	}
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o750); err != nil {
		return "", false, fmt.Errorf("lockdown: mkdir hook: %w", err)
	}
	if err := os.WriteFile(hookPath, []byte(body), 0o755); err != nil {
		return "", false, fmt.Errorf("lockdown: write hook: %w", err)
	}
	return hookPath, existed, nil
}

// MergeDenyInto reasserts canonical denies + prunes shadowed allows. cli-guard#26.
func MergeDenyInto(targetPath string, d *Defaults) (bool, error) {
	root, existed, err := readSettingsJSON(targetPath)
	if err != nil {
		return false, err
	}

	perms, _ := root["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}

	existingDeny := toStringSliceAny(perms["deny"])
	merged := uniqueSorted(append(append([]string(nil), existingDeny...), d.Deny...))
	if merged == nil {
		merged = []string{}
	}

	existingAllow := toStringSliceAny(perms["allow"])
	prunedAllow := pruneShadowedAllows(existingAllow, merged)

	denyChanged := !stringSliceEqual(existingDeny, merged)
	allowChanged := !stringSliceEqual(existingAllow, prunedAllow)
	if existed && !denyChanged && !allowChanged {
		return false, nil
	}

	perms["deny"] = merged
	if existingAllow != nil || allowChanged {
		perms["allow"] = prunedAllow
	}
	root["permissions"] = perms

	if err := writeSettingsJSON(targetPath, root); err != nil {
		return false, err
	}
	return true, nil
}

// readSettingsJSON parses a settings.json file, returning (root, existed, err).
func readSettingsJSON(targetPath string) (map[string]any, bool, error) {
	root := map[string]any{}
	raw, err := os.ReadFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return root, false, nil
		}
		return nil, false, fmt.Errorf("lockdown: read %s: %w", targetPath, err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, true, fmt.Errorf("lockdown: parse %s: %w", targetPath, err)
		}
	}
	return root, true, nil
}

// writeSettingsJSON serializes root to targetPath with canonical formatting.
func writeSettingsJSON(targetPath string, root map[string]any) error {
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("lockdown: marshal %s: %w", targetPath, err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return fmt.Errorf("lockdown: mkdir %s: %w", filepath.Dir(targetPath), err)
	}
	if err := os.WriteFile(targetPath, encoded, 0o600); err != nil {
		return fmt.Errorf("lockdown: write %s: %w", targetPath, err)
	}
	return nil
}

// pruneShadowedAllows drops dead allow entries (shadowed by a bare-verb
// canonical deny under Claude Code's deny-before-allow). See cli-guard#26.
func pruneShadowedAllows(allows, denies []string) []string {
	shadow := shadowVerbs(denies)
	if len(shadow) == 0 {
		return allows
	}
	out := make([]string, 0, len(allows))
	for _, a := range allows {
		if !isShadowedBashAllow(a, shadow) {
			out = append(out, a)
		}
	}
	return out
}

// shadowVerbs extracts bare-verb prefixes from `Bash(<verb>:*)` denies.
// Compound denies like `Bash(git push --force:*)` are excluded.
func shadowVerbs(denies []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, d := range denies {
		if !strings.HasPrefix(d, "Bash(") || !strings.HasSuffix(d, ":*)") {
			continue
		}
		inner := d[len("Bash(") : len(d)-len(":*)")]
		if inner == "" || strings.ContainsAny(inner, " :()") {
			continue
		}
		out[inner] = struct{}{}
	}
	return out
}

// isShadowedBashAllow returns true if a Bash allow's leading verb is in shadow.
func isShadowedBashAllow(rule string, shadow map[string]struct{}) bool {
	if !strings.HasPrefix(rule, "Bash(") || !strings.HasSuffix(rule, ")") {
		return false
	}
	inner := rule[len("Bash(") : len(rule)-1]
	end := len(inner)
	for i := 0; i < len(inner); i++ {
		if inner[i] == ' ' || inner[i] == ':' {
			end = i
			break
		}
	}
	verb := inner[:end]
	_, ok := shadow[verb]
	return ok
}

func toStringSliceAny(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// TargetPath returns the settings file path under dir. If local is
// true, uses settings.local.json. Otherwise settings.json.
func TargetPath(dir string, local bool) string {
	name := "settings.json"
	if local {
		name = "settings.local.json"
	}
	return filepath.Join(dir, ".claude", name)
}

// uniqueSorted dedupes and sorts a string slice. Returns nil for empty
// input so json.Marshal omits the field when used with omitempty.
func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	slices.Sort(in)
	return slices.Compact(in)
}
