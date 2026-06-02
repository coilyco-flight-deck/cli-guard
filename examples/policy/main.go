// Command policy demonstrates argv-validation rejection.
package main

import (
	"context"
	"fmt"
	"os"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/examples/treebuilders"
)

func main() {
	if err := treebuilders.Policy().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "rejected:", err)
		os.Exit(2)
	}
}
