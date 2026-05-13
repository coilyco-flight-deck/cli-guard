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

	"github.com/coilysiren/cli-guard/examples/treebuilders"
)

func main() {
	if err := treebuilders.Policy().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "rejected:", err)
		os.Exit(2)
	}
}
