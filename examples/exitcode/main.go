// Command exitcode demonstrates the public exit-code taxonomy.
package main

import (
	"context"
	"fmt"
	"os"

	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/internal/clitrees"
	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/pkg/exitcode"
)

func main() {
	if err := clitrees.Exitcode().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if coded := exitcode.From(err); coded != nil {
			os.Exit(coded.Code())
		}
		os.Exit(exitcode.Generic)
	}
}
