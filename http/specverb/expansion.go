// Curated, committed verb-class -> operation expansion table: the human-
// reviewed allowlist. Deny-by-default lives here. See docs/specverb.md.

package specverb

// grantKey is the (verb-class, resource) pair an authored grant names.
type grantKey struct {
	Verb     string
	Resource string
}

// tableEntry maps a grant onto a CLI placement and the spec op it drives.
// Group is the singular CLI noun (repos -> repo); Leaf is the CLI verb.
type tableEntry struct {
	Group       string
	Leaf        string
	OperationID string
}

// expansionTable is the M0 allowlist: the forgejo repo and org
// read/create/delete trios. Everything else fails closed.
var expansionTable = map[grantKey]tableEntry{
	{Verb: "read", Resource: "repos"}:   {Group: "repo", Leaf: "get", OperationID: "repoGet"},
	{Verb: "create", Resource: "repos"}: {Group: "repo", Leaf: "create", OperationID: "createCurrentUserRepo"},
	{Verb: "delete", Resource: "repos"}: {Group: "repo", Leaf: "delete", OperationID: "repoDelete"},

	{Verb: "read", Resource: "orgs"}:   {Group: "org", Leaf: "get", OperationID: "orgGet"},
	{Verb: "list", Resource: "orgs"}:   {Group: "org", Leaf: "list", OperationID: "orgGetAll"},
	{Verb: "create", Resource: "orgs"}: {Group: "org", Leaf: "create", OperationID: "orgCreate"},
	{Verb: "delete", Resource: "orgs"}: {Group: "org", Leaf: "delete", OperationID: "orgDelete"},
}

// destructiveLeaves names the irreversibly-mutating leaves. The descriptor
// surfaces this; the --yes confirm UX that consumes it is the M2 pass.
var destructiveLeaves = map[string]bool{"delete": true}

// lookupExpansion resolves a grant; ok=false is the deny-by-default signal.
func lookupExpansion(verb, resource string) (tableEntry, bool) {
	e, ok := expansionTable[grantKey{Verb: verb, Resource: resource}]
	return e, ok
}
