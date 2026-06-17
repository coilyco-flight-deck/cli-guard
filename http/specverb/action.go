// Complex actions: named composite verbs running a bounded poll-until loop over
// an already-granted leaf, with a fail-when exit. See docs/specverb-actions.md.

package specverb

import (
	"context"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/respfmt"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
	"github.com/urfave/cli/v3"
)

// actionGroup is the CLI noun every complex action mounts under, e.g.
// `forgejo action ci-watch`, mirroring the audit name `<path>.action.<name>`.
const actionGroup = "action"

// ownerRepoArg is the documented `args` sugar: a single `owner/name` value
// split across the leaf's `owner` and `repo` path params.
const ownerRepoArg = "owner-repo"

// actionDescriptor is one resolved complex action: its envelope identity, the
// inputs, the granted poll leaf, and the bounds. All validated at Build time.
type actionDescriptor struct {
	Name     string            // CLI leaf, e.g. ci-watch
	VerbName string            // envelope audit name, e.g. ward.ops.forgejo.action.ci-watch
	Describe string            // optional human note
	Inputs   []guardfile.Input // declared positional args and flags
	Leaf     opDescriptor      // the resolved poll target (a granted leaf)
	Args     []guardfile.ArgBind
	Until    string        // JMESPath; truthy ends the loop
	Every    time.Duration // sample interval, > 0
	Timeout  time.Duration // wall-clock bound, > 0
	As       string        // binding name for the final response
	FailWhen string        // JMESPath over the final response + bindings; truthy => non-zero exit
}

// resolveActions resolves every Guardfile action into a descriptor, failing
// closed at each gate; granted is the (verb, resource) set the Guardfile grants.
func resolveActions(spec *swaggerSpec, gf *guardfile.Guardfile, granted map[grantKey]guardfile.Grant) ([]actionDescriptor, error) {
	var out []actionDescriptor
	for _, a := range gf.Actions {
		ad, err := resolveAction(spec, gf, granted, a)
		if err != nil {
			return nil, err
		}
		out = append(out, ad)
	}
	return out, nil
}

func resolveAction(spec *swaggerSpec, gf *guardfile.Guardfile, granted map[grantKey]guardfile.Grant, a guardfile.Action) (actionDescriptor, error) {
	// Multi-call (`call`) execution lands in its own pass; poll is the only shape
	// the engine runs today. Parser guarantees exactly one of Poll/Calls is set.
	if a.Poll == nil {
		return actionDescriptor{}, fmt.Errorf("specverb: action %q: multi-call (`call`) actions are parsed but not yet executed by the engine", a.Name)
	}
	p := a.Poll
	// Granted-only: an action may only poll an op the same Guardfile grants.
	g, ok := granted[grantKey{Verb: p.Verb, Resource: p.Resource}]
	if !ok {
		return actionDescriptor{}, fmt.Errorf("specverb: action %q polls %q %q which no `can` grant authorizes (deny-by-default; add `can %s %s`)", a.Name, p.Verb, p.Resource, p.Verb, p.Resource)
	}
	leaf, err := resolveDescriptor(spec, gf.Group, g)
	if err != nil {
		return actionDescriptor{}, fmt.Errorf("specverb: action %q: %w", a.Name, err)
	}
	every, err := positiveDuration(p.Every)
	if err != nil {
		return actionDescriptor{}, fmt.Errorf("specverb: action %q: every: %w", a.Name, err)
	}
	timeout, err := positiveDuration(p.Timeout)
	if err != nil {
		return actionDescriptor{}, fmt.Errorf("specverb: action %q: timeout: %w", a.Name, err)
	}
	if err := respfmt.Validate(p.Until); err != nil {
		return actionDescriptor{}, fmt.Errorf("specverb: action %q: until: %w", a.Name, err)
	}
	if a.FailWhen != "" {
		if err := respfmt.Validate(a.FailWhen); err != nil {
			return actionDescriptor{}, fmt.Errorf("specverb: action %q: fail-when: %w", a.Name, err)
		}
	}
	inputNames := map[string]bool{}
	for _, in := range a.Inputs {
		inputNames[in.Name] = true
	}
	if err := validateArgs(a, leaf, inputNames); err != nil {
		return actionDescriptor{}, err
	}
	return actionDescriptor{
		Name:     a.Name,
		VerbName: strings.Join(gf.Group, ".") + "." + actionGroup + "." + a.Name,
		Describe: a.Describe,
		Inputs:   a.Inputs,
		Leaf:     leaf,
		Args:     p.Args,
		Until:    p.Until,
		Every:    every,
		Timeout:  timeout,
		As:       p.As,
		FailWhen: a.FailWhen,
	}, nil
}

