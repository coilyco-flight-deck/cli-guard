package agentclaim

import (
	"encoding/json"
	"strings"
	"testing"

	kdl "github.com/calico32/kdl-go"
)

func TestParseKDLContextOnly(t *testing.T) {
	t.Parallel()

	claim, err := ParseKDL([]byte(`agent-claim schema-version=1 {
    agent "example-agent"
    role "builder" domain="context"
    model "example-model"
    model-class "frontier"
    harness "example-harness"
    reasoning-effort "high"
}`))
	if err != nil {
		t.Fatalf("ParseKDL: %v", err)
	}
	if claim.SchemaVersion != SchemaVersion || claim.Agent != "example-agent" ||
		claim.Model != "example-model" || claim.ModelClass != "frontier" ||
		claim.Harness != "example-harness" || claim.ReasoningEffort != "high" {
		t.Fatalf("claim scalar facts = %+v", claim)
	}
	if len(claim.Roles) != 1 || claim.Roles[0] != (Role{Domain: DomainContext, Name: "builder"}) {
		t.Fatalf("claim roles = %+v", claim.Roles)
	}
}

func TestParseKDLAuthorityOnlyAllowsPartialSubject(t *testing.T) {
	t.Parallel()

	claim, err := ParseKDL([]byte(`agent-claim schema-version=1 {
    role "reviewer" domain="authority"
}`))
	if err != nil {
		t.Fatalf("ParseKDL: %v", err)
	}
	if len(claim.Roles) != 1 || claim.Roles[0].Domain != DomainAuthority {
		t.Fatalf("claim roles = %+v", claim.Roles)
	}
	if claim.Agent != "" || claim.Harness != "" {
		t.Fatalf("partial claim gained defaults: %+v", claim)
	}
}

func TestParseKDLComposedRolesStaySeparate(t *testing.T) {
	t.Parallel()

	claim, err := ParseKDL([]byte(`agent-claim schema-version=1 {
    role "builder" domain="context"
    role "reviewer" domain="authority"
}`))
	if err != nil {
		t.Fatalf("ParseKDL: %v", err)
	}
	if len(claim.Roles) != 2 || claim.Roles[0].Name != "builder" || claim.Roles[1].Name != "reviewer" {
		t.Fatalf("claim roles = %+v", claim.Roles)
	}
}

func TestParseNodeAcceptsEmbeddedClaim(t *testing.T) {
	t.Parallel()

	doc, err := kdl.ParseString(`consumer schema-version=1 {
    agent-claim schema-version=1 {
        agent "example-agent"
        role "builder" domain="context"
        harness "example-harness"
    }
}`)
	if err != nil {
		t.Fatalf("parse consumer KDL: %v", err)
	}
	claim, err := ParseNode(doc.Nodes[0].Children().Nodes[0])
	if err != nil {
		t.Fatalf("ParseNode: %v", err)
	}
	if claim.Agent != "example-agent" || claim.Harness != "example-harness" ||
		len(claim.Roles) != 1 || claim.Roles[0].Domain != DomainContext {
		t.Fatalf("embedded claim = %+v", claim)
	}
}

func TestParseNodeRejectsNonClaimNode(t *testing.T) {
	t.Parallel()

	doc, err := kdl.ParseString(`subject schema-version=1 {}`)
	if err != nil {
		t.Fatalf("parse KDL: %v", err)
	}
	_, err = ParseNode(doc.Nodes[0])
	if err == nil || !strings.Contains(err.Error(), "is not `agent-claim`") {
		t.Fatalf("ParseNode error = %v", err)
	}
}

func TestClaimJSONUsesStablePublicFields(t *testing.T) {
	t.Parallel()

	claim := Claim{
		SchemaVersion: SchemaVersion,
		Agent:         "example-agent",
		Roles:         []Role{{Domain: DomainContext, Name: "builder"}},
		ModelClass:    "frontier",
	}
	got, err := json.Marshal(claim)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"schema_version":1,"agent":"example-agent","roles":[{"domain":"context","name":"builder"}],"model_class":"frontier"}`
	if string(got) != want {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

func TestParseKDLFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "invalid KDL", src: `agent-claim {`, want: "parse KDL"},
		{name: "empty document", src: ``, want: "exactly one"},
		{name: "extra top level", src: `agent-claim schema-version=1 {}
agent-claim schema-version=1 {}`, want: "exactly one"},
		{name: "wrong root", src: `subject schema-version=1 {}`, want: "is not `agent-claim`"},
		{name: "root argument", src: `agent-claim "x" schema-version=1 {}`, want: "takes no arguments"},
		{name: "missing version", src: `agent-claim {
    role "builder" domain="context"
}`, want: "missing schema-version"},
		{name: "wrong version kind", src: `agent-claim schema-version="1" {}`, want: "must be an integer"},
		{name: "unsupported version", src: `agent-claim schema-version=2 {
    role "builder" domain="context"
}`, want: "supported dialect"},
		{name: "unknown root property", src: `agent-claim schema-version=1 extra=true {}`, want: "unknown property"},
		{name: "no role", src: `agent-claim schema-version=1 {
    agent "example"
}`, want: "declares no role"},
		{name: "unknown node", src: `agent-claim schema-version=1 {
    role "builder" domain="context"
    personality "warm"
}`, want: "unknown node"},
		{name: "duplicate scalar", src: `agent-claim schema-version=1 {
    role "builder" domain="context"
    agent "a"
    agent "b"
}`, want: "duplicate `agent`"},
		{name: "duplicate domain", src: `agent-claim schema-version=1 {
    role "a" domain="context"
    role "b" domain="context"
}`, want: "duplicate role domain"},
		{name: "missing role domain", src: `agent-claim schema-version=1 {
    role "builder"
}`, want: "domain="},
		{name: "unknown role domain", src: `agent-claim schema-version=1 {
    role "builder" domain="permission"
}`, want: "unknown domain"},
		{name: "extra role property", src: `agent-claim schema-version=1 {
    role "builder" domain="context" grant=#true
}`, want: "exactly one domain="},
		{name: "empty role", src: `agent-claim schema-version=1 {
    role "" domain="context"
}`, want: "non-empty value"},
		{name: "scalar property", src: `agent-claim schema-version=1 {
    role "builder" domain="context"
    agent "a" extra=#true
}`, want: "takes no properties"},
		{name: "scalar child", src: `agent-claim schema-version=1 {
    role "builder" domain="context"
    agent "a" {
        nested "x"
    }
}`, want: "takes no child block"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseKDL([]byte(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseKDL error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateRejectsProgrammaticWhitespace(t *testing.T) {
	t.Parallel()

	claim := Claim{
		SchemaVersion: SchemaVersion,
		Harness:       " example ",
		Roles:         []Role{{Domain: DomainContext, Name: "builder"}},
	}
	if err := claim.Validate(); err == nil || !strings.Contains(err.Error(), "surrounding whitespace") {
		t.Fatalf("Validate error = %v", err)
	}
}
