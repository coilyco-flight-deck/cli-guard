// Command dispatch demonstrates wiring the cli-guard dispatch subsystem
// into a host CLI. dispatch fires `claude` against a real open GitHub
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/audit"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/examples/treebuilders"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/shell"
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
