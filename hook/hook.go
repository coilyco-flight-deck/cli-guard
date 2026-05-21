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

// PreToolUse evaluates a payload against integrity rules, routes, and
// the engine-level arbitrary-code-execution deny. Returns
// Decision{Block: false} on no match.
//
// Two classes of Block:
//
//   - Route hints and integrity warnings are best-effort signalling on
//     top of the harness's own permissions.deny.
//   - The arbitrary-code-execution deny (interpreter invocation,
//     execution from a writable scratch directory) is a hard denial
//     the engine owns outright. It is not consumer-configurable: a
//     consumer cannot allowlist its way around running arbitrary
//     Python. See coilysiren/cli-guard#87.
//
// Every segment of a compound command is evaluated, so a denied
// command cannot launder clean by piping it behind an allowed one
// (`head file | python3 -c ...`).
//
// Failure modes (non-Bash tool, empty command, malformed segments) pass
// through silently.
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
		if d := evaluateSegment(seg, source, payload.CWD, integrity, routeByToken, lookup); d.Block {
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

func evaluateSegment(seg, source, cwd string, integrity map[string][]string, routeByToken map[string]Route, lookup LookPath) Decision {
	token := LeadingToken(seg)

	// Engine-level arbitrary-code-execution deny, checked before any
	// consumer route. Runs on every segment, so piping a denied
	// command behind an allowed one does not launder it.
	if name := interpreterName(token); name != "" {
		return Decision{Block: true, Message: fmt.Sprintf(
			"%s hook: blocked interpreter `%s`. Running arbitrary code through an interpreter defeats the command allowlist. This deny holds no matter the spelling: bare, with `-c`, piped behind an allowed command, by absolute path, or via an executable shebang script.",
			source, name)}
	}
	if scratchExec(token, cwd) {
		return Decision{Block: true, Message: fmt.Sprintf(
			"%s hook: blocked execution of `%s` from a writable scratch directory. A file under /tmp (or a similar scratch dir) can carry an interpreter shebang, so the kernel runs arbitrary code the moment it is exec'd - the guard never sees the interpreter token. Writing scratch files to /tmp stays allowed; executing them does not.",
			source, token)}
	}

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

// interpreterTokens is the set of program basenames that denote
// arbitrary-code execution: a process whose job is to take a script or
// inline source and run it. Matched on the basename so an absolute
// path (`/usr/bin/python3`) or a scratch-dir copy is caught the same as
// the bare name. Engine-owned and not consumer-configurable. #87.
var interpreterTokens = map[string]bool{
	"python": true, "python2": true, "python3": true,
	"ruby": true, "perl": true, "node": true, "nodejs": true,
	"deno": true, "bun": true, "osascript": true, "php": true,
	"lua": true, "tclsh": true, "Rscript": true, "groovy": true,
	"sh": true, "bash": true, "zsh": true, "fish": true,
	"dash": true, "ksh": true, "csh": true, "tcsh": true, "ash": true,
	"powershell": true, "powershell.exe": true,
	"pwsh": true, "pwsh.exe": true,
	"cmd": true, "cmd.exe": true,
	"wscript": true, "wscript.exe": true,
	"cscript": true, "cscript.exe": true,
	"mshta": true, "mshta.exe": true,
}

// interpreterName returns the interpreter program name when token names
// an interpreter (bare, absolute, or any path form), or "" otherwise.
// `/usr/bin/python3` and `/tmp/x/python3` both return "python3".
func interpreterName(token string) string {
	if token == "" {
		return ""
	}
	base := token
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if interpreterTokens[base] {
		return base
	}
	return ""
}

// scratchExecRoots are filesystem prefixes that are world-writable
// scratch space. They carry no trailing slash; a path equal to a root
// or living under one is scratch. /private/* covers macOS, where /tmp
// and /var/tmp are symlinks into /private.
var scratchExecRoots = []string{
	"/tmp", "/var/tmp", "/dev/shm",
	"/private/tmp", "/private/var/tmp",
}

// scratchExec reports whether token executes a file that lives in a
// writable scratch directory. An agent can write a script there,
// `chmod +x` it, and the kernel runs it through its shebang
// interpreter - the guard would otherwise only ever see the path
// token, never the interpreter. #87 Gap 2.
//
// Only path-shaped tokens execute a file directly: a bare name with no
// slash resolves through $PATH, not the working directory. Relative
// paths are anchored against cwd (the PreToolUse payload's CWD); with
// no cwd a relative path cannot be classified and is left to pass.
func scratchExec(token, cwd string) bool {
	if token == "" || !strings.ContainsRune(token, '/') {
		return false
	}
	var abs string
	switch {
	case strings.HasPrefix(token, "/"):
		abs = token
	case cwd != "":
		abs = filepath.Join(cwd, token)
	default:
		return false
	}
	abs = filepath.Clean(abs)
	for _, root := range scratchExecRoots {
		if abs == root || strings.HasPrefix(abs, root+"/") {
			return true
		}
	}
	return false
}
