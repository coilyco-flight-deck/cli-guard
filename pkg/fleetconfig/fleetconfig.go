// Package fleetconfig is the typed validator for the KDL fleet config: the
// agent-shaped schema that names which agents a fleet runs and how each one is
// launched. It is pure core data-typing, deliberately NOT a guarded surface.
//
// The three guarded surfaces (cli/, http/, mcp/) express permissions; this
// package structurally cannot. Its vocabulary has no `mount`, no `exec`, no
// `can run` - a fleet config names agents and their launch shape, never a
// grant. That package boundary IS the config/permission partition: config
// validation is a core concern, not a fourth surface, so the three-surface
// least-privilege identity stays legible. See docs/fleetconfig.md.
package fleetconfig

import (
	"fmt"

	kdl "github.com/calico32/kdl-go"
)

// SchemaVersion is the fleet-config dialect this package parses. A `fleet` block
// must declare exactly this version; a mismatch fails closed. See docs/fleetconfig.md.
const SchemaVersion = 2

// Fleet is the parsed form of one fleet config: the agent roster plus the
// fleet-wide defaults, and optionally the per-host director settings.
type Fleet struct {
	// SchemaVersion is the declared dialect version (== SchemaVersion). Zero in
	// an operator-local source, which carries no `fleet` block.
	SchemaVersion int

	// Defaults are the fleet-wide fallbacks: the default agent and the commit
	// attribution. Zero-valued in an operator-local source.
	Defaults Defaults

	// Agents is the roster, in source order. Empty in an operator-local source.
	Agents []Agent

	// Director carries the per-host director settings, the only vocabulary an
	// operator-local source may set. Nil when no `director` block is present.
	Director *Director
}

// Agent describes one agent the fleet can run and how to launch it. Every field
// is descriptive - a binary, a model, an argv shape - never a permission.
type Agent struct {
	Name            string // block name, e.g. `agent codex` -> "codex"
	Binary          string // the launcher binary on PATH
	ContextLevel    int    // WARD_CONTEXT_LEVEL floor (0..2); -1 when unset
	Stream          string // stream mode, e.g. "none"
	Auth            string // auth mode, e.g. "codex-file"
	Model           string // model id
	Endpoint        string // optional API endpoint override
	Provider        string // optional provider name
	ReasoningEffort string // reasoning-effort knob, e.g. "low"
	Verbosity       string // verbosity knob, e.g. "low"
	Argv            Argv   // the three launch argvs (preflight/headless/interactive)
}

// Argv holds an agent's three launch argvs. Each is the full token list for
// that mode (binary first), empty when the mode is not declared.
type Argv struct {
	Preflight   []string // optional pre-launch probe argv
	Headless    []string // non-interactive (exec) launch argv
	Interactive []string // interactive launch argv
}

// Defaults are the fleet-wide fallbacks under the `defaults` block.
type Defaults struct {
	Agent       string      // default agent name when a caller names none
	Attribution Attribution // commit identity the fleet writes under
}

// Attribution is the `attribution name=... email=...` commit identity.
type Attribution struct {
	Name  string
	Email string
}

// Director holds the per-host director settings: the narrow node set an
// operator-local source may set (the embedded fleet may seed a default).
type Director struct {
	DefaultScope string // director.default-scope: the default coordinate scope
}

// Source selects which subset of the one grammar a parse accepts. One parser,
// two sources: the grammar is shared, the accepted node set differs.
type Source int

const (
	// Embedded is the fleet config shipped inside a binary: it accepts the full
	// schema (the `fleet` block plus an optional `director` seed).
	Embedded Source = iota

	// OperatorLocal is a per-host operator file: it accepts only the narrow
	// per-host node set (`director`) and rejects the embed-only `fleet` block.
	OperatorLocal
)

// String renders the source for error messages.
func (s Source) String() string {
	switch s {
	case Embedded:
		return "embedded"
	case OperatorLocal:
		return "operator-local"
	default:
		return fmt.Sprintf("Source(%d)", int(s))
	}
}

