// Command specverb-gen is the no-code uv-style driver for a spec-driven
// consumer CLI: gen / lock / skew / run. See docs/specverb.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/specdrv"
	"github.com/urfave/cli/v3"
)

func main() {
	if err := app().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "specverb-gen:", err)
		os.Exit(exitCode(err))
	}
}

// exitCode maps a driver error to a process exit code, so skew drift is
// distinguishable from an offline fetch or any other failure.
func exitCode(err error) int {
	if errors.Is(err, specdrv.ErrSkew) {
		return 3
	}
	return 1
}

// app builds the driver command tree. The shared --guardfile is a persistent
// root flag so every verb reads the same value.
func app() *cli.Command {
	return &cli.Command{
		Name:  "specverb-gen",
		Usage: "no-code driver for a spec-driven consumer CLI (gen / lock / skew / run)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "guardfile",
				Usage: "path to the consumer's KDL Guardfile (default: the lone *.guardfile.kdl in cwd)",
			},
			// --out on the root keeps the legacy one-shot signature working.
			// Local so it does not collide with gen's own --out on subcommands.
			&cli.StringFlag{Name: "out", Hidden: true, Local: true},
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
			gf, err := resolveGuardfile(c)
			if err != nil {
				return err
			}
			return specdrv.Gen(specdrv.Options{GuardfilePath: gf, Out: c.String("out")})
		},
	}
}

func genCmd() *cli.Command {
	return &cli.Command{
		Name:  "gen",
		Usage: "render the consumer main.go (into the cache, or --out for inspection)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "out", Usage: "write main.go here instead of the cache (debug)"},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			gf, err := resolveGuardfile(c)
			if err != nil {
				return err
			}
			return specdrv.Gen(specdrv.Options{GuardfilePath: gf, Out: c.String("out")})
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
			gf, err := resolveGuardfile(c)
			if err != nil {
				return err
			}
			return specdrv.Lock(specdrv.Options{
				GuardfilePath:   gf,
				CLIGuardRef:     c.String("cli-guard-ref"),
				CLIGuardReplace: c.String("cli-guard-replace"),
			})
		},
	}
}

func skewCmd() *cli.Command {
	return &cli.Command{
		Name:  "skew",
		Usage: "report drift between the committed spec lock and live upstream (exit 3 on drift)",
		Action: func(_ context.Context, c *cli.Command) error {
			gf, err := resolveGuardfile(c)
			if err != nil {
				return err
			}
			return specdrv.Skew(specdrv.Options{GuardfilePath: gf})
		},
	}
}

func buildCmd() *cli.Command {
	return &cli.Command{
		Name:  "build",
		Usage: "materialize+build the consumer binary if stale, then write it to --out (a dir or file path)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "out", Value: "bin", Usage: "output directory (binary keeps its Guardfile name) or explicit file path"},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			gf, err := resolveGuardfile(c)
			if err != nil {
				return err
			}
			return specdrv.Build(specdrv.Options{GuardfilePath: gf, Out: c.String("out")})
		},
	}
}

func runCmd() *cli.Command {
	return &cli.Command{
		Name:  "run",
		Usage: "materialize+build the consumer binary if stale, then exec it with the remaining args",
		Action: func(_ context.Context, c *cli.Command) error {
			gf, err := resolveGuardfile(c)
			if err != nil {
				return err
			}
			return specdrv.Run(specdrv.Options{GuardfilePath: gf, Args: c.Args().Slice()})
		},
	}
}

// resolveGuardfile returns the --guardfile value, or discovers the lone
// *.guardfile.kdl in the current directory when the flag is unset.
func resolveGuardfile(c *cli.Command) (string, error) {
	if p := c.String("guardfile"); p != "" {
		return p, nil
	}
	matches, err := filepath.Glob("*.guardfile.kdl")
	if err != nil {
		return "", fmt.Errorf("discover guardfile: %w", err)
	}
	switch len(matches) {
	case 0:
		return "", errors.New("no *.guardfile.kdl in cwd; pass --guardfile")
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple guardfiles in cwd (%v); pass --guardfile", matches)
	}
}
