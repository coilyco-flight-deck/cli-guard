// Command policy demonstrates argv-validation rejection.
package main

import (
	"context"
	"fmt"
	"os"

	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/internal/clitrees"
)

func main() {
	if err := clitrees.Policy().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "rejected:", err)
		os.Exit(2)
	}
}
