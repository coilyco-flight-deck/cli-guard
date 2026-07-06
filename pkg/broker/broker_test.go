package broker

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
)

// fakeExecutor records the last call and returns canned results, so a test can
// assert what the Server delegated and how it folded the result.
type fakeExecutor struct {
	lastOp     Op
	lastTarget Target
	lastTitle  string
	lastBody   string
	lastState  string
	lastMode   string
	lastLabels []string
	result     Result
	err        error
}

func (f *fakeExecutor) FileIssue(_ context.Context, t Target, title, body string) (Result, error) {
	f.lastOp, f.lastTarget, f.lastTitle, f.lastBody = OpFileIssue, t, title, body
	return f.result, f.err
}

func (f *fakeExecutor) EditIssue(_ context.Context, t Target, title, body, state string) (Result, error) {
	f.lastOp, f.lastTarget, f.lastTitle, f.lastBody, f.lastState = OpEditIssue, t, title, body, state
	return f.result, f.err
}

func (f *fakeExecutor) CommentIssue(_ context.Context, t Target, body string) (Result, error) {
	f.lastOp, f.lastTarget, f.lastBody = OpCommentIssue, t, body
	return f.result, f.err
}

func (f *fakeExecutor) LabelIssue(_ context.Context, t Target, mode string, labels []string) (Result, error) {
	f.lastOp, f.lastTarget, f.lastMode, f.lastLabels = OpLabelIssue, t, mode, labels
	return f.result, f.err
}

func (f *fakeExecutor) Dispatch(_ context.Context, t Target) (Result, error) {
	f.lastOp, f.lastTarget = OpDispatch, t
	return f.result, f.err
}

// newTestServer spins a Server on a unix socket under t.TempDir and returns a
// Client wired to it. The server stops when the test's context is cancelled.
func newTestServer(t *testing.T, ex Executor, auth Authorizer) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "broker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv, err := NewServer(ln, ex, auth)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = srv.Serve(ctx) }()
	return NewClient(sock)
}

func TestServerFileIssueRoundTrip(t *testing.T) {
	ex := &fakeExecutor{result: Result{Number: 42, URL: "https://example/issues/42"}}
	c := newTestServer(t, ex, Policy{})

	target := Target{Owner: "acme", Repo: "widget"}
	resp, err := c.FileIssue(context.Background(), target, "a title", "a body")
	if err != nil {
		t.Fatalf("FileIssue transport: %v", err)
	}
	if !resp.OK {
		t.Fatalf("want OK, got error %q", resp.Error)
	}
	if resp.Result.Number != 42 || resp.Result.URL != "https://example/issues/42" {
		t.Fatalf("result not folded through: %+v", resp.Result)
	}
	if ex.lastOp != OpFileIssue || ex.lastTitle != "a title" || ex.lastBody != "a body" {
		t.Fatalf("executor saw wrong call: op=%s title=%q body=%q", ex.lastOp, ex.lastTitle, ex.lastBody)
	}
	if ex.lastTarget != target {
		t.Fatalf("executor saw wrong target: %+v", ex.lastTarget)
	}
}

func TestServerAllWriteOpsReachExecutor(t *testing.T) {
	target := Target{Owner: "acme", Repo: "widget", Number: 7}
	cases := []struct {
		name string
		call func(*Client) (Response, error)
		want Op
	}{
		{"edit", func(c *Client) (Response, error) {
			return c.EditIssue(context.Background(), target, "t", "b", "closed")
		}, OpEditIssue},
		{"comment", func(c *Client) (Response, error) {
			return c.CommentIssue(context.Background(), target, "hi")
		}, OpCommentIssue},
		{"label", func(c *Client) (Response, error) {
			return c.LabelIssue(context.Background(), target, LabelAdd, []string{"headless"})
		}, OpLabelIssue},
		{"dispatch", func(c *Client) (Response, error) {
			return c.Dispatch(context.Background(), target)
		}, OpDispatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex := &fakeExecutor{result: Result{Detail: "done"}}
			c := newTestServer(t, ex, Policy{})
			resp, err := tc.call(c)
			if err != nil {
				t.Fatalf("transport: %v", err)
			}
			if !resp.OK {
				t.Fatalf("want OK, got %q", resp.Error)
			}
			if ex.lastOp != tc.want {
				t.Fatalf("want op %s, executor saw %s", tc.want, ex.lastOp)
			}
		})
	}
}

