// Package agentclaim defines the policy-free identity facts shared by agent
// context producers and authority launchers.
//
// A Claim keeps context and authority roles in separate domains. The package
// validates structure only. Consumers decide which facts they require and
// whether they trust the producer that supplied each role.
package agentclaim

import (
	"fmt"
	"strings"

	kdl "github.com/calico32/kdl-go"
)

// SchemaVersion is the agent-claim KDL dialect this package parses.
const SchemaVersion = 1

// Domain identifies what a role claim describes.
type Domain string

const (
	// DomainContext identifies organizational purpose and context selection.
	DomainContext Domain = "context"

	// DomainAuthority identifies independently resolved execution authority.
	DomainAuthority Domain = "authority"
)

// Role is one opaque role name in an explicit domain.
type Role struct {
	Domain Domain `json:"domain"`
	Name   string `json:"name"`
}

// Claim is a resolved agent subject. Optional scalar facts remain independent:
// the package never derives one from another or assigns consumer defaults.
type Claim struct {
	SchemaVersion   int    `json:"schema_version"`
	Agent           string `json:"agent,omitempty"`
	Roles           []Role `json:"roles"`
	Model           string `json:"model,omitempty"`
	ModelClass      string `json:"model_class,omitempty"`
	Harness         string `json:"harness,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// ParseKDL parses and validates one agent-claim document. Unknown vocabulary,
// duplicate scalar facts, and unsupported schema versions fail closed.
func ParseKDL(src []byte) (Claim, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return Claim{}, fmt.Errorf("agentclaim: parse KDL: %w", err)
	}
	root, err := singleClaimNode(doc.Nodes)
	if err != nil {
		return Claim{}, err
	}
	return ParseNode(root)
}

// ParseNode validates an embedded agent-claim node. It applies the same rules
// as ParseKDL after that wrapper checks whole-document cardinality.
func ParseNode(root *kdl.Node) (Claim, error) {
	if root == nil {
		return Claim{}, fmt.Errorf("agentclaim: node is nil (fail-closed)")
	}
	if root.Name() != "agent-claim" {
		return Claim{}, fmt.Errorf("agentclaim: node %q is not `agent-claim` (fail-closed)", root.Name())
	}
	if len(root.Arguments()) != 0 {
		return Claim{}, fmt.Errorf("agentclaim: `agent-claim` takes no arguments, only schema-version= and a block (fail-closed)")
	}
	version, err := schemaVersion(root)
	if err != nil {
		return Claim{}, err
	}

	state := claimParseState{
		claim: Claim{SchemaVersion: version},
		seen:  map[string]bool{},
	}
	for _, child := range root.Children().Nodes {
		if err := state.applyChild(child); err != nil {
			return Claim{}, err
		}
	}
	if err := state.claim.Validate(); err != nil {
		return Claim{}, err
	}
	return state.claim, nil
}

func singleClaimNode(nodes []*kdl.Node) (*kdl.Node, error) {
	if len(nodes) != 1 {
		return nil, fmt.Errorf("agentclaim: document needs exactly one `agent-claim` node, got %d (fail-closed)", len(nodes))
	}
	return nodes[0], nil
}

func schemaVersion(root *kdl.Node) (int, error) {
	properties := root.Properties()
	version, hasVersion := properties["schema-version"]
	if !hasVersion {
		for name := range properties {
			return 0, fmt.Errorf("agentclaim: `agent-claim` has unknown property %q (want schema-version; fail-closed)", name)
		}
		return 0, fmt.Errorf("agentclaim: `agent-claim` is missing schema-version (fail-closed)")
	}
	if len(properties) != 1 {
		for name := range properties {
			if name != "schema-version" {
				return 0, fmt.Errorf("agentclaim: `agent-claim` has unknown property %q (want schema-version; fail-closed)", name)
			}
		}
	}
	if version.Kind() != kdl.Int {
		return 0, fmt.Errorf("agentclaim: schema-version must be an integer (fail-closed)")
	}
	return version.Int(), nil
}

type claimParseState struct {
	claim Claim
	seen  map[string]bool
}

func (s *claimParseState) applyChild(child *kdl.Node) error {
	if child.Name() == "role" {
		role, err := parseRole(child)
		if err != nil {
			return err
		}
		s.claim.Roles = append(s.claim.Roles, role)
		return nil
	}

	field, ok := scalarField(&s.claim, child.Name())
	if !ok {
		return fmt.Errorf("agentclaim: unknown node %q (want agent | role | model | model-class | harness | reasoning-effort; fail-closed)", child.Name())
	}
	if s.seen[child.Name()] {
		return fmt.Errorf("agentclaim: duplicate `%s` node (fail-closed)", child.Name())
	}
	s.seen[child.Name()] = true
	value, err := singleString(child, child.Name())
	if err != nil {
		return err
	}
	*field = value
	return nil
}

// Validate applies ParseKDL's structural rules to a constructed claim.
// Consumers apply completeness and producer-trust policy afterward.
func (c Claim) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("agentclaim: schema-version %d is not the supported dialect %d (fail-closed)", c.SchemaVersion, SchemaVersion)
	}
	if len(c.Roles) == 0 {
		return fmt.Errorf("agentclaim: claim declares no role (fail-closed)")
	}

	optional := []struct {
		name  string
		value string
	}{
		{name: "agent", value: c.Agent},
		{name: "model", value: c.Model},
		{name: "model-class", value: c.ModelClass},
		{name: "harness", value: c.Harness},
		{name: "reasoning-effort", value: c.ReasoningEffort},
	}
	for _, field := range optional {
		if err := validateOptional(field.name, field.value); err != nil {
			return err
		}
	}

	seen := map[Domain]bool{}
	for _, role := range c.Roles {
		switch role.Domain {
		case DomainContext, DomainAuthority:
		default:
			return fmt.Errorf("agentclaim: role %q has unknown domain %q (want context | authority; fail-closed)", role.Name, role.Domain)
		}
		if strings.TrimSpace(role.Name) == "" || strings.TrimSpace(role.Name) != role.Name {
			return fmt.Errorf("agentclaim: role in domain %q needs a non-empty name without surrounding whitespace (fail-closed)", role.Domain)
		}
		if seen[role.Domain] {
			return fmt.Errorf("agentclaim: duplicate role domain %q (fail-closed)", role.Domain)
		}
		seen[role.Domain] = true
	}
	return nil
}

func scalarField(c *Claim, name string) (*string, bool) {
	switch name {
	case "agent":
		return &c.Agent, true
	case "model":
		return &c.Model, true
	case "model-class":
		return &c.ModelClass, true
	case "harness":
		return &c.Harness, true
	case "reasoning-effort":
		return &c.ReasoningEffort, true
	default:
		return nil, false
	}
}

func parseRole(node *kdl.Node) (Role, error) {
	name, err := singleString(node, "role")
	if err != nil {
		return Role{}, err
	}
	if len(node.Properties()) != 1 {
		return Role{}, fmt.Errorf("agentclaim: role %q needs exactly one domain= property (fail-closed)", name)
	}
	domainValue, ok := node.Properties()["domain"]
	if !ok {
		return Role{}, fmt.Errorf("agentclaim: role %q is missing domain= (fail-closed)", name)
	}
	if domainValue.Kind() != kdl.String {
		return Role{}, fmt.Errorf("agentclaim: role %q domain must be a string (fail-closed)", name)
	}
	return Role{Domain: Domain(domainValue.String()), Name: name}, nil
}

func singleString(node *kdl.Node, label string) (string, error) {
	if len(node.Children().Nodes) != 0 {
		return "", fmt.Errorf("agentclaim: `%s` takes no child block (fail-closed)", label)
	}
	if label != "role" && len(node.Properties()) != 0 {
		return "", fmt.Errorf("agentclaim: `%s` takes no properties (fail-closed)", label)
	}
	args := node.Arguments()
	if len(args) != 1 || args[0].Kind() != kdl.String {
		return "", fmt.Errorf("agentclaim: `%s` needs exactly one string value (fail-closed)", label)
	}
	value := args[0].String()
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("agentclaim: `%s` needs a non-empty value without surrounding whitespace (fail-closed)", label)
	}
	return value, nil
}

func validateOptional(name, value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("agentclaim: %s has surrounding whitespace (fail-closed)", name)
	}
	return nil
}
