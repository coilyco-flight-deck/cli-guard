package dispatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// defaultClaudeConfigPath resolves the Claude Code config file that holds
// per-folder trust state (~/.claude.json). It is the default for
func defaultClaudeConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude.json"), nil
}

// ensureClaudeFolderTrust marks dir as trusted in the Claude Code config
// so the dispatched Claude session opens straight into work instead of
func (d *Dispatcher) ensureClaudeFolderTrust(dir string) error {
	path, err := d.cfg.ClaudeConfigPath()
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	projects, ok := doc["projects"].(map[string]any)
	if !ok {
		projects = map[string]any{}
		doc["projects"] = projects
	}
	entry, ok := projects[dir].(map[string]any)
	if !ok {
		entry = map[string]any{}
		projects[dir] = entry
	}
	if trusted, ok := entry["hasTrustDialogAccepted"].(bool); ok && trusted {
		return nil // already trusted - skip the rewrite, avoid a needless race
	}
	entry["hasTrustDialogAccepted"] = true
	return writeClaudeConfig(path, doc)
}

// writeClaudeConfig serializes doc back to path via a tmp-write + rename
// in the same directory, so a reader - Claude Code itself, or a
func writeClaudeConfig(path string, doc map[string]any) error {
	payload, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal claude config: %w", err)
	}
	tmp := path + ".dispatch-tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("write tmp claude config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename tmp claude config: %w", err)
	}
	return nil
}
