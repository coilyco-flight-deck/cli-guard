package hook

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// lookFunc returns a LookPath that resolves the given map; unknown
// keys return exec.ErrNotFound (which the engine treats as
// pass-through, mirroring real "binary not installed" cases).
func lookFunc(m map[string]string) LookPath {
	return func(name string) (string, error) {
		if v, ok := m[name]; ok {
			return v, nil
		}
		return "", exec.ErrNotFound
	}
}

func TestPreToolUse_PassThroughOnNonBashOrEmpty(t *testing.T) {
	routes := []Route{{Token: "gh", Hint: "use guard ops gh"}}
	cases := []struct {
		name    string
		payload Payload
	}{
		{"non-bash tool", Payload{ToolName: "Edit", ToolInput: map[string]any{"command": "gh issue view"}}},
		{"empty command", Payload{ToolName: "Bash", ToolInput: map[string]any{"command": ""}}},
		{"missing command key", Payload{ToolName: "Bash", ToolInput: map[string]any{}}},
		{"all whitespace", Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "   "}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := PreToolUse(tc.payload, "test", nil, routes, lookFunc(nil))
			if d.Block {
				t.Errorf("expected pass-through, got Block: %q", d.Message)
			}
		})
	}
}

func TestPreToolUse_RouteHintFiresOnBareToken(t *testing.T) {
	routes := []Route{
		{Token: "gh", Hint: "use guard ops gh"},
		{Token: "brew", Hint: "use guard pkg brew"},
	}
	payload := Payload{
		ToolName:  "Bash",
		ToolInput: map[string]any{"command": "gh issue view 42"},
	}
	d := PreToolUse(payload, "test", nil, routes, lookFunc(nil))
	if !d.Block {
		t.Fatalf("expected block, got pass-through")
	}
	if !strings.Contains(d.Message, "test hook: blocked bare `gh`") {
		t.Errorf("missing source prefix: %q", d.Message)
	}
	if !strings.Contains(d.Message, "use guard ops gh") {
		t.Errorf("missing hint: %q", d.Message)
	}
}

func TestPreToolUse_ExtraSuffixAppendsWhenPresent(t *testing.T) {
	routes := []Route{{
		Token: "gh",
		Hint:  "use guard ops gh",
		Extra: func(seg string) string {
			if strings.Contains(seg, "issue view") {
				return " (use the REST API instead of GraphQL)"
			}
			return ""
		},
	}}
	d := PreToolUse(
		Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "gh issue view 1"}},
		"test", nil, routes, lookFunc(nil))
	if !strings.Contains(d.Message, "use the REST API") {
		t.Errorf("Extra suffix missing: %q", d.Message)
	}
	d2 := PreToolUse(
		Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "gh pr list"}},
		"test", nil, routes, lookFunc(nil))
	if strings.Contains(d2.Message, "use the REST API") {
		t.Errorf("Extra suffix should not fire for non-matching segment: %q", d2.Message)
	}
}

func TestPreToolUse_IntegrityRuleBlocksOffPathBinary(t *testing.T) {
	rules := []IntegrityRule{{
		Binary:       "coily",
		AllowedPaths: []string{"/opt/homebrew/bin/coily", "/home/linuxbrew/.linuxbrew/bin/coily"},
	}}
	d := PreToolUse(
		Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "coily whoami"}},
		"test", rules, nil,
		lookFunc(map[string]string{"coily": "/tmp/evil/coily"}))
	if !d.Block {
		t.Fatalf("expected block on off-path coily, got pass-through")
	}
	if !strings.Contains(d.Message, "PATH-hijack") {
		t.Errorf("missing PATH-hijack message: %q", d.Message)
	}
}

func TestPreToolUse_IntegrityRulePassesWhenPathMatches(t *testing.T) {
	rules := []IntegrityRule{{
		Binary:       "coily",
		AllowedPaths: []string{"/opt/homebrew/bin/coily"},
	}}
	d := PreToolUse(
		Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "coily whoami"}},
		"test", rules, nil,
		lookFunc(map[string]string{"coily": "/opt/homebrew/bin/coily"}))
	if d.Block {
		t.Errorf("expected pass-through for canonical path, got block: %q", d.Message)
	}
}

func TestPreToolUse_StripsEnvAndSudoBeforeTokenMatch(t *testing.T) {
	routes := []Route{{Token: "gh", Hint: "use guard ops gh"}}
	cases := []string{
		"env FOO=bar gh issue view",
		"sudo gh issue view",
		"sudo env FOO=bar gh issue view",
		"  env FOO=bar BAR=baz   gh issue view",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			d := PreToolUse(
				Payload{ToolName: "Bash", ToolInput: map[string]any{"command": cmd}},
				"test", nil, routes, lookFunc(nil))
			if !d.Block {
				t.Errorf("expected route to fire after env/sudo strip: %q", cmd)
			}
		})
	}
}

func TestPreToolUse_SplitsOnShellBoundaries(t *testing.T) {
	routes := []Route{{Token: "gh", Hint: "use guard ops gh"}}
	cases := []string{
		"echo hi && gh issue view",
		"echo hi || gh issue view",
		"echo hi | gh issue view",
		"echo hi; gh issue view",
		"$(gh issue view)",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			d := PreToolUse(
				Payload{ToolName: "Bash", ToolInput: map[string]any{"command": cmd}},
				"test", nil, routes, lookFunc(nil))
			if !d.Block {
				t.Errorf("expected route to fire across boundary: %q", cmd)
			}
		})
	}
}

func TestCheckBinaryPath_EnoentPassesThrough(t *testing.T) {
	msg := CheckBinaryPath("nonexistent", []string{"/opt/homebrew/bin/nonexistent"},
		lookFunc(nil), "test")
	if msg != "" {
		t.Errorf("ENOENT should pass through, got %q", msg)
	}
}

func TestCheckBinaryPath_OtherErrorBlocks(t *testing.T) {
	lookup := func(_ string) (string, error) {
		return "", errors.New("permission denied")
	}
	msg := CheckBinaryPath("coily", []string{"/opt/homebrew/bin/coily"}, lookup, "test")
	if msg == "" || !strings.Contains(msg, "permission denied") {
		t.Errorf("non-ENOENT error should block with the underlying message, got %q", msg)
	}
}

func TestSplitSegments(t *testing.T) {
	got := SplitSegments("a && b | c ; d $(e)")
	want := []string{"a ", " b ", " c ", " d ", "e", ""}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStripEnvPrefix(t *testing.T) {
	cases := map[string]string{
		"gh issue view":                  "gh issue view",
		"env FOO=bar gh issue view":      "gh issue view",
		"sudo gh issue view":             "gh issue view",
		"sudo env FOO=bar gh issue view": "gh issue view",
		"env A=1 B=2 cmd arg":            "cmd arg",
		"env":                            "env", // no var=val pair, no peel
		"plain command":                  "plain command",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := StripEnvPrefix(in); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestLeadingToken(t *testing.T) {
	cases := map[string]string{
		"gh issue view": "gh",
		"":              "",
		"coily":         "coily",
		"coily\tops gh": "coily",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := LeadingToken(in); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}
