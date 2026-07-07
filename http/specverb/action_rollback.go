// The runtime's HTTP implementation of the stepflow transport seam: firing,
// sampling, and planning one resolved action step. The sequence/rollback/canary
// engine itself lives in pkg/stepflow. See docs/specverb-rollback.md.

package specverb

import (
	"context"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/stepflow"
	"github.com/urfave/cli/v3"
)

// Fire is the HTTP implementation of stepflow.Runner: it assembles the leaf
// request and fires it through the audited verb pipeline.
func (rt *runtime) Fire(ctx context.Context, c *cli.Command, leaf stepflow.Leaf, args []guardfile.ArgBind, resolve stepflow.Resolve) (any, []byte, error) {
	op := leafOp(leaf)
	method, url, body, contentType, err := rt.buildCallRequest(ctx, false, op, args, resolve)
	if err != nil {
		return nil, nil, err
	}
	return rt.fireCallAudited(ctx, op, method, url, body, contentType, c)
}

// Sample takes one canary observation. Over HTTP a failed request is a
// transport failure, so sampling and firing share one implementation.
func (rt *runtime) Sample(ctx context.Context, c *cli.Command, leaf stepflow.Leaf, args []guardfile.ArgBind, resolve stepflow.Resolve) (any, []byte, error) {
	return rt.Fire(ctx, c, leaf, args, resolve)
}

// Plan is the HTTP implementation of the dry-run seam: it builds the request
// offline and renders it, resolving no secret and firing nothing.
func (rt *runtime) Plan(ctx context.Context, leaf stepflow.Leaf, args []guardfile.ArgBind, resolve stepflow.Resolve) (map[string]any, error) {
	op := leafOp(leaf)
	method, url, body, contentType, err := rt.buildCallRequest(ctx, true, op, args, resolve)
	if err != nil {
		return nil, err
	}
	return rt.leafPlan(op, method, url, body, contentType), nil
}
