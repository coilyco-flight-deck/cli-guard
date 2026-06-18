// Package sandbox jails a consumer's gate-spawned child in a Linux user+mount
// namespace so the child and its descendants re-enter the gate; no-op off Linux.
package sandbox

import (
	"os"
	"strings"
)

// Spec configures the jail applied to a sandboxed child. A nil *Spec on a
// shell.Runner means "run directly" — the pre-sandbox behavior.
type Spec struct {
	// SelfExe is the consumer binary path, re-execed as the jail helper and
	// bind-mounted over each wrapped tool as the shim (os.Executable()).
	SelfExe string
	// Tools is the set of wrapped-tool basenames to shim inside the jail,
	// e.g. []string{"brew"}; each is bind-mounted over with SelfExe.
	Tools []string
}

// Env keys shared between the launcher (parent) and the jail helper (child).
const (
	// EnvJailed is "1" inside the jail; re-entrant gate calls then skip
	// re-cloning and resolve the real binary instead of the shim.
	EnvJailed = "CLIGUARD_JAILED"
	// realBinPrefix + sanitized tool name holds the stashed real-binary path
	// the gate execs once the canonical path is masked by the shim.
	realBinPrefix = "CLIGUARD_REALBIN_"
	// EnvNoSandbox, when truthy, opts out of the jail: Wrap no-ops, tools run
	// directly (for containers / namespace-denying shells). See docs/sandbox.md.
	EnvNoSandbox = "CLIGUARD_NO_SANDBOX"
)

// Jailed reports whether the current process is already running inside a jail.
func Jailed() bool { return os.Getenv(EnvJailed) == "1" }

// NoSandbox reports whether the operator opted out of the jail via
// EnvNoSandbox. Accepts 1/true/yes/on (case-insensitive).
func NoSandbox() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvNoSandbox))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// RealBinEnv returns the env-var name carrying tool's stashed real path,
// upper-casing the name and mapping non-alphanumerics to '_'.
func RealBinEnv(tool string) string {
	var b strings.Builder
	b.WriteString(realBinPrefix)
	for _, r := range strings.ToUpper(tool) {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// RealBin returns the stashed real path for bin inside a jail (else ""), so the
// gate execs the real tool rather than looping back into the shim.
func RealBin(bin string) string {
	if !Jailed() {
		return ""
	}
	return os.Getenv(RealBinEnv(bin))
}
