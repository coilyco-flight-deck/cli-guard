// Package broker is the policy core for a root credential broker: a small
// request/response protocol over a unix socket through which an unprivileged
// client (e.g. an explore agent) asks a privileged Server to file, edit, and
// comment on issues, and to dispatch work - without ever holding the
// credential itself.
//
// The package is deliberately self-contained. It carries no git, docker, or
// ward-kdl knowledge: the privileged side is injected as an [Executor], and
// the write-tier policy as an [Authorizer]. That keeps broker importable by a
// consumer (ward) without a dependency cycle, and keeps this high-risk
// live-dispatch path's surface minimal and auditable.
//
// The wire format is newline-delimited JSON: exactly one [Request] in, exactly
// one [Response] out, then the connection closes. Every Request carries
// [ProtocolVersion]; the Server rejects a mismatch rather than guessing.
// See docs/broker.md.
package broker

// ProtocolVersion is the broker wire-protocol version, bumped on any breaking
// change to [Request]/[Response]. The Server refuses an unrecognized Version.
const ProtocolVersion = 1

// Op names a broker operation. The four below are the complete write-tier
// surface; the Server authorizes and executes each and rejects any other.
type Op string

const (
	// OpFileIssue files a new issue. Target carries Owner+Repo; the forge
	// assigns the number, returned in the [Result].
	OpFileIssue Op = "file_issue"
	// OpEditIssue edits an existing issue's title, body, and/or state.
	OpEditIssue Op = "edit_issue"
	// OpCommentIssue posts a comment on an existing issue.
	OpCommentIssue Op = "comment_issue"
	// OpDispatch dispatches work for an existing issue.
	OpDispatch Op = "dispatch"
)

// WriteOps is the fixed set of write-tier operations the broker serves: the
// default [Authorizer] allowlist. Callers read it but must not mutate it.
var WriteOps = map[Op]bool{
	OpFileIssue:    true,
	OpEditIssue:    true,
	OpCommentIssue: true,
	OpDispatch:     true,
}

// Target identifies the issue an operation acts on. [OpFileIssue] uses only
// Owner+Repo; the other ops require a positive Number.
type Target struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int    `json:"number,omitempty"`
}

// Request is one broker call: Op selects the operation, Target names the
// issue, and Title/Body/State carry the op-specific payload.
type Request struct {
	// Version must equal [ProtocolVersion]; the Server rejects a mismatch.
	Version int `json:"version"`
	// Op is the operation to authorize and execute.
	Op Op `json:"op"`
	// Target is the issue the operation acts on.
	Target Target `json:"target"`
	// Title is the issue title (required for file, optional for edit).
	Title string `json:"title,omitempty"`
	// Body is the issue body or comment text.
	Body string `json:"body,omitempty"`
	// State is the desired issue state for edit; empty leaves it unchanged.
	State string `json:"state,omitempty"`
}

// Result is the success payload of a [Response]; which fields are populated
// depends on the [Op] (file/edit/comment set Number+URL, dispatch sets Detail).
type Result struct {
	// Number is the affected issue's number.
	Number int `json:"number,omitempty"`
	// URL is a link to the affected issue or comment.
	URL string `json:"url,omitempty"`
	// Detail is a human-readable note (e.g. a dispatch handle).
	Detail string `json:"detail,omitempty"`
}

// Response is the broker's single reply: OK reports success; on failure Error
// carries the reason and Result is zero.
type Response struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Result Result `json:"result,omitempty"`
}
