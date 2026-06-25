package broker

import "context"

// Executor is the injected privileged side: it holds the credential and talks
// to the forge/dispatch backend. Lives in the consumer; see docs/broker.md.
type Executor interface {
	// FileIssue files a new issue under target with title+body, returning its
	// created number and URL.
	FileIssue(ctx context.Context, target Target, title, body string) (Result, error)
	// EditIssue edits target's issue; empty title/body/state leave that field
	// unchanged.
	EditIssue(ctx context.Context, target Target, title, body, state string) (Result, error)
	// CommentIssue posts body as a comment on target's issue.
	CommentIssue(ctx context.Context, target Target, body string) (Result, error)
	// Dispatch dispatches work for target's issue.
	Dispatch(ctx context.Context, target Target) (Result, error)
}

// execute routes an authorized request to the matching [Executor] method. The
// op is already validated; the default branch is a defensive backstop.
func execute(ctx context.Context, ex Executor, req Request) (Result, error) {
	switch req.Op {
	case OpFileIssue:
		return ex.FileIssue(ctx, req.Target, req.Title, req.Body)
	case OpEditIssue:
		return ex.EditIssue(ctx, req.Target, req.Title, req.Body, req.State)
	case OpCommentIssue:
		return ex.CommentIssue(ctx, req.Target, req.Body)
	case OpDispatch:
		return ex.Dispatch(ctx, req.Target)
	default:
		return Result{}, errUnknownOp(req.Op)
	}
}
