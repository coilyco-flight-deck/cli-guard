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
	"fmt"
	"os"

	"github.com/coilysiren/cli-guard/examples/treebuilders"
	"github.com/coilysiren/cli-guard/exitcode"
)

func main() {
	if err := treebuilders.Exitcode().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if coded := exitcode.From(err); coded != nil {
			os.Exit(coded.Code())
		}
		os.Exit(exitcode.Generic)
	}
}
