// Command passthrough demonstrates wrapping an existing binary as an
// audited urfave/cli subcommand. Run with:
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shell"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/examples/treebuilders"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/audit"
)

func main() {
	auditPath := filepath.Join(os.TempDir(), "cli-guard-passthrough.jsonl")
	writer := audit.NewWriter(auditPath)
	defer func() { _ = writer.Close() }()
	if err := writer.Preflight(); err != nil {
		fmt.Fprintln(os.Stderr, "audit preflight:", err)
		_ = writer.Close()
		os.Exit(1) //nolint:gocritic
	}

	runner := &shell.Runner{Resolve: shell.PathResolver}

	if err := treebuilders.Passthrough(runner, writer).Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = writer.Close()
		os.Exit(1) //nolint:gocritic
	}
	fmt.Fprintln(os.Stderr, "audit log:", auditPath)
}
