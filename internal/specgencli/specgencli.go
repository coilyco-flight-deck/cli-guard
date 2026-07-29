// Package specgencli owns the shared command tree behind the canonical specgen
// binary and its temporary kdl-specs compatibility entrypoint.
package specgencli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/specgen"
	"github.com/urfave/cli/v3"
)

// Run executes the specgen command tree and returns its process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cmd := app()
	cmd.Writer = stdout
	cmd.ErrWriter = stderr
	if err := cmd.Run(ctx, args); err != nil {
		_, _ = fmt.Fprintln(stderr, "specgen:", err)
		return exitCode(err)
	}
	return 0
}

// exitCode maps a driver error to a process exit code, so skew drift is
// distinguishable from an offline fetch or any other failure.
func exitCode(err error) int {
	if errors.Is(err, specgen.ErrSkew) {
		return 3
	}
	return 1
}

// app builds the driver command tree. Shared discovery flags are persistent so
// every verb reads the same project boundary and optional member selector.
func app() *cli.Command {
	return &cli.Command{
		Name:    "specgen",
		Usage:   "no-code driver for a spec-driven consumer CLI (gen / lock / skew / build / run)",
		Version: fmt.Sprintf("%s (cli-guard ref %s)", specgen.DriverVersion(), specgen.DefaultCLIGuardRef()),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "guardfile",
				Usage: "operation KDL member selecting its wrap binary",
			},
			&cli.StringFlag{
				Name:  "project-root",
				Usage: "recursive KDL discovery root (members are identified by parsed wrap declarations)",
			},
			&cli.StringFlag{
				Name:  "skills-out",
				Usage: "explicit skill root; writes <root>/<binary>/SKILL.md and references/commands.yaml",
			},
			// --out on the root keeps the legacy one-shot signature working.
			// Local so it does not collide with gen's own --out on subcommands.
			&cli.StringFlag{Name: "out", Hidden: true, Local: true},
			&cli.StringFlag{Name: "binary", Hidden: true, Local: true},
		},
		Commands: []*cli.Command{
			genCmd(),
			lockCmd(),
			skewCmd(),
			buildCmd(),
			runCmd(),
		},
		// Root action keeps the legacy `--guardfile X --out Y` one-shot working:
		// with --out set and no subcommand, behave as `gen --out Y`.
		Action: func(_ context.Context, c *cli.Command) error {
			if c.String("out") == "" {
				return cli.ShowAppHelp(c)
			}
			return specgen.Gen(options(c, c.String("out"), c.String("binary")))
		},
	}
}

func binaryNameFlag() cli.Flag {
	return &cli.StringFlag{Name: "binary", Usage: "generated CLI/binary name (default: Guardfile wrap binary)"}
}

func genCmd() *cli.Command {
	return &cli.Command{
		Name:  "gen",
		Usage: "render the consumer main.go (into the cache, or --out for inspection)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "out", Usage: "write main.go here instead of the cache (debug)"},
			binaryNameFlag(),
		},
		Action: func(_ context.Context, c *cli.Command) error {
			return specgen.Gen(options(c, c.String("out"), c.String("binary")))
		},
	}
}

func lockCmd() *cli.Command {
	return &cli.Command{
		Name:  "lock",
		Usage: "fetch the upstream spec and freeze the build (writes the spec lock + specverb.lock)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "cli-guard-ref", Usage: "cli-guard module version/commit to pin (default: the driver's own version, else latest)"},
			&cli.StringFlag{Name: "cli-guard-replace", Usage: "local cli-guard checkout to build against (dev locks only)"},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			return specgen.Lock(specgen.Options{
				GuardfilePath:   resolveGuardfile(c),
				ProjectRoot:     c.String("project-root"),
				CLIGuardRef:     c.String("cli-guard-ref"),
				CLIGuardReplace: c.String("cli-guard-replace"),
				SkillsOut:       c.String("skills-out"),
			})
		},
	}
}

func skewCmd() *cli.Command {
	return &cli.Command{
		Name:  "skew",
		Usage: "report drift between the committed spec lock and live upstream (exit 3 on drift)",
		Action: func(_ context.Context, c *cli.Command) error {
			return specgen.Skew(options(c, "", ""))
		},
	}
}

func buildCmd() *cli.Command {
	return &cli.Command{
		Name:  "build",
		Usage: "materialize+build the consumer binary if stale, then write it to --out (a dir or file path)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "out", Value: "bin", Usage: "output directory (binary keeps its generated name) or explicit file path"},
			binaryNameFlag(),
			&cli.StringFlag{Name: "set-version", Usage: "release version stamped into the binary's --version via -ldflags (default \"dev\")"},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			return specgen.Build(specgen.Options{
				GuardfilePath: resolveGuardfile(c),
				ProjectRoot:   c.String("project-root"),
				BinaryName:    c.String("binary"),
				Out:           c.String("out"),
				Version:       c.String("set-version"),
				SkillsOut:     c.String("skills-out"),
			})
		},
	}
}

func runCmd() *cli.Command {
	return &cli.Command{
		Name:  "run",
		Usage: "materialize+build the consumer binary if stale, then exec it with the remaining args",
		Flags: []cli.Flag{
			binaryNameFlag(),
		},
		Action: func(_ context.Context, c *cli.Command) error {
			return specgen.Run(specgen.Options{
				GuardfilePath: resolveGuardfile(c),
				ProjectRoot:   c.String("project-root"),
				BinaryName:    c.String("binary"),
				Args:          c.Args().Slice(),
				SkillsOut:     c.String("skills-out"),
			})
		},
	}
}

// resolveGuardfile returns the --guardfile value verbatim (empty when unset).
// The driver (specgen.loadGroup) owns discovery and the merge-vs-error rules.
func resolveGuardfile(c *cli.Command) string {
	return c.String("guardfile")
}

func options(c *cli.Command, out, binary string) specgen.Options {
	return specgen.Options{
		GuardfilePath: resolveGuardfile(c),
		ProjectRoot:   c.String("project-root"),
		BinaryName:    binary,
		Out:           out,
		SkillsOut:     c.String("skills-out"),
	}
}
