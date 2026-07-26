// Package skillgen renders an urfave/cli v3 command tree into agent-facing
// discovery artifacts without making those artifacts runtime authority.
package skillgen

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"
)

// Entry is one reachable leaf in the structured YAML command index.
type Entry struct {
	Path    []string `yaml:"path"`
	Summary string   `yaml:"summary,omitempty"`
	Flags   []string `yaml:"flags,omitempty"`
}

// Bundle is one native agent skill plus its lazy command index.
type Bundle struct {
	Name         string
	Skill        string
	CommandsYAML string
}

// RenderSkill builds a concise SKILL.md plus a complete lazy command index.
// Live help and describe output remain authoritative for command behavior.
func RenderSkill(commands []*cli.Command, root string) (Bundle, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return Bundle{}, fmt.Errorf("skillgen: root command is empty")
	}
	name := skillName(root)
	if name == "" {
		return Bundle{}, fmt.Errorf("skillgen: root command %q has no skill-safe characters", root)
	}
	index, err := RenderYAML(commands, root)
	if err != nil {
		return Bundle{}, err
	}

	meta, err := yaml.Marshal(struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}{
		Name: name,
		Description: fmt.Sprintf(
			"Use the guarded %s CLI when a task needs a command exposed by its generated policy.",
			root,
		),
	})
	if err != nil {
		return Bundle{}, fmt.Errorf("skillgen: frontmatter marshal: %w", err)
	}

	var body strings.Builder
	body.WriteString("---\n")
	body.Write(meta)
	body.WriteString("---\n\n# ")
	body.WriteString(root)
	body.WriteString(" guarded CLI\n\n")
	fmt.Fprintf(&body, "Start with `%s --help`, then follow group help to the needed leaf.\n", root)
	body.WriteString("Use a group's `describe` command when it is available for the pulled policy view.\n")
	body.WriteString("The running CLI's help and describe output are authoritative for behavior and flags.\n\n")
	body.WriteString("`references/commands.yaml` indexes every reachable leaf for lazy discovery.\n")

	return Bundle{Name: name, Skill: body.String(), CommandsYAML: index}, nil
}

// RenderYAML walks commands and emits a yaml document of Entry rows
// under a top-level `commands:` key. Each path is prefixed with root.
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

func skillName(root string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(root) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
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
