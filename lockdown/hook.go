package lockdown

import (
	"fmt"
	"sort"
	"strings"
)

// renderBinaryCheck emits the shell block that rejects invocations of
// the wrapper binary outside its allowed install paths. Catches both
func renderBinaryCheck(d *Driver) string {
	allowed := strings.Join(d.BinaryAllowedPaths, ", ")
	binary := d.BinaryName
	var b strings.Builder
	titled := strings.ToUpper(binary[:1]) + binary[1:]
	fmt.Fprintf(&b, `  # %[2]s binary check: reject any invocation that resolves to a %[1]s
  # binary outside the homebrew install paths. Catches bare-name (PATH
  # lookup) and explicit-path forms (absolute or */%[1]s). This is a
  # content-based check, not a leading-token literal match, so it lives
  # outside the case statement below.
  seg_first=${seg%%%% *}
  case "$seg_first" in
    %[1]s)
      %[1]s_path=$(command -v %[1]s 2>/dev/null || true)
      case "$%[1]s_path" in
        "") ;; # not on PATH; let bash report command-not-found
`, binary, titled)
	for _, p := range d.BinaryAllowedPaths {
		fmt.Fprintf(&b, "        %q) ;;\n", p)
	}
	fmt.Fprintf(&b, "        *) printf 'lockdown: blocked: %s resolves to %%s, which is not a homebrew install (allowed: %s)\\n' \"$%s_path\" >&2; exit 2 ;;\n", binary, allowed, binary)
	fmt.Fprintf(&b, `      esac
      ;;
    */%s)
      case "$seg_first" in
`, binary)
	for _, p := range d.BinaryAllowedPaths {
		fmt.Fprintf(&b, "        %q) ;;\n", p)
	}
	fmt.Fprintf(&b, "        *) printf 'lockdown: blocked: %s binary at %%s is not a homebrew install (allowed: %s)\\n' \"$seg_first\" >&2; exit 2 ;;\n", binary, allowed)
	b.WriteString(`      esac
      ;;
  esac
`)
	return b.String()
}

// renderScratchExecCheck emits the shell block that denies executing a
// file from a writable scratch directory (coilysiren/cli-guard#87 Gap
func renderScratchExecCheck() string {
	return `  # Scratch-dir execution check (cli-guard#87 Gap 2): reject executing
  # a file that lives under a writable scratch directory.
  case "$seg_first" in
    /tmp/*|/var/tmp/*|/dev/shm/*|/private/tmp/*|/private/var/tmp/*)
      printf 'lockdown: blocked: execution of %s from a writable scratch directory. A file there can carry an interpreter shebang and run arbitrary code. Writing scratch files to /tmp stays allowed; executing them does not.\n' "$seg_first" >&2
      exit 2
      ;;
  esac
`
}

// renderHookHeader emits the script preamble: shebang, header comment,
// stdin parse, segment split, and check_segment open through env strip.
func renderHookHeader() string {
	return `#!/bin/sh
# Auto-generated. Do not edit by hand; regenerate via the host CLI's
# lockdown apply command.
#
# PreToolUse Bash hook for Claude Code Desktop, where the built-in
# permissions.deny matcher is silently bypassed (verified 2026-04-23 on
# v2.1.119). On the CLI the built-in matcher is the primary gate; this
# hook is defense-in-depth there. On Desktop this hook is the only gate.
#
# Reads tool-input JSON on stdin, extracts tool_input.command, splits on
# common shell separators, strips leading "env VAR=val" tokens, then
# applies content-based and prefix-based deny checks. Exits 2 with a
# stderr message on hit.

set -u

INPUT=$(cat)
# Pull tool_input.command via a single-line sed. JSON parsing in pure sh
# is fragile; this matches Claude Code's well-formed input. Backslash-
# escapes in the original command are not unescaped here - the leading-
# token compare is robust to that as long as the binary name itself does
# not contain escapes (it never does in practice).
CMD=$(printf '%s' "$INPUT" | sed -n 's/.*"command"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
if [ -z "$CMD" ]; then
  exit 0
fi

# Inline-authoring check (cli-guard#51): reject defining a shell function
# or using eval inside an ad-hoc Bash invocation. Both author AND run an
# arbitrary program with no on-disk artifact to inspect - the same
# no-artifact laundering renderScratchExecCheck blocks for files. Runs on
# the raw $CMD before the segment split below, because that split
# fragments a function body across separators, so no single segment then
# leads with a denied token (the function name and "eval" both look
# benign as leading tokens). Remediation: write the logic to a file in
# your repo and execute it directly (./script.sh), which leaves an
# inspectable artifact the scan can see before it runs.
if printf '%s' "$CMD" | grep -qE '[A-Za-z_][A-Za-z0-9_]*[[:space:]]*\(\)[[:space:]]*\{|function[[:space:]]+[A-Za-z_][A-Za-z0-9_]*[[:space:]]*(\(\))?[[:space:]]*\{'; then
  printf 'lockdown: blocked: inline shell function definition. Define-and-execute in one Bash call leaves no inspectable artifact. Write the script to a file in your repo and run it directly: ./script.sh\n' >&2
  exit 2
fi
if printf '%s' "$CMD" | grep -qE '(^|[;|&])[[:space:]]*eval([[:space:]]|$)'; then
  printf 'lockdown: blocked: inline eval. Dynamic code execution with no on-disk artifact. Write the script to a file in your repo and run it directly: ./script.sh\n' >&2
  exit 2
fi

# Split CMD into segments at shell separators. A segment is the leading
# command of a pipeline / boolean / sequence / command substitution.
SEGMENTS=$(printf '%s\n' "$CMD" | awk '
{
  s = $0
  gsub(/\$\(/, "\n", s)
  gsub(/\)/,   "\n", s)
  gsub(/\|\|/, "\n", s)
  gsub(/&&/,   "\n", s)
  gsub(/[|;&]/, "\n", s)
  print s
}')

check_segment() {
  seg=$1
  # Strip leading whitespace.
  seg=$(printf '%s' "$seg" | sed -E 's/^[[:space:]]+//')
  [ -z "$seg" ] && return 0
  # Strip leading "env VAR=val ..." tokens (env on its own is denied
  # anyway, but env-prefixed invocations of an allowed binary should be
  # tested against the underlying binary).
  while printf '%s' "$seg" | grep -qE '^env( +[A-Za-z_][A-Za-z0-9_]*=[^ ]+)+( +|$)'; do
    seg=$(printf '%s' "$seg" | sed -E 's/^env( +[A-Za-z_][A-Za-z0-9_]*=[^ ]+)+( +|$)//')
  done
`
}

