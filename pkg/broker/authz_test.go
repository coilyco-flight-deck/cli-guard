package broker

import (
	"context"
	"testing"
)

func TestPolicyAuthorize(t *testing.T) {
	cases := []struct {
		name   string
		policy Policy
		req    Request
		wantOK bool
	}{
		{
			name:   "zero policy allows file with title",
			policy: Policy{},
			req:    Request{Op: OpFileIssue, Target: Target{Owner: "acme", Repo: "r"}, Title: "t"},
			wantOK: true,
		},
		{
			name:   "file without title refused",
			policy: Policy{},
			req:    Request{Op: OpFileIssue, Target: Target{Owner: "acme", Repo: "r"}},
			wantOK: false,
		},
		{
			name:   "edit needs positive number",
			policy: Policy{},
			req:    Request{Op: OpEditIssue, Target: Target{Owner: "acme", Repo: "r"}},
			wantOK: false,
		},
		{
			name:   "edit with number allowed",
			policy: Policy{},
			req:    Request{Op: OpEditIssue, Target: Target{Owner: "acme", Repo: "r", Number: 5}},
			wantOK: true,
		},
		{
			name:   "missing owner refused",
			policy: Policy{},
			req:    Request{Op: OpCommentIssue, Target: Target{Repo: "r", Number: 5}},
			wantOK: false,
		},
		{
			name:   "owner allowlist permits listed",
			policy: Policy{Owners: []string{"acme"}},
			req:    Request{Op: OpDispatch, Target: Target{Owner: "acme", Repo: "r", Number: 1}},
			wantOK: true,
		},
		{
			name:   "owner allowlist rejects unlisted",
			policy: Policy{Owners: []string{"acme"}},
			req:    Request{Op: OpDispatch, Target: Target{Owner: "evil", Repo: "r", Number: 1}},
			wantOK: false,
		},
		{
			name:   "op allowlist narrows surface",
			policy: Policy{Ops: map[Op]bool{OpCommentIssue: true}},
			req:    Request{Op: OpDispatch, Target: Target{Owner: "acme", Repo: "r", Number: 1}},
			wantOK: false,
		},
		{
			name:   "unknown op refused",
			policy: Policy{},
			req:    Request{Op: Op("delete_everything"), Target: Target{Owner: "acme", Repo: "r", Number: 1}},
			wantOK: false,
		},
		{
			name:   "label add with number and labels allowed",
			policy: Policy{},
			req:    Request{Op: OpLabelIssue, Target: Target{Owner: "acme", Repo: "r", Number: 5}, LabelMode: LabelAdd, Labels: []string{"headless"}},
			wantOK: true,
		},
		{
			name:   "label set with labels allowed",
			policy: Policy{},
			req:    Request{Op: OpLabelIssue, Target: Target{Owner: "acme", Repo: "r", Number: 5}, LabelMode: LabelSet, Labels: []string{"a", "b"}},
			wantOK: true,
		},
		{
			name:   "label remove with labels allowed",
			policy: Policy{},
			req:    Request{Op: OpLabelIssue, Target: Target{Owner: "acme", Repo: "r", Number: 5}, LabelMode: LabelRemove, Labels: []string{"stale"}},
			wantOK: true,
		},
		{
			name:   "label without number refused",
			policy: Policy{},
			req:    Request{Op: OpLabelIssue, Target: Target{Owner: "acme", Repo: "r"}, LabelMode: LabelAdd, Labels: []string{"headless"}},
			wantOK: false,
		},
		{
			name:   "label without labels refused",
			policy: Policy{},
			req:    Request{Op: OpLabelIssue, Target: Target{Owner: "acme", Repo: "r", Number: 5}, LabelMode: LabelAdd},
			wantOK: false,
		},
		{
			name:   "label with unknown mode refused",
			policy: Policy{},
			req:    Request{Op: OpLabelIssue, Target: Target{Owner: "acme", Repo: "r", Number: 5}, LabelMode: "toggle", Labels: []string{"headless"}},
			wantOK: false,
		},
		{
			name:   "label with empty mode refused",
			policy: Policy{},
			req:    Request{Op: OpLabelIssue, Target: Target{Owner: "acme", Repo: "r", Number: 5}, Labels: []string{"headless"}},
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Authorize(context.Background(), tc.req)
			if tc.wantOK && err != nil {
				t.Fatalf("want allowed, got %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatal("want refused, got nil")
			}
		})
	}
}

func TestAuthorizerFunc(t *testing.T) {
	called := false
	var a Authorizer = AuthorizerFunc(func(context.Context, Request) error {
		called = true
		return nil
	})
	if err := a.Authorize(context.Background(), Request{}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !called {
		t.Fatal("AuthorizerFunc not invoked")
	}
}
