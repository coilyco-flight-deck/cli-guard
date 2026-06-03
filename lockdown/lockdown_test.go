package lockdown_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/lockdown"
)

func TestLoadDefaults_ReturnsNonEmpty(t *testing.T) {
	d, err := lockdown.LoadDefaults()
	if err != nil {
		t.Fatalf("LoadDefaults: %v", err)
	}
	if len(d.Allow) == 0 {
		t.Error("allow list is empty")
	}
	if len(d.Deny) == 0 {
		t.Error("deny list is empty")
	}
}

func TestLoadDefaults_AllowsConsumerBash(t *testing.T) {
	d, _ := lockdown.LoadDefaults()
	if !contains(d.Allow, "Bash(coily:*)") {
		t.Errorf("allow list missing Bash(coily:*). Got: %v", d.Allow)
	}
}

// TestLoadDefaults_AllowsNonShellEvaluators pins coilysiren/cli-guard#39:
// jq and yq are pure non-shell evaluators (same safety class as grep/rg)
func TestLoadDefaults_AllowsNonShellEvaluators(t *testing.T) {
	d, _ := lockdown.LoadDefaults()
	for _, want := range []string{"Bash(jq:*)", "Bash(yq:*)"} {
		if !contains(d.Allow, want) {
			t.Errorf("allow list missing %s. Got: %v", want, d.Allow)
		}
	}
}

// TestLoadDefaults_DeniesWrappedBinaries pins cli-guard#13, #14: the deny
// list maps 1:1 to the binaries the consumer wraps.
func TestLoadDefaults_DeniesWrappedBinaries(t *testing.T) {
	d, _ := lockdown.LoadDefaults()
	mustDeny := []string{
		"Bash(aws:*)", "Bash(kubectl:*)", "Bash(gh:*)",
		"Bash(uv:*)", "Bash(pip:*)", "Bash(cargo:*)", "Bash(npm:*)",
		"Bash(docker:*)", "Bash(tailscale:*)", "Bash(ssh:*)",
	}
	for _, rule := range mustDeny {
		if !contains(d.Deny, rule) {
			t.Errorf("deny list missing required rule %q", rule)
		}
	}
}

func TestBuildPlan_OmitsDeniedMcpServersKey(t *testing.T) {
	// MCP-server gating is deliberately not lockdown's job. Output JSON must
	// not carry a deniedMcpServers key.
	d, _ := lockdown.LoadDefaults()
	target := filepath.Join(t.TempDir(), ".claude", "settings.json")
	plan, err := lockdown.BuildPlan(target, d, testDriver())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal(plan.After, &after); err != nil {
		t.Fatalf("unmarshal After: %v", err)
	}
	if _, ok := after["deniedMcpServers"]; ok {
		t.Errorf("After contains deniedMcpServers; want it absent. After=%s", string(plan.After))
	}
}

func TestBuildPlan_AfterEndsWithNewline(t *testing.T) {
	// Settings JSON must end in a trailing newline so it does not fight
	// end-of-file-fixer and re-dirty on every re-baseline.
	d, _ := lockdown.LoadDefaults()
	target := filepath.Join(t.TempDir(), ".claude", "settings.json")
	plan, err := lockdown.BuildPlan(target, d, testDriver())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if n := len(plan.After); n == 0 || plan.After[n-1] != '\n' {
		t.Errorf("After must end with a trailing newline; got %q", string(plan.After))
	}
}

func TestBuildPlan_NewFileGetsFullDefaults(t *testing.T) {
	d, _ := lockdown.LoadDefaults()
	target := filepath.Join(t.TempDir(), ".claude", "settings.json")
	plan, err := lockdown.BuildPlan(target, d, testDriver())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if plan.Existed {
		t.Error("plan.Existed is true for a new target")
	}
	var after map[string]any
	if err := json.Unmarshal(plan.After, &after); err != nil {
		t.Fatalf("unmarshal After: %v", err)
	}
	perms := after["permissions"].(map[string]any)
	allow := toStringSlice(perms["allow"])
	if !contains(allow, "Bash(coily:*)") {
		t.Errorf("allow missing Bash(coily:*)")
	}
}

