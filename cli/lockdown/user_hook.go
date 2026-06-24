package lockdown

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// hookMarker tags a PreToolUse hook entry as cli-guard's own so re-runs
// reconcile the existing entry in place instead of appending a duplicate.
const hookMarker = "_cli-guard"

// legacyHookMarker is the pre-retirement key written under the coily consumer,
// still recognized on read so old settings.json reconcile in place.
const legacyHookMarker = "_coily"

// markerOf returns the cli-guard hook marker on entry, preferring the current
// key and falling back to the legacy one.
func markerOf(entry map[string]any) string {
	if m, ok := entry[hookMarker].(string); ok {
		return m
	}
	m, _ := entry[legacyHookMarker].(string)
	return m
}

// EnsureUserHook writes the user-level PreToolUse Bash hook script
// under homeDir/<SettingsRelPath>/<driver.UserHookFilename> and
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
func ensureHookEntry(preToolUse []any, hookPath, markerKey string) []any {
	wantHook := map[string]any{
		"type":     "command",
		"command":  hookPath,
		hookMarker: markerKey,
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
			if markerOf(hm) == markerKey {
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
