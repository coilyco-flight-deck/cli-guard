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

// TestResolveOpConvention covers the generic conventions against the proving
// slice: CRUD, toggles, compound membership, sub-collection create - no `op`.
func TestResolveOpConvention(t *testing.T) {
	spec := loadProvingSpec(t)
	cases := []struct {
		verb, resource, want string
	}{
		{"get", "repo", "repoGet"},                  // GET item
		{"delete", "repo", "repoDelete"},            // DELETE item
		{"create", "repo", "createCurrentUserRepo"}, // POST collection
		{"list", "issue", "issueListIssues"},        // GET collection
		{"create", "issue", "issueCreateIssue"},
		{"edit", "issue", "issueEditIssue"},   // PATCH item
		{"view", "repo", "repoGet"},           // view aliases get
		{"close", "issue", "issueEditIssue"},  // toggle -> edit op (PATCH item)
		{"reopen", "issue", "issueEditIssue"}, // toggle -> edit op
		{"list", "tasks", "ListActionTasks"},
		{"add", "issue-label", "issueAddLabel"},                    // membership on compound resource
		{"upload-asset", "release", "repoCreateReleaseAttachment"}, // sub-collection create (unknown verb)
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

// TestResolveOpExplicitOverride asserts an explicit `op` always wins.
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

// TestResolveOpLeastNested asserts the canonical (shallowest) path wins when a
// deeper nested path shares the resource segment.
func TestResolveOpLeastNested(t *testing.T) {
	spec := &swaggerSpec{Paths: map[string]map[string]swaggerOp{
		"/repos/{owner}/{repo}":          {"get": {OperationID: "repoGet"}},
		"/teams/{id}/repos/{org}/{repo}": {"get": {OperationID: "orgListTeamRepo"}},
	}}
	got, err := resolveOp(spec, guardfile.Grant{Modal: "can", Verb: "get", Resource: "repo"})
	if err != nil {
		t.Fatalf("least-nested resolve errored: %v", err)
	}
	if got != "repoGet" {
		t.Errorf("least-nested = %q, want repoGet (the shallowest path)", got)
	}
}

// TestResolveOpPreferPluralCollection asserts a true (plural) collection segment
// wins over a singular singleton at the same depth (createKey vs updateDeviceKey).
func TestResolveOpPreferPluralCollection(t *testing.T) {
	spec := &swaggerSpec{Paths: map[string]map[string]swaggerOp{
		"/tailnet/{tailnet}/keys": {"post": {OperationID: "createKey"}},
		"/device/{deviceId}/key":  {"post": {OperationID: "updateDeviceKey"}},
	}}
	got, err := resolveOp(spec, guardfile.Grant{Modal: "can", Verb: "create", Resource: "keys"})
	if err != nil {
		t.Fatalf("plural-preference resolve errored: %v", err)
	}
	if got != "createKey" {
		t.Errorf("plural preference = %q, want createKey", got)
	}
}

// TestResolveOpSearch asserts the search verb matches the `<resource>/search` suffix.
func TestResolveOpSearch(t *testing.T) {
	spec := &swaggerSpec{Paths: map[string]map[string]swaggerOp{
		"/repos/search": {"get": {OperationID: "repoSearch"}},
		"/user/repos":   {"get": {OperationID: "userCurrentListRepos"}},
	}}
	got, err := resolveOp(spec, guardfile.Grant{Modal: "can", Verb: "search", Resource: "repo"})
	if err != nil {
		t.Fatalf("search resolve errored: %v", err)
	}
	if got != "repoSearch" {
		t.Errorf("search repo = %q, want repoSearch", got)
	}
}

// TestResolveOpListChild asserts `list-<child>` lists a sub-collection of the resource.
func TestResolveOpListChild(t *testing.T) {
	spec := &swaggerSpec{Paths: map[string]map[string]swaggerOp{
		"/boards/{id}/lists": {"get": {OperationID: "get-boards-id-lists"}},
	}}
	got, err := resolveOp(spec, guardfile.Grant{Modal: "can", Verb: "list-lists", Resource: "board"})
	if err != nil {
		t.Fatalf("list-lists resolve errored: %v", err)
	}
	if got != "get-boards-id-lists" {
		t.Errorf("list-lists board = %q, want get-boards-id-lists", got)
	}
}

// TestResolveOpFailsClosed asserts resolution is deny-by-default: a verb+resource
// with no matching operation is an error, never a silent guess.
func TestResolveOpFailsClosed(t *testing.T) {
	spec := loadProvingSpec(t)
	// no GET .../issues/{index} in the slice -> no item op for "get issue"
	_, err := resolveOp(spec, guardfile.Grant{Modal: "can", Verb: "get", Resource: "issue"})
	if err == nil {
		t.Fatal("resolveOp(get issue) succeeded, want error")
	}
	if !strings.Contains(err.Error(), "no operation matches") {
		t.Errorf("error %q does not name the deny-by-default reason", err.Error())
	}
}

// TestResolveOpAmbiguousFailsClosed asserts a genuine depth tie (two equally-shallow
// paths) fails closed, naming both candidates.
func TestResolveOpAmbiguousFailsClosed(t *testing.T) {
	spec := &swaggerSpec{Paths: map[string]map[string]swaggerOp{
		"/orgs/{org}/repos": {"post": {OperationID: "createOrgRepo"}},
		"/teams/{id}/repos": {"post": {OperationID: "createTeamRepo"}},
	}}
	_, err := resolveOp(spec, guardfile.Grant{Modal: "can", Verb: "create", Resource: "repo"})
	if err == nil {
		t.Fatal("ambiguous resolveOp succeeded, want error")
	}
	for _, want := range []string{"createOrgRepo", "createTeamRepo", "operations match"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error %q missing %q", err.Error(), want)
		}
	}
}
