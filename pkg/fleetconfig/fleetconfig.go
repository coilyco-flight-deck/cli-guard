// Package fleetconfig is the typed validator for the KDL fleet config: the
// agent-shaped schema that names which agents a fleet runs and how each one is
// launched. It is pure core data-typing, deliberately NOT a guarded surface.
//
// The two guarded surfaces (cli/, http/) express permissions; this
// package structurally cannot. Its vocabulary has no `mount`, no `exec`, no
// `can run` - a fleet config names agents and their launch shape, never a
// grant. That package boundary IS the config/permission partition: config
// validation is a core concern, not a third surface, so the two-surface
// least-privilege identity stays legible. See docs/fleetconfig.md.
package fleetconfig

import (
	"fmt"

	kdl "github.com/calico32/kdl-go"
)

// SchemaVersion is the fleet-config dialect this package parses. `agents` must
// declare exactly this version, and a mismatch fails closed.
const SchemaVersion = 2

// Fleet is the parsed form of one fleet config: the agent roster plus the
// fleet-wide defaults, and optionally the per-host director settings.
type Fleet struct {
	// Description is the optional top-level `description "..."` prose: standing
	// "what/why" context as queryable data, "" when none. See docs/kdl-description.md.
	Description string

	// SchemaVersion is the declared dialect version (== SchemaVersion). Zero in
	// an operator-local source, which carries no embedded roster block.
	SchemaVersion int

	// Defaults are the fleet-wide fallbacks: the default agent and the commit
	// attribution. Zero-valued in an operator-local source.
	Defaults Defaults

	// Agents is the roster, in source order. Empty in an operator-local source.
	Agents []Agent

	// Roles is the per-role capability roster: each startup role names
	// the guardfile set it holds. Empty when no `roles` block is present.
	Roles []Role

	// Director carries the per-host director settings, the only vocabulary an
	// operator-local source may set. Nil when no `director` block is present.
	Director *Director
}

// Agent describes one agent the fleet can run and how to launch it.
// Sparse top-level nodes may leave launch fields empty for consumer-side defaulting.
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

// Role is one entry in the per-role capability roster: a name, the guardfile
// set it holds, and an optional per-agent overlay.
type Role struct {
	Name       string     // block name, e.g. `role advisor` -> "advisor"
	Guardfiles Guardfiles // the guardfile set this role holds (list or legacy prefix)

	// AgentConfig is the sparse per-agent override overlay, keyed by agent name;
	// nil when the role sets none. See docs/fleetconfig.md.
	AgentConfig map[string]RoleAgentOverride
}

// RoleAgentOverride is a role's sparse overlay on a top-level `agent` node: only
// the launch knobs a role may retune. See docs/fleetconfig.md.
type RoleAgentOverride struct {
	Model           string // model id override
	Endpoint        string // API endpoint override
	ReasoningEffort string // reasoning-effort override
	Verbosity       string // verbosity override
}

// Guardfiles is a role's guardfile set: EITHER a flat List of names OR a legacy
// Prefix selecting by name prefix, mutually exclusive; both zero means none.
type Guardfiles struct {
	List   []string // flat list of guardfile names; nil when Prefix is set
	Prefix string   // a name prefix selecting a set; "" when List is used
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
	// schema (the `agents` block plus an optional `director` seed).
	Embedded Source = iota

	// OperatorLocal is a per-host operator file: it accepts only the narrow
	// per-host node set (`director`) and rejects the embed-only roster block.
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

// ParseSource validates a fleet config under the given source's accepted
// subset. It fails closed on any out-of-subset node or malformed field.
func ParseSource(src []byte, source Source) (Fleet, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return Fleet{}, fmt.Errorf("fleetconfig: parse KDL: %w", err)
	}
	f := Fleet{}
	seenFleet := false
	for _, n := range doc.Nodes {
		if err := applyTopLevel(n, &f, source, &seenFleet); err != nil {
			return Fleet{}, err
		}
	}
	if source == Embedded && !seenFleet {
		return Fleet{}, fmt.Errorf("fleetconfig: embedded source needs a top-level `agents` block (or legacy `fleet`; fail-closed)")
	}
	return f, nil
}

// applyTopLevel dispatches top-level roster, director, and description nodes onto f.
// It enforces the source subset and the once-only rules.
func applyTopLevel(n *kdl.Node, f *Fleet, source Source, seenFleet *bool) error {
	switch n.Name() {
	case "agents":
		if source == OperatorLocal {
			return fmt.Errorf("fleetconfig: `agents` is an embed-only block, rejected in an %s source (fail-closed)", source)
		}
		if *seenFleet {
			return fmt.Errorf("fleetconfig: duplicate top-level roster block (`agents`/legacy `fleet`; fail-closed)")
		}
		*seenFleet = true
		return parseFleetBlock(n, f)
	case "fleet":
		if source == OperatorLocal {
			return fmt.Errorf("fleetconfig: `fleet` is an embed-only block, rejected in an %s source (fail-closed)", source)
		}
		if *seenFleet {
			return fmt.Errorf("fleetconfig: duplicate top-level roster block (`agents`/legacy `fleet`; fail-closed)")
		}
		*seenFleet = true
		return parseFleetBlock(n, f)
	case "director":
		d, err := parseDirector(n)
		if err != nil {
			return err
		}
		f.Director = &d
		return nil
	case "description":
		return applyTopLevelDescription(n, f)
	default:
		return unknownNode("top-level", n.Name(), "agents | fleet | director | description")
	}
}

