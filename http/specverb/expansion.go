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

// expansionTable is the curated allowlist: the forgejo repo and org trios
// plus the all-scalar label and milestone groups. Everything else fails closed.
var expansionTable = map[grantKey]tableEntry{
	{Verb: "read", Resource: "repos"}:   {Group: "repo", Leaf: "get", OperationID: "repoGet"},
	{Verb: "create", Resource: "repos"}: {Group: "repo", Leaf: "create", OperationID: "createCurrentUserRepo"},
	{Verb: "delete", Resource: "repos"}: {Group: "repo", Leaf: "delete", OperationID: "repoDelete"},

	{Verb: "read", Resource: "orgs"}:   {Group: "org", Leaf: "get", OperationID: "orgGet"},
	{Verb: "list", Resource: "orgs"}:   {Group: "org", Leaf: "list", OperationID: "orgGetAll"},
	{Verb: "create", Resource: "orgs"}: {Group: "org", Leaf: "create", OperationID: "orgCreate"},
	{Verb: "delete", Resource: "orgs"}: {Group: "org", Leaf: "delete", OperationID: "orgDelete"},

	// labels and milestones: the all-scalar-body groups, so the full
	// read/list/create/edit/delete fan-out is pure table rows.
	{Verb: "read", Resource: "labels"}:   {Group: "label", Leaf: "get", OperationID: "issueGetLabel"},
	{Verb: "list", Resource: "labels"}:   {Group: "label", Leaf: "list", OperationID: "issueListLabels"},
	{Verb: "create", Resource: "labels"}: {Group: "label", Leaf: "create", OperationID: "issueCreateLabel"},
	{Verb: "edit", Resource: "labels"}:   {Group: "label", Leaf: "edit", OperationID: "issueEditLabel"},
	{Verb: "delete", Resource: "labels"}: {Group: "label", Leaf: "delete", OperationID: "issueDeleteLabel"},

	{Verb: "read", Resource: "milestones"}:   {Group: "milestone", Leaf: "get", OperationID: "issueGetMilestone"},
	{Verb: "list", Resource: "milestones"}:   {Group: "milestone", Leaf: "list", OperationID: "issueGetMilestonesList"},
	{Verb: "create", Resource: "milestones"}: {Group: "milestone", Leaf: "create", OperationID: "issueCreateMilestone"},
	{Verb: "edit", Resource: "milestones"}:   {Group: "milestone", Leaf: "edit", OperationID: "issueEditMilestone"},
	{Verb: "delete", Resource: "milestones"}: {Group: "milestone", Leaf: "delete", OperationID: "issueDeleteMilestone"},
}

// destructiveLeaves names the irreversibly-mutating leaves. The descriptor
// surfaces this; the --yes confirm UX that consumes it is the M2 pass.
var destructiveLeaves = map[string]bool{"delete": true}

// lookupExpansion resolves a grant; ok=false is the deny-by-default signal.
func lookupExpansion(verb, resource string) (tableEntry, bool) {
	e, ok := expansionTable[grantKey{Verb: verb, Resource: resource}]
	return e, ok
}
