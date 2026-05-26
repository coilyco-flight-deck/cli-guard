// Command exitcode demonstrates the public exit-code taxonomy.
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