// applyTopLevelDescription reads the optional top-level `description "..."` node
// into f.Description, rejecting a duplicate or an empty string (fail-closed).
func applyTopLevelDescription(n *kdl.Node, f *Fleet) error {
	if f.Description != "" {
		return fmt.Errorf("fleetconfig: duplicate top-level `description` (fail-closed)")
	}
	d, err := singleStringArg(n, "description")
	if err != nil {
		return err
	}
	if d == "" {
		return fmt.Errorf("fleetconfig: `description` must be a non-empty string (fail-closed)")
	}
	f.Description = d
	return nil
}

// fleetState tracks the once-only fields seen while walking a fleet block body.
type fleetState struct {
	seenVersion  bool
	seenDefaults bool
	seenRoles    bool
	names        map[string]bool // agent names, for duplicate detection
}

// parseFleetBlock fills the fleet-level fields from the `agents` block body.
func parseFleetBlock(n *kdl.Node, f *Fleet) error {
	if len(n.Arguments()) != 0 {
		return fmt.Errorf("fleetconfig: `agents` takes no arguments, only a block (fail-closed)")
	}
	st := &fleetState{names: map[string]bool{}}
	for _, c := range n.Children().Nodes {
		if err := applyFleetChild(c, f, st); err != nil {
			return err
		}
	}
	if !st.seenVersion {
		return fmt.Errorf("fleetconfig: `agents` block is missing `schema-version` (fail-closed)")
	}
	if len(f.Agents) == 0 {
		return fmt.Errorf("fleetconfig: `agents` block declares no `agent` (nothing to run; fail-closed)")
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
		return applyAgentChild(c, f, st)
	case "roles":
		return applyRolesChild(c, f, st)
	default:
		return unknownNode("agents body", c.Name(), "schema-version | defaults | agent | roles")
	}
}

// applyAgentChild parses one `agent` block and appends it, rejecting a duplicate.
func applyAgentChild(c *kdl.Node, f *Fleet, st *fleetState) error {
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
}

// applyRolesChild parses the once-only `roles` block into the per-role roster.
func applyRolesChild(c *kdl.Node, f *Fleet, st *fleetState) error {
	if st.seenRoles {
		return fmt.Errorf("fleetconfig: duplicate `roles` block (fail-closed)")
	}
	st.seenRoles = true
	roles, err := parseRoles(c)
	if err != nil {
		return err
	}
	f.Roles = roles
	return nil
}

// parseRoles reads the `roles { role <name> { guardfile ... } }` block: the
// per-role capability roster. Empty or malformed fails closed.
func parseRoles(n *kdl.Node) ([]Role, error) {
	if len(n.Arguments()) != 0 {
		return nil, fmt.Errorf("fleetconfig: `roles` takes no arguments, only a block (fail-closed)")
	}
	var out []Role
	seen := map[string]bool{}
	for _, c := range n.Children().Nodes {
		if c.Name() != "role" {
			return nil, unknownNode("roles body", c.Name(), "role")
		}
		role, err := parseRole(c)
		if err != nil {
			return nil, err
		}
		if seen[role.Name] {
			return nil, fmt.Errorf("fleetconfig: duplicate role %q (fail-closed)", role.Name)
		}
		seen[role.Name] = true
		out = append(out, role)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("fleetconfig: `roles` block declares no `role` (nothing to key capability on; fail-closed)")
	}
	return out, nil
}

// parseRole reads one role block with guardfile nodes and optional agent overrides.
// A role may hold no guardfiles and no agent overlay. `guardfiles` is a temporary alias.
func parseRole(n *kdl.Node) (Role, error) {
	name, err := singleStringArg(n, "role")
	if err != nil {
		return Role{}, fmt.Errorf("fleetconfig: `role` needs a single name, e.g. `role advisor`: %w", err)
	}
	st := roleState{
		role:           Role{Name: name},
		guardfileNames: map[string]bool{},
	}
	for _, c := range n.Children().Nodes {
		if err := st.applyChild(c); err != nil {
			return Role{}, err
		}
	}
	return st.role, nil
}

type roleState struct {
	role             Role
	guardfileNames   map[string]bool
	seenGuardfiles   bool
	seenLegacyBundle bool
}

func (st *roleState) applyChild(c *kdl.Node) error {
	switch c.Name() {
	case "guardfile":
		return st.applyGuardfile(c)
	case "guardfiles":
		return st.applyLegacyGuardfiles(c)
	case "agent":
		return st.applyAgent(c)
	default:
		return unknownNode(fmt.Sprintf("role %q body", st.role.Name), c.Name(), "guardfile | guardfiles | agent")
	}
}

