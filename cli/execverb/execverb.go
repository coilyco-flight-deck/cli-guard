// The execverb engine: builds a guarded cli tree from an exec-dialect
// Guardfile, one passthrough leaf per `can run` grant. See docs/execverb.md.

package execverb

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"github.com/urfave/cli/v3"
)

// Runner fires the resolved command. Injected so tests capture argv; nil uses
// the real exec with inherited stdio.
type Runner func(ctx context.Context, bin string, argv []string) error

// Config is everything the engine needs to build a command tree.
type Config struct {
	// Guardfile is the parsed exec-dialect policy.
	Guardfile *Guardfile

	// Wrap adapts a verb.Spec into a guarded cli.ActionFunc (the audit + argv
	// pipeline). nil mounts the bare action, for doc rendering only.
	Wrap func(verb.Spec) cli.ActionFunc

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
	root := &cli.Command{
		Name:  gf.Group[len(gf.Group)-1],
		Usage: fmt.Sprintf("guarded %s verbs (exec dialect)", strings.Join(gf.Group, " ")),
	}
	for _, g := range gf.Grants {
		if err := mountGrant(root, gf, g, wrap, run); err != nil {
			return nil, err
		}
	}
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
func mountGrant(root *cli.Command, gf *Guardfile, g Grant, wrap func(verb.Spec) cli.ActionFunc, run Runner) error {
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
	parent.Commands = append(parent.Commands, &cli.Command{
		Name:            leafName,
		Usage:           leafUsage(gf, g),
		SkipFlagParsing: true, // every arg passes through to the wrapped binary
		Action: wrap(verb.Spec{
			Name: verbName,
			ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
				return nil, c.Args().Slice()
			},
			Action: actionFor(gf, g, run),
		}),
	})
	return nil
}

// actionFor returns the leaf action: flag policy, then exec with the fixed
// prefix + subcommand + caller args. The caller can never alter bin or prefix.
func actionFor(gf *Guardfile, g Grant, run Runner) cli.ActionFunc {
	return func(ctx context.Context, c *cli.Command) error {
		args := c.Args().Slice()
		if err := checkFlagPolicy(args, g); err != nil {
			return exitcode.New(exitcode.UserError, "user_error", err, "this flag is refused by the Guardfile policy")
		}
		argv := append(append(append([]string{}, gf.ArgvPrefix...), g.Subcommand...), args...)
		if err := run(ctx, gf.Bin, argv); err != nil {
			return exitcode.New(exitcode.UpstreamFailed, "upstream_failed", err, "the wrapped command failed")
		}
		return nil
	}
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

// leafUsage renders the one-line help: the real invocation plus the policy.
func leafUsage(gf *Guardfile, g Grant) string {
	u := fmt.Sprintf("exec: %s %s", gf.Bin, strings.Join(append(append([]string{}, gf.ArgvPrefix...), g.Subcommand...), " "))
	if g.Describe != "" {
		u += " - " + g.Describe
	}
	return u
}

// realRunner execs bin with inherited stdio, the production Runner.
func realRunner(ctx context.Context, bin string, argv []string) error {
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
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