func TestBuildPlan_ExistingFilePreservesNonManagedTopLevelKeys(t *testing.T) {
	// Regression for #103. Permissions are still replaced wholesale with
	// the canonical defaults, but every other top-level key from the
	d, _ := lockdown.LoadDefaults()
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	existing := map[string]any{
		"permissions": map[string]any{
			"allow": []any{"Bash(custom-tool:*)"},
			"deny":  []any{"Bash(npm run dangerous:*)"},
		},
		"enabledPlugins":           map[string]any{"foo@bar": true},
		"extraKnownMarketplaces":   map[string]any{"x": map[string]any{"source": "y"}},
		"model":                    "claude-opus-4-7",
		"effortLevel":              "high",
		"viewMode":                 "compact",
		"skipAutoPermissionPrompt": true,
	}
	raw, _ := json.Marshal(existing)
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := lockdown.BuildPlan(target, d, testDriver())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !plan.Existed {
		t.Error("plan.Existed is false for an existing target")
	}
	var after map[string]any
	if err := json.Unmarshal(plan.After, &after); err != nil {
		t.Fatalf("unmarshal After: %v", err)
	}

	// Permissions still get the canonical-replacement contract.
	allow := toStringSlice(after["permissions"].(map[string]any)["allow"])
	if contains(allow, "Bash(custom-tool:*)") {
		t.Error("custom allow entry leaked into After (permissions should be canonical-replaced)")
	}
	if !contains(allow, "Bash(coily:*)") {
		t.Error("default allow entry is missing")
	}

	// Every other top-level key from the existing file must carry through.
	for _, key := range []string{
		"enabledPlugins",
		"extraKnownMarketplaces",
		"model",
		"effortLevel",
		"viewMode",
		"skipAutoPermissionPrompt",
	} {
		if _, ok := after[key]; !ok {
			t.Errorf("non-managed top-level key %q was clobbered (regression for #103)", key)
		}
	}
	if got, _ := after["model"].(string); got != "claude-opus-4-7" {
		t.Errorf("model value mutated: got %q", got)
	}
}

func TestBuildPlan_ExistingFileWithBadJSONErrors(t *testing.T) {
	// Post-#103 contract: BuildPlan parses the existing file so it can
	// merge non-managed top-level keys back in. An unparseable existing
	d, _ := lockdown.LoadDefaults()
	target := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(target, []byte("this is not json"), 0o600)
	if _, err := lockdown.BuildPlan(target, d, testDriver()); err == nil {
		t.Error("BuildPlan accepted opaque existing file; want error")
	}
}

func TestBuildPlan_ExistingFilePreservesPostToolUseHookEvent(t *testing.T) {
	// Regression for #103. PostToolUse and any other hook event must be
	// left untouched - lockdown's contract is only over PreToolUse Bash.
	d, _ := lockdown.LoadDefaults()
	target := filepath.Join(t.TempDir(), "settings.json")
	existing := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{
					"matcher": "Edit",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/local/bin/skill-rebuild"},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(existing)
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := lockdown.BuildPlan(target, d, testDriver())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal(plan.After, &after); err != nil {
		t.Fatalf("unmarshal After: %v", err)
	}
	hooks, _ := after["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Error("PostToolUse hook event was clobbered")
	}
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) == 0 {
		t.Fatal("PreToolUse Bash hook was not installed")
	}
}

func TestBuildPlan_ExistingFilePreservesNonBashPreToolUseMatchers(t *testing.T) {
	// Within PreToolUse, only the Bash matcher entry is lockdown's
	// surface. Other matchers (e.g. Edit, MultiEdit, Write) must carry
	d, _ := lockdown.LoadDefaults()
	target := filepath.Join(t.TempDir(), "settings.json")
	existing := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Edit",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/local/bin/edit-guard"},
					},
				},
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/some/old/path"},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(existing)
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := lockdown.BuildPlan(target, d, testDriver())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal(plan.After, &after); err != nil {
		t.Fatalf("unmarshal After: %v", err)
	}
	pre := after["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse length = %d, want 2 (Edit kept, Bash swapped in place)", len(pre))
	}
	if pre[0].(map[string]any)["matcher"] != "Edit" {
		t.Errorf("Edit matcher was reordered or replaced; got %v", pre[0])
	}
	bashEntry := pre[1].(map[string]any)
	if bashEntry["matcher"] != "Bash" {
		t.Errorf("Bash matcher slot has wrong matcher; got %v", bashEntry)
	}
	bashHooks, _ := bashEntry["hooks"].([]any)
	if len(bashHooks) != 1 {
		t.Fatalf("Bash hooks length = %d, want 1", len(bashHooks))
	}
	if cmd, _ := bashHooks[0].(map[string]any)["command"].(string); cmd == "/some/old/path" {
		t.Error("old Bash hook command was not replaced with canonical path")
	}
}

