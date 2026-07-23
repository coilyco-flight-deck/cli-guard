// Command specgen is the no-code driver for generating guarded consumer CLIs.
package main

import (
	"context"
	"os"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/internal/specgencli"
)

func main() {
	if code := specgencli.Run(context.Background(), os.Args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}
