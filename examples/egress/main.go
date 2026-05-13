// Command egress demonstrates the per-invocation CONNECT proxy with a
// pinned allowlist. The proxy logs every CONNECT and (in enforce mode)
// rejects hosts not on the list with HTTP 403. Used by cli-guard's
// passthrough wrapper to gate package-manager network reach.
//
//	go run ./examples/egress allowed
//	# https://example.com/ -> 200
//
//	go run ./examples/egress denied
//	# https://denied.example.invalid/ -> 403 from proxy
//
// Both runs print the captured egress-row summary at the end so the
// audit-trail shape is visible.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/coilysiren/cli-guard/examples/treebuilders"
)

func main() {
	if err := treebuilders.Egress().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
