// Package skillgen renders an urfave/cli v3 command tree into either a
// flat markdown lookup table or a structured yaml document. Pairs with
package skillgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"
)

// Entry is one leaf verb in the structured (yaml) form of the command
// tree. Programmatic consumers (an external orchestrator, a plan-time
type Entry struct {
	Path    []string `yaml:"path"`
	Summary string   `yaml:"summary,omitempty"`
	Flags   []string `yaml:"flags,omitempty"`
}

// RenderYAML walks commands and emits a yaml document of Entry rows
// under a top-level `commands:` key. Each path is prefixed with root
func RenderYAML(commands []*cli.Command, root string) (string, error) {
	var entries []Entry
	for _, c := range sorted(commands) {
		walkEntries(&entries, []string{root}, c)
	}
	doc := map[string]any{"commands": entries}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("skillgen: yaml marshal: %w", err)
	}
	return string(out), nil
}

func walkEntries(out *[]Entry, parent []string, c *cli.Command) {
	path := append(append([]string{}, parent...), c.Name)
	if len(c.Commands) == 0 {
		*out = append(*out, Entry{
			Path:    path,
			Summary: strings.TrimSpace(c.Usage),
			Flags:   flagNames(c.Flags),
		})
		return
	}
	for _, sub := range sorted(c.Commands) {
		walkEntries(out, path, sub)
	}
}

// RenderMarkdown walks commands and emits a deterministic markdown
// lookup table, one section per leaf verb:
func RenderMarkdown(commands []*cli.Command, root string) string {
	var b strings.Builder
	for _, c := range sorted(commands) {
		walkMarkdown(&b, []string{root}, c)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func walkMarkdown(b *strings.Builder, parent []string, c *cli.Command) {
	path := append(append([]string{}, parent...), c.Name)
	if len(c.Commands) == 0 {
		fmt.Fprintf(b, "## `%s`\n\n", strings.Join(path, " "))
		if u := strings.TrimSpace(c.Usage); u != "" {
			fmt.Fprintf(b, "%s\n\n", u)
		}
		if names := flagNames(c.Flags); len(names) > 0 {
			fmt.Fprintf(b, "Flags: %s\n\n", strings.Join(names, ", "))
		}
		return
	}
	for _, sub := range sorted(c.Commands) {
		walkMarkdown(b, path, sub)
	}
}

func sorted(in []*cli.Command) []*cli.Command {
	out := append([]*cli.Command(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func flagNames(flags []cli.Flag) []string {
	names := make([]string, 0, len(flags))
	seen := map[string]bool{}
	for _, f := range flags {
		// Take the first registered name (canonical long form). Aliases
		// and short forms are skipped to keep the table scannable.
		all := f.Names()
		if len(all) == 0 {
			continue
		}
		n := all[0]
		if seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, "--"+n)
	}
	sort.Strings(names)
	return names
}