func TestServerLabelIssueFoldsModeAndLabels(t *testing.T) {
	ex := &fakeExecutor{result: Result{Number: 7, URL: "https://example/issues/7"}}
	c := newTestServer(t, ex, Policy{})

	target := Target{Owner: "acme", Repo: "widget", Number: 7}
	resp, err := c.LabelIssue(context.Background(), target, LabelAdd, []string{"headless", "42"})
	if err != nil {
		t.Fatalf("LabelIssue transport: %v", err)
	}
	if !resp.OK {
		t.Fatalf("want OK, got %q", resp.Error)
	}
	if ex.lastOp != OpLabelIssue || ex.lastMode != LabelAdd {
		t.Fatalf("executor saw wrong call: op=%s mode=%q", ex.lastOp, ex.lastMode)
	}
	if len(ex.lastLabels) != 2 || ex.lastLabels[0] != "headless" || ex.lastLabels[1] != "42" {
		t.Fatalf("labels not folded through: %+v", ex.lastLabels)
	}
}

func TestServerAuthorizerVetoSkipsExecutor(t *testing.T) {
	ex := &fakeExecutor{}
	// Owner allowlist that excludes the request's owner.
	c := newTestServer(t, ex, Policy{Owners: []string{"trusted"}})

	resp, err := c.FileIssue(context.Background(), Target{Owner: "stranger", Repo: "r"}, "t", "b")
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	if resp.OK {
		t.Fatal("want refusal, got OK")
	}
	if ex.lastOp != "" {
		t.Fatalf("executor ran despite veto: op=%s", ex.lastOp)
	}
}

func TestServerExecutorErrorFoldsToResponse(t *testing.T) {
	ex := &fakeExecutor{err: errors.New("forge exploded")}
	c := newTestServer(t, ex, Policy{})

	resp, err := c.FileIssue(context.Background(), Target{Owner: "acme", Repo: "r"}, "t", "b")
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	if resp.OK {
		t.Fatal("want failure, got OK")
	}
	if resp.Error == "" {
		t.Fatal("want error text, got empty")
	}
}

func TestServerRejectsProtocolMismatch(t *testing.T) {
	ex := &fakeExecutor{}
	c := newTestServer(t, ex, Policy{})

	// Bypass the Client's auto-stamp by sending the request raw.
	resp, err := c.Do(context.Background(), Request{Op: OpFileIssue, Target: Target{Owner: "acme", Repo: "r"}, Title: "t"})
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	if !resp.OK {
		t.Fatalf("Client.Do should stamp version and succeed: %q", resp.Error)
	}

	// Now a hand-built mismatched version must be rejected.
	bad := newRawClient(t, c.SocketPath)
	got := bad.send(t, Request{Version: 999, Op: OpFileIssue, Target: Target{Owner: "acme", Repo: "r"}, Title: "t"})
	if got.OK {
		t.Fatal("want version rejection, got OK")
	}
}

func TestNewServerRejectsNilDeps(t *testing.T) {
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "s.sock"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	if _, err := NewServer(nil, &fakeExecutor{}, Policy{}); err == nil {
		t.Error("want error for nil listener")
	}
	if _, err := NewServer(ln, nil, Policy{}); err == nil {
		t.Error("want error for nil executor")
	}
	if _, err := NewServer(ln, &fakeExecutor{}, nil); err == nil {
		t.Error("want error for nil authorizer")
	}
}

// rawClient sends hand-built frames so a test can exercise wire-level edge
// cases the typed Client would normalize away.
type rawClient struct{ path string }

func newRawClient(t *testing.T, path string) *rawClient {
	t.Helper()
	return &rawClient{path: path}
}

func (r *rawClient) send(t *testing.T, req Request) Response {
	t.Helper()
	conn, err := net.Dial("unix", r.path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if werr := writeRequest(conn, req); werr != nil {
		t.Fatalf("write: %v", werr)
	}
	resp, rerr := readResponse(conn)
	if rerr != nil {
		t.Fatalf("read: %v", rerr)
	}
	return resp
}
