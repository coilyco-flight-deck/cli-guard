// Command specverb-gen renders a consumer `main.go` from a KDL Guardfile via
// specgen, so a spec-driven consumer declares only its policy. See docs/specverb.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/specgen"
)

func main() {
	guardPath := flag.String("guardfile", "", "path to the consumer's KDL Guardfile")
	outPath := flag.String("out", "", "path to write the generated main.go")
	flag.Parse()

	if err := run(*guardPath, *outPath); err != nil {
		fmt.Fprintln(os.Stderr, "specverb-gen:", err)
		os.Exit(1)
	}
}

func run(guardPath, outPath string) error {
	if guardPath == "" || outPath == "" {
		return fmt.Errorf("--guardfile and --out are required")
	}
	src, err := os.ReadFile(guardPath) //nolint:gosec // operator-supplied build input
	if err != nil {
		return fmt.Errorf("read guardfile: %w", err)
	}
	gf, err := guardfile.Parse(src)
	if err != nil {
		return fmt.Errorf("parse guardfile: %w", err)
	}
	out, err := specgen.Render(gf, filepath.Base(guardPath))
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, out, 0o600); err != nil { //nolint:gosec // operator-supplied build output path
		return fmt.Errorf("write %s: %w", outPath, err)
	}
	fmt.Fprintf(os.Stderr, "specverb-gen: wrote %s\n", outPath)
	return nil
}
