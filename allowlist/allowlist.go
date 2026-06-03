// Package allowlist validates a repocfg-shaped command allowlist against the
// repo's Makefile so the verb surface and the make-target surface cannot
// drift. Consumers (ward, coily) wrap Lint in their own doctor / lint
// subcommands and supply already-resolved paths; this package owns the
// engine, not the resolution policy or the CLI surface.
package allowlist

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Problem is one contract violation. File is the source the problem is
// anchored to (the yaml for verb-side problems, the Makefile for target-
// side drift). Consumers format `File:Line: Msg` however suits them.
type Problem struct {
	File string
	Line int
	Msg  string
}

// Lint validates yamlPath against makefilePath. Both must be absolute or
// already cleaned by the caller; the package does not resolve paths.
//
// Rules:
//   - commands.<verb>.run must equal "make <verb>".
//   - The Makefile must declare a target named <verb>.
//   - The verb description must equal the Makefile target's `## desc`
//     auto-help comment.
//
// An empty []Problem with nil error means clean. A non-nil error means
// the inputs could not be parsed (malformed yaml, unreadable Makefile);
// rule violations come back as Problems, never as errors.
func Lint(yamlPath, makefilePath string) ([]Problem, error) {
	verbs, err := loadYamlVerbs(yamlPath)
	if err != nil {
		return nil, err
	}
	targets, err := loadMakefileTargets(makefilePath)
	if err != nil {
		return nil, err
	}

	var problems []Problem
	for _, v := range verbs {
		want := "make " + v.name
		if v.run != want {
			problems = append(problems, Problem{
				File: yamlPath, Line: v.line,
				Msg: fmt.Sprintf("commands.%s.run = %q, want %q", v.name, v.run, want),
			})
		}
		t, ok := targets[v.name]
		if !ok {
			problems = append(problems, Problem{
				File: yamlPath, Line: v.line,
				Msg: fmt.Sprintf("commands.%s has no matching Makefile target", v.name),
			})
			continue
		}
		if v.description != t.description {
			problems = append(problems, Problem{
				File: yamlPath, Line: v.line,
				Msg: fmt.Sprintf(
					"commands.%s.description = %q, want %q (from %s:%d)",
					v.name, v.description, t.description, makefilePath, t.line),
			})
		}
	}
	return problems, nil
}

// makeTargetHelp matches `target: deps  ## description` lines.
var makeTargetHelp = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:[^=]*?##\s*(.*)$`)

type yamlVerb struct {
	name        string
	run         string
	description string
	line        int
}

func loadYamlVerbs(path string) ([]yamlVerb, error) {
	path = filepath.Clean(path)
	data, err := os.ReadFile(path) // #nosec G304 G703 -- caller-supplied repo-local config path, cleaned
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	commands, err := findCommandsNode(path, &root)
	if err != nil {
		return nil, err
	}
	verbs := make([]yamlVerb, 0, len(commands.Content)/2)
	for i := 0; i+1 < len(commands.Content); i += 2 {
		k, v := commands.Content[i], commands.Content[i+1]
		if v.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s:%d: commands.%s is not a mapping", path, k.Line, k.Value)
		}
		verbs = append(verbs, parseVerbNode(k, v))
	}
	return verbs, nil
}

func findCommandsNode(path string, root *yaml.Node) (*yaml.Node, error) {
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: top level is not a mapping", path)
	}
	doc := root.Content[0]
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "commands" {
			continue
		}
		commands := doc.Content[i+1]
		if commands.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s: 'commands' is not a mapping", path)
		}
		return commands, nil
	}
	return nil, fmt.Errorf("%s: missing top-level 'commands:' map", path)
}

func parseVerbNode(key, value *yaml.Node) yamlVerb {
	verb := yamlVerb{name: key.Value, line: key.Line}
	for j := 0; j+1 < len(value.Content); j += 2 {
		switch value.Content[j].Value {
		case "run":
			verb.run = value.Content[j+1].Value
		case "description":
			verb.description = value.Content[j+1].Value
		}
	}
	return verb
}

type makeTarget struct {
	name        string
	description string
	line        int
}

func loadMakefileTargets(path string) (map[string]makeTarget, error) {
	path = filepath.Clean(path)
	f, err := os.Open(path) // #nosec G304 G703 -- caller-supplied Makefile path, cleaned
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close() // #nosec G307 -- read-only file handle
	out := make(map[string]makeTarget)
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		m := makeTargetHelp.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		out[m[1]] = makeTarget{name: m[1], description: strings.TrimSpace(m[2]), line: lineNo}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return out, nil
}
