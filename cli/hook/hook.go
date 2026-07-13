// Package hook implements the Claude Code PreToolUse hook engine in
// the shared substrate. Each cli-guard consumer wires it in.
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
type Route struct {
	// Token is the bare leading-token to match, e.g. "gh", "brew".
	Token string

	// Hint is the human-readable recovery message. The engine
	// prefixes "<source> hook: blocked bare `<token>`. Recovery: "
	Hint string

	// Extra, when non-nil, is consulted for token-specific suffixes after a
	// match. It receives the matched segment.
	Extra func(segment string) string
}

// IntegrityRule names a binary and the expected absolute paths it may resolve to.
// The binary name is the target identity; the paths are integrity hints.
type IntegrityRule struct {
	Binary       string
	AllowedPaths []string
}

// Protected names a host binary whose direct invocation by an agent is denied.
// Matches by basename, closing the absolute-path bypass a token deny leaves.
type Protected struct {
	// Name is the binary basename to protect, e.g. "gcloud".
	Name string
	// Hint is the recovery sentence appended after the deny. When empty, the
	// engine synthesizes one from Wrappers (or a bare deny if both are empty).
	Hint string
	// Wrappers names the audited wrappers a human routes through instead.
	// Used to synthesize a hint when Hint is empty.
	Wrappers []string
}

// ForbiddenArgv denies a whole command segment by glob, distinguishing read
// from write verbs where the basename-only Protected cannot. See docs/FEATURES.md.
type ForbiddenArgv struct {
	// Description is the required human label, surfaced in the deny message.
	Description string
	// MatchesGlobAny holds POSIX fnmatch patterns (path/filepath.Match grammar,
	// but * and ? cross '/'); the first to match the whole segment wins.
	MatchesGlobAny []string
	// Hint is the optional recovery sentence; empty synthesizes "argv matched
	// <glob>".
	Hint string
}

// Decision is what PreToolUse returns. Block=true means the caller
// should emit Message to stderr and exit with the host's hook-block
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