// permissionTokens are surface-grammar words a fleet config must never carry:
// naming them turns the boundary into a legible error, not a generic "unknown node".
var permissionTokens = map[string]bool{
	"mount":       true,
	"exec":        true,
	"can":         true,
	"cannot":      true,
	"never":       true,
	"allow":       true,
	"passthrough": true,
	"wrap":        true,
	"deny":        true,
}

// Parse validates an embedded fleet config (the full schema): the common case,
// equivalent to ParseSource(src, Embedded).
func Parse(src []byte) (Fleet, error) {
	return ParseSource(src, Embedded)
}

// ParseSource validates a fleet config under the given source's accepted subset.
// It fails closed on any out-of-subset node or malformed field. See docs/fleetconfig.md.
func ParseSource(src []byte, source Source) (Fleet, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return Fleet{}, fmt.Errorf("fleetconfig: parse KDL: %w", err)
	}
	f := Fleet{}
	seenFleet := false
	for _, n := range doc.Nodes {
		switch n.Name() {
		case "fleet":
			if source == OperatorLocal {
				return Fleet{}, fmt.Errorf("fleetconfig: `fleet` is an embed-only block, rejected in an %s source (fail-closed)", source)
			}
			if seenFleet {
				return Fleet{}, fmt.Errorf("fleetconfig: duplicate top-level `fleet` block (fail-closed)")
			}
			seenFleet = true
			if err := parseFleetBlock(n, &f); err != nil {
				return Fleet{}, err
			}
		case "director":
			d, err := parseDirector(n)
			if err != nil {
				return Fleet{}, err
			}
			f.Director = &d
		default:
			return Fleet{}, unknownNode("top-level", n.Name(), "fleet | director")
		}
	}
	if source == Embedded && !seenFleet {
		return Fleet{}, fmt.Errorf("fleetconfig: embedded source needs a top-level `fleet` block (fail-closed)")
	}
	return f, nil
}

// fleetState tracks the once-only fields seen while walking a fleet block body.
type fleetState struct {
	seenVersion  bool
	seenDefaults bool
	names        map[string]bool // agent names, for duplicate detection
}

// parseFleetBlock fills the fleet-level fields from the `fleet` block body.
func parseFleetBlock(n *kdl.Node, f *Fleet) error {
	if len(n.Arguments()) != 0 {
		return fmt.Errorf("fleetconfig: `fleet` takes no arguments, only a block (fail-closed)")
	}
	st := &fleetState{names: map[string]bool{}}
	for _, c := range n.Children().Nodes {
		if err := applyFleetChild(c, f, st); err != nil {
			return err
		}
	}
	if !st.seenVersion {
		return fmt.Errorf("fleetconfig: `fleet` block is missing `schema-version` (fail-closed)")
	}
	if len(f.Agents) == 0 {
		return fmt.Errorf("fleetconfig: `fleet` block declares no `agent` (nothing to run; fail-closed)")
	}
	return nil
}

// applyFleetChild dispatches one child of the fleet block onto f.
func applyFleetChild(c *kdl.Node, f *Fleet, st *fleetState) error {
	switch c.Name() {
	case "schema-version":
		v, err := singleIntArg(c, "schema-version")
		if err != nil {
			return err
		}
		if v != SchemaVersion {
			return fmt.Errorf("fleetconfig: schema-version %d is not the supported dialect %d (fail-closed)", v, SchemaVersion)
		}
		f.SchemaVersion = v
		st.seenVersion = true
		return nil
	case "defaults":
		if st.seenDefaults {
			return fmt.Errorf("fleetconfig: duplicate `defaults` block (fail-closed)")
		}
		st.seenDefaults = true
		d, err := parseDefaults(c)
		if err != nil {
			return err
		}
		f.Defaults = d
		return nil
	case "agent":
		a, err := parseAgent(c)
		if err != nil {
			return err
		}
		if st.names[a.Name] {
			return fmt.Errorf("fleetconfig: duplicate agent %q (fail-closed)", a.Name)
		}
		st.names[a.Name] = true
		f.Agents = append(f.Agents, a)
		return nil
	default:
		return unknownNode("fleet body", c.Name(), "schema-version | defaults | agent")
	}
}

