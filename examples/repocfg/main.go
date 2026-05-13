// Command repocfg demonstrates loading a per-repo command allowlist.
//
// repocfg.Discover walks up from cwd looking for a coily.yaml that
// declares named verbs (test, lint, build, ...). Each verb is a fixed
// argv that has already been argv-validated at load time, so what gets
// exposed at runtime can never be a shell pipeline or contain
// metacharacters.
//
// Run from inside this example's directory:
//
//	cd examples/repocfg
//	go run . list
//	# build: go build ./...
//	# greet: echo hello world
//
//	go run . run greet
//	# hello world
//
// The .coily/coily.yaml sibling file is the declaration. Edit it and
// re-run to see the surface change.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/coilysiren/cli-guard/examples/treebuilders"
	"github.com/coilysiren/cli-guard/repocfg"
)

func main() {
	cfg, err := repocfg.LoadDefault()
	if err != nil {
		if errors.Is(err, repocfg.ErrNoConfig) {
			fmt.Fprintln(os.Stderr, "no .coily/coily.yaml found in cwd ancestry")
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
