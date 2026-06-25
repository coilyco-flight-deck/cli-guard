// inherit: compose guardfiles by merging grant sets. A child wrap may carry one
// or more `inherit "<path>"` directives at the top of its body; each pulls in the
// referenced guardfile's grants (and its `spec`/`base-url`/`auth` singletons when
// the child declares none of its own). Resolution is textual and happens before
// the typed Parse runs, so the engine's resolve/prune/deny pipeline operates on
// the already-merged set unchanged. See docs/specverb-inherit.md.

package guardfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	kdl "github.com/calico32/kdl-go"
)

// inheritNode is the directive's node name.
const inheritNode = "inherit"

// restrictNode is the scope-gate node name. Unlike cli-guard#160, `restrict` now
// inherits (deduped by param, child wins); see specverb-inherit.md.
const restrictNode = "restrict"

// singletonNodes are the wrap-body fields a child inherits only when it declares
// none of its own; the child always wins. Grants, by contrast, always merge.
var singletonNodes = map[string]bool{"spec": true, "base-url": true, "auth": true}

// isGrantNode reports whether a node is a policy sentence that merges across the
// inherit boundary: a modal grant or an `override`. `action` stays child-local.
func isGrantNode(name string) bool { return modals[name] || name == overrideNode }

// Flatten resolves every `inherit` in the guardfile at path into one self-contained
// KDL document's bytes (no `inherit` => source verbatim). docs/specverb-inherit.md.
func Flatten(path string) ([]byte, error) {
	return flattenFile(path, nil)
}

// ParseFile flattens the guardfile at path (resolving inherit) and parses the
// merged result into a typed Guardfile.
func ParseFile(path string) (*Guardfile, error) {
	flat, err := Flatten(path)
	if err != nil {
		return nil, err
	}
	return Parse(flat)
}

// flattenFile is the recursive worker behind Flatten. stack carries the chain of
// absolute paths currently being resolved, so a re-entry is reported as a cycle.
func flattenFile(path string, stack []string) ([]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("guardfile: inherit: resolve %q: %w", path, err)
	}
	for _, s := range stack {
		if s == abs {
			chain := strings.Join(append(append([]string{}, stack...), abs), " -> ")
			return nil, fmt.Errorf("guardfile: inherit cycle detected: %s", chain)
		}
	}
	src, err := os.ReadFile(abs) //nolint:gosec // operator-supplied policy input
	if err != nil {
		return nil, fmt.Errorf("guardfile: inherit: read %q: %w", path, err)
	}
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return nil, fmt.Errorf("guardfile: inherit: parse %q: %w", path, err)
	}
	wrap := doc.GetNode("wrap")
	if wrap == nil {
		return nil, fmt.Errorf("guardfile: inherit: %q has no top-level `wrap` node", path)
	}
	refs, err := collectInherits(wrap)
	if err != nil {
		return nil, fmt.Errorf("guardfile: inherit: %q: %w", path, err)
	}
	if len(refs) == 0 {
		return src, nil // no inherit directive: leave the source byte-identical
	}
	if err := mergeInherited(wrap, filepath.Dir(abs), refs, append(stack, abs)); err != nil {
		return nil, err
	}
	out, err := kdl.EmitToString(doc)
	if err != nil {
		return nil, fmt.Errorf("guardfile: inherit: emit merged %q: %w", path, err)
	}
	return []byte(out), nil
}

// collectInherits returns the ordered list of paths named by the wrap's `inherit`
// directives, fail-closing on a malformed directive.
func collectInherits(wrap *kdl.Node) ([]string, error) {
	var refs []string
	for _, n := range wrap.Children().Nodes {
		if n.Name() != inheritNode {
			continue
		}
		args := n.Arguments()
		if len(args) != 1 || args[0].String() == "" {
			return nil, fmt.Errorf("`inherit` needs exactly one guardfile path, e.g. `inherit \"base.guardfile.kdl\"`")
		}
		refs = append(refs, args[0].String())
	}
	return refs, nil
}

// mergeInherited rewrites wrap's children in place: inherited grants/singletons
// prepended, then the child's own nodes (inherit stripped); child stays authoritative.
func mergeInherited(wrap *kdl.Node, dir string, refs []string, stack []string) error {
	m := &merge{grantSeen: map[string]bool{}, satisfied: map[string]bool{}, restrictSeen: map[string]bool{}}
	// Pre-seed from the child so the child wins every singleton, grant, and restrict.
	for _, n := range wrap.Children().Nodes {
		m.claim(n)
	}
	for _, ref := range refs {
		parentPath := ref
		if !filepath.IsAbs(parentPath) {
			parentPath = filepath.Join(dir, ref)
		}
		pBytes, err := flattenFile(parentPath, stack)
		if err != nil {
			return err
		}
		pWrap, err := wrapOf(pBytes, ref)
		if err != nil {
			return err
		}
		m.absorb(pWrap)
	}
	// Precedence check before the splice: provenance (inherited vs local) is only
	// knowable here. Deny set is every parent deny. See specverb-override.md.
	if err := validateInheritedPrecedence(wrap.Children().Nodes, m.parentDenies); err != nil {
		return err
	}
	merged := m.inherited
	for _, n := range wrap.Children().Nodes {
		if n.Name() != inheritNode {
			merged = append(merged, n)
		}
	}
	wrap.Children().Nodes = merged
	return nil
}

