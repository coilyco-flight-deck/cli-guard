// Command dispatch demonstrates wiring the cli-guard dispatch subsystem
// into a host CLI. dispatch fires `claude` against a real open GitHub
// issue, in the matching local checkout, headless or interactive.
//
// The package owns the verb logic; the consumer supplies the
// host-specific seams (allowed org, workspace layout, verb pipeline)
// through dispatch.Config. This demo wires the smallest valid Config.
//
// Run with:
//
//	go run ./examples/dispatch dispatch --help
//	go run ./examples/dispatch dispatch headless --dry-run coilysiren/coily#1
//
// Every invocation lands a row in $TMPDIR/cli-guard-dispatch.jsonl.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/coilysiren/cli-guard/audit"
	"github.com/coilysiren/cli-guard/examples/treebuilders"
	"github.com/coilysiren/cli-guard/shell"
)

func main() {
	auditPath := filepath.Join(os.TempDir(), "cli-guard-dispatch.jsonl")
	writer := audit.NewWriter(auditPath)
	defer func() { _ = writer.Close() }()
	if err := writer.Preflight(); err != nil {
		fmt.Fprintln(os.Stderr, "audit preflight:", err)
		_ = writer.Close()
		os.Exit(1) //nolint:gocritic
	}

	runner := &shell.Runner{
		Resolve: shell.PathResolver,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}

	if err := treebuilders.Dispatch(runner, writer).Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = writer.Close()
		os.Exit(1) //nolint:gocritic
	}
	fmt.Fprintln(os.Stderr, "audit log:", auditPath)
}
