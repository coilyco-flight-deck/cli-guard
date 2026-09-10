// Command demo is a tiny urfave/cli v3 application that exercises the
// umbra framework primitives. Run with:
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/internal/clitrees"
	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/pkg/audit"
)

func main() {
	auditPath := filepath.Join(os.TempDir(), "umbra-demo.jsonl")
	writer := audit.NewWriter(auditPath)
	defer func() { _ = writer.Close() }()
	if err := writer.Preflight(); err != nil {
		fmt.Fprintln(os.Stderr, "umbra-demo: audit preflight:", err)
		_ = writer.Close()
		os.Exit(1) //nolint:gocritic // intentional: failed preflight cannot proceed
	}

	if err := clitrees.Audit(writer).Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "umbra-demo:", err)
		_ = writer.Close()
		os.Exit(1) //nolint:gocritic // intentional: defer handled above
	}
}
