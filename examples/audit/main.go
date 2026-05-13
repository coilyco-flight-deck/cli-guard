// Command demo is a tiny urfave/cli v3 application that exercises the
// cli-guard framework primitives. Run with:
//
//	go run ./examples/demo hello world
//
// Audit rows land in $TMPDIR/cli-guard-demo.jsonl.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/coilysiren/cli-guard/audit"
	"github.com/coilysiren/cli-guard/examples/treebuilders"
)

func main() {
	auditPath := filepath.Join(os.TempDir(), "cli-guard-demo.jsonl")
	writer := audit.NewWriter(auditPath)
	defer func() { _ = writer.Close() }()
	if err := writer.Preflight(); err != nil {
		fmt.Fprintln(os.Stderr, "cli-guard-demo: audit preflight:", err)
		_ = writer.Close()
		os.Exit(1) //nolint:gocritic // intentional: failed preflight cannot proceed
	}

	if err := treebuilders.Audit(writer).Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "cli-guard-demo:", err)
		_ = writer.Close()
		os.Exit(1) //nolint:gocritic // intentional: defer handled above
	}
}
