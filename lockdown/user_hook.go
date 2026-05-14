package lockdown

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// EnsureUserHook writes the user-level PreToolUse Bash hook script
// under homeDir/<SettingsRelPath>/<driver.UserHookFilename> and
// patches the user settings.json (same dir) so a PreToolUse Bash
// matcher invokes it. Idempotent: re-runs overwrite the script and
// leave the settings entry alone if the driver's marker is already
// present. Returns the resolved hook path and a flag for whether
// settings.json was changed.
func EnsureUserHook(homeDir string, drv *Driver) (hookPath string, settingsChanged bool, err error) {
	if err := drv.Validate(); err != nil {
		return "", false, err
	}
	if homeDir == "" {
		return "", false, errors.New("lockdown: EnsureUserHook: homeDir is empty")
	}
	settingsDir := filepath.Join(homeDir, drv.SettingsRelPath)
	if mkErr := os.MkdirAll(settingsDir, 0o755); mkErr != nil {
		return "", false, fmt.Errorf("lockdown: mkdir %s: %w", settingsDir, mkErr)
	}
	hookPath = filepath.Join(settingsDir, drv.UserHookFilename)
	if wErr := os.WriteFile(hookPath, []byte(drv.RenderUserHookScript(drv)), 0o755); wErr != nil {
		return "", false, fmt.Errorf("lockdown: write %s: %w", hookPath, wErr)
	}

	settingsPath := filepath.Join(settingsDir, "settings.json")
	changed, sErr := patchUserSettings(settingsPath, hookPath, drv.UserHookMarkerKey)
	if sErr != nil {
		return hookPath, false, sErr
	}
	return hookPath, changed, nil
}

// patchUserSettings reads the user settings.json, ensures
// hooks.PreToolUse contains an entry whose hooks[].command matches
// hookPath and whose matcher is "Bash", and writes the file back if
// anything changed. A missing file is created with a minimal
// structure.
func patchUserSettings(settingsPath, hookPath, markerKey string) (bool, error) {
	raw, readErr := os.ReadFile(settingsPath)
	root := map[string]any{}
	if readErr == nil {
		if uErr := json.Unmarshal(raw, &root); uErr != nil {
			return false, fmt.Errorf("lockdown: parse %s: %w", settingsPath, uErr)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return false, fmt.Errorf("lockdown: read %s: %w", settingsPath, readErr)
	}

	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	preToolUse, _ := hooks["PreToolUse"].([]any)
	preToolUse = ensureHookEntry(preToolUse, hookPath, markerKey)
	hooks["PreToolUse"] = preToolUse
	root["hooks"] = hooks

	after, mErr := json.MarshalIndent(root, "", "  ")
	if mErr != nil {
		return false, fmt.Errorf("lockdown: marshal settings: %w", mErr)
	}
	after = append(after, '\n')
	if len(raw) > 0 && string(raw) == string(after) {
		return false, nil
	}
	if wErr := os.WriteFile(settingsPath, after, 0o600); wErr != nil {
		return false, fmt.Errorf("lockdown: write %s: %w", settingsPath, wErr)
	}
	return true, nil
}

// ensureHookEntry returns preToolUse with a guaranteed entry whose
// inner hooks slice contains a {type: "command", command: hookPath,
// _coily: markerKey} record under matcher "Bash". An existing entry
// is identified by the marker and updated in place; other entries
// (user-added Bash hooks) are preserved verbatim.
//
// The marker key is stored under the JSON key "_coily" for backward
// compatibility with hook entries already on disk from the
// coily-internal era. The key name is independent of the marker
// value, which can be any driver-supplied string.
func ensureHookEntry(preToolUse []any, hookPath, markerKey string) []any {
	wantHook := map[string]any{
		"type":    "command",
		"command": hookPath,
		"_coily":  markerKey,
	}
	for _, entry := range preToolUse {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if matcher, _ := m["matcher"].(string); matcher != "Bash" {
			continue
		}
		inner, _ := m["hooks"].([]any)
		updated := false
		for i, h := range inner {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if marker, _ := hm["_coily"].(string); marker == markerKey {
				inner[i] = wantHook
				updated = true
				break
			}
		}
		if !updated {
			inner = append(inner, wantHook)
		}
		m["hooks"] = inner
		return preToolUse
	}
	// No Bash matcher entry exists; add one with our hook.
	return append(preToolUse, map[string]any{
		"matcher": "Bash",
		"hooks":   []any{wantHook},
	})
}