// parseDefaults reads the `defaults { agent ...; attribution ... }` block.
func parseDefaults(n *kdl.Node) (Defaults, error) {
	var d Defaults
	seenAttribution := false
	for _, c := range n.Children().Nodes {
		switch c.Name() {
		case "agent":
			v, err := singleStringArg(c, "defaults > agent")
			if err != nil {
				return Defaults{}, err
			}
			d.Agent = v
		case "attribution":
			if seenAttribution {
				return Defaults{}, fmt.Errorf("fleetconfig: duplicate `attribution` in defaults (fail-closed)")
			}
			seenAttribution = true
			att, err := parseAttribution(c)
			if err != nil {
				return Defaults{}, err
			}
			d.Attribution = att
		default:
			return Defaults{}, unknownNode("defaults body", c.Name(), "agent | attribution")
		}
	}
	return d, nil
}

// parseAttribution reads `attribution name=... email=...` from its properties.
func parseAttribution(n *kdl.Node) (Attribution, error) {
	if len(n.Arguments()) != 0 {
		return Attribution{}, fmt.Errorf("fleetconfig: `attribution` takes only name=/email= properties, not arguments (fail-closed)")
	}
	att := Attribution{}
	for k, v := range n.Properties() {
		switch k {
		case "name":
			att.Name = v.String()
		case "email":
			att.Email = v.String()
		default:
			return Attribution{}, fmt.Errorf("fleetconfig: `attribution` has unknown property %q (want name | email; fail-closed)", k)
		}
	}
	if att.Name == "" || att.Email == "" {
		return Attribution{}, fmt.Errorf("fleetconfig: `attribution` needs both name= and email= (fail-closed)")
	}
	return att, nil
}

// parseAgent reads one `agent <name> { ... }` block into an Agent.
func parseAgent(n *kdl.Node) (Agent, error) {
	name, err := singleStringArg(n, "agent")
	if err != nil {
		return Agent{}, fmt.Errorf("fleetconfig: `agent` needs a single name, e.g. `agent codex`: %w", err)
	}
	a := Agent{Name: name, ContextLevel: -1}
	// String knobs share one shape (a single string arg into a struct field), so
	// a pointer table keeps the per-child switch flat.
	strFields := map[string]*string{
		"binary":           &a.Binary,
		"stream":           &a.Stream,
		"auth":             &a.Auth,
		"model":            &a.Model,
		"endpoint":         &a.Endpoint,
		"provider":         &a.Provider,
		"reasoning-effort": &a.ReasoningEffort,
		"verbosity":        &a.Verbosity,
	}
	seenArgv := false
	for _, c := range n.Children().Nodes {
		if dst, ok := strFields[c.Name()]; ok {
			v, err := singleStringArg(c, fmt.Sprintf("agent %q > %s", name, c.Name()))
			if err != nil {
				return Agent{}, err
			}
			*dst = v
			continue
		}
		switch c.Name() {
		case "context-level":
			a.ContextLevel, err = singleIntArg(c, fmt.Sprintf("agent %q > context-level", name))
		case "argv":
			if seenArgv {
				return Agent{}, fmt.Errorf("fleetconfig: agent %q has a duplicate `argv` block (fail-closed)", name)
			}
			seenArgv = true
			a.Argv, err = parseArgv(c, name)
		default:
			return Agent{}, unknownNode(fmt.Sprintf("agent %q body", name), c.Name(),
				"binary | context-level | stream | auth | model | endpoint | provider | reasoning-effort | verbosity | argv")
		}
		if err != nil {
			return Agent{}, err
		}
	}
	if a.Binary == "" {
		return Agent{}, fmt.Errorf("fleetconfig: agent %q is missing `binary` (cannot launch; fail-closed)", name)
	}
	return a, nil
}

