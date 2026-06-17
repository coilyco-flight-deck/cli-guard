// The execverb engine: builds a guarded cli tree from an exec-dialect
// Guardfile, one passthrough leaf per `can run` grant. See docs/execverb.md.

package execverb

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/awsgate"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/valuesource"
	"github.com/urfave/cli/v3"
)

// Runner fires the resolved command; env is the `NAME=VALUE` overrides to layer
// over the inherited environment. Injected for tests; nil execs for real.
type Runner func(ctx context.Context, bin string, argv, env []string) error

// Config is everything the engine needs to build a command tree.
type Config struct {
	// Guardfile is the parsed exec-dialect policy.
	Guardfile *Guardfile

	// Wrap adapts a verb.Spec into a guarded cli.ActionFunc (the audit + argv
	// pipeline). nil mounts the bare action, for doc rendering only.
	Wrap func(verb.Spec) cli.ActionFunc

	// Providers registers the value resolvers a guardfile `env` source names;
	// cli-guard merges its built-ins (env, file, literal). Resolved at exec time.
	Providers map[string]valuesource.Provider

	// Run fires the command. nil execs for real.
	Run Runner
}

// Build assembles the guarded command tree and returns the Guardfile group's
// leaf command (e.g. `git`). Fails closed: a malformed grant is an error.
func Build(cfg Config) (*cli.Command, error) {
	gf := cfg.Guardfile
	if gf == nil {
		return nil, fmt.Errorf("execverb: Config.Guardfile is nil")
	}
	wrap := cfg.Wrap
	if wrap == nil {
		wrap = func(s verb.Spec) cli.ActionFunc { return s.Action }
	}
	run := cfg.Run
	if run == nil {
		run = realRunner
	}
	providers := valuesource.Merge(cfg.Providers)
	root := &cli.Command{
		Name:  gf.Group[len(gf.Group)-1],
		Usage: fmt.Sprintf("guarded %s verbs (exec dialect)", strings.Join(gf.Group, " ")),
	}
	for _, g := range gf.Grants {
		if g.Wildcard {
			if len(gf.Grants) != 1 {
				return nil, fmt.Errorf("execverb: `can run *` must be the only grant (fail-closed)")
			}
			return mountWildcard(root, gf, g, wrap, run, providers)
		}
		if err := mountGrant(root, gf, g, wrap, run, providers); err != nil {
			return nil, err
		}
	}
	return root, nil
}

// mountWildcard turns the group itself into one open passthrough leaf; the
// grant's gates and flag policy still decide whether the call happens.
func mountWildcard(root *cli.Command, gf *Guardfile, g Grant, wrap func(verb.Spec) cli.ActionFunc, run Runner, providers map[string]valuesource.Provider) (*cli.Command, error) {
	gates, err := buildGates(g)
	if err != nil {
		return nil, err
	}
	root.Usage = leafUsage(gf, g)
	root.SkipFlagParsing = true
	root.Action = wrap(verb.Spec{
		Name: strings.Join(gf.Group, "."),
		ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
			return nil, c.Args().Slice()
		},
		Action: actionFor(gf, g, gates, run, providers),
	})
	return root, nil
}

// Mount builds the guarded group and grafts it onto root, generating the
// intermediate path groups the Guardfile names, mirroring specverb.Mount.
func Mount(root *cli.Command, cfg Config) error {
	if root == nil {
		return fmt.Errorf("execverb: Mount root is nil")
	}
	group, err := Build(cfg)
	if err != nil {
		return err
	}
	path := cfg.Guardfile.Group
	parent := root
	if len(path) > 1 {
		for _, seg := range path[1 : len(path)-1] {
			parent = findOrCreateGroup(parent, seg)
		}
	}
	parent.Commands = append(parent.Commands, group)
	return nil
}

// mountGrant places one grant's leaf at its subcommand path under root.
func mountGrant(root *cli.Command, gf *Guardfile, g Grant, wrap func(verb.Spec) cli.ActionFunc, run Runner, providers map[string]valuesource.Provider) error {
	if len(g.Subcommand) == 0 {
		return fmt.Errorf("execverb: grant with empty subcommand")
	}
	parent := root
	for _, seg := range g.Subcommand[:len(g.Subcommand)-1] {
		parent = findOrCreateGroup(parent, seg)
	}
	leafName := g.Subcommand[len(g.Subcommand)-1]
	if findChild(parent, leafName) != nil {
		return fmt.Errorf("execverb: duplicate grant for subcommand %q", strings.Join(g.Subcommand, " "))
	}
	verbName := strings.Join(gf.Group, ".") + "." + strings.Join(g.Subcommand, ".")
	gates, err := buildGates(g)
	if err != nil {
		return err
	}
	parent.Commands = append(parent.Commands, &cli.Command{
		Name:            leafName,
		Usage:           leafUsage(gf, g),
		SkipFlagParsing: true, // every arg passes through to the wrapped binary
		Action: wrap(verb.Spec{
			Name: verbName,
			ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
				return nil, c.Args().Slice()
			},
			Action: actionFor(gf, g, gates, run, providers),
		}),
	})
	return nil
}

