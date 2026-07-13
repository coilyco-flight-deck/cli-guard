package repocfg

import (
	"fmt"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ModeDenyDirect is the only protected-binary mode supported in v1: the binary
// may not be invoked directly by an agent, only through an allowed wrapper.
const ModeDenyDirect = "deny-direct"

// Security is the optional security: section of a repo config: declarative
// policy that doctor/hook surfaces read. A zero value means no policy declared.
type Security struct {
	// ProtectedBinaries are host tools a semi-trusted agent must not invoke
	// directly. Empty means no binary is protected by config.
	ProtectedBinaries []ProtectedBinary
	// Sudo carries sudo-posture policy.
	Sudo SudoPolicy
	// Hooks carries PreToolUse deny/route policy, kept explicit so a consumer
	// can tune hook behavior without changing the protected-binary contract.
	Hooks HookPolicy
	// ForbiddenArgv are glob-pattern argv deny rules matching the whole command
	// segment (not just a basename), e.g. to deny write verbs. Empty means none.
	ForbiddenArgv []ForbiddenArgv
}

// ForbiddenArgv is one glob-pattern argv deny rule from the forbidden_argv:
// list. A command segment matching any glob in MatchesGlobAny is denied.
type ForbiddenArgv struct {
	// Description is the required human label, surfaced in the deny hint.
	Description string
	// MatchesGlobAny holds the POSIX fnmatch patterns (path/filepath.Match
	// grammar). Required and non-empty; each is validated at load.
	MatchesGlobAny []string
	// Hint is the optional recovery sentence. When empty, the engine
	// synthesizes one from the matched glob.
	Hint string
}

// ProtectedBinary declares one host tool that direct agent invocation is denied
// for. Humans still run it through the named wrappers.
type ProtectedBinary struct {
	// Name is the binary's basename, e.g. "gcloud". Required.
	Name string
	// Mode is how the binary is protected. Empty defaults to deny-direct;
	// only deny-direct is accepted in v1.
	Mode string
	// AllowedWrappers names the wrapper commands a human routes through
	// instead (e.g. "<cli>", "<cli>-ops"). Surfaced in remediation text.
	AllowedWrappers []string
	// ExpectedRealPaths are optional integrity hints for doctor.
	// They help verify the basename target's real installs.
	ExpectedRealPaths []string
	// CredentialEnv names environment variables that hand the agent the
	// binary's credentials when set. doctor warns when they are present.
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
	// ForbidPasswordless asserts the agent user must not have broad
	// passwordless sudo. doctor fails when `sudo -n` succeeds and this is set.
	ForbidPasswordless bool
}

// HookPolicy is the optional PreToolUse deny/route block. When omitted, the
// same effect derives from ProtectedBinaries; this states it explicitly.
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
	ForbiddenArgv []rawForbiddenArgv `yaml:"forbidden_argv"`
}

// rawForbiddenArgv mirrors one on-disk forbidden_argv entry.
type rawForbiddenArgv struct {
	Description    string   `yaml:"description"`
	MatchesGlobAny []string `yaml:"matches_glob_any"`
	Hint           string   `yaml:"hint"`
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
	forbidden, err := decodeForbiddenArgv(raw.ForbiddenArgv)
	if err != nil {
		return Security{}, err
	}
	sec.ForbiddenArgv = forbidden
	return sec, nil
}

// decodeForbiddenArgv validates and maps the forbidden_argv entries: each needs
// a description and at least one well-formed glob, anchored to its entry index.
func decodeForbiddenArgv(raw []rawForbiddenArgv) ([]ForbiddenArgv, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]ForbiddenArgv, 0, len(raw))
	for i, fa := range raw {
		if fa.Description == "" {
			return nil, fmt.Errorf("forbidden_argv[%d]: description is empty", i)
		}
		if len(fa.MatchesGlobAny) == 0 {
			return nil, fmt.Errorf("forbidden_argv[%d] (%s): matches_glob_any is empty", i, fa.Description)
		}
		for j, glob := range fa.MatchesGlobAny {
			if glob == "" {
				return nil, fmt.Errorf("forbidden_argv[%d] (%s): matches_glob_any[%d] is empty", i, fa.Description, j)
			}
			if _, err := filepath.Match(glob, ""); err != nil {
				return nil, fmt.Errorf("forbidden_argv[%d] (%s): matches_glob_any[%d] %q: invalid glob: %w", i, fa.Description, j, glob, err)
			}
		}
		out = append(out, ForbiddenArgv(fa))
	}
	return out, nil
}

// basename returns the final path element of s, matching filepath.Base for
// the cases we care about without importing path semantics here.
func basename(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}
