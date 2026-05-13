// Command scope demonstrates --commit-scope resolution.
//
// scope.Resolve binds every audit row to a specific git toplevel, so the
// audit log can be reconstructed from git history after the fact. Run
// from inside a git checkout:
//
//	cd /path/to/some/git/repo
//	go run /path/to/cli-guard/examples/scope where
//	# scope: /path/to/some/git/repo
//
// Run from outside any git repo with the default "auto" value:
//
//	cd /tmp && go run /path/to/cli-guard/examples/scope where
//	# error: scope: cwd is not inside a git repo
//
// Pass an explicit path to override:
//
//	go run ./examples/scope --commit-scope=/some/repo where
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/coilysiren/cli-guard/scope"
	"github.com/coilysiren/cli-guard/verb"
	"github.com/urfave/cli/v3"
)

func main() {
	app := &cli.Command{
		Name:    "scope-demo",
		Usage:   "show --commit-scope resolution",
		Version: "v0.0.0",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  verb.CommitScopeFlag,
				Value: "auto",
				Usage: "bind audit rows to a commit scope (auto resolves to git toplevel of cwd)",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "where",
				Usage: "print the resolved commit scope",
				Action: func(_ context.Context, c *cli.Command) error {
					cwd, _ := os.Getwd()
					flagVal := c.String(verb.CommitScopeFlag)
					resolved, err := scope.Resolve(flagVal, "", cwd)
					if err != nil {
						return fmt.Errorf("scope.Resolve: %w", err)
					}
					fmt.Printf("scope: %s\n", resolved)
					return nil
				},
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
