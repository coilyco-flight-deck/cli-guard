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

	"github.com/coilysiren/cli-guard/examples/treebuilders"
)

func main() {
	if err := treebuilders.Scope().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
