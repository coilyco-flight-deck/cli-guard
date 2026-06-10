// Curated, committed verb-class -> operation expansion table: the human-
// reviewed allowlist. Deny-by-default lives here. See docs/specverb.md.

package specverb

// grantKey is the (verb-class, resource) pair an authored grant names.
type grantKey struct {
	Verb     string
	Resource string
}

// tableEntry maps a grant onto a CLI placement (Group noun, Leaf verb) and the
// spec op; FixedBody set = state-toggle leaf sending exactly that JSON, no flags.
type tableEntry struct {
	Group       string
	Leaf        string
	OperationID string
	FixedBody   map[string]string
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

	// issues: the array-body + query-param group; view aliases read, and
	// comment posts to the comments sub-resource.
	{Verb: "read", Resource: "issues"}:    {Group: "issue", Leaf: "get", OperationID: "issueGetIssue"},
	{Verb: "view", Resource: "issues"}:    {Group: "issue", Leaf: "view", OperationID: "issueGetIssue"},
	{Verb: "list", Resource: "issues"}:    {Group: "issue", Leaf: "list", OperationID: "issueListIssues"},
	{Verb: "create", Resource: "issues"}:  {Group: "issue", Leaf: "create", OperationID: "issueCreateIssue"},
	{Verb: "edit", Resource: "issues"}:    {Group: "issue", Leaf: "edit", OperationID: "issueEditIssue"},
	{Verb: "comment", Resource: "issues"}: {Group: "issue", Leaf: "comment", OperationID: "issueCreateComment"},
	{Verb: "delete", Resource: "issues"}:  {Group: "issue", Leaf: "delete", OperationID: "issueDelete"},

	// state toggles: fixed-body PATCHes over the edit ops. Reversible by
	// construction (close <-> reopen), so never flagged destructive.
	{Verb: "close", Resource: "issues"}:      {Group: "issue", Leaf: "close", OperationID: "issueEditIssue", FixedBody: map[string]string{"state": "closed"}},
	{Verb: "reopen", Resource: "issues"}:     {Group: "issue", Leaf: "reopen", OperationID: "issueEditIssue", FixedBody: map[string]string{"state": "open"}},
	{Verb: "close", Resource: "milestones"}:  {Group: "milestone", Leaf: "close", OperationID: "issueEditMilestone", FixedBody: map[string]string{"state": "closed"}},
	{Verb: "reopen", Resource: "milestones"}: {Group: "milestone", Leaf: "reopen", OperationID: "issueEditMilestone", FixedBody: map[string]string{"state": "open"}},
}

// destructiveLeaves names the irreversibly-mutating leaves. The descriptor
// surfaces this; the --yes confirm UX that consumes it is the M2 pass.
var destructiveLeaves = map[string]bool{"delete": true}

// lookupExpansion resolves a grant; ok=false is the deny-by-default signal.
func lookupExpansion(verb, resource string) (tableEntry, bool) {
	e, ok := expansionTable[grantKey{Verb: verb, Resource: resource}]
	return e, ok
}
