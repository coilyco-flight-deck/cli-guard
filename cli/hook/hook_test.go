package hook

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// lookFunc returns a LookPath that resolves the given map; unknown
// keys return exec.ErrNotFound (which the engine treats as
func lookFunc(m map[string]string) LookPath {
	return func(name string) (string, error) {
		if v, ok := m[name]; ok {
			return v, nil
		}
		return "", exec.ErrNotFound
	}
}

func TestExfilExpansion(t *testing.T) {
	cases := []struct {
		name string
		seg  string
		want string
	}{
		{"echo literal", "echo hello world", ""},
		{"echo quoted literal", `echo "hello"`, ""},
		{"echo safe var bare", "echo $HOME", ""},
		{"echo safe var braced", "echo ${HOME}", ""},
		{"echo safe var quoted", `echo "user: $USER"`, ""},
		{"echo shell metavar", "echo $$ pid", ""},
		{"echo positional", "echo $1 $2", ""},
		{"echo question", "echo $?", ""},
		{"echo bare secret", "echo $SECRET", "echo"},
		{"echo braced secret", "echo ${AWS_SECRET_ACCESS_KEY}", "echo"},
		{"echo command sub", "echo $(cat /etc/passwd)", "echo"},
		{"echo backtick sub", "echo `whoami`", "echo"},
		{"echo mixed safe and unsafe", "echo $HOME $TOKEN", "echo"},
		{"printf literal", `printf "hello\n"`, ""},
		{"printf safe", `printf "%s\n" $HOME`, ""},
		{"printf secret", `printf "%s" $API_KEY`, "printf"},
		{"non-exfil command", "ls $SECRET", ""},
		{"echo no args", "echo", ""},
		{"echo trailing dollar", "echo abc$", "echo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := exfilExpansion(tc.seg)
			if got != tc.want {
				t.Errorf("exfilExpansion(%q) = %q, want %q", tc.seg, got, tc.want)
			}
		})
	}
}

func TestPreToolUse_BlocksEchoExfil(t *testing.T) {
	payload := Payload{
		ToolName:  "Bash",
		ToolInput: map[string]any{"command": "echo $AWS_SECRET_ACCESS_KEY"},
	}
	d := PreToolUse(payload, "test", nil, nil, lookFunc(nil), nil)
	if !d.Block {
		t.Fatalf("expected block, got pass-through")
	}
	if !strings.Contains(d.Message, "blocked `echo` with variable expansion") {
		t.Errorf("wrong message: %q", d.Message)
	}
}

func TestPreToolUse_AllowsEchoSafeVar(t *testing.T) {
	payload := Payload{
		ToolName:  "Bash",
		ToolInput: map[string]any{"command": "echo $HOME"},
	}
	d := PreToolUse(payload, "test", nil, nil, lookFunc(nil), nil)
	if d.Block {
		t.Errorf("expected pass-through for safe var, got Block: %q", d.Message)
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
			d := PreToolUse(tc.payload, "test", nil, routes, lookFunc(nil), nil)
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
	d := PreToolUse(payload, "test", nil, routes, lookFunc(nil), nil)
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
		"test", nil, routes, lookFunc(nil), nil)
	if !strings.Contains(d.Message, "use the REST API") {
		t.Errorf("Extra suffix missing: %q", d.Message)
	}
	d2 := PreToolUse(
		Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "gh pr list"}},
		"test", nil, routes, lookFunc(nil), nil)
	if strings.Contains(d2.Message, "use the REST API") {
		t.Errorf("Extra suffix should not fire for non-matching segment: %q", d2.Message)
	}
}

func TestPreToolUse_IntegrityRuleBlocksOffPathBinary(t *testing.T) {
	rules := []IntegrityRule{{
		Binary:       "app",
		AllowedPaths: []string{"/opt/homebrew/bin/app", "/home/linuxbrew/.linuxbrew/bin/app"},
	}}
	d := PreToolUse(
		Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "app whoami"}},
		"test", rules, nil,
		lookFunc(map[string]string{"app": "/tmp/evil/app"}), nil)
	if !d.Block {
		t.Fatalf("expected block on off-path app, got pass-through")
	}
	if !strings.Contains(d.Message, "PATH-hijack") {
		t.Errorf("missing PATH-hijack message: %q", d.Message)
	}
}

