package lockdown

import (
	"fmt"
	"strings"
)

// Driver describes the binary and runtime that lockdown should write
// settings/hooks for. Callers either construct a Driver directly or use
// the ClaudeCode constructor for the default coily-style behavior.
//
// The struct decouples lockdown from any particular CLI host:
//
//   - Binary fields name the wrapper binary (today: coily) and the
//     install paths it must resolve to; the hook rejects invocations
//     resolving anywhere else.
//   - WrapperRecovery maps a denied bare binary (e.g. "gh") to the
//     audited wrapper Kai should reach for instead ("coily ops gh").
//     Surfaced in deny messages so opaque errors stay phone-dictatable.
//   - HookFilename / UserHookFilename / UserHookMarkerKey name the
//     per-repo + user-wide hook files and the JSON marker used to
//     recognize our own entry in shared settings.
//   - SettingsRelPath is the project-relative dir containing settings
//     ('.claude' for Claude Code).
//   - BuildSettings / RenderHookScript / RenderUserHookScript are the
//     runtime-specific producers; the ClaudeCode constructor wires the
//     defaults. A future Driver for a different AI tool runtime can
//     swap these without touching the rest of the package.
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
}

// HookSettingsPath is the relative path baked into settings.json's hook
// entry. The host CLI resolves it relative to the project root.
func (d *Driver) HookSettingsPath() string {
	return d.SettingsRelPath + "/" + d.HookFilename
}

// Validate returns an error if the driver is missing fields the
// lockdown package needs to operate. Callers should run this before
// passing the driver into BuildPlan / Write / WriteHook / EnsureUserHook
// so misconfiguration fails loudly at construction time rather than
// silently writing a half-formed gate.
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
// name (typically "coily"), the closed set of filesystem paths that
// binary is permitted to resolve to, and a wrapperRecovery map that
// turns denied bare-binary tokens into the audited wrappers Kai should
// dictate instead.
//
// Filenames default to the coily-style ('lockdown-deny.sh' and
// '<binary>-binary-gate.sh') and the user-hook marker matches the
// gate filename minus the suffix. Override fields on the returned
// Driver before passing it into the package functions if needed.
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
