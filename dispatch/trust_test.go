package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withClaudeConfig points the dispatcher's ClaudeConfigPath at a tempfile
// seeded with the given JSON (empty string = no file on disk) and returns
func withClaudeConfig(t *testing.T, d *Dispatcher, seed string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".claude.json")
	if seed != "" {
		if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
			t.Fatalf("seed claude config: %v", err)
		}
	}
	d.cfg.ClaudeConfigPath = func() (string, error) { return path, nil }
	return path
}

// trustOf reads the config back and reports the hasTrustDialogAccepted
// flag for dir. ok is false when there is no entry for dir at all.
func trustOf(t *testing.T, path, dir string) (trusted, ok bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back config: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	projects, _ := doc["projects"].(map[string]any)
	entry, ok := projects[dir].(map[string]any)
	if !ok {
		return false, false
	}
	trusted, _ = entry["hasTrustDialogAccepted"].(bool)
	return trusted, true
}

// TestEnsureClaudeFolderTrust_MissingFile pins the soft contract: a config
// that has never been written is not an error, and the function leaves no
func TestEnsureClaudeFolderTrust_MissingFile(t *testing.T) {
	d := newTestDispatcher(t)
	path := withClaudeConfig(t, d, "")
	if err := d.ensureClaudeFolderTrust("/some/repo"); err != nil {
		t.Fatalf("missing config should be a no-op, got error: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("missing config should stay missing, stat err = %v", err)
	}
}

// TestEnsureClaudeFolderTrust_NewEntry covers the dispatch-worktree case:
// a path Claude Code has never seen gets a fresh project entry with the
func TestEnsureClaudeFolderTrust_NewEntry(t *testing.T) {
	d := newTestDispatcher(t)
	dir := "/Users/example/projects/example-org/.dispatch-worktrees/example-repo/issue-290"
	path := withClaudeConfig(t, d, `{"numStartups":7,"projects":{"/other/repo":{"hasTrustDialogAccepted":true}}}`)

	if err := d.ensureClaudeFolderTrust(dir); err != nil {
		t.Fatalf("ensureClaudeFolderTrust: %v", err)
	}

	if trusted, ok := trustOf(t, path, dir); !ok || !trusted {
		t.Errorf("dispatch dir trust = (%v, ok=%v), want (true, ok=true)", trusted, ok)
	}
	if trusted, ok := trustOf(t, path, "/other/repo"); !ok || !trusted {
		t.Errorf("unrelated repo entry was clobbered: (%v, ok=%v)", trusted, ok)
	}
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if n, _ := doc["numStartups"].(float64); n != 7 {
		t.Errorf("unrelated top-level key numStartups = %v, want 7", n)
	}
}

// TestEnsureClaudeFolderTrust_FlipsFalse covers the case where a repo has
// an explicit hasTrustDialogAccepted:false (the state a headless run
func TestEnsureClaudeFolderTrust_FlipsFalse(t *testing.T) {
	d := newTestDispatcher(t)
	dir := "/Users/example/projects/example-org/example-repo"
	path := withClaudeConfig(t, d, `{"projects":{"`+dir+`":{"hasTrustDialogAccepted":false,"history":[]}}}`)

	if err := d.ensureClaudeFolderTrust(dir); err != nil {
		t.Fatalf("ensureClaudeFolderTrust: %v", err)
	}

	if trusted, ok := trustOf(t, path, dir); !ok || !trusted {
		t.Errorf("trust after flip = (%v, ok=%v), want (true, ok=true)", trusted, ok)
	}
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	projects, _ := doc["projects"].(map[string]any)
	entry, _ := projects[dir].(map[string]any)
	if _, hasHistory := entry["history"]; !hasHistory {
		t.Errorf("sibling field history was dropped from the project entry")
	}
}
