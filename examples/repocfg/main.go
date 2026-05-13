// Command repocfg demonstrates loading a per-repo command allowlist.
//
// repocfg.Discover walks up from cwd looking for a coily.yaml that
// declares named verbs (test, lint, build, ...). Each verb is a fixed
// argv that has already been argv-validated at load time, so what gets
// exposed at runtime can never be a shell pipeline or contain
// metacharacters.
//
// Run from inside this example's directory:
//
//	cd examples/repocfg
//	go run . list
//	# build: go build ./...
//	# greet: echo hello world
//
//	go run . run greet
//	# hello world
//
// The .coily/coily.yaml sibling file is the declaration. Edit it and
// re-run to see the surface change.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/coilysiren/cli-guard/repocfg"
	"github.com/urfave/cli/v3"
)

func main() {
	cfg, err := repocfg.LoadDefault()
	if err != nil {
		if errors.Is(err, repocfg.ErrNoConfig) {
			fmt.Fprintln(os.Stderr, "no .coily/coily.yaml found in cwd ancestry")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "load repocfg:", err)
		os.Exit(1)
	}

	app := &cli.Command{
		Name:    "repocfg-demo",
		Usage:   "show per-repo command allowlist loading",
		Version: "v0.0.0",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "print the verbs declared in coily.yaml",
				Action: func(_ context.Context, _ *cli.Command) error {
					for _, v := range cfg.Commands {
						fmt.Printf("%s: %s\n", v.Name, strings.Join(v.Argv, " "))
					}
					return nil
				},
			},
			{
				Name:      "run",
				Usage:     "execute a declared verb by name",
				ArgsUsage: "<verb>",
				Action: func(ctx context.Context, c *cli.Command) error {
					name := c.Args().First()
					if name == "" {
						return errors.New("usage: run <verb>")
					}
					for _, v := range cfg.Commands {
						if v.Name == name {
							cmd := exec.CommandContext(ctx, v.Argv[0], v.Argv[1:]...) //nolint:gosec // tokens validated at load time
							cmd.Stdout = os.Stdout
							cmd.Stderr = os.Stderr
							return cmd.Run()
						}
					}
					return fmt.Errorf("no such verb: %s", name)
				},
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
