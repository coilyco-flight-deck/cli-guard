// Command egress demonstrates the per-invocation CONNECT proxy with a
// pinned allowlist. The proxy logs every CONNECT and (in enforce mode)
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