func TestPreToolUse_IntegrityRulePassesWhenPathMatches(t *testing.T) {
	rules := []IntegrityRule{{
		Binary:       "app",
		AllowedPaths: []string{"/opt/homebrew/bin/app"},
	}}
	d := PreToolUse(
		Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "app whoami"}},
		"test", rules, nil,
		lookFunc(map[string]string{"app": "/opt/homebrew/bin/app"}), nil)
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
				"test", nil, routes, lookFunc(nil), nil)
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
				"test", nil, routes, lookFunc(nil), nil)
			if !d.Block {
				t.Errorf("expected route to fire across boundary: %q", cmd)
			}
		})
	}
}

func TestPreToolUse_DeniesInterpreterEverySpelling(t *testing.T) {
	// No routes, no integrity rules: the interpreter deny is
	// engine-owned and must fire on its own.
	cases := []string{
		"python3 /tmp/script.py",
		`python3 -c "import os"`,
		"head -1 file | python3 -c 'print(1)'", // Gap 1: pipeline laundering
		"cat data.txt && python3 evil.py",      // && chain
		"echo hi ; ruby -e 'exec\"sh\"'",       // ; sequence
		"$(node -e 'process.exit(0)')",         // command substitution
		"/usr/bin/python3 script.py",           // absolute path
		"ls | /opt/homebrew/bin/python3",       // absolute path behind a pipe
		"perl -e 'system(\"sh\")'",
		"env FOO=bar python3 -c 'pass'", // env-prefixed
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			d := PreToolUse(
				Payload{ToolName: "Bash", ToolInput: map[string]any{"command": cmd}},
				"test", nil, nil, lookFunc(nil), nil)
			if !d.Block {
				t.Fatalf("expected interpreter deny, got pass-through")
			}
			if !strings.Contains(d.Message, "blocked interpreter") {
				t.Errorf("missing interpreter-deny message: %q", d.Message)
			}
		})
	}
}

func TestPreToolUse_DeniesScratchDirExecution(t *testing.T) {
	// Gap 2: an executable shebang script run directly from /tmp. The
	// guard sees only the path token, never the interpreter.
	cases := []struct {
		cmd string
		cwd string
	}{
		{"/tmp/script", ""},
		{"/tmp/build/helper.sh", ""},
		{"/var/tmp/x", ""},
		{"/dev/shm/payload", ""},
		{"/private/tmp/script", ""},
		{"cat foo | /tmp/script", ""},          // laundered behind a pipe
		{"./script", "/tmp/work"},              // relative, cwd in scratch
		{"../sibling/script", "/tmp/work/sub"}, // relative climbs, still scratch
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			d := PreToolUse(
				Payload{ToolName: "Bash", CWD: tc.cwd, ToolInput: map[string]any{"command": tc.cmd}},
				"test", nil, nil, lookFunc(nil), nil)
			if !d.Block {
				t.Fatalf("expected scratch-exec deny, got pass-through")
			}
			if !strings.Contains(d.Message, "writable scratch directory") {
				t.Errorf("missing scratch-exec message: %q", d.Message)
			}
		})
	}
}

func TestPreToolUse_AllowsWritingToScratch(t *testing.T) {
	// Writing scratch files to /tmp must stay unaffected: only
	// executing a file from there is denied.
	cases := []string{
		"cat /tmp/notes.txt",
		"head -1 /tmp/data.json",
		"ls /tmp",
		"echo hi > /tmp/out.txt",
		"grep foo /tmp/log",
		"/usr/bin/touch /tmp/marker", // a non-interpreter binary, scratch arg
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			d := PreToolUse(
				Payload{ToolName: "Bash", ToolInput: map[string]any{"command": cmd}},
				"test", nil, nil, lookFunc(nil), nil)
			if d.Block {
				t.Errorf("expected pass-through for scratch write, got block: %q", d.Message)
			}
		})
	}
}

