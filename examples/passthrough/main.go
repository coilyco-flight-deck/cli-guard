// Command passthrough demonstrates wrapping an existing binary as an
// audited urfave/cli subcommand. Run with:
//
//	go run ./examples/passthrough -- echo hello world
//
// Every invocation lands a row in $TMPDIR/cli-guard-passthrough.jsonl.
// Try a deny path:
//
//	go run ./examples/passthrough -- echo 'hello; rm -rf /'
//	# rejected by policy: shell metacharacter in argv
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/coilysiren/cli-guard/audit"
	"github.com/coilysiren/cli-guard/passthrough"
	"github.com/coilysiren/cli-guard/shell"
	"github.com/urfave/cli/v3"
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

	echoCmd := passthrough.Command("echo", runner, writer)

	app := &cli.Command{
		Name:     "passthrough-demo",
		Usage:    "wrap an existing binary as an audited subcommand",
		Version:  "v0.0.0",
		Commands: []*cli.Command{echoCmd},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = writer.Close()
		os.Exit(1) //nolint:gocritic
	}
	fmt.Fprintln(os.Stderr, "audit log:", auditPath)
}
