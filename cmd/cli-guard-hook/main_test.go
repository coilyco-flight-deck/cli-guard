package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

const vaultProtectedYAML = `commands:
  noop:
    run: make noop
    description: noop
security:
  protected_binaries:
    - name: vault
      allowed_wrappers: [kap]
  hooks:
    route_hints:
      vault: "Use kap for vault access."
`

func notFoundLookup(string) (string, error) { return "", exec.ErrNotFound }

func runCmd(args []string, stdin string, env map[string]string) (int, string) {
	var stderr bytes.Buffer
	getenv := func(k string) string { return env[k] }
	code := run(args, strings.NewReader(stdin), &stderr, getenv, notFoundLookup)
	return code, stderr.String()
}

func TestRun_BareVaultBlocked(t *testing.T) {
	path := writeConfig(t, vaultProtectedYAML)
	payload := `{"tool_name":"Bash","tool_input":{"command":"vault read secret/foo"}}`
	code, stderr := runCmd([]string{"--config", path}, payload, nil)
	if code != exitBlock {
		t.Fatalf("want %d, got %d. stderr=%q", exitBlock, code, stderr)
	}
	if !strings.Contains(stderr, "vault") || !strings.Contains(stderr, "kap") {
		t.Errorf("stderr should mention vault + the kap hint, got %q", stderr)
	}
}

func TestRun_AbsolutePathBlocked(t *testing.T) {
	path := writeConfig(t, vaultProtectedYAML)
	payload := `{"tool_name":"Bash","tool_input":{"command":"/usr/local/bin/vault read secret/foo"}}`
	code, _ := runCmd([]string{"--config", path}, payload, nil)
	if code != exitBlock {
		t.Fatalf("want block, got %d", code)
	}
}

func TestRun_WrapperPasses(t *testing.T) {
	path := writeConfig(t, vaultProtectedYAML)
	payload := `{"tool_name":"Bash","tool_input":{"command":"kap vault read secret/foo"}}`
	code, _ := runCmd([]string{"--config", path}, payload, nil)
	if code != 0 {
		t.Fatalf("kap-wrapped invocation should pass, got %d", code)
	}
}

func TestRun_UnknownBinaryPasses(t *testing.T) {
	path := writeConfig(t, vaultProtectedYAML)
	payload := `{"tool_name":"Bash","tool_input":{"command":"ls -la"}}`
	code, _ := runCmd([]string{"--config", path}, payload, nil)
	if code != 0 {
		t.Fatalf("unrelated bash should pass, got %d", code)
	}
}

func TestRun_NoConfigPasses(t *testing.T) {
	payload := `{"tool_name":"Bash","tool_input":{"command":"vault read secret/foo"}}`
	code, stderr := runCmd(nil, payload, nil)
	if code != 0 || stderr != "" {
		t.Fatalf("no config should silently pass, got code=%d stderr=%q", code, stderr)
	}
}

func TestRun_MissingConfigPasses(t *testing.T) {
	payload := `{"tool_name":"Bash","tool_input":{"command":"vault read secret/foo"}}`
	code, stderr := runCmd([]string{"--config", "/does/not/exist.yaml"}, payload, nil)
	if code != 0 || stderr != "" {
		t.Fatalf("missing config should silently pass, got code=%d stderr=%q", code, stderr)
	}
}

func TestRun_MalformedConfigPasses(t *testing.T) {
	path := writeConfig(t, "::: not yaml :::")
	payload := `{"tool_name":"Bash","tool_input":{"command":"vault read secret/foo"}}`
	code, stderr := runCmd([]string{"--config", path}, payload, nil)
	if code != 0 {
		t.Fatalf("malformed config should silently pass, got %d. stderr=%q", code, stderr)
	}
}

func TestRun_EnvConfigUsedWhenFlagAbsent(t *testing.T) {
	path := writeConfig(t, vaultProtectedYAML)
	payload := `{"tool_name":"Bash","tool_input":{"command":"vault read secret/foo"}}`
	code, _ := runCmd(nil, payload, map[string]string{envConfig: path})
	if code != exitBlock {
		t.Fatalf("env-supplied config should block, got %d", code)
	}
}

func TestRun_FlagWinsOverEnv(t *testing.T) {
	// env points at a no-protections config; flag points at vault-protected. flag wins.
	envPath := writeConfig(t, "commands:\n  noop:\n    run: make noop\n    description: noop\n")
	flagPath := writeConfig(t, vaultProtectedYAML)
	payload := `{"tool_name":"Bash","tool_input":{"command":"vault read secret/foo"}}`
	code, _ := runCmd(
		[]string{"--config", flagPath},
		payload,
		map[string]string{envConfig: envPath},
	)
	if code != exitBlock {
		t.Fatalf("flag should win, got %d", code)
	}
}

func TestRun_SourceFlagShowsInMessage(t *testing.T) {
	path := writeConfig(t, vaultProtectedYAML)
	payload := `{"tool_name":"Bash","tool_input":{"command":"vault read secret/foo"}}`
	code, stderr := runCmd([]string{"--config", path, "--source", "kap"}, payload, nil)
	if code != exitBlock {
		t.Fatalf("want block, got %d", code)
	}
	if !strings.Contains(stderr, "kap") {
		t.Errorf("source `kap` should appear in deny message, got %q", stderr)
	}
}

func TestRun_NonBashPasses(t *testing.T) {
	path := writeConfig(t, vaultProtectedYAML)
	payload := `{"tool_name":"Read","tool_input":{"file_path":"/etc/passwd"}}`
	code, _ := runCmd([]string{"--config", path}, payload, nil)
	if code != 0 {
		t.Fatalf("non-Bash tool should pass, got %d", code)
	}
}

func TestRun_EmptyStdinPasses(t *testing.T) {
	path := writeConfig(t, vaultProtectedYAML)
	code, stderr := runCmd([]string{"--config", path}, "", nil)
	if code != 0 || stderr != "" {
		t.Fatalf("empty stdin should silently pass, got code=%d stderr=%q", code, stderr)
	}
}

func TestParseFlags_EqualsForm(t *testing.T) {
	cfg, src := parseFlags([]string{"--config=/a.yaml", "--source=kap"}, func(string) string { return "" })
	if cfg != "/a.yaml" {
		t.Errorf("--config=/a.yaml: %q", cfg)
	}
	if src != "kap" {
		t.Errorf("--source=kap: %q", src)
	}
}

func TestParseFlags_SpaceForm(t *testing.T) {
	cfg, src := parseFlags([]string{"--config", "/a.yaml", "--source", "kap"}, func(string) string { return "" })
	if cfg != "/a.yaml" || src != "kap" {
		t.Errorf("space form: cfg=%q src=%q", cfg, src)
	}
}

func TestParseFlags_DefaultSource(t *testing.T) {
	_, src := parseFlags(nil, func(string) string { return "" })
	if src != defaultSourceName {
		t.Errorf("default source: %q", src)
	}
}

func TestParseFlags_DanglingFlagsIgnored(t *testing.T) {
	cfg, src := parseFlags([]string{"--config"}, func(string) string { return "" })
	if cfg != "" || src != defaultSourceName {
		t.Errorf("dangling --config should leave defaults: cfg=%q src=%q", cfg, src)
	}
}

// sanity: ensure exec.ErrNotFound import isn't dropped by goimports during refactor
var _ = errors.Is(exec.ErrNotFound, exec.ErrNotFound)