// PreToolUse evaluates a payload against integrity rules, routes, protected
// basenames, and the trailing forbidden-argv glob deny. Returns
func PreToolUse(payload Payload, source string, rules []IntegrityRule, routes []Route, lookup LookPath, protected []Protected, forbidden ...ForbiddenArgv) Decision {
	if payload.ToolName != "Bash" {
		return Decision{}
	}
	cmd, _ := payload.ToolInput["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		return Decision{}
	}
	integrity := indexIntegrity(rules)
	routeByToken := indexRoutes(routes)
	protectedByBase := indexProtected(protected)
	for _, seg := range SplitSegments(cmd) {
		seg = StripEnvPrefix(strings.TrimSpace(seg))
		if seg == "" {
			continue
		}
		if d := evaluateSegment(seg, source, payload.CWD, integrity, routeByToken, protectedByBase, forbidden, lookup); d.Block {
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

func indexProtected(protected []Protected) map[string]Protected {
	out := map[string]Protected{}
	for _, p := range protected {
		out[Basename(p.Name)] = p
	}
	return out
}

func evaluateSegment(seg, source, cwd string, integrity map[string][]string, routeByToken map[string]Route, protectedByBase map[string]Protected, forbidden []ForbiddenArgv, lookup LookPath) Decision {
	token := LeadingToken(seg)

	// Engine-level arbitrary-code-execution deny, checked before any
	// consumer route. Runs on every segment, so piping a denied
	if name := interpreterName(token); name != "" {
		return Decision{Block: true, Message: fmt.Sprintf(
			"%s hook: blocked interpreter `%s`. Running arbitrary code through an interpreter defeats the command allowlist. This deny holds no matter the spelling: bare, with `-c`, piped behind an allowed command, by absolute path, or via an executable shebang script.",
			source, name)}
	}
	if name := exfilExpansion(seg); name != "" {
		return Decision{Block: true, Message: fmt.Sprintf(
			"%s hook: blocked `%s` with variable expansion or command substitution. The shell expands the value before the guard sees the command, so a non-allowlisted env var or $() / backtick substitution can leak secrets to stdout. Safe expansions: $HOME, $PWD, $USER, $SHELL, $TERM, $LANG, $LC_ALL, $LOGNAME, $HOSTNAME, $OSTYPE. Use a wrapper that audits the value.",
			source, name)}
	}
	if scratchExec(token, cwd) {
		return Decision{Block: true, Message: fmt.Sprintf(
			"%s hook: blocked execution of `%s` from a writable scratch directory. A file under /tmp (or a similar scratch dir) can carry an interpreter shebang, so the kernel runs arbitrary code the moment it is exec'd - the guard never sees the interpreter token. Writing scratch files to /tmp stays allowed; executing them does not.",
			source, token)}
	}

	// Protected-binary deny, basename-aware so an absolute path is denied the
	// same as the bare token. Checked before integrity/route.
	if p, ok := protectedByBase[Basename(token)]; ok {
		return Decision{Block: true, Message: protectedDeny(p, token, source)}
	}

	// Forbidden-argv deny: glob-match the whole segment before falling through
	// to integrity/route. First matching glob across all rules wins.
	if f, glob, ok := matchForbidden(seg, forbidden); ok {
		return Decision{Block: true, Message: forbiddenDeny(f, glob, source)}
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
func SplitSegments(cmd string) []string {
	replacers := []string{"$(", "\n", ")", "\n", "||", "\n", "&&", "\n", "|", "\n", ";", "\n", "&", "\n"}
	r := strings.NewReplacer(replacers...)
	return strings.Split(r.Replace(cmd), "\n")
}

// StripEnvPrefix peels leading `env VAR=val ...` and `sudo` tokens so
// `env FOO=bar gh issue view` classifies the same as bare
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

// Basename returns the final path element of token, e.g. "gcloud" for
// "/opt/homebrew/bin/gcloud". Used so an absolute path cannot slip past a deny.
func Basename(token string) string {
	if i := strings.LastIndexByte(token, '/'); i >= 0 {
		return token[i+1:]
	}
	return token
}

// protectedDeny renders the deny message for a matched protected binary,
// preferring the Hint, else synthesizing from wrappers, else a bare deny.
func protectedDeny(p Protected, token, source string) string {
	base := Basename(token)
	msg := fmt.Sprintf("%s hook: blocked protected binary `%s`. Direct invocation is denied", source, base)
	switch {
	case p.Hint != "":
		return msg + ". Recovery: " + p.Hint
	case len(p.Wrappers) > 0:
		return fmt.Sprintf("%s; route through an audited wrapper: %s.", msg, strings.Join(p.Wrappers, ", "))
	default:
		return msg + "."
	}
}

// matchForbidden returns the first ForbiddenArgv whose glob matches seg and the
// glob that matched, scanning rules then globs in declaration order.
func matchForbidden(seg string, forbidden []ForbiddenArgv) (ForbiddenArgv, string, bool) {
	for _, f := range forbidden {
		for _, glob := range f.MatchesGlobAny {
			if matchGlob(glob, seg) {
				return f, glob, true
			}
		}
	}
	return ForbiddenArgv{}, "", false
}

// matchGlob reports whether seg matches the fnmatch pattern. It substitutes '/'
// with a sentinel before filepath.Match so * and ? cross separators (no FNM_PATHNAME).
func matchGlob(pattern, seg string) bool {
	const sentinel = "\x00"
	p := strings.ReplaceAll(pattern, "/", sentinel)
	s := strings.ReplaceAll(seg, "/", sentinel)
	ok, err := filepath.Match(p, s)
	return err == nil && ok
}

// forbiddenDeny renders the deny message for a matched ForbiddenArgv,
// preferring the Hint, else synthesizing one from the matched glob.
func forbiddenDeny(f ForbiddenArgv, glob, source string) string {
	msg := fmt.Sprintf("%s hook: blocked argv (%s)", source, f.Description)
	if f.Hint != "" {
		return msg + ". Recovery: " + f.Hint
	}
	return fmt.Sprintf("%s: argv matched %q.", msg, glob)
}

// interpreterTokens is the set of program basenames that denote
// arbitrary-code execution: a process whose job is to take a script or
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
func interpreterName(token string) string {
	if token == "" {
		return ""
	}
	base := Basename(token)
	if interpreterTokens[base] {
		return base
	}
	return ""
}

// exfilCommands print arguments verbatim; shell-expanded args leak
// the expansion to stdout before the guard ever sees the value.
var exfilCommands = map[string]bool{
	"echo":   true,
	"printf": true,
}

// safeExpansionNames lists env vars whose values are not secret and
// are routinely echoed for debugging. Their expansion is allowed.
var safeExpansionNames = map[string]bool{
	"HOME": true, "PWD": true, "USER": true, "SHELL": true,
	"TERM": true, "LANG": true, "LC_ALL": true,
	"LOGNAME": true, "HOSTNAME": true, "OSTYPE": true,
}

// exfilExpansion returns the exfil command name on a blocking match,
// "" otherwise. Shell metavars and positional params pass.
func exfilExpansion(seg string) string {
	token := LeadingToken(seg)
	if !exfilCommands[token] {
		return ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(seg, token))
	if rest == "" {
		return ""
	}
	if strings.Contains(rest, "$(") || strings.Contains(rest, "`") {
		return token
	}
	if hasUnsafeVarExpansion(rest) {
		return token
	}
	return ""
}

// hasUnsafeVarExpansion returns true when s contains a $NAME or
// ${NAME} expansion whose name is not in safeExpansionNames.
func hasUnsafeVarExpansion(s string) bool {
	for i := 0; i < len(s); {
		idx := strings.IndexByte(s[i:], '$')
		if idx < 0 {
			return false
		}
		i += idx + 1
		if i >= len(s) {
			return true
		}
		next, blocked := readExpansion(s, i)
		if blocked {
			return true
		}
		i = next
	}
	return false
}

// readExpansion parses one $-expansion starting at i (just past the
// $). Returns the new index and whether the name is non-safe.
func readExpansion(s string, i int) (int, bool) {
	braced := i < len(s) && s[i] == '{'
	if braced {
		i++
	}
	if i < len(s) && isDigit(s[i]) {
		return skipPositional(s, i, braced), false
	}
	nameEnd := scanName(s, i)
	if nameEnd == i {
		return nameEnd, false
	}
	if !safeExpansionNames[s[i:nameEnd]] {
		return nameEnd, true
	}
	return skipBraceClose(s, nameEnd, braced), false
}

func scanName(s string, i int) int {
	for i < len(s) && isNameChar(s[i]) {
		i++
	}
	return i
}

func skipPositional(s string, i int, braced bool) int {
	i++
	return skipBraceClose(s, i, braced)
}

func skipBraceClose(s string, i int, braced bool) int {
	if !braced {
		return i
	}
	for i < len(s) && s[i] != '}' {
		i++
	}
	if i < len(s) {
		i++
	}
	return i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isNameChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
}

// scratchExecRoots are filesystem prefixes that are world-writable
// scratch space. They carry no trailing slash; a path equal to a root
var scratchExecRoots = []string{
	"/tmp", "/var/tmp", "/dev/shm",
	"/private/tmp", "/private/var/tmp",
}

// scratchExec reports whether token executes a file that lives in a
// writable scratch directory. An agent can write a script there,
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