func (st *roleState) applyGuardfile(c *kdl.Node) error {
	if st.seenLegacyBundle {
		return fmt.Errorf("fleetconfig: role %q mixes new `guardfile` nodes with legacy `guardfiles` (fail-closed)", st.role.Name)
	}
	st.seenGuardfiles = true
	gf, err := singleStringArg(c, fmt.Sprintf("role %q > guardfile", st.role.Name))
	if err != nil {
		return fmt.Errorf("fleetconfig: role %q `guardfile` needs a single guardfile name: %w", st.role.Name, err)
	}
	if st.guardfileNames[gf] {
		return fmt.Errorf("fleetconfig: role %q has a duplicate `guardfile %q` (fail-closed)", st.role.Name, gf)
	}
	st.guardfileNames[gf] = true
	st.role.Guardfiles.List = append(st.role.Guardfiles.List, gf)
	return nil
}

func (st *roleState) applyLegacyGuardfiles(c *kdl.Node) error {
	if st.seenGuardfiles {
		return fmt.Errorf("fleetconfig: role %q mixes new `guardfile` nodes with legacy `guardfiles` (fail-closed)", st.role.Name)
	}
	if st.seenLegacyBundle {
		return fmt.Errorf("fleetconfig: role %q has a duplicate legacy `guardfiles` alias (fail-closed)", st.role.Name)
	}
	st.seenLegacyBundle = true
	gf, err := parseGuardfiles(c, st.role.Name, st.guardfileNames)
	if err != nil {
		return err
	}
	st.role.Guardfiles = gf
	return nil
}

func (st *roleState) applyAgent(c *kdl.Node) error {
	an, ov, err := parseRoleAgent(c, st.role.Name)
	if err != nil {
		return err
	}
	if _, dup := st.role.AgentConfig[an]; dup {
		return fmt.Errorf("fleetconfig: role %q has a duplicate `agent %s` override (fail-closed)", st.role.Name, an)
	}
	if st.role.AgentConfig == nil {
		st.role.AgentConfig = map[string]RoleAgentOverride{}
	}
	st.role.AgentConfig[an] = ov
	return nil
}

// parseRoleAgent reads a role's `agent <name> { ... }` block: a sparse overlay of
// the overridable launch knobs, reusing the agent grammar's names.
func parseRoleAgent(n *kdl.Node, role string) (string, RoleAgentOverride, error) {
	name, err := singleStringArg(n, fmt.Sprintf("role %q > agent", role))
	if err != nil {
		return "", RoleAgentOverride{}, fmt.Errorf("fleetconfig: role %q `agent` needs a single name, e.g. `agent claude`: %w", role, err)
	}
	var o RoleAgentOverride
	// The overridable subset of the agent grammar's string knobs; a role may not
	// retune structural fields (binary, argv, context-level), only launch tuning.
	fields := map[string]*string{
		"model":            &o.Model,
		"endpoint":         &o.Endpoint,
		"reasoning-effort": &o.ReasoningEffort,
		"verbosity":        &o.Verbosity,
	}
	for _, c := range n.Children().Nodes {
		dst, ok := fields[c.Name()]
		if !ok {
			return "", RoleAgentOverride{}, unknownNode(
				fmt.Sprintf("role %q agent %q body", role, name), c.Name(),
				"model | endpoint | reasoning-effort | verbosity")
		}
		v, verr := singleStringArg(c, fmt.Sprintf("role %q agent %q > %s", role, name, c.Name()))
		if verr != nil {
			return "", RoleAgentOverride{}, verr
		}
		*dst = v
	}
	return name, o, nil
}

// parseGuardfiles reads a role's legacy `guardfiles` set: EITHER positional
// names (a flat list) OR a single `prefix=` property, never both, never neither.
func parseGuardfiles(n *kdl.Node, role string, seen map[string]bool) (Guardfiles, error) {
	args := stringArgs(n)
	prefix := ""
	for k, v := range n.Properties() {
		if k != "prefix" {
			return Guardfiles{}, fmt.Errorf("fleetconfig: role %q `guardfiles` has unknown property %q (want prefix; fail-closed)", role, k)
		}
		prefix = v.String()
	}
	switch {
	case len(args) > 0 && prefix != "":
		return Guardfiles{}, fmt.Errorf("fleetconfig: role %q `guardfiles` is a flat list OR a prefix=, not both (fail-closed)", role)
	case prefix != "":
		return Guardfiles{Prefix: prefix}, nil
	case len(args) > 0:
		for _, name := range args {
			if seen[name] {
				return Guardfiles{}, fmt.Errorf("fleetconfig: role %q has a duplicate `guardfile %q` (fail-closed)", role, name)
			}
			seen[name] = true
		}
		return Guardfiles{List: args}, nil
	default:
		return Guardfiles{}, fmt.Errorf("fleetconfig: role %q `guardfiles` needs a flat list of names or a prefix= (an empty node is ambiguous; fail-closed)", role)
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
// It accepts sparse top-level data for consumer-side defaulting.
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
