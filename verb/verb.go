// Package verb is the middleware that wraps every coily command action in
// the standard pipeline of:
package verb

import (
	"context"
	"fmt"
	"os"

	"github.com/coilysiren/cli-guard/audit"
	"github.com/coilysiren/cli-guard/exitcode"
	"github.com/coilysiren/cli-guard/policy"
	"github.com/coilysiren/cli-guard/scope"
	"github.com/urfave/cli/v3"
)

// CommitScopeFlag is the canonical name of the global --commit-scope flag.
// Exported so the host CLI can declare the flag and verb.Wrap can read it
const CommitScopeFlag = "commit-scope"

// AuditParentFlag is the canonical name of the global --audit-parent flag.
// Set by a coily-on-host A invocation that ssh-passthroughs into host B, so
const AuditParentFlag = "audit-parent"

// AuditParentEnvVar is the env-var alternative to --audit-parent. Reads
// the same value into audit.Record.AuditParent. Useful when the parent
const AuditParentEnvVar = "COILY_AUDIT_PARENT"

// Spec describes a verb before it is wrapped into a cli.ActionFunc.
type Spec struct {
	// Name is the dotted verb path used for audit logging, e.g.
	// "aws.route53.change-resource-record-sets" or "lockdown".
	Name string

	// ArgsFunc extracts the user-supplied string arguments from the
	// *cli.Command for validation. Returns named flags and positional args.
	ArgsFunc func(*cli.Command) (args map[string]string, positional []string)

	// Action is the verb's real work. Called only after argv validation passes.
	Action cli.ActionFunc

	// SkipPolicy disables the shell-metacharacter check for this verb. Set
	// true only for pass-throughs whose argv goes straight through execve to
	SkipPolicy bool

	// SkipScope disables --commit-scope resolution for this verb. Set true
	// for read-only or self-referential verbs that would refuse to run
	SkipScope bool

	// OnComplete, if set, runs inside writer.Wrap after Action returns and
	// before the audit record is appended. Receives a pointer to the record
	OnComplete func(*audit.Record)

	// CommitScopeOverride, when non-empty, replaces flag/env resolution and
	// uses this absolute path as the audit row's commit-scope. Set by `coily
	CommitScopeOverride string

	// CommitScopeArgvHint, when set, runs as a fallback resolver before
	// scope.Resolve and only when neither --commit-scope (still at "auto")
	CommitScopeArgvHint func(argv []string) string

	// OnEvaluate, when set, runs after argv validation and before Action.
	// Returning a non-nil *ProfileDecision attaches it to the audit row.
	OnEvaluate func(ctx context.Context, cmd *cli.Command) (*audit.ProfileDecision, error)

	// IDOverride, when non-empty, is used as audit.Record.ID for this
	// invocation in place of the default auto-generated UUID v7. Set by
	IDOverride string

	// ResolveInvokeCWD, when set, returns the operator's invoke-time cwd
	// (distinct from os.Getwd() which captures the post-cd subprocess
	ResolveInvokeCWD func() string
}

// Wrap returns a cli.ActionFunc that runs the full coily verb pipeline.
func Wrap(spec Spec, writer *audit.Writer) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		// os.Args is what the user typed. Better for audit than trying to
		// reconstruct from cli.Command state (which requires a fully-
		argv := append([]string{}, os.Args...)
		if !spec.SkipPolicy {
			args, positional := extractArgs(spec, cmd)
			if err := policy.ValidateArgs(args); err != nil {
				coded := exitcode.New(exitcode.PolicyDenied, "policy_denied", err,
					"argv contains a shell metacharacter that coily refuses to forward")
				logReject(writer, spec.Name, argv, coded)
				return coded
			}
			if err := policy.ValidateArgSlice("positional", positional); err != nil {
				coded := exitcode.New(exitcode.PolicyDenied, "policy_denied", err,
					"a positional argument failed shell-metacharacter validation")
				logReject(writer, spec.Name, argv, coded)
				return coded
			}
		}

		base, scopeErr := buildBaseRecord(spec, argv, cmd)
		if scopeErr != nil {
			coded := exitcode.New(exitcode.Generic, "scope_unresolved", scopeErr,
				"set --commit-scope=<repo-path> or COILY_COMMIT_SCOPE=<repo-path>; "+
					"there is no opt-out, every audit row must bind to a real repo")
			logReject(writer, spec.Name, argv, coded)
			return coded
		}

		profileDecision, evalCoded := runOnEvaluate(ctx, cmd, spec, base, writer, argv)
		if evalCoded != nil {
			return evalCoded
		}

		if writer == nil {
			return spec.Action(ctx, cmd)
		}
		onComplete := spec.OnComplete
		if profileDecision != nil {
			user := onComplete
			onComplete = func(rec *audit.Record) {
				rec.ProfileDecision = profileDecision
				if user != nil {
					user(rec)
				}
			}
		}
		return writer.WrapHook(ctx, base, func() error {
			return spec.Action(ctx, cmd)
		}, onComplete)
	}
}

