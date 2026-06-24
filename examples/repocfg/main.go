// Command repocfg demonstrates loading a per-repo command allowlist.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/repocfg"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/examples/treebuilders"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/config"
)

func main() {
	// A consumer names its own app dir; repocfg derives the overlay filename
	// from it (".ward" -> .ward/ward.yaml). cli-guard hardcodes no consumer.
	config.SetAppDir(".ward")

	cfg, err := repocfg.LoadDefault()
	if err != nil {
		if errors.Is(err, repocfg.ErrNoConfig) {
			fmt.Fprintln(os.Stderr, "no .ward/ward.yaml found in cwd ancestry")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "load repocfg:", err)
		os.Exit(1)
	}

	if err := treebuilders.Repocfg(cfg).Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