func TestBuildPlan_ExistingFileAppendsBashWhenMissing(t *testing.T) {
	// PreToolUse exists with non-Bash matchers but no Bash matcher: the
	// canonical Bash entry must be appended (not silently dropped).
	d, _ := lockdown.LoadDefaults()
	target := filepath.Join(t.TempDir(), "settings.json")
	existing := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Edit",
					"hooks": []any{
						map[string]any{"type": "command", "command": "/usr/local/bin/edit-guard"},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(existing)
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := lockdown.BuildPlan(target, d, testDriver())
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	var after map[string]any
	_ = json.Unmarshal(plan.After, &after)
	pre := after["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse length = %d, want 2 (Edit kept, Bash appended)", len(pre))
	}
	matchers := []string{}
	for _, e := range pre {
		matchers = append(matchers, e.(map[string]any)["matcher"].(string))
	}
	if !contains(matchers, "Bash") {
		t.Errorf("canonical Bash matcher was not appended; got %v", matchers)
	}
}

func TestWrite_WritesWithTightPerms(t *testing.T) {
	d, _ := lockdown.LoadDefaults()
	target := filepath.Join(t.TempDir(), ".claude", "settings.json")
	plan, _ := lockdown.BuildPlan(target, d, testDriver())
	if err := lockdown.Write(plan); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 0600", perm)
	}
}

func TestRenderHookScript_PassesShellSyntaxCheck(t *testing.T) {
	d, _ := lockdown.LoadDefaults()
	body, err := testDriver().RenderHookScript(d, testDriver())
	if err != nil {
		t.Fatalf("RenderHookScript: %v", err)
	}
	if !strings.Contains(body, "#!/bin/sh") {
		t.Error("hook script missing /bin/sh shebang")
	}
	// Must mention at least one well-known deny prefix.
	for _, want := range []string{"aws", "kubectl", "docker", "ssh"} {
		if !strings.Contains(body, want) {
			t.Errorf("hook script missing deny prefix %q", want)
		}
	}
}

func TestRenderHookScript_NamesConsumerWrapperOnDeny(t *testing.T) {
	d, _ := lockdown.LoadDefaults()
	body, err := testDriver().RenderHookScript(d, testDriver())
	if err != nil {
		t.Fatalf("RenderHookScript: %v", err)
	}
	// Issue #61: deny-rule message must name `app ops <bin>` as the
	// recovery path for the wrapped binaries the agent reaches for most.
	for prefix, recovery := range map[string]string{
		"gh":        "app ops gh",
		"aws":       "app ops aws",
		"kubectl":   "app ops kubectl",
		"docker":    "app docker",
		"tailscale": "app tailscale",
		"npm":       "app pkg npm",
		"uv":        "app pkg uv",
		"pip":       "app pkg pip",
		"cargo":     "app pkg cargo",
		"brew":      "app brew",
	} {
		want := "blocked by deny rule: " + prefix + ". Recovery: use `" + recovery
		if !strings.Contains(body, want) {
			t.Errorf("hook script for %q missing recovery hint %q", prefix, want)
		}
	}
}

func TestWriteHook_Executable(t *testing.T) {
	d, _ := lockdown.LoadDefaults()
	target := filepath.Join(t.TempDir(), ".claude", "settings.json")
	plan, _ := lockdown.BuildPlan(target, d, testDriver())
	if err := lockdown.Write(plan); err != nil {
		t.Fatalf("Write: %v", err)
	}
	hookPath, _, err := lockdown.WriteHook(plan.TargetPath, d, testDriver())
	if err != nil {
		t.Fatalf("WriteHook: %v", err)
	}
	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("stat hook: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("hook perm = %o, want 0755", perm)
	}
}