// gateFunc is one built preflight gate: a non-nil error denies the call.
type gateFunc func(argv []string) error

// gateRegistry maps Guardfile gate names onto builders. Unknown names fail
// closed at build time, so a typo can never become a silently absent gate.
var gateRegistry = map[string]func(GateSpec) gateFunc{
	"aws-read": awsReadGate,
}

// awsReadGate adapts awsgate's sensitive-read denial to the gate contract.
func awsReadGate(gs GateSpec) gateFunc {
	g := awsgate.Gate{Patterns: gs.Patterns, AllowPatterns: gs.Allow}
	return func(argv []string) error {
		token, pattern, denied := g.Check(argv)
		if !denied {
			return nil
		}
		return fmt.Errorf("read-only aws denied: %q matched the sensitive-read pattern %q (add an allow glob to proceed deliberately)", token, pattern)
	}
}

// buildGates resolves a grant's gate specs against the registry, fail-closed.
func buildGates(g Grant) ([]gateFunc, error) {
	var gates []gateFunc
	for _, gs := range g.Gates {
		build, ok := gateRegistry[gs.Name]
		if !ok {
			return nil, fmt.Errorf("execverb: grant %q: unknown gate %q (fail-closed)", g.subcommandLabel(), gs.Name)
		}
		gates = append(gates, build(gs))
	}
	return gates, nil
}

// actionFor returns the leaf action: gates, flag policy, env resolution, then
// exec with the fixed prefix + subcommand + caller args (all immutable).
func actionFor(gf *Guardfile, g Grant, gates []gateFunc, run Runner, providers map[string]valuesource.Provider) cli.ActionFunc {
	return func(ctx context.Context, c *cli.Command) error {
		args := c.Args().Slice()
		for _, gate := range gates {
			if err := gate(args); err != nil {
				return exitcode.New(exitcode.UserError, "user_error", err, "this call is refused by a Guardfile gate")
			}
		}
		if err := checkWhens(args, g); err != nil {
			return exitcode.New(exitcode.UserError, "user_error", err, "this call is refused by a Guardfile guard")
		}
		if err := checkFlagPolicy(args, g); err != nil {
			return exitcode.New(exitcode.UserError, "user_error", err, "this flag is refused by the Guardfile policy")
		}
		env, err := resolveEnv(ctx, gf, providers)
		if err != nil {
			return exitcode.New(exitcode.Internal, "internal", err, "check the env value provider address and credentials")
		}
		argv := append(append(append([]string{}, gf.ArgvPrefix...), g.ExecArgv()...), args...)
		if err := run(ctx, gf.Bin, argv, env); err != nil {
			return exitcode.New(exitcode.UpstreamFailed, "upstream_failed", err, "the wrapped command failed")
		}
		return nil
	}
}

// resolveEnv reads each env injection through its provider into `NAME=VALUE`
// overrides. Fails closed: a missing provider or error aborts before any exec.
func resolveEnv(ctx context.Context, gf *Guardfile, providers map[string]valuesource.Provider) ([]string, error) {
	if len(gf.Env) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(gf.Env))
	for _, e := range gf.Env {
		v, err := valuesource.Resolve(ctx, providers, e.Provider, e.Address)
		if err != nil {
			return nil, fmt.Errorf("resolve env %s from %s %s: %w", e.Name, e.Provider, e.Address, err)
		}
		out = append(out, e.Name+"="+v)
	}
	return out, nil
}

// checkFlagPolicy enforces the grant's flag rules over the caller args:
// strict allowlist when AllowFlags is set, otherwise default-allow minus denials.
func checkFlagPolicy(args []string, g Grant) error {
	allow := map[string]bool{}
	for _, f := range g.AllowFlags {
		allow[f] = true
	}
	deny := map[string]bool{}
	for _, f := range g.DenyFlags {
		deny[f] = true
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, _, _ := strings.Cut(a, "=")
		if deny[name] {
			return fmt.Errorf("flag %q is denied for `%s`", name, strings.Join(g.Subcommand, " "))
		}
		if len(allow) > 0 && !allow[name] {
			return fmt.Errorf("flag %q is not in the allowlist for `%s`", name, strings.Join(g.Subcommand, " "))
		}
	}
	return nil
}

