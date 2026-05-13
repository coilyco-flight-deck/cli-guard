// Command policy demonstrates argv-validation rejection.
//
// Two leaf commands exercise the same policy.ValidateArgs gate that
// verb.Wrap installs by default. Shell metacharacters in a positional
// argument or named flag value are refused.
//
//	go run ./examples/policy safe foo
//	# ok
//
//	go run ./examples/policy unsafe 'foo; rm -rf /'
//	# error: argv contains a shell metacharacter that cli-guard refuses to forward
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/coilysiren/cli-guard/policy"
	"github.com/urfave/cli/v3"
)

func main() {
	app := &cli.Command{
		Name:    "policy-demo",
		Usage:   "show shell-metacharacter argv rejection",
		Version: "v0.0.0",
		Commands: []*cli.Command{
			{
				Name:      "safe",
				Usage:     "validates a single positional arg",
				ArgsUsage: "<value>",
				Action: func(_ context.Context, c *cli.Command) error {
					vals := c.Args().Slice()
					if err := policy.ValidateArgSlice("positional", vals); err != nil {
						return err
					}
					fmt.Printf("accepted: %v\n", vals)
					return nil
				},
			},
			{
				Name:      "unsafe",
				Usage:     "demonstrate the rejection path",
				ArgsUsage: "<value-with-shell-metachar>",
				Action: func(_ context.Context, c *cli.Command) error {
					vals := c.Args().Slice()
					if err := policy.ValidateArgSlice("positional", vals); err != nil {
						return err
					}
					fmt.Printf("accepted (unexpected): %v\n", vals)
					return nil
				},
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "rejected:", err)
		os.Exit(2)
	}
}