// renderHookFooter emits the closing brace of check_segment and the
// segment-iteration loop.
func renderHookFooter() string {
	return `}

# Iterate segments. heredoc is a single-process loop so an exit inside
# check_segment terminates the whole script.
while IFS= read -r seg; do
  check_segment "$seg"
done <<EOF
$SEGMENTS
EOF

exit 0
`
}

// renderDenyPrefixCase emits the case statement that matches each deny
// prefix as the segment's leading token (with or without trailing args).
func renderDenyPrefixCase(prefixes []string, recovery map[string]string) string {
	var b strings.Builder
	b.WriteString("  case \"$seg\" in\n")
	for _, p := range prefixes {
		// case patterns: literal-quoted prefix and prefix-followed-by-space.
		// POSIX sh interprets "..." as literal in case patterns.
		msg := fmt.Sprintf("lockdown: blocked by deny rule: %s", p)
		if rec, ok := recovery[p]; ok {
			msg = fmt.Sprintf("%s. Recovery: use `%s ...` (audited wrapper).", msg, rec)
		}
		fmt.Fprintf(&b, "    %q|%q*) printf '%s\\n' >&2; exit 2 ;;\n",
			p, p+" ", msg)
	}
	b.WriteString("  esac\n")
	return b.String()
}

// extractBashDenyPrefixes pulls the leading-token shape out of every
// Bash(<prefix>:*) entry in the deny list. Other shapes (PowerShell,
func extractBashDenyPrefixes(deny []string) []string {
	const pre = "Bash("
	const suf = ":*)"
	seen := map[string]bool{}
	out := make([]string, 0, len(deny))
	for _, rule := range deny {
		if !strings.HasPrefix(rule, pre) || !strings.HasSuffix(rule, suf) {
			continue
		}
		prefix := strings.TrimSuffix(strings.TrimPrefix(rule, pre), suf)
		if prefix == "" || seen[prefix] {
			continue
		}
		seen[prefix] = true
		out = append(out, prefix)
	}
	sort.Strings(out)
	return out
}

// claudeCodeRenderHookScript emits the per-repo PreToolUse hook for the
// Claude Code runtime: header + binary check + deny-prefix case +
func claudeCodeRenderHookScript(d *Defaults, drv *Driver) (string, error) {
	prefixes := extractBashDenyPrefixes(d.Deny)
	if len(prefixes) == 0 {
		return "", fmt.Errorf("lockdown: no Bash(...:*) deny rules to render")
	}
	return renderHookHeader() + renderBinaryCheck(drv) + renderScratchExecCheck() + renderDenyPrefixCase(prefixes, drv.WrapperRecovery) + renderHookFooter(), nil
}

// claudeCodeRenderUserHookScript emits the user-level PreToolUse hook:
// header + binary check + footer (no deny-prefix case). Wired into the
func claudeCodeRenderUserHookScript(drv *Driver) string {
	return renderHookHeader() + renderBinaryCheck(drv) + renderHookFooter()
}
