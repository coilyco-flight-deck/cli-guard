// Package hook implements the Claude Code PreToolUse hook engine in
// the shared substrate. Each cli-guard consumer (coily, agent-guard,
// future guards) ships its own integrity rules and routing-hint
// table through this interface; no consumer names another in source.
//
// Boundary: cli-guard knows about hook payloads, segment parsing,
// binary-path integrity, and the Decision shape Claude Code expects.
// It does not know about coily verbs or agent-guard verbs - those are
// strings in the consumer's Route table. The consumer wires its own
// `<binary> hook pre-tool-use` subcommand that calls PreToolUse with
// its tables, and renders its per-repo lockdown-deny.sh to exec its
// own binary.
//
// Architecture context: coilysiren/cli-guard#74, coilysiren/coily#248.
package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

// Route maps a bare leading-token in argv to a recovery hint string
// the consumer wants surfaced when the harness denies that token.
// Consumers register their own at compile time; the engine renders
// the matching hint when the PreToolUse payload's command segment
// starts with that token.
type Route struct {
	// Token is the bare leading-token to match, e.g. "gh", "brew".
	Token string

	// Hint is the human-readable recovery message. The engine
	// prefixes "<source> hook: blocked bare `<token>`. Recovery: "
	// to the hint so the origin and the matched token are visible.
	Hint string

	// Extra, when non-nil, is consulted for token-specific suffixes
	// after a match. Receives the matched segment so the consumer
	// can append sub-verb-specific notes (e.g. the GraphQL-trap
	// warning some consumers attach to `gh issue view`). Empty
	// return means no suffix.
	Extra func(segment string) string
}

// IntegrityRule names a binary and the canonical absolute paths it
// is allowed to resolve to. A bare invocation of Binary that
// resolves outside AllowedPaths is rejected with a path-hijack
// message before any Route lookup fires. Consumers register their
// own integrity rules for their own binaries; cli-guard does not
// know which binaries any given consumer wraps.
type IntegrityRule struct {
	Binary       string
	AllowedPaths []string
}

// Decision is what PreToolUse returns. Block=true means the caller
// should emit Message to stderr and exit with the host's hook-block
// exit code (Claude Code's PreToolUse contract is exit 2). Block=
// false is the pass-through case (no route matched, no integrity
// rule fired).
type Decision struct {
	Block   bool
	Message string
}

// Payload is the subset of Claude Code's PreToolUse hook payload the
// engine consumes. Other fields are ignored.
type Payload struct {
	ToolName  string                 `json:"tool_name"`
	ToolInput map[string]interface{} `json:"tool_input"`
	CWD       string                 `json:"cwd"`
}

// LookPath mirrors exec.LookPath. Injected for tests.
type LookPath func(name string) (string, error)

// PreToolUse evaluates a payload against integrity rules and routes.
// Returns Decision{Block: false} on no match (best-effort hint surface;
// hard denial belongs to the harness's permissions.deny, not this hook).
// source is the consumer name prefixed onto Block messages.
//
// Failure modes (non-Bash tool, empty command, malformed segments) pass
// through silently. The hook is signaling, not policing.
func PreToolUse(payload Payload, source string, rules []IntegrityRule, routes []Route, lookup LookPath) Decision {
	if payload.ToolName != "Bash" {
		return Decision{}
	}
	cmd, _ := payload.ToolInput["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		return Decision{}
	}
	integrity := indexIntegrity(rules)
	routeByToken := indexRoutes(routes)
	for _, seg := range SplitSegments(cmd) {
		seg = StripEnvPrefix(strings.TrimSpace(seg))
		if seg == "" {
			continue
		}
		if d := evaluateSegment(seg, source, integrity, routeByToken, lookup); d.Block {
			return d
		}
	}
	return Decision{}
}

func indexIntegrity(rules []IntegrityRule) map[string][]string {
	out := map[string][]string{}
	for _, r := range rules {
		out[r.Binary] = r.AllowedPaths
	}
	return out
}

func indexRoutes(routes []Route) map[string]Route {
	out := map[string]Route{}
	for _, r := range routes {
		out[r.Token] = r
	}
	return out
}