// merge accumulates the inherited node prefix, tracking which grant keys,
// singletons, and restrict params are claimed so a closer layer always wins.
type merge struct {
	grantSeen    map[string]bool
	satisfied    map[string]bool
	restrictSeen map[string]bool
	inherited    []*kdl.Node
	parentDenies []*kdl.Node // every never/cannot a parent declares, dedup notwithstanding
}

// claim marks a child node's grant key, singleton, or restrict param as taken,
// without inheriting it.
func (m *merge) claim(n *kdl.Node) {
	switch name := n.Name(); {
	case isGrantNode(name):
		m.grantSeen[grantNodeKey(n)] = true
	case singletonNodes[name]:
		m.satisfied[name] = true
	case name == restrictNode:
		m.restrictSeen[restrictNodeKey(n)] = true
	}
}

// absorb pulls a parent wrap's still-unclaimed grants, restrict clauses, and
// singleton headers into the inherited prefix; `action` stays child-local.
func (m *merge) absorb(pWrap *kdl.Node) {
	for _, n := range pWrap.Children().Nodes {
		switch name := n.Name(); {
		case isGrantNode(name):
			if isDenyModal(name) {
				m.parentDenies = append(m.parentDenies, n) // provenance record, not spliced
			}
			if k := grantNodeKey(n); !m.grantSeen[k] {
				m.grantSeen[k] = true
				m.inherited = append(m.inherited, n.Clone())
			}
		case singletonNodes[name]:
			if !m.satisfied[name] {
				m.satisfied[name] = true
				m.inherited = append(m.inherited, n.Clone())
			}
		case name == restrictNode:
			if k := restrictNodeKey(n); !m.restrictSeen[k] {
				m.restrictSeen[k] = true
				m.inherited = append(m.inherited, n.Clone())
			}
		}
	}
}

// restrictNodeKey is the dedup identity of a restrict clause: its param. A child
// `restrict owner ...` shadows an inherited one for the same param.
func restrictNodeKey(n *kdl.Node) string {
	if args := n.Arguments(); len(args) > 0 {
		return args[0].String()
	}
	return ""
}

// wrapOf parses guardfile bytes and returns their top-level wrap node, naming ref
// in any error.
func wrapOf(src []byte, ref string) (*kdl.Node, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return nil, fmt.Errorf("guardfile: inherit: parse %q: %w", ref, err)
	}
	wrap := doc.GetNode("wrap")
	if wrap == nil {
		return nil, fmt.Errorf("guardfile: inherit: %q has no top-level `wrap` node", ref)
	}
	return wrap, nil
}

// grantNodeKey is the dedup identity of a grant node: modal + verb + resource
// (an `override` keys on its verb+resource so distinct overrides never collide).
func grantNodeKey(n *kdl.Node) string {
	v, r := grantVerbResource(n)
	return n.Name() + "\x00" + v + "\x00" + r
}

// grantVerbResource extracts the (verb, resource) a grant or override node names,
// accounting for the `override can <verb> <resource>` shape's leading `can`.
func grantVerbResource(n *kdl.Node) (verb, resource string) {
	args := n.Arguments()
	off := 0
	if n.Name() == overrideNode {
		off = 1 // skip the `can` token
	}
	if len(args) > off {
		verb = args[off].String()
	}
	if len(args) > off+1 {
		resource = args[off+1].String()
	}
	return verb, resource
}

// validateInheritedPrecedence enforces the load-bearing rule at the inherit seam:
// only an `override` crosses an inherited `never`. See docs/specverb-override.md.
func validateInheritedPrecedence(local, inherited []*kdl.Node) error {
	exact := map[string]bool{}    // verb\x00resource
	verbStar := map[string]bool{} // verb (a `never <verb> "*"`)
	for _, n := range inherited {
		if !modals[n.Name()] || !isDenyModal(n.Name()) {
			continue
		}
		v, r := grantVerbResource(n)
		if r == wildcardSentinel {
			verbStar[v] = true
		} else {
			exact[v+"\x00"+r] = true
		}
	}
	inheritedDenies := func(v, r string) bool { return verbStar[v] || exact[v+"\x00"+r] }
	for _, n := range local {
		switch n.Name() {
		case overrideNode:
			v, r := grantVerbResource(n)
			if !inheritedDenies(v, r) {
				return fmt.Errorf("guardfile: `override can %s %s` lifts no inherited `never` (nothing to cross); deny it in a base tier or drop the override", v, r)
			}
		case "can":
			v, r := grantVerbResource(n)
			if r == wildcardSentinel || !inheritedDenies(v, r) {
				continue
			}
			return fmt.Errorf("guardfile: `can %s %s` is blocked by an inherited `never` and cannot silently cross it; use `override can %s %s` to escalate this tier by name", v, r, v, r)
		}
	}
	return nil
}
