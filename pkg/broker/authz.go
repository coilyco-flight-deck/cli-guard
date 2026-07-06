package broker

import (
	"context"
	"fmt"
)

// Authorizer vets each [Request] before execution - the write-tier check. It
// returns nil to permit and a non-nil error to refuse (folded into [Response]).
type Authorizer interface {
	Authorize(ctx context.Context, req Request) error
}

// AuthorizerFunc adapts a plain function to the [Authorizer] interface.
type AuthorizerFunc func(ctx context.Context, req Request) error

// Authorize calls f.
func (f AuthorizerFunc) Authorize(ctx context.Context, req Request) error {
	return f(ctx, req)
}

// Policy is the default [Authorizer]: owner allowlist x op allowlist, plus the
// structural invariants every op needs. Zero value permits any owner+[WriteOps].
type Policy struct {
	// Owners is the allowlist of issue owners. Empty means any owner.
	Owners []string
	// Ops is the allowlist of permitted operations. Nil means [WriteOps].
	Ops map[Op]bool
}

// Authorize enforces the policy. ctx is accepted for interface conformance;
// the built-in checks are synchronous.
func (p Policy) Authorize(_ context.Context, req Request) error {
	ops := p.Ops
	if ops == nil {
		ops = WriteOps
	}
	if !ops[req.Op] {
		return fmt.Errorf("broker: operation %q not permitted", req.Op)
	}
	if req.Target.Owner == "" || req.Target.Repo == "" {
		return fmt.Errorf("broker: %s requires target owner and repo", req.Op)
	}
	if !p.ownerAllowed(req.Target.Owner) {
		return fmt.Errorf("broker: owner %q not in allowlist", req.Target.Owner)
	}
	// Every op but file-issue acts on an existing issue and needs its number.
	if req.Op != OpFileIssue && req.Target.Number <= 0 {
		return fmt.Errorf("broker: %s requires a positive issue number", req.Op)
	}
	if req.Op == OpFileIssue && req.Title == "" {
		return fmt.Errorf("broker: %s requires a title", req.Op)
	}
	if req.Op == OpLabelIssue {
		if err := labelInvariants(req); err != nil {
			return err
		}
	}
	return nil
}

// labelInvariants enforces the [OpLabelIssue] rules fail-closed: a known mode
// ([LabelAdd]/[LabelSet]/[LabelRemove]) and at least one label to act on.
func labelInvariants(req Request) error {
	switch req.LabelMode {
	case LabelAdd, LabelSet, LabelRemove:
	default:
		return fmt.Errorf("broker: %s has unknown mode %q", req.Op, req.LabelMode)
	}
	if len(req.Labels) == 0 {
		return fmt.Errorf("broker: %s %s requires at least one label", req.Op, req.LabelMode)
	}
	return nil
}

// ownerAllowed reports whether owner passes the Owners allowlist (empty
// allowlist permits any owner).
func (p Policy) ownerAllowed(owner string) bool {
	if len(p.Owners) == 0 {
		return true
	}
	for _, o := range p.Owners {
		if o == owner {
			return true
		}
	}
	return false
}
