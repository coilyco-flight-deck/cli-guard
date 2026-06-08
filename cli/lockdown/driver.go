package lockdown

import (
	"fmt"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/profile"
)

// Driver describes the binary and runtime that lockdown should write
// settings/hooks for. Callers either construct a Driver directly or use
type Driver struct {
	BinaryName         string
	BinaryAllowedPaths []string
	WrapperRecovery    map[string]string
	HookFilename       string
	UserHookFilename   string
	UserHookMarkerKey  string
	SettingsRelPath    string

	BuildSettings        func(existing []byte, d *Defaults, drv *Driver) ([]byte, error)
	RenderHookScript     func(d *Defaults, drv *Driver) (string, error)
	RenderUserHookScript func(drv *Driver) string

	// Coordinate is the resolved per-session lockdown coordinate, when a
	// consumer is profile-aware. Optional: BuildSettings consumers may
	Coordinate *profile.Coordinate
}

// HookSettingsPath is the relative path baked into settings.json's hook
// entry. The host CLI resolves it relative to the project root.
func (d *Driver) HookSettingsPath() string {
	return d.SettingsRelPath + "/" + d.HookFilename
}

// Validate returns an error if the driver is missing fields the
// lockdown package needs to operate. Callers should run this before
func (d *Driver) Validate() error {
	if d == nil {
		return fmt.Errorf("lockdown: nil driver")
	}
	var missing []string
	if d.BinaryName == "" {
		missing = append(missing, "BinaryName")
	}
	if len(d.BinaryAllowedPaths) == 0 {
		missing = append(missing, "BinaryAllowedPaths")
	}
	if d.HookFilename == "" {
		missing = append(missing, "HookFilename")
	}
	if d.UserHookFilename == "" {
		missing = append(missing, "UserHookFilename")
	}
	if d.UserHookMarkerKey == "" {
		missing = append(missing, "UserHookMarkerKey")
	}
	if d.SettingsRelPath == "" {
		missing = append(missing, "SettingsRelPath")
	}
	if d.BuildSettings == nil {
		missing = append(missing, "BuildSettings")
	}
	if d.RenderHookScript == nil {
		missing = append(missing, "RenderHookScript")
	}
	if d.RenderUserHookScript == nil {
		missing = append(missing, "RenderUserHookScript")
	}
	if len(missing) > 0 {
		return fmt.Errorf("lockdown: driver missing fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ClaudeCode returns a Driver pre-wired for Claude Code's settings.json
// shape and PreToolUse Bash hook contract. Callers supply the binary
func ClaudeCode(binary string, allowedPaths []string, recovery map[string]string) *Driver {
	d := &Driver{
		BinaryName:         binary,
		BinaryAllowedPaths: allowedPaths,
		WrapperRecovery:    recovery,
		HookFilename:       "lockdown-deny.sh",
		UserHookFilename:   binary + "-binary-gate.sh",
		UserHookMarkerKey:  binary + "-binary-gate",
		SettingsRelPath:    ".claude",
	}
	d.BuildSettings = claudeCodeBuildSettings
	d.RenderHookScript = claudeCodeRenderHookScript
	d.RenderUserHookScript = claudeCodeRenderUserHookScript
	return d
}