// validateArgs checks every poll arg resolves: a `$ref` names a declared input,
// and the arg targets a real leaf path param, flag, or the owner-repo sugar.
func validateArgs(a guardfile.Action, leaf opDescriptor, inputNames map[string]bool) error {
	paramNames := map[string]bool{}
	for _, p := range leaf.PathParams {
		paramNames[p] = true
	}
	flagNames := map[string]bool{}
	for _, f := range append(append(append([]fieldFlag{}, leaf.QueryFlags...), leaf.BodyFlags...), leaf.FormFlags...) {
		flagNames[f.Name] = true
	}
	for _, arg := range a.Poll.Args {
		if strings.HasPrefix(arg.Value, "$") {
			ref := strings.TrimPrefix(arg.Value, "$")
			if !inputNames[ref] {
				return fmt.Errorf("specverb: action %q: arg %q references $%s, which no `input` declares", a.Name, arg.Name, ref)
			}
		}
		switch {
		case arg.Name == ownerRepoArg:
			if !paramNames["owner"] || !paramNames["repo"] {
				return fmt.Errorf("specverb: action %q: arg %q needs the leaf to take owner+repo path params, but %s %s does not", a.Name, ownerRepoArg, leaf.Method, leaf.Path)
			}
		case paramNames[arg.Name] || flagNames[arg.Name]:
			// binds a real path param or flag
		default:
			return fmt.Errorf("specverb: action %q: arg %q targets nothing on %s %s (not a path param or flag; fail-closed)", a.Name, arg.Name, leaf.Method, leaf.Path)
		}
	}
	return nil
}

// positiveDuration parses a Go duration string and requires it to be > 0, so a
// bound of "0s" can never disable the loop's clock.
func positiveDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration (e.g. \"10s\", \"30m\"): %w", s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q must be greater than zero", s)
	}
	return d, nil
}

// buildActionGroup mounts the resolved actions under the `action` noun, or
// returns nil when the Guardfile declares none.
func (rt *runtime) buildActionGroup(descs []actionDescriptor) *cli.Command {
	if len(descs) == 0 {
		return nil
	}
	grp := &cli.Command{Name: actionGroup, Usage: "complex actions: bounded composite verbs"}
	for _, ad := range descs {
		grp.Commands = append(grp.Commands, rt.buildActionLeaf(ad))
	}
	return grp
}

// buildActionLeaf turns one action descriptor into a guarded leaf: positional
// inputs become args, flag inputs flags, wrapped for the envelope audit row.
func (rt *runtime) buildActionLeaf(ad actionDescriptor) *cli.Command {
	flags := []cli.Flag{
		&cli.BoolFlag{Name: flagDryRun, Usage: "print the action plan (the call sequence and compiled until) without firing it"},
		&cli.StringFlag{Name: flagOutput, Usage: "output format: yaml | yaml-stream | json | text | table"},
	}
	var positional []guardfile.Input
	for _, in := range ad.Inputs {
		if in.Positional {
			positional = append(positional, in)
			continue
		}
		flags = append(flags, &cli.StringFlag{Name: in.Name, Usage: in.Help})
	}
	return &cli.Command{
		Name:        ad.Name,
		Usage:       actionUsage(ad),
		Description: actionDescription(ad),
		ArgsUsage:   argsUsage(inputNamesOf(positional)),
		Flags:       flags,
		Action: rt.wrap(verb.Spec{
			Name:     ad.VerbName,
			ArgsFunc: actionArgsFunc(ad),
			Action:   rt.runAction(ad),
		}),
	}
}

// actionArgsFunc feeds the shell-metacharacter gate: flag inputs are the named
// args, positional inputs the positional slice.
func actionArgsFunc(ad actionDescriptor) func(*cli.Command) (map[string]string, []string) {
	return func(c *cli.Command) (map[string]string, []string) {
		named := map[string]string{}
		for _, in := range ad.Inputs {
			if !in.Positional && c.IsSet(in.Name) {
				named[in.Name] = c.String(in.Name)
			}
		}
		return named, c.Args().Slice()
	}
}