// parseArgv reads the `argv { preflight ...; headless ...; interactive ... }`
// block into an Argv. Each child's arguments are the mode's full token list.
func parseArgv(n *kdl.Node, agent string) (Argv, error) {
	if len(n.Arguments()) != 0 {
		return Argv{}, fmt.Errorf("fleetconfig: agent %q `argv` takes only a block, not arguments (fail-closed)", agent)
	}
	var av Argv
	seen := map[string]bool{}
	for _, c := range n.Children().Nodes {
		if seen[c.Name()] {
			return Argv{}, fmt.Errorf("fleetconfig: agent %q argv has a duplicate `%s` (fail-closed)", agent, c.Name())
		}
		tokens := stringArgs(c)
		if len(tokens) == 0 {
			return Argv{}, fmt.Errorf("fleetconfig: agent %q argv `%s` needs at least one token (fail-closed)", agent, c.Name())
		}
		switch c.Name() {
		case "preflight":
			av.Preflight = tokens
		case "headless":
			av.Headless = tokens
		case "interactive":
			av.Interactive = tokens
		default:
			return Argv{}, unknownNode(fmt.Sprintf("agent %q argv", agent), c.Name(), "preflight | headless | interactive")
		}
		seen[c.Name()] = true
	}
	return av, nil
}

// parseDirector reads the per-host `director { default-scope ... }` block. It is
// accepted by both sources; in an operator-local source it is the whole surface.
func parseDirector(n *kdl.Node) (Director, error) {
	if len(n.Arguments()) != 0 {
		return Director{}, fmt.Errorf("fleetconfig: `director` takes only a block, not arguments (fail-closed)")
	}
	var d Director
	seen := map[string]bool{}
	for _, c := range n.Children().Nodes {
		if seen[c.Name()] {
			return Director{}, fmt.Errorf("fleetconfig: director has a duplicate `%s` (fail-closed)", c.Name())
		}
		seen[c.Name()] = true
		switch c.Name() {
		case "default-scope":
			v, err := singleStringArg(c, "director > default-scope")
			if err != nil {
				return Director{}, err
			}
			d.DefaultScope = v
		default:
			return Director{}, unknownNode("director body", c.Name(), "default-scope")
		}
	}
	return d, nil
}

// unknownNode builds the fail-closed error for an out-of-vocabulary node,
// upgrading a permission token to a doctrine-specific message.
func unknownNode(where, name, want string) error {
	if permissionTokens[name] {
		return fmt.Errorf("fleetconfig: %s: %q is a permission token; a fleet config names agents, it cannot express a grant (config is core, not a guarded surface; fail-closed)", where, name)
	}
	return fmt.Errorf("fleetconfig: %s: unknown node %q (want %s; fail-closed)", where, name, want)
}

// singleStringArg returns a node's one string argument, erroring on a wrong
// count or a non-string kind (fail-closed on `binary 5`).
func singleStringArg(n *kdl.Node, label string) (string, error) {
	args := n.Arguments()
	if len(args) != 1 {
		return "", fmt.Errorf("fleetconfig: %s expects exactly one value, got %d (fail-closed)", label, len(args))
	}
	if args[0].Kind() != kdl.String {
		return "", fmt.Errorf("fleetconfig: %s must be a string (fail-closed)", label)
	}
	return args[0].String(), nil
}

// singleIntArg returns a node's one integer argument, erroring on a wrong count
// or a non-integer kind.
func singleIntArg(n *kdl.Node, label string) (int, error) {
	args := n.Arguments()
	if len(args) != 1 {
		return 0, fmt.Errorf("fleetconfig: %s expects exactly one value, got %d (fail-closed)", label, len(args))
	}
	if args[0].Kind() != kdl.Int {
		return 0, fmt.Errorf("fleetconfig: %s must be an integer (fail-closed)", label)
	}
	return args[0].Int(), nil
}

// stringArgs returns a node's arguments as strings, in order.
func stringArgs(n *kdl.Node) []string {
	args := n.Arguments()
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, a.String())
	}
	return out
}
