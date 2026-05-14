// Package lockdown writes a per-repo Claude Code settings file that
// enforces an allowlist-inversion for the wrapper binary supplied by a
// Driver. Defaults are embedded at build time. cli-guard ships one
// Driver out of the box (ClaudeCode); future drivers can plug into the
// same surface to gate other AI tool runtimes.
//
// The shape of the Claude Code output is:
//
//	{
//	  "permissions": { "allow": [...], "deny": [...] }
//	}
//
// MCP server allowlisting is intentionally out of scope. The Bash deny
// list gates shell-level blast radius (cluster mutations, secret reads,
// package installs). MCP-server gating is a different threat model -
// "is this MCP server trustworthy" - and the answer is per-user /
// per-machine, not per-repo. Baking it into a repo-scoped settings.json
// puts the decision in the wrong place. Drop it; let the user manage
// MCP allowlisting at the user-settings level.
//
// Behavior model:
//
//   - Bare `<host CLI> lockdown` prints the plan and exits. No write.
//   - `<host CLI> lockdown --apply` writes a fresh file only if
//     .claude/settings.json does not already exist.
//   - `<host CLI> lockdown --apply --replace` overwrites an existing
//     file wholesale.
//
// BuildPlan always returns the canonical defaults regardless of what is
// on disk. Any hand-edited allow/deny entries are dropped on --replace.
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
// would silently neutralize the Desktop deny gate.
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
// LoadDefaults. Caller-supplied Defaults are also accepted.
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
// driver's BuildSettings function so the canonical settings shape stays
// pluggable across hosts.
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
// a fresh bootstrap) and returns the bytes that should be written.
//
// Fresh-file path: the output contains only the canonical permissions
// and hooks keys.
//
// Existing-file path: preserves every top-level key from the existing
// file, replaces permissions wholesale with the canonical allow + deny
// lists, and under hooks.PreToolUse swaps in (or appends) the canonical
// Bash matcher entry while leaving any other PreToolUse matchers and
// any other hook events (PostToolUse, SessionStart, ...) untouched.
//
// An existing file that does not parse as JSON is a hard error.
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
	return encoded, nil
}

// mergeBashHook returns a hooks-shaped map with the canonical Bash
// matcher entry installed under PreToolUse. Other PreToolUse matchers
// are preserved in place; other top-level hook events (PostToolUse,
// SessionStart, etc.) carry through untouched.
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
// directory.
func HookPath(settingsPath string, drv *Driver) string {
	return filepath.Join(filepath.Dir(settingsPath), drv.HookFilename)
}

// WriteHook renders and writes the PreToolUse hook script with 0755
// perms. Validates the generated script with `sh -n` before writing -
// a syntax error would silently neutralize the deny gate on Desktop.
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

// MergeDenyInto reasserts the canonical deny list at an ancestor
// settings file. Returns (mutated, error) where mutated is true iff
// the file's effective content changed.
func MergeDenyInto(targetPath string, d *Defaults) (bool, error) {
	root := map[string]any{}
	existed := false
	if raw, err := os.ReadFile(targetPath); err == nil {
		existed = true
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &root); err != nil {
				return false, fmt.Errorf("lockdown: parse %s: %w", targetPath, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("lockdown: read %s: %w", targetPath, err)
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

	if existed && stringSliceEqual(existingDeny, merged) {
		return false, nil
	}

	perms["deny"] = merged
	root["permissions"] = perms

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, fmt.Errorf("lockdown: marshal %s: %w", targetPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return false, fmt.Errorf("lockdown: mkdir %s: %w", filepath.Dir(targetPath), err)
	}
	if err := os.WriteFile(targetPath, encoded, 0o600); err != nil {
		return false, fmt.Errorf("lockdown: write %s: %w", targetPath, err)
	}
	return true, nil
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