// runAction binds inputs, builds the leaf request, then prints the plan
// (--dry-run) or runs the bounded poll loop with the fail-when exit predicate.
func (rt *runtime) runAction(ad actionDescriptor) cli.ActionFunc {
	return func(ctx context.Context, c *cli.Command) error {
		strVars, jmesVars, err := bindInputs(ad, c)
		if err != nil {
			return err
		}
		method, url, body, contentType, err := rt.buildLeafRequest(ad, strVars)
		if err != nil {
			return err
		}
		if c.Bool(flagDryRun) {
			return rt.renderActionPlan(ad, method, url, body, contentType, c.String(flagOutput))
		}
		return rt.runPoll(ctx, c, ad, method, url, body, contentType, jmesVars)
	}
}

// bindInputs reads inputs into strVars (raw, for the request) and jmesVars
// (coerced, for conditions). Unset optional flags bind in neither scope.
func bindInputs(ad actionDescriptor, c *cli.Command) (strVars map[string]string, jmesVars map[string]any, err error) {
	strVars = map[string]string{}
	jmesVars = map[string]any{}
	positional := c.Args().Slice()
	pi := 0
	for _, in := range ad.Inputs {
		if in.Positional {
			if pi >= len(positional) {
				if in.Required {
					return nil, nil, exitcode.New(exitcode.UserError, "user_error",
						fmt.Errorf("missing required argument <%s>", in.Name), "supply the positional arguments this action names")
				}
				continue
			}
			val := positional[pi]
			pi++
			strVars[in.Name] = val
			jmesVars[in.Name] = coerceScalar(val)
			continue
		}
		if c.IsSet(in.Name) {
			val := c.String(in.Name)
			strVars[in.Name] = val
			jmesVars[in.Name] = coerceScalar(val)
		}
	}
	if pi < len(positional) {
		return nil, nil, exitcode.New(exitcode.UserError, "user_error",
			fmt.Errorf("got %d positional args, this action takes %d", len(positional), pi), "remove the extra arguments")
	}
	return strVars, jmesVars, nil
}

