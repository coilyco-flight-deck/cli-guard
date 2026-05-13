// Command demo is a tiny urfave/cli v3 application that exercises the
// cli-guard framework primitives. Run with:
//
//	go run ./examples/demo hello world
//
// Audit rows land in $TMPDIR/cli-guard-demo.jsonl.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/coilysiren/cli-guard/audit"
	"github.com/coilysiren/cli-guard/verb"
	"github.com/urfave/cli/v3"
)

func main() {
	auditPath := filepath.Join(os.TempDir(), "cli-guard-demo.jsonl")
	writer := audit.NewWriter(auditPath)
	defer writer.Close()
	if err := writer.Preflight(); err != nil {
		fmt.Fprintln(os.Stderr, "cli-guard-demo: audit preflight:", err)
		os.Exit(1)
	}

	app := &cli.Command{
		Name:    "demo",
		Usage:   "tiny cli-guard demo app",
		Version: "v0.0.0",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  verb.CommitScopeFlag,
				Value: "auto",
				Usage: "bind audit rows to a commit scope",
			},
		},
		Commands: []*cli.Command{
			{
				Name:      "hello",
				Usage:     "print a greeting",
				ArgsUsage: "<name>",
				Action: verb.Wrap(verb.Spec{
					Name: "hello",
					ArgsFunc: func(c *cli.Command) (map[string]string, []string) {
						return nil, c.Args().Slice()
					},
					Action: func(_ context.Context, c *cli.Command) error {
						name := c.Args().First()
						if name == "" {
							name = "world"
						}
						fmt.Printf("hello, %s\n", name)
						return nil
					},
				}, writer),
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "cli-guard-demo:", err)
		os.Exit(1)
	}
}