func TestInterpreterName(t *testing.T) {
	cases := map[string]string{
		"python3":               "python3",
		"/usr/bin/python3":      "python3",
		"/tmp/copy/python3":     "python3",
		"node":                  "node",
		"gh":                    "",
		"":                      "",
		"/opt/homebrew/bin/app": "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := interpreterName(in); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestScratchExec(t *testing.T) {
	cases := []struct {
		token string
		cwd   string
		want  bool
	}{
		{"/tmp/script", "", true},
		{"/tmp", "", true},
		{"/var/tmp/x", "", true},
		{"/private/tmp/x", "", true},
		{"/usr/bin/python3", "", false},
		{"script", "/tmp", false}, // bare name resolves via PATH, not cwd
		{"./script", "/tmp/work", true},
		{"./script", "/home/kai", false},
		{"./script", "", false}, // relative with no cwd cannot classify
		{"", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.token+"|"+tc.cwd, func(t *testing.T) {
			if got := scratchExec(tc.token, tc.cwd); got != tc.want {
				t.Errorf("scratchExec(%q,%q) = %v, want %v", tc.token, tc.cwd, got, tc.want)
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
	msg := CheckBinaryPath("app", []string{"/opt/homebrew/bin/app"}, lookup, "test")
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
		"app":           "app",
		"app\tops gh":   "app",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := LeadingToken(in); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestSecurityClaim_ProtectedBinaryMatchesBasenameEverywhere(t *testing.T) {
	protected := []Protected{
		{Name: "gcloud", Wrappers: []string{"kap"}},
		{Name: "clusterctl", Hint: "use `kap cluster ...`."},
		{Name: "kubectl", Wrappers: []string{"kap"}},
	}
	cases := []struct {
		name    string
		command string
		block   bool
		substr  string
	}{
		{"bare basename", "gcloud projects list", true, "blocked protected binary `gcloud`"},
		{"usr-local path", "/usr/local/bin/gcloud config", true, "blocked protected binary `gcloud`"},
		{"homebrew path", "/opt/homebrew/bin/gcloud auth list", true, "blocked protected binary `gcloud`"},
		{"path-discovered copy", "env PATH=/tmp/toolbin:$PATH gcloud status", true, "blocked protected binary `gcloud`"},
		{"env prefix", "env CLOUDSDK_CONFIG=/x gcloud projects list", true, "gcloud"},
		{"sudo prefix", "sudo gcloud auth list", true, "gcloud"},
		{"piped behind allowed", "ls | gcloud feed", true, "gcloud"},
		{"kubectl basename target", "kubectl get pods", true, "blocked protected binary `kubectl`"},
		{"kubectl absolute path", "/usr/local/bin/kubectl get pods", true, "blocked protected binary `kubectl`"},
		{"wrappers in hint", "gcloud x", true, "route through an audited wrapper: kap"},
		{"explicit hint", "clusterctl get pods", true, "use `kap cluster ...`."},
		{"unprotected passes", "ls -la", false, ""},
		{"substring not matched", "gcloudfoo run", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := Payload{ToolName: "Bash", ToolInput: map[string]interface{}{"command": tc.command}}
			d := PreToolUse(payload, "test", nil, nil, lookFunc(nil), protected)
			if d.Block != tc.block {
				t.Fatalf("Block = %v, want %v (msg=%q)", d.Block, tc.block, d.Message)
			}
			if tc.substr != "" && !strings.Contains(d.Message, tc.substr) {
				t.Errorf("Message = %q, want substring %q", d.Message, tc.substr)
			}
		})
	}
}

func TestPreToolUse_ForbiddenArgvGlobDeny(t *testing.T) {
	forbidden := []ForbiddenArgv{
		{
			Description:    "gh writes",
			MatchesGlobAny: []string{"gh * create*", "gh * delete*", "gh * edit*"},
			Hint:           "use the relevant issue tracker, not gh.",
		},
		{
			Description:    "substring deny on gh / forgejo",
			MatchesGlobAny: []string{"gh *secret*", "forgejo *secret*"},
			// no Hint: synthesized message expected.
		},
	}
	cases := []struct {
		name    string
		command string
		block   bool
		substr  string
	}{
		// Allowed read verbs and unrelated commands pass through.
		{"gh pr view", "gh pr view 1", false, ""},
		{"gh issue list", "gh issue list", false, ""},
		{"gh api read", "gh api repos/foo/bar", false, ""},
		{"gh api graphql", "gh api graphql -f query=foo", false, ""},
		// Write verbs are denied, with the custom hint surfaced.
		{"gh issue create", "gh issue create", true, "use the relevant issue tracker, not gh."},
		{"gh pr create flags", "gh pr create -t foo", true, "blocked argv (gh writes)"},
		{"gh repo delete", "gh repo delete", true, "gh writes"},
		// env prefix is stripped before the glob runs.
		{"env-prefixed edit", "env GH_TOKEN=x gh release edit", true, "gh writes"},
		// Substring rule fires across '/' and synthesizes a hint from the glob.
		{"substring across slash", "gh api repos/secret/foo", true, `argv matched "gh *secret*"`},
		{"forgejo substring", "forgejo issue create --repo secret/x", true, `argv matched "forgejo *secret*"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := Payload{ToolName: "Bash", ToolInput: map[string]any{"command": tc.command}}
			d := PreToolUse(payload, "test", nil, nil, lookFunc(nil), nil, forbidden...)
			if d.Block != tc.block {
				t.Fatalf("Block = %v, want %v (msg=%q)", d.Block, tc.block, d.Message)
			}
			if tc.substr != "" && !strings.Contains(d.Message, tc.substr) {
				t.Errorf("Message = %q, want substring %q", d.Message, tc.substr)
			}
		})
	}
}

func TestPreToolUse_ForbiddenArgvFirstMatchWins(t *testing.T) {
	// Two rules can match the same segment; the first declared wins.
	forbidden := []ForbiddenArgv{
		{Description: "first", MatchesGlobAny: []string{"gh *create*"}, Hint: "first hint"},
		{Description: "second", MatchesGlobAny: []string{"gh issue *"}, Hint: "second hint"},
	}
	payload := Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "gh issue create"}}
	d := PreToolUse(payload, "test", nil, nil, lookFunc(nil), nil, forbidden...)
	if !d.Block {
		t.Fatalf("expected block, got pass-through")
	}
	if !strings.Contains(d.Message, "first hint") {
		t.Errorf("first match should win: %q", d.Message)
	}
}

func TestPreToolUse_ForbiddenArgvSynthesizedHint(t *testing.T) {
	forbidden := []ForbiddenArgv{
		{Description: "no hint rule", MatchesGlobAny: []string{"gh * create*"}},
	}
	payload := Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "gh issue create"}}
	d := PreToolUse(payload, "test", nil, nil, lookFunc(nil), nil, forbidden...)
	if !d.Block {
		t.Fatalf("expected block, got pass-through")
	}
	if strings.Contains(d.Message, "Recovery:") {
		t.Errorf("absent hint should not synthesize a Recovery clause: %q", d.Message)
	}
	if !strings.Contains(d.Message, "no hint rule") || !strings.Contains(d.Message, `argv matched "gh * create*"`) {
		t.Errorf("synthesized message missing description or glob: %q", d.Message)
	}
}

func TestPreToolUse_ForbiddenArgvNoneIsPassThrough(t *testing.T) {
	// A consumer passing no forbidden rules sees identical behavior: the
	// segment falls through to routes (here, none) and passes.
	payload := Payload{ToolName: "Bash", ToolInput: map[string]any{"command": "gh issue create"}}
	if d := PreToolUse(payload, "test", nil, nil, lookFunc(nil), nil); d.Block {
		t.Errorf("no forbidden rules should pass through: %q", d.Message)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern string
		seg     string
		want    bool
	}{
		{"gh * create*", "gh issue create", true},
		{"gh * create*", "gh pr create -t foo", true},
		{"gh * create*", "gh issue list", false},
		{"gh * create*", "gh pr view", false},
		{"gh *secret*", "gh api repos/secret/foo", true}, // * crosses '/'
		{"gh *secret*", "gh api repos/foo/bar", false},
		{"forgejo *secret*", "forgejo issue create --repo secret/x", true},
		{"[bad", "anything", false}, // malformed pattern never matches
	}
	for _, tc := range cases {
		if got := matchGlob(tc.pattern, tc.seg); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.seg, got, tc.want)
		}
	}
}

func TestBasename(t *testing.T) {
	cases := map[string]string{
		"gcloud":                   "gcloud",
		"/opt/homebrew/bin/gcloud": "gcloud",
		"./rel/x":                  "x",
		"":                         "",
		"/trailing/":               "",
	}
	for in, want := range cases {
		if got := Basename(in); got != want {
			t.Errorf("Basename(%q) = %q, want %q", in, got, want)
		}
	}
}