// coerceScalar lowers an input string to a number where it parses as one (so
// `run_number==$run` compares number to number), otherwise the string itself.
func coerceScalar(s string) any {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return float64(i) // JSON numbers decode to float64; match that
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// buildLeafRequest assembles the polled leaf's HTTP request from the arg
// bindings, using the same path/query machinery a directly-invoked leaf uses.
func (rt *runtime) buildLeafRequest(ad actionDescriptor, strVars map[string]string) (method, url string, body []byte, contentType string, err error) {
	leaf := ad.Leaf
	b := newArgBinder(leaf)
	for _, arg := range ad.Args {
		val, rerr := resolveArgValue(arg.Value, strVars)
		if rerr != nil {
			return "", "", nil, "", exitcode.New(exitcode.UserError, "user_error",
				fmt.Errorf("action arg %q: %w", arg.Name, rerr), "supply the input this arg references")
		}
		if berr := b.bind(arg.Name, val); berr != nil {
			return "", "", nil, "", berr
		}
	}
	if berr := b.requireAllPaths(); berr != nil {
		return "", "", nil, "", berr
	}
	pathVals, query, bodyObj := b.pathVals, b.query, b.bodyObj

	qs := ""
	if len(query) > 0 {
		qs = "?" + query.Encode()
	}
	url = rt.baseURL + fillPath(leaf.Path, pathVals) + qs
	contentType = contentTypeJSON
	switch {
	case len(leaf.FixedBody) > 0:
		body, err = json.Marshal(leaf.FixedBody)
	case len(bodyObj) > 0:
		body, err = json.Marshal(bodyObj)
	}
	if err != nil {
		return "", "", nil, "", exitcode.New(exitcode.Internal, "internal", err, "")
	}
	return leaf.Method, url, body, contentType, nil
}

// argBinder routes one poll arg onto a path param (by name or owner-repo
// sugar), a query param, or a body field, keeping buildLeafRequest flat.
type argBinder struct {
	pathIdx    map[string]int
	pathVals   []string
	queryNames map[string]bool
	flagNames  map[string]bool
	query      neturl.Values
	bodyObj    map[string]any
}

func newArgBinder(leaf opDescriptor) *argBinder {
	b := &argBinder{
		pathIdx:    map[string]int{},
		pathVals:   make([]string, len(leaf.PathParams)),
		queryNames: map[string]bool{},
		flagNames:  map[string]bool{},
		query:      neturl.Values{},
		bodyObj:    map[string]any{},
	}
	for i, p := range leaf.PathParams {
		b.pathIdx[p] = i
	}
	for _, f := range leaf.QueryFlags {
		b.queryNames[f.Name] = true
	}
	for _, f := range append(append([]fieldFlag{}, leaf.QueryFlags...), append(leaf.BodyFlags, leaf.FormFlags...)...) {
		b.flagNames[f.Name] = true
	}
	return b
}

// bind routes one resolved arg value; validateArgs already proved every arg
// targets something, so an unmatched name here is a no-op, not an error.
func (b *argBinder) bind(name, val string) error {
	switch {
	case name == ownerRepoArg:
		owner, repo, ok := splitOwnerRepo(val)
		if !ok {
			return exitcode.New(exitcode.UserError, "user_error",
				fmt.Errorf("arg %q value %q is not owner/name", ownerRepoArg, val), "pass it as owner/name")
		}
		b.pathVals[b.pathIdx["owner"]] = owner
		b.pathVals[b.pathIdx["repo"]] = repo
	case hasKey(b.pathIdx, name):
		b.pathVals[b.pathIdx[name]] = val
	case b.queryNames[name]:
		b.query.Set(name, val)
	case b.flagNames[name]:
		b.bodyObj[name] = val
	}
	return nil
}

// requireAllPaths fails closed if any path param went unbound, so a poll tick
// never fires an under-bound URL.
func (b *argBinder) requireAllPaths() error {
	for p, i := range b.pathIdx {
		if b.pathVals[i] == "" {
			return exitcode.New(exitcode.UserError, "user_error",
				fmt.Errorf("path param %q was not bound by any arg", p), "add an `args { ... }` binding for it")
		}
	}
	return nil
}

// resolveArgValue resolves a `$ref` against the bound input strings, or returns
// a literal verbatim. A `$ref` to an unbound input is an error.
func resolveArgValue(value string, strVars map[string]string) (string, error) {
	if !strings.HasPrefix(value, "$") {
		return value, nil
	}
	ref := strings.TrimPrefix(value, "$")
	v, ok := strVars[ref]
	if !ok {
		return "", fmt.Errorf("$%s is not set (it is an optional input that was not supplied)", ref)
	}
	return v, nil
}

// splitOwnerRepo splits an `owner/name` value into its two halves; ok=false
// unless there are exactly two non-empty parts.
func splitOwnerRepo(v string) (owner, repo string, ok bool) {
	parts := strings.Split(v, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// runPoll runs the bounded loop: each tick fires the leaf, binds the response
// under `as`, tests `until`; timeout is a non-zero exit. See specverb-actions.md.
func (rt *runtime) runPoll(ctx context.Context, c *cli.Command, ad actionDescriptor, method, url string, body []byte, contentType string, jmesVars map[string]any) error {
	loopCtx, cancel := context.WithTimeout(ctx, ad.Timeout)
	defer cancel()
	ticker := time.NewTicker(ad.Every)
	defer ticker.Stop()

	var finalRaw []byte
	for {
		decoded, raw, ferr := rt.fireLeafAudited(loopCtx, ad, method, url, body, contentType, c)
		if ferr != nil {
			return ferr
		}
		finalRaw = raw
		jmesVars[ad.As] = decoded

		done, eerr := respfmt.EvalBool(decoded, ad.Until, jmesVars)
		if eerr != nil {
			return exitcode.New(exitcode.UserError, "user_error", eerr, "check the `until` JMESPath against the response shape")
		}
		if done {
			break
		}
		select {
		case <-loopCtx.Done():
			return exitcode.New(exitcode.UpstreamFailed, "action_timeout",
				fmt.Errorf("action %q: `until` did not settle within %s", ad.Name, ad.Timeout),
				"raise `timeout`, or check the run is progressing")
		case <-ticker.C:
		}
	}

	if err := renderFinal(finalRaw, c.String(flagOutput)); err != nil {
		return err
	}
	return rt.applyFailWhen(ad, finalRaw, jmesVars)
}

// fireLeafAudited fires one tick through the verb pipeline so the call writes
// its own leaf audit row. SkipPolicy: the action envelope already gated argv.
func (rt *runtime) fireLeafAudited(ctx context.Context, ad actionDescriptor, method, url string, body []byte, contentType string, c *cli.Command) (decoded any, raw []byte, err error) {
	inner := verb.Spec{
		Name:       ad.Leaf.VerbName,
		SkipPolicy: true, // the action envelope already gated the operator's argv
		Action: func(ictx context.Context, _ *cli.Command) error {
			var e error
			decoded, raw, _, e = rt.fireCapture(ictx, method, url, body, contentType)
			return e
		},
	}
	err = rt.wrap(inner)(ctx, c)
	return decoded, raw, err
}

// renderFinal prints the loop's final response through respfmt, honoring
// --output so an action reads the same as a single verb.
func renderFinal(raw []byte, output string) error {
	rendered, err := respfmt.Render(raw, "", output)
	if err != nil {
		return exitcode.New(exitcode.Internal, "internal", err, "the response was not valid JSON")
	}
	if len(rendered) == 0 {
		return nil
	}
	fmt.Print(string(rendered))
	return nil
}

// applyFailWhen evaluates fail-when against the final response (with bindings as
// $variables); a truthy result is a non-zero exit. See specverb-actions.md.
func (rt *runtime) applyFailWhen(ad actionDescriptor, finalRaw []byte, jmesVars map[string]any) error {
	if ad.FailWhen == "" {
		return nil
	}
	var data any
	if len(finalRaw) > 0 {
		if err := json.Unmarshal(finalRaw, &data); err != nil {
			return exitcode.New(exitcode.Internal, "internal", err, "the response was not valid JSON")
		}
	}
	fail, err := respfmt.EvalBool(data, ad.FailWhen, jmesVars)
	if err != nil {
		return exitcode.New(exitcode.UserError, "user_error", err, "check the `fail-when` JMESPath against the response shape")
	}
	if fail {
		return exitcode.New(exitcode.Generic, "action_failed",
			fmt.Errorf("action %q: fail-when predicate matched", ad.Name),
			"the watched operation reported failure; inspect the output above")
	}
	return nil
}

// renderActionPlan prints the bound call sequence and compiled until/fail-when,
// firing nothing: --dry-run is a plan (auth value redacted).
func (rt *runtime) renderActionPlan(ad actionDescriptor, method, url string, body []byte, contentType, output string) error {
	poll := map[string]any{
		"method":  method,
		"url":     rt.previewURL(url),
		"headers": rt.previewHeaders(body != nil, contentType),
		"every":   ad.Every.String(),
		"timeout": ad.Timeout.String(),
		"until":   ad.Until,
		"as":      ad.As,
	}
	if body != nil {
		var parsed any
		if err := json.Unmarshal(body, &parsed); err == nil {
			poll["body"] = parsed
		}
	}
	plan := map[string]any{
		"action": ad.Name,
		"poll":   poll,
	}
	if ad.FailWhen != "" {
		plan["fail_when"] = ad.FailWhen
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return exitcode.New(exitcode.Internal, "internal", err, "")
	}
	rendered, err := respfmt.Render(raw, "", output)
	if err != nil {
		return exitcode.New(exitcode.Internal, "internal", err, "")
	}
	fmt.Print(string(rendered))
	return nil
}

// actionUsage is the one-line help: the action describe note, or a default.
func actionUsage(ad actionDescriptor) string {
	if ad.Describe != "" {
		return ad.Describe
	}
	return fmt.Sprintf("complex action polling %s %s", ad.Leaf.Method, ad.Leaf.Path)
}

// actionDescription is the rich per-action help body.
func actionDescription(ad actionDescriptor) string {
	var b strings.Builder
	if ad.Describe != "" {
		fmt.Fprintf(&b, "%s\n\n", ad.Describe)
	}
	fmt.Fprintf(&b, "Polls %s %s every %s, up to %s, until:\n  %s\n", ad.Leaf.Method, ad.Leaf.Path, ad.Every, ad.Timeout, ad.Until)
	fmt.Fprintf(&b, "\nAuthorized by grant: %s.\n", ad.Leaf.Grant)
	if ad.FailWhen != "" {
		fmt.Fprintf(&b, "\nExits non-zero when: %s\n", ad.FailWhen)
	}
	b.WriteString("\nUse --dry-run to print the plan without firing it.")
	return b.String()
}

// inputNamesOf returns the names of the given inputs, in order.
func inputNamesOf(inputs []guardfile.Input) []string {
	names := make([]string, len(inputs))
	for i, in := range inputs {
		names[i] = in.Name
	}
	return names
}

// hasKey reports whether m contains key.
func hasKey(m map[string]int, key string) bool {
	_, ok := m[key]
	return ok
}
