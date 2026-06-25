package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Installer idempotently registers a PreToolUse hook command in a Claude
// Code settings.json. See docs/ward-helpers.md for the additive-merge rules.
type Installer struct {
	// Matcher is the PreToolUse matcher the hook registers under, e.g. "Bash".
	Matcher string
	// Command is the hook command line to run.
	Command string
	// Type is the hook entry type; defaults to "command" when empty.
	Type string
}

// hookType returns the entry type, defaulting to "command".
func (in Installer) hookType() string {
	if in.Type == "" {
		return "command"
	}
	return in.Type
}

// Ensure returns (present, merged): whether settings already register this
// Matcher+Command, and a deep copy with the entry added if it was missing.
func (in Installer) Ensure(settings map[string]any) (bool, map[string]any) {
	out := cloneMap(settings)
	hooks, _ := out["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	preToolUse, _ := hooks["PreToolUse"].([]any)

	wanted := map[string]any{"type": in.hookType(), "command": in.Command}
	matcherIdx := -1
	for i, raw := range preToolUse {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if matcher, _ := entry["matcher"].(string); matcher == in.Matcher {
			matcherIdx = i
			inner, _ := entry["hooks"].([]any)
			for _, h := range inner {
				hm, ok := h.(map[string]any)
				if !ok {
					continue
				}
				if cmd, _ := hm["command"].(string); cmd == in.Command {
					return true, out
				}
			}
		}
	}

	if matcherIdx >= 0 {
		entry, _ := preToolUse[matcherIdx].(map[string]any)
		inner, _ := entry["hooks"].([]any)
		entry["hooks"] = append(inner, wanted)
		preToolUse[matcherIdx] = entry
	} else {
		preToolUse = append(preToolUse, map[string]any{
			"matcher": in.Matcher,
			"hooks":   []any{wanted},
		})
	}
	hooks["PreToolUse"] = preToolUse
	out["hooks"] = hooks
	return false, out
}

// LoadSettings reads a settings.json. A missing or empty file returns an
// empty map; malformed JSON is an error (never silently overwritten).
func LoadSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- caller-controlled target path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w (refusing to overwrite a malformed settings.json)", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// MarshalSettings emits two-space-indented JSON with a trailing newline.
func MarshalSettings(m map[string]any) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// WriteSettings writes data to path atomically (tempfile + rename),
// creating the parent directory if missing.
func WriteSettings(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".settings.json.*.tmp")
	if err != nil {
		return fmt.Errorf("tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tempfile: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// ResolveSettingsPath returns the absolute settings.json path: explicit made
// absolute, else auto-detected as <git-toplevel-of-cwd>/.claude/settings.json.
func ResolveSettingsPath(explicit string) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", fmt.Errorf("abs %s: %w", explicit, err)
		}
		return abs, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	top, err := gitToplevel(cwd)
	if err != nil {
		return "", fmt.Errorf("auto-detect failed (cwd is not in a git repo); pass an explicit path: %w", err)
	}
	return filepath.Join(top, ".claude", "settings.json"), nil
}

// gitToplevel runs `git rev-parse --show-toplevel` rooted at dir.
func gitToplevel(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// cloneMap deep-clones a settings map so Ensure never mutates its input.
func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch tv := v.(type) {
		case map[string]any:
			out[k] = cloneMap(tv)
		case []any:
			cp := make([]any, len(tv))
			for i, item := range tv {
				if itemMap, ok := item.(map[string]any); ok {
					cp[i] = cloneMap(itemMap)
				} else {
					cp[i] = item
				}
			}
			out[k] = cp
		default:
			out[k] = v
		}
	}
	return out
}