func evaluateSegment(seg, source string, integrity map[string][]string, routeByToken map[string]Route, lookup LookPath) Decision {
	token := LeadingToken(seg)
	if allowed, ok := integrity[token]; ok {
		if msg := CheckBinaryPath(token, allowed, lookup, source); msg != "" {
			return Decision{Block: true, Message: msg}
		}
	}
	route, ok := routeByToken[token]
	if !ok {
		return Decision{}
	}
	msg := fmt.Sprintf("%s hook: blocked bare `%s`. Recovery: %s", source, token, route.Hint)
	if route.Extra != nil {
		if suffix := route.Extra(seg); suffix != "" {
			msg += suffix
		}
	}
	return Decision{Block: true, Message: msg}
}

// ReadPayload decodes a PreToolUse payload from r. Empty stream or
// unparseable JSON returns an empty Payload (not an error); the engine
// is best-effort and a malformed payload passes through.
func ReadPayload(r io.Reader) Payload {
	data, _ := io.ReadAll(r)
	if len(data) == 0 {
		return Payload{}
	}
	var p Payload
	_ = json.Unmarshal(data, &p)
	return p
}

// CheckBinaryPath resolves token via lookup and returns a non-empty
// hijack-warning string when the resolved path is outside allowed.
// ENOENT (binary not on PATH) returns "" - bash will surface the
// command-not-found error naturally.
//
// Resolution uses lookup directly without canonicalizing symlinks
// (e.g. brew's /opt/homebrew/bin/<binary> symlink, not the Cellar
// realpath). Matching the symlink is the documented contract from
// coily's original shell-gate; preserved here.
func CheckBinaryPath(token string, allowed []string, lookup LookPath, source string) string {
	resolved, err := lookup(token)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ""
		}
		return fmt.Sprintf(
			"%s hook: blocked `%s`. Resolution of `%s` failed: %v. Canonical install paths: %s",
			source, token, token, err, strings.Join(allowed, ", "),
		)
	}
	abs, absErr := filepath.Abs(resolved)
	if absErr != nil {
		abs = resolved
	}
	for _, p := range allowed {
		if abs == p {
			return ""
		}
	}
	return fmt.Sprintf(
		"%s hook: blocked `%s`. `%s` resolves to %s, which is outside the canonical install paths (%s). This looks like a PATH-hijack of the guard binary. Reinstall via the official tap or unset the offending PATH entry.",
		source, token, token, abs, strings.Join(allowed, ", "),
	)
}

// SplitSegments breaks a bash command into the leading-token segments
// the engine classifies. Splits on $( ) || && | ; & boundaries.
// Imperfect (not a full shell parser), but tight enough for the
// routing-hint surface.
func SplitSegments(cmd string) []string {
	replacers := []string{"$(", "\n", ")", "\n", "||", "\n", "&&", "\n", "|", "\n", ";", "\n", "&", "\n"}
	r := strings.NewReplacer(replacers...)
	return strings.Split(r.Replace(cmd), "\n")
}

// StripEnvPrefix peels leading `env VAR=val ...` and `sudo` tokens so
// `env FOO=bar gh issue view` classifies the same as bare
// `gh issue view`. Strips iteratively in case both are present.
func StripEnvPrefix(seg string) string {
	for {
		trimmed := strings.TrimLeft(seg, " \t")
		switch {
		case strings.HasPrefix(trimmed, "sudo "):
			seg = strings.TrimPrefix(trimmed, "sudo ")
		case strings.HasPrefix(trimmed, "env "):
			rest := strings.TrimPrefix(trimmed, "env ")
			peeled := false
			for {
				rest = strings.TrimLeft(rest, " \t")
				eq := strings.IndexByte(rest, '=')
				sp := strings.IndexByte(rest, ' ')
				if eq <= 0 || (sp >= 0 && sp < eq) {
					break
				}
				if sp < 0 {
					rest = ""
				} else {
					rest = rest[sp+1:]
				}
				peeled = true
			}
			if !peeled {
				return trimmed
			}
			seg = rest
		default:
			return trimmed
		}
	}
}

// LeadingToken returns the first whitespace-delimited token of seg.
// "gh issue view" -> "gh", "" -> "".
func LeadingToken(seg string) string {
	i := strings.IndexAny(seg, " \t")
	if i < 0 {
		return seg
	}
	return seg[:i]
}
