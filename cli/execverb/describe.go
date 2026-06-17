// The describe model for the exec dialect: the in-engine view of the mounted
// surface, source for the committed reference doc. See docs/execverb.md.

package execverb

import (
	"fmt"
	"strings"
)

// Surface is the in-engine model of a mounted exec surface: the wrapped binary,
// the fixed argv prefix, and every granted subcommand leaf.
type Surface struct {
	Group      []string    `json:"group"`                 // command path, e.g. ["ward","ops","aws"]
	Bin        string      `json:"bin"`                   // the wrapped binary, fixed at parse
	ArgvPrefix []string    `json:"argv_prefix,omitempty"` // unoverridable leading argv
	Env        []string    `json:"env,omitempty"`         // env injections as "NAME = provider address" (source, never the resolved value)
	Grants     []GrantInfo `json:"grants"`                // every mounted leaf, in mount order
}

// GrantInfo is one mounted grant: its CLI placement, the real invocation it
// authorizes, its flag policy, and any gates/guards that can still refuse it.
type GrantInfo struct {
	Name       string   `json:"name"`                  // dotted audit name, e.g. ward.ops.aws.s3.ls
	Subcommand []string `json:"subcommand,omitempty"`  // e.g. ["s3","ls"]; empty for the wildcard
	Exec       []string `json:"exec,omitempty"`        // tokens appended after the binary: the argv override when set, else the subcommand
	Wildcard   bool     `json:"wildcard"`              // `can run *`: the whole binary passes through
	Describe   string   `json:"describe,omitempty"`    // Guardfile describe note
	AllowFlags []string `json:"allow_flags,omitempty"` // non-empty: strict flag allowlist
	DenyFlags  []string `json:"deny_flags,omitempty"`  // default-allow minus these
	Gates      []string `json:"gates,omitempty"`       // registered preflight gate names
	Guards     []string `json:"guards,omitempty"`      // rendered when/deny-when guards
}

// Describe builds the surface model for an exec Guardfile without mounting a
// command tree, so the driver and docs can read it directly.
func Describe(gf *Guardfile) *Surface {
	if gf == nil {
		return &Surface{}
	}
	s := &Surface{Group: gf.Group, Bin: gf.Bin, ArgvPrefix: gf.ArgvPrefix}
	for _, e := range gf.Env {
		s.Env = append(s.Env, e.Name+" = "+strings.TrimSpace(e.Provider+" "+e.Address))
	}
	for _, g := range gf.Grants {
		s.Grants = append(s.Grants, grantInfo(gf, g))
	}
	return s
}

// grantInfo flattens one Grant into its describe view, naming the gates and
// rendering the guards in the same vocabulary the Guardfile uses.
func grantInfo(gf *Guardfile, g Grant) GrantInfo {
	name := strings.Join(gf.Group, ".")
	if !g.Wildcard {
		name += "." + strings.Join(g.Subcommand, ".")
	}
	gi := GrantInfo{
		Name:       name,
		Subcommand: g.Subcommand,
		Exec:       g.ExecArgv(),
		Wildcard:   g.Wildcard,
		Describe:   g.Describe,
		AllowFlags: g.AllowFlags,
		DenyFlags:  g.DenyFlags,
	}
	for _, gs := range g.Gates {
		gi.Gates = append(gi.Gates, gs.Name)
	}
	for _, wc := range g.Whens {
		gi.Guards = append(gi.Guards, guardSentence(wc))
	}
	return gi
}

// guardSentence renders one when/deny-when guard as a readable clause.
func guardSentence(wc WhenClause) string {
	verb := "requires"
	if wc.Deny {
		verb = "denies when"
	}
	clause := fmt.Sprintf("%s %s matches %s", verb, wc.Selector, strings.Join(wc.Patterns, " or "))
	if wc.OnlyReads {
		clause += " (read-only calls only)"
	}
	return clause
}

// Markdown renders the surface as the committed reference doc, the exec-dialect
// analog of specverb's Surface.Markdown().
func (s *Surface) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", strings.Join(s.Group, " "))
	invocation := strings.TrimSpace(s.Bin + " " + strings.Join(s.ArgvPrefix, " "))
	fmt.Fprintf(&b, "Exec-dialect CLI. Every verb runs `%s` with the granted subcommand (or its `argv` override) appended; the binary and its prefix are fixed and the caller can never substitute them.\n", invocation)
	if len(s.Env) > 0 {
		fmt.Fprintf(&b, "\nEnv set on the process (resolved at exec time): %s.\n", joinCode(s.Env))
	}

	prefix := strings.Join(s.Group, " ")
	for _, g := range s.Grants {
		leaf := strings.Join(g.Subcommand, " ")
		if g.Wildcard {
			leaf = "* (open passthrough)"
		}
		heading := fmt.Sprintf("## %s %s", prefix, leaf)
		if g.Describe != "" {
			heading += " - " + g.Describe
		}
		fmt.Fprintf(&b, "\n%s\n\n", heading)
		fmt.Fprintf(&b, "`%s`\n", strings.TrimSpace(invocation+" "+strings.Join(g.Exec, " ")))
		writeFlagPolicy(&b, g)
		writeGuards(&b, g)
	}
	return b.String()
}

// writeFlagPolicy states the grant's flag rule: a strict allowlist, a denylist,
// or unrestricted passthrough.
func writeFlagPolicy(b *strings.Builder, g GrantInfo) {
	switch {
	case len(g.AllowFlags) > 0:
		fmt.Fprintf(b, "\nFlags: only %s allowed (strict allowlist).\n", joinCode(g.AllowFlags))
	case len(g.DenyFlags) > 0:
		fmt.Fprintf(b, "\nFlags: all allowed except %s.\n", joinCode(g.DenyFlags))
	default:
		b.WriteString("\nFlags: unrestricted passthrough.\n")
	}
}

// writeGuards renders the preflight gates and argv guards that can refuse a
// call before it reaches the binary.
func writeGuards(b *strings.Builder, g GrantInfo) {
	if len(g.Gates) == 0 && len(g.Guards) == 0 {
		return
	}
	b.WriteString("\nPreflight:\n\n")
	for _, name := range g.Gates {
		fmt.Fprintf(b, "- gate `%s`\n", name)
	}
	for _, guard := range g.Guards {
		fmt.Fprintf(b, "- %s\n", guard)
	}
}

// joinCode renders a flag list as comma-separated code spans.
func joinCode(flags []string) string {
	quoted := make([]string, len(flags))
	for i, f := range flags {
		quoted[i] = "`" + f + "`"
	}
	return strings.Join(quoted, ", ")
}