// checkWhens enforces the grant's `when` / `deny-when` guards over the caller
// args. The first guard to refuse stops the call, before any exec happens.
func checkWhens(args []string, g Grant) error {
	for _, wc := range g.Whens {
		if err := evalWhen(wc, g, args); err != nil {
			return err
		}
	}
	return nil
}

// evalWhen resolves a guard's selector and applies its match rule: `when`
// refuses on no match, `deny-when` on a match. See docs/execverb.md.
func evalWhen(wc WhenClause, g Grant, args []string) error {
	if wc.OnlyReads {
		full := append(append([]string{}, g.Subcommand...), args...)
		if !awsgate.IsReadOnly(full) {
			return nil // guard is scoped to reads; this is not one
		}
	}
	val, pat, matched := firstMatch(resolveSelector(wc.Selector, args), wc.Patterns)
	label := g.subcommandLabel()
	if wc.Deny {
		if matched {
			return fmt.Errorf("`%s` denied: %s %q matched %q", label, wc.Selector, val, pat)
		}
		return nil
	}
	if !matched {
		return fmt.Errorf("`%s` denied: %s did not match any allowed pattern %v", label, wc.Selector, wc.Patterns)
	}
	return nil
}

// firstMatch returns the first (value, pattern) pair where a selector value
// matches a glob, case-insensitively, with the aws-read gate's `*` semantics.
func firstMatch(values, patterns []string) (val, pat string, ok bool) {
	for _, v := range values {
		for _, p := range patterns {
			if awsgate.GlobMatch(strings.ToLower(p), strings.ToLower(v)) {
				return v, p, true
			}
		}
	}
	return "", "", false
}

// resolveSelector reads the argv slot a selector names: `any-arg`, `argN`, or
// the `--<selector>` flag value. Selector forms: docs/execverb.md.
func resolveSelector(sel string, args []string) []string {
	switch {
	case sel == "any-arg":
		return awsgate.Positionals(args)
	case strings.HasPrefix(sel, "arg") && isAllDigits(sel[len("arg"):]):
		idx, _ := strconv.Atoi(sel[len("arg"):])
		pos := awsgate.Positionals(args)
		if idx >= 0 && idx < len(pos) {
			return []string{pos[idx]}
		}
		return nil
	default:
		if v, ok := flagValue(args, "--"+sel); ok {
			return []string{v}
		}
		return nil
	}
}

// isAllDigits reports whether s is one or more ASCII digits (the `argN` index).
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// flagValue returns flag's value in args (`--flag value` or `--flag=value`).
// The bool reports presence; a valueless `--flag` is present, empty.
func flagValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag {
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		}
		if strings.HasPrefix(a, flag+"=") {
			return a[len(flag)+1:], true
		}
	}
	return "", false
}

// leafUsage renders the one-line help: the real invocation (ExecArgv reflects
// any `argv` override, so it shows what runs) plus the policy.
func leafUsage(gf *Guardfile, g Grant) string {
	invocation := append(append([]string{}, gf.ArgvPrefix...), g.ExecArgv()...)
	u := fmt.Sprintf("exec: %s %s", gf.Bin, strings.TrimSpace(strings.Join(invocation, " ")))
	if g.Wildcard {
		u += " <args...> (open passthrough)"
	}
	if g.Describe != "" {
		u += " - " + g.Describe
	}
	return u
}

// realRunner execs bin with inherited stdio, the production Runner. env layers
// the resolved `NAME=VALUE` overrides on top of the inherited environment.
func realRunner(ctx context.Context, bin string, argv, env []string) error {
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.Run()
}

// findChild returns parent's child named name, nil when absent.
func findChild(parent *cli.Command, name string) *cli.Command {
	for _, c := range parent.Commands {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// findOrCreateGroup returns parent's child named name, creating an empty group
// for an intermediate path segment so it is mounted once.
func findOrCreateGroup(parent *cli.Command, name string) *cli.Command {
	if c := findChild(parent, name); c != nil {
		return c
	}
	g := &cli.Command{Name: name, Usage: name + " operations"}
	parent.Commands = append(parent.Commands, g)
	return g
}