// buildBaseRecord composes the per-invocation Record that writer.Wrap will
// fill in with Decision/ExitCode/DurationMS. Resolves --commit-scope here
func buildBaseRecord(spec Spec, argv []string, cmd *cli.Command) (audit.Record, error) {
	cwd := scope.CWD()
	repoRoot, _ := scope.Resolve("auto", "", cwd) // forensic-only, ignore error
	invokeCWD := ""
	if spec.ResolveInvokeCWD != nil {
		invokeCWD = spec.ResolveInvokeCWD()
	}
	auditParent := resolveAuditParent(cmd)
	if spec.SkipScope {
		return audit.Record{
			ID:              spec.IDOverride,
			Verb:            spec.Name,
			Argv:            argv,
			RepoRoot:        repoRoot,
			CWDSubprocess:   cwd,
			CWDAtInvocation: invokeCWD,
			AuditParent:     auditParent,
		}, nil
	}
	if spec.CommitScopeOverride != "" {
		return audit.Record{
			ID:              spec.IDOverride,
			Verb:            spec.Name,
			Argv:            argv,
			RepoRoot:        repoRoot,
			CommitScope:     spec.CommitScopeOverride,
			CWDSubprocess:   cwd,
			CWDAtInvocation: invokeCWD,
			AuditParent:     auditParent,
		}, nil
	}
	root := cmd
	if r := cmd.Root(); r != nil {
		root = r
	}
	flagVal := root.String(CommitScopeFlag)
	envVal := os.Getenv("COILY_COMMIT_SCOPE")
	if (flagVal == "" || flagVal == "auto") && envVal == "" && spec.CommitScopeArgvHint != nil {
		if hint := spec.CommitScopeArgvHint(argv); hint != "" {
			flagVal = hint
		}
	}
	commitScope, err := scope.Resolve(flagVal, envVal, cwd)
	if err != nil {
		return audit.Record{}, err
	}
	return audit.Record{
		ID:              spec.IDOverride,
		Verb:            spec.Name,
		Argv:            argv,
		RepoRoot:        repoRoot,
		CommitScope:     commitScope,
		CWDSubprocess:   cwd,
		CWDAtInvocation: invokeCWD,
		AuditParent:     auditParent,
	}, nil
}

// resolveAuditParent reads --audit-parent off the root command, then falls
// back to $COILY_AUDIT_PARENT. Empty when neither is set (the common case;
func resolveAuditParent(cmd *cli.Command) string {
	root := cmd
	if r := cmd.Root(); r != nil {
		root = r
	}
	if v := root.String(AuditParentFlag); v != "" {
		return v
	}
	return os.Getenv(AuditParentEnvVar)
}

// runOnEvaluate calls spec.OnEvaluate (if set) and returns the
// attached decision plus an optional coded error that Wrap should
func runOnEvaluate(ctx context.Context, cmd *cli.Command, spec Spec, base audit.Record, writer *audit.Writer, argv []string) (*audit.ProfileDecision, error) {
	if spec.OnEvaluate == nil {
		return nil, nil //nolint:nilnil // intentional: no decision, no error
	}
	pd, evalErr := spec.OnEvaluate(ctx, cmd)
	if evalErr == nil {
		return pd, nil
	}
	if pd != nil && !pd.Allowed {
		coded := exitcode.New(exitcode.PolicyDenied, "policy_denied", evalErr,
			"the active lockdown profile refuses this verb on the current axis")
		writeDenyRecord(writer, base, pd, evalErr)
		return pd, coded
	}
	coded := exitcode.New(exitcode.Generic, "evaluator_failed", evalErr,
		"profile evaluator returned an internal error; check ~/.coily/coily.yaml is well-formed")
	logReject(writer, spec.Name, argv, coded)
	return pd, coded
}

func writeDenyRecord(writer *audit.Writer, base audit.Record, pd *audit.ProfileDecision, err error) {
	if writer == nil {
		return
	}
	rec := base
	rec.Decision = audit.DecisionReject
	rec.ExitCode = exitcode.PolicyDenied
	rec.Error = err.Error()
	rec.ProfileDecision = pd
	if aerr := writer.Append(rec); aerr != nil {
		fmt.Fprintf(os.Stderr, "audit: %v\n", aerr)
	}
}

func logReject(writer *audit.Writer, verbName string, argv []string, err error) {
	if writer == nil {
		return
	}
	rec := audit.Record{
		Decision: audit.DecisionReject,
		Verb:     verbName,
		Argv:     argv,
		ExitCode: 1,
		Error:    err.Error(),
	}
	if aerr := writer.Append(rec); aerr != nil {
		fmt.Fprintf(os.Stderr, "audit: %v\n", aerr)
	}
}

func extractArgs(spec Spec, cmd *cli.Command) (args map[string]string, positional []string) {
	if spec.ArgsFunc == nil {
		return nil, nil
	}
	return spec.ArgsFunc(cmd)
}
