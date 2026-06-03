package repocfg

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ModeDenyDirect is the only protected-binary mode supported in v1: the
// binary may not be invoked directly by a semi-trusted agent; it is routed
// through an allowed wrapper instead. Other modes (e.g. a future audited
// read-only allowlist) are reserved and rejected until implemented.
const ModeDenyDirect = "deny-direct"

// Security is the optional security: section of a repo config. It is
// declarative policy a consumer's doctor/hook surfaces read; repocfg parses
// and validates it but enforces nothing on its own. A config with no
// security: block yields a zero Security, which every field treats as "no
// policy declared". Field names are snake_case to match the commands:
// scheme (allow_metacharacters, audit.egress).
type Security struct {
	// ProtectedBinaries are host tools a semi-trusted agent must not invoke
	// directly. Empty means no binary is protected by config.
	ProtectedBinaries []ProtectedBinary
	// Sudo carries sudo-posture policy.
	Sudo SudoPolicy
	// Hooks carries PreToolUse deny/route policy derived from the protected
	// set, kept as an explicit block so a consumer can tune hook behavior
	// without changing the protected-binary contract.
	Hooks HookPolicy
}

// ProtectedBinary declares one host tool that direct agent invocation is
// denied for. The real binary stays runnable by humans through the named
// wrappers; the policy is about keeping the agent off the bare binary.
type ProtectedBinary struct {
	// Name is the binary's basename, e.g. "gcloud". Required.
	Name string
	// Mode is how the binary is protected. Empty defaults to deny-direct;
	// only deny-direct is accepted in v1.
	Mode string
	// AllowedWrappers names the wrapper commands a human routes through
	// instead (e.g. "kap", "ward"). Surfaced in remediation text.
	AllowedWrappers []string
	// ExpectedRealPaths are the canonical install locations of the real
	// binary, used by doctor to reason about PATH-shim posture.
	ExpectedRealPaths []string
	// CredentialEnv names environment variables that, when present in an
	// agent session, hand the agent the binary's credentials. doctor warns
	// when these are set.
	CredentialEnv []string
}

// EffectiveMode returns the protected-binary mode with the empty-default
// applied. Callers should switch on this rather than the raw field.
func (p ProtectedBinary) EffectiveMode() string {
	if p.Mode == "" {
		return ModeDenyDirect
	}
	return p.Mode
}

// SudoPolicy carries sudo-posture expectations doctor checks against.
type SudoPolicy struct {
	// ForbidPasswordless asserts the host must not let the agent user run
	// broad passwordless sudo. doctor fails when `sudo -n` succeeds and
	// this is set.
	ForbidPasswordless bool
}

// HookPolicy is the optional PreToolUse deny/route block. When omitted, a
// consumer can derive the same effect from ProtectedBinaries; this block
// exists for consumers that want to state it explicitly or add hints for
// binaries that are not full protected entries.
type HookPolicy struct {
	// DenyBareBinaries are basenames the hook denies on bare invocation.
	DenyBareBinaries []string
	// RouteHints maps a binary basename to the recovery sentence surfaced
	// when its invocation is denied.
	RouteHints map[string]string
}

// rawSecurity mirrors the on-disk YAML shape. Kept separate from the public
// structs so the wire format (snake_case tags) is decoupled from the Go API.
type rawSecurity struct {
	ProtectedBinaries []struct {
		Name              string   `yaml:"name"`
		Mode              string   `yaml:"mode"`
		AllowedWrappers   []string `yaml:"allowed_wrappers"`
		ExpectedRealPaths []string `yaml:"expected_real_paths"`
		CredentialEnv     []string `yaml:"credential_env"`
	} `yaml:"protected_binaries"`
	Sudo struct {
		ForbidPasswordless bool `yaml:"forbid_passwordless"`
	} `yaml:"sudo"`
	Hooks struct {
		DenyBareBinaries []string          `yaml:"deny_bare_binaries"`
		RouteHints       map[string]string `yaml:"route_hints"`
	} `yaml:"hooks"`
}

// decodeSecurity parses and validates the security: node. A zero-value node
// (no security: key) returns a zero Security and no error.
func decodeSecurity(node yaml.Node) (Security, error) {
	if node.IsZero() {
		return Security{}, nil
	}
	var raw rawSecurity
	if err := node.Decode(&raw); err != nil {
		return Security{}, fmt.Errorf("decode security: %w", err)
	}

	sec := Security{
		Sudo:  SudoPolicy{ForbidPasswordless: raw.Sudo.ForbidPasswordless},
		Hooks: HookPolicy{DenyBareBinaries: raw.Hooks.DenyBareBinaries, RouteHints: raw.Hooks.RouteHints},
	}
	seen := make(map[string]bool, len(raw.ProtectedBinaries))
	for i, pb := range raw.ProtectedBinaries {
		if pb.Name == "" {
			return Security{}, fmt.Errorf("protected_binaries[%d]: name is empty", i)
		}
		if pb.Name != basename(pb.Name) {
			return Security{}, fmt.Errorf("protected_binaries[%d]: name %q must be a bare basename, not a path", i, pb.Name)
		}
		if seen[pb.Name] {
			return Security{}, fmt.Errorf("protected_binaries[%d]: duplicate name %q", i, pb.Name)
		}
		seen[pb.Name] = true
		mode := pb.Mode
		if mode != "" && mode != ModeDenyDirect {
			return Security{}, fmt.Errorf("protected_binaries[%d] (%s): unsupported mode %q (v1 supports %q)", i, pb.Name, mode, ModeDenyDirect)
		}
		sec.ProtectedBinaries = append(sec.ProtectedBinaries, ProtectedBinary{
			Name:              pb.Name,
			Mode:              mode,
			AllowedWrappers:   pb.AllowedWrappers,
			ExpectedRealPaths: pb.ExpectedRealPaths,
			CredentialEnv:     pb.CredentialEnv,
		})
	}
	return sec, nil
}

// basename returns the final path element of s, matching filepath.Base for
// the cases we care about without importing path semantics into the config
// layer's validation message.
func basename(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}
