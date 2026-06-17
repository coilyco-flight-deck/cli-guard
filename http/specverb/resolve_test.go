package specverb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
)

// loadProvingSpec reads and parses the proving-slice Forgejo spec from testdata.
func loadProvingSpec(t *testing.T) *swaggerSpec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "forgejo.swagger.v1.json"))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	spec, err := parseSwagger(raw)
	if err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	return spec
}

// TestResolveOpConvention asserts the CRUD verbs resolve to the right operationId
// from verb + resource alone (no `op`): method + the path resource segment.
func TestResolveOpConvention(t *testing.T) {
	spec := loadProvingSpec(t)
	cases := []struct {
		verb, resource, want string
	}{
		{"get", "repo", "repoGet"},
		{"delete", "repo", "repoDelete"},
		{"create", "repo", "createCurrentUserRepo"},
		{"list", "issue", "issueListIssues"},
		{"create", "issue", "issueCreateIssue"},
		{"edit", "issue", "issueEditIssue"},
		{"list", "tasks", "ListActionTasks"},
		{"view", "repo", "repoGet"}, // view aliases get (GET item)
	}
	for _, c := range cases {
		t.Run(c.verb+" "+c.resource, func(t *testing.T) {
			got, err := resolveOp(spec, guardfile.Grant{Modal: "can", Verb: c.verb, Resource: c.resource})
			if err != nil {
				t.Fatalf("resolveOp(%s %s) errored: %v", c.verb, c.resource, err)
			}
			if got != c.want {
				t.Errorf("resolveOp(%s %s) = %q, want %q", c.verb, c.resource, got, c.want)
			}
		})
	}
}

// TestResolveOpExplicitOverride asserts an explicit `op` always wins, even when
// it names a different operation than convention would pick.
func TestResolveOpExplicitOverride(t *testing.T) {
	spec := loadProvingSpec(t)
	got, err := resolveOp(spec, guardfile.Grant{Modal: "can", Verb: "get", Resource: "repo", Op: "repoDelete"})
	if err != nil {
		t.Fatalf("resolveOp with override errored: %v", err)
	}
	if got != "repoDelete" {
		t.Errorf("explicit op override = %q, want repoDelete", got)
	}
}

// TestResolveOpFailsClosed asserts resolution is deny-by-default: an
// unresolvable verb+resource is an error, never a silent guess.
func TestResolveOpFailsClosed(t *testing.T) {
	spec := loadProvingSpec(t)
	cases := []struct {
		name, verb, resource, wantSubstr string
	}{
		// no GET .../issues/{index} in the slice -> no item op for "get issue"
		{"no match", "get", "issue", "no GET operation"},
		// "frobnicate" is not a CRUD verb -> no convention
		{"unknown verb", "frobnicate", "repo", "no resolution convention"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := resolveOp(spec, guardfile.Grant{Modal: "can", Verb: c.verb, Resource: c.resource})
			if err == nil {
				t.Fatalf("resolveOp(%s %s) succeeded, want error", c.verb, c.resource)
			}
			if !strings.Contains(err.Error(), c.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), c.wantSubstr)
			}
		})
	}
}

// TestResolveOpAmbiguousFailsClosed asserts two operations matching the same
// verb+resource is a fail-closed error naming both candidates (create-repo case).
func TestResolveOpAmbiguousFailsClosed(t *testing.T) {
	spec := &swaggerSpec{Paths: map[string]map[string]swaggerOp{
		"/user/repos":       {"post": {OperationID: "createCurrentUserRepo"}},
		"/orgs/{org}/repos": {"post": {OperationID: "createOrgRepo"}},
	}}
	_, err := resolveOp(spec, guardfile.Grant{Modal: "can", Verb: "create", Resource: "repo"})
	if err == nil {
		t.Fatal("ambiguous resolveOp succeeded, want error")
	}
	for _, want := range []string{"createCurrentUserRepo", "createOrgRepo", "operations match"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error %q missing %q", err.Error(), want)
		}
	}
}

// TestResolveOpEditFallsBackToPut asserts `edit` resolves a PUT whole-replace API
// (Trello) when no PATCH exists, while preferring PATCH when both are present.
func TestResolveOpEditFallsBackToPut(t *testing.T) {
	put := &swaggerSpec{Paths: map[string]map[string]swaggerOp{
		"/boards/{id}": {"put": {OperationID: "put-boards-id"}},
	}}
	got, err := resolveOp(put, guardfile.Grant{Modal: "can", Verb: "edit", Resource: "board"})
	if err != nil {
		t.Fatalf("edit board (PUT) errored: %v", err)
	}
	if got != "put-boards-id" {
		t.Errorf("edit board fell back to %q, want put-boards-id", got)
	}
}
