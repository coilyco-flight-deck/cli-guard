// Command gen-webdocs renders each cli-guard example's CLI tree as a
// static HTML site under ../../site/cli/<example>/ via cli-web-docs.
//
// Invoked from the repo root by `make docs-cli`. Lives in its own
// go module so the runtime library does not depend on cli-web-docs.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/coilysiren/cli-guard/examples/treebuilders"
	webdocs "github.com/coilysiren/cli-web-docs"
	"github.com/urfave/cli/v3"
)

type entry struct {
	dir   string
	title string
	build func() *cli.Command
}

func main() {
	entries := []entry{
		{"audit", "cli-guard examples/audit", func() *cli.Command { return treebuilders.Audit(nil) }},
		{"egress", "cli-guard examples/egress", treebuilders.Egress},
		{"exitcode", "cli-guard examples/exitcode", treebuilders.Exitcode},
		{"gittree", "cli-guard examples/gittree", treebuilders.Gittree},
		{"passthrough", "cli-guard examples/passthrough", func() *cli.Command { return treebuilders.Passthrough(nil, nil) }},
		{"policy", "cli-guard examples/policy", treebuilders.Policy},
		{"repocfg", "cli-guard examples/repocfg", func() *cli.Command { return treebuilders.Repocfg(nil) }},
		{"scope", "cli-guard examples/scope", treebuilders.Scope},
	}

	outRoot := "../../site/cli"
	for _, e := range entries {
		out := filepath.Join(outRoot, e.dir)
		if err := webdocs.Render(e.build(), webdocs.Options{
			OutputDir: out,
			Title:     e.title,
			PerPage:   true,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "gen-webdocs:", e.dir, err)
			os.Exit(1)
		}
		fmt.Println("rendered", out)
	}

	if err := writeIndex(outRoot, entries); err != nil {
		fmt.Fprintln(os.Stderr, "gen-webdocs: index:", err)
		os.Exit(1)
	}
}

func writeIndex(outRoot string, entries []entry) error {
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(outRoot, "index.html"))
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintln(f, `<!doctype html><meta charset="utf-8"><title>cli-guard CLI reference</title>`)
	fmt.Fprintln(f, `<h1>cli-guard examples</h1><ul>`)
	for _, e := range entries {
		fmt.Fprintf(f, `<li><a href="%s/">%s</a></li>`+"\n", e.dir, e.dir)
	}
	fmt.Fprintln(f, `</ul>`)
	return nil
}
