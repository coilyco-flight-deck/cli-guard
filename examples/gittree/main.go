// Command gittree demonstrates the clean+synced gate.
//
// gittree.CheckClean refuses a verb when the repo's working tree could
// not be reconstructed from git history: uncommitted changes, untracked
// files, detached HEAD, or a branch without an upstream. cli-guard uses
// this to gate repo-declared verbs (build, test, deploy, etc) so every
// invocation is bound to a real commit.
//
// Run inside a clean git repo:
//
//	cd /path/to/clean/repo
//	go run /path/to/cli-guard/examples/gittree build
//	# ok: tree is clean
//
// Then touch a file and re-run:
//
//	echo dirt > /tmp/dirt && cp /tmp/dirt /path/to/clean/repo/dirt
//	go run /path/to/cli-guard/examples/gittree build
//	# refused: working tree is dirty
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/coilysiren/cli-guard/examples/treebuilders"
)

func main() {
	if err := treebuilders.Gittree().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
