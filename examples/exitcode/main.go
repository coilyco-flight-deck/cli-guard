// Command exitcode demonstrates the public exit-code taxonomy.
//
// External orchestrators (CI steps, watchdogs, retry loops) can pattern-
// match on the process exit code to decide retry vs abort vs handoff,
// without parsing stderr. The taxonomy:
//
//	0 - success
//	1 - generic
//	2 - policy denied (argv rejected, deny rule hit)
//	3 - upstream failed (the wrapped binary returned non-zero)
//	4 - internal (config load, manifest miss, coily bug)
//	5 - user error (bad flag, malformed input)
//
// Run any of:
//
//	go run ./examples/exitcode success    ; echo "exit: $?"
//	go run ./examples/exitcode policy     ; echo "exit: $?"
//	go run ./examples/exitcode upstream   ; echo "exit: $?"
//	go run ./examples/exitcode internal   ; echo "exit: $?"
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/coilysiren/cli-guard/exitcode"
	"github.com/urfave/cli/v3"
)

func main() {
	app := &cli.Command{
		Name:    "exitcode-demo",
		Usage:   "show the public exit-code taxonomy",
		Version: "v0.0.0",
		Commands: []*cli.Command{
			{Name: "success", Action: func(_ context.Context, _ *cli.Command) error { return nil }},
			{Name: "policy", Action: func(_ context.Context, _ *cli.Command) error {
				return exitcode.New(exitcode.PolicyDenied, "policy", errors.New("argv rejected"), "fix the input")
			}},
			{Name: "upstream", Action: func(_ context.Context, _ *cli.Command) error {
				return exitcode.New(exitcode.UpstreamFailed, "upstream_failed", errors.New("wrapped tool exited 7"), "check the tool")
			}},
			{Name: "internal", Action: func(_ context.Context, _ *cli.Command) error {
				return exitcode.New(exitcode.Internal, "internal", errors.New("config load failed"), "report a bug")
			}},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if coded := exitcode.From(err); coded != nil {
			os.Exit(coded.Code())
		}
		os.Exit(exitcode.Generic)
	}
}