func TestWriteHook_BlocksDeniedCommand(t *testing.T) {
	// End-to-end: render the hook, write it, invoke it with a synthetic
	// PreToolUse JSON for a denied command, expect exit 2 + stderr message.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	d, _ := lockdown.LoadDefaults()
	target := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if err := lockdown.Write(must(lockdown.BuildPlan(target, d, testDriver()))); err != nil {
		t.Fatalf("Write: %v", err)
	}
	hookPath, _, err := lockdown.WriteHook(target, d, testDriver())
	if err != nil {
		t.Fatalf("WriteHook: %v", err)
	}

	cases := []struct {
		name   string
		stdin  string
		wantRC int
	}{
		{"aws s3 cp denied", `{"tool_input":{"command":"aws s3 cp foo s3://b/x"}}`, 2},
		{"aws ssm get-parameter denied", `{"tool_input":{"command":"aws ssm get-parameter --name /foo"}}`, 2},
		{"kubectl apply denied", `{"tool_input":{"command":"kubectl apply -f x.yaml"}}`, 2},
		{"piped aws s3 cp denied", `{"tool_input":{"command":"echo hi | aws s3 cp - s3://b/x"}}`, 2},
		{"env-prefixed aws s3 cp denied", `{"tool_input":{"command":"env AWS_PROFILE=x aws s3 cp foo s3://b/x"}}`, 2},
		{"gh pr merge denied", `{"tool_input":{"command":"gh pr merge 123"}}`, 2},
		{"gh api denied", `{"tool_input":{"command":"gh api repos/foo/bar"}}`, 2},
		// Inverted reads: bare aws/kubectl/gh now route through the consumer.
		{"aws s3 ls denied (route via the consumer)", `{"tool_input":{"command":"aws s3 ls"}}`, 2},
		{"aws sts get-caller-identity denied", `{"tool_input":{"command":"aws sts get-caller-identity"}}`, 2},
		{"kubectl get denied (route via the consumer)", `{"tool_input":{"command":"kubectl get pods"}}`, 2},
		{"gh pr view denied (route via the consumer)", `{"tool_input":{"command":"gh pr view 123"}}`, 2},
		{"ls allowed", `{"tool_input":{"command":"ls -la"}}`, 0},
		{"empty command allowed", `{"tool_input":{"command":""}}`, 0},
		// Consumer binary check: paths outside homebrew rejected, brew paths allowed.
		{"~/go/bin/app denied", `{"tool_input":{"command":"/Users/kai/go/bin/app systemctl"}}`, 2},
		{"/tmp/app denied", `{"tool_input":{"command":"/tmp/app ops kubectl get pods"}}`, 2},
		{"./bin/app denied", `{"tool_input":{"command":"./bin/app lockdown --check"}}`, 2},
		{"/opt/homebrew/bin/app allowed", `{"tool_input":{"command":"/opt/homebrew/bin/app systemctl"}}`, 0},
		{"/usr/local/bin/app allowed", `{"tool_input":{"command":"/usr/local/bin/app kubectl"}}`, 0},
		{"linuxbrew app allowed", `{"tool_input":{"command":"/home/linuxbrew/.linuxbrew/bin/app systemctl"}}`, 0},
		{"app denied via piped second segment", `{"tool_input":{"command":"echo go | /tmp/app systemctl"}}`, 2},
		// Gap 1: a denied wrapped binary laundered behind an allowed leading
		// token. Every segment of the pipeline must be evaluated.
		{"aws laundered behind head", `{"tool_input":{"command":"head -1 file | aws s3 cp - s3://x/y"}}`, 2},
		{"kubectl laundered behind cat in && chain", `{"tool_input":{"command":"cat x && kubectl get pods"}}`, 2},
		// Gap 2: executing a shebang script from a writable scratch dir.
		{"/tmp shebang script denied", `{"tool_input":{"command":"/tmp/script.py"}}`, 2},
		{"/tmp script with args denied", `{"tool_input":{"command":"/tmp/build/helper arg1"}}`, 2},
		{"/var/tmp script denied", `{"tool_input":{"command":"/var/tmp/x"}}`, 2},
		{"/dev/shm script denied", `{"tool_input":{"command":"/dev/shm/payload"}}`, 2},
		{"/tmp script laundered behind cat", `{"tool_input":{"command":"cat data | /tmp/script"}}`, 2},
		// Writing to /tmp stays allowed: only executing from it is denied.
		{"cat from /tmp allowed", `{"tool_input":{"command":"cat /tmp/notes.txt"}}`, 0},
		{"redirect into /tmp allowed", `{"tool_input":{"command":"head -1 /tmp/in > /tmp/out"}}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("sh", hookPath)
			cmd.Stdin = strings.NewReader(tc.stdin)
			err := cmd.Run()
			rc := 0
			if err != nil {
				var ee *exec.ExitError
				if errors.As(err, &ee) {
					rc = ee.ExitCode()
				} else {
					t.Fatalf("run hook: %v", err)
				}
			}
			if rc != tc.wantRC {
				t.Errorf("exit code = %d, want %d", rc, tc.wantRC)
			}
		})
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestMergeDenyInto_CreatesFileWithCanonicalDeny(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude", "settings.local.json")
	d, _ := lockdown.LoadDefaults()

	mutated, err := lockdown.MergeDenyInto(target, d)
	if err != nil {
		t.Fatalf("MergeDenyInto: %v", err)
	}
	if !mutated {
		t.Errorf("expected mutated=true on fresh create")
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	perms, _ := got["permissions"].(map[string]any)
	if perms == nil {
		t.Fatalf("permissions key missing: %s", string(raw))
	}
	if !contains(toStringSlice(perms["deny"]), "Bash(gh:*)") {
		t.Errorf("deny list missing canonical Bash(gh:*); got %v", perms["deny"])
	}
	if perms["allow"] != nil {
		t.Errorf("allow should be absent on fresh create; got %v", perms["allow"])
	}
}

func TestMergeDenyInto_PreservesAllowAndExtraKeys(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := []byte(`{
  "permissions": {
    "allow": ["Bash(gh issue *)", "Bash(jq:*)"],
    "deny": ["Bash(rm -rf:*)"]
  },
  "env": {"FOO": "bar"}
}`)
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	d, _ := lockdown.LoadDefaults()

	mutated, err := lockdown.MergeDenyInto(target, d)
	if err != nil {
		t.Fatalf("MergeDenyInto: %v", err)
	}
	if !mutated {
		t.Errorf("expected mutated=true when canonical denies absent")
	}

	raw, _ := os.ReadFile(target)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	perms, _ := got["permissions"].(map[string]any)
	if perms == nil {
		t.Fatalf("permissions missing: %s", string(raw))
	}

	allow := toStringSlice(perms["allow"])
	if contains(allow, "Bash(gh issue *)") {
		t.Errorf("shadowed allow Bash(gh issue *) not pruned; got %v", allow)
	}
	if !contains(allow, "Bash(jq:*)") {
		t.Errorf("non-shadowed allow Bash(jq:*) dropped; got %v", allow)
	}

	deny := toStringSlice(perms["deny"])
	if !contains(deny, "Bash(gh:*)") {
		t.Errorf("canonical Bash(gh:*) not merged into deny; got %v", deny)
	}
	if !contains(deny, "Bash(rm -rf:*)") {
		t.Errorf("pre-existing user deny entry dropped; got %v", deny)
	}

	env, _ := got["env"].(map[string]any)
	if env == nil || env["FOO"] != "bar" {
		t.Errorf("top-level env key not preserved; got %v", got["env"])
	}
}

func TestMergeDenyInto_NoOpWhenAlreadyCovered(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	d, _ := lockdown.LoadDefaults()

	if _, err := lockdown.MergeDenyInto(target, d); err != nil {
		t.Fatalf("first MergeDenyInto: %v", err)
	}

	mutated, err := lockdown.MergeDenyInto(target, d)
	if err != nil {
		t.Fatalf("second MergeDenyInto: %v", err)
	}
	if mutated {
		t.Errorf("expected mutated=false on second call (idempotent)")
	}
}

// TestMergeDenyInto_PrunesShadowedAllows pins cli-guard#26.
func TestMergeDenyInto_PrunesShadowedAllows(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := []byte(`{"permissions":{"allow":[
"Bash(gh issue *)",
"Bash(npm install foo)",
"Bash(kubectl get pods)",
"Bash(docker:*)",
"Bash(jq:*)",
"Bash(grep -E foo)",
"Bash(app git :*)",
"Read(/Users/kai/.claude/**)"
]}}`)
	if err := os.WriteFile(target, seed, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	d, _ := lockdown.LoadDefaults()
	if _, err := lockdown.MergeDenyInto(target, d); err != nil {
		t.Fatalf("MergeDenyInto: %v", err)
	}
	raw, _ := os.ReadFile(target)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)
	perms, _ := got["permissions"].(map[string]any)
	allow := toStringSlice(perms["allow"])

	shouldPrune := []string{
		"Bash(gh issue *)",
		"Bash(npm install foo)",
		"Bash(kubectl get pods)",
		"Bash(docker:*)",
	}
	for _, p := range shouldPrune {
		if contains(allow, p) {
			t.Errorf("expected %q pruned (shadowed by canonical deny); got %v", p, allow)
		}
	}

	shouldKeep := []string{
		"Bash(jq:*)",
		"Bash(grep -E foo)",
		"Bash(app git :*)",
		"Read(/Users/kai/.claude/**)",
	}
	for _, k := range shouldKeep {
		if !contains(allow, k) {
			t.Errorf("expected %q preserved (not shadowed); got %v", k, allow)
		}
	}
}

func TestTargetPath_LocalToggle(t *testing.T) {
	if got := lockdown.TargetPath("/tmp/a", false); !strings.HasSuffix(got, "/settings.json") {
		t.Errorf("TargetPath(false) = %q", got)
	}
	if got := lockdown.TargetPath("/tmp/a", true); !strings.HasSuffix(got, "/settings.local.json") {
		t.Errorf("TargetPath(true) = %q", got)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func toStringSlice(v any) []string {
	out := []string{}
	if arr, ok := v.([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
