// Package verb is the middleware that wraps every consumer command action in
// the standard pipeline of:
package verb

import (
	"context"
	"fmt"
	"os"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/audit"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/exitcode"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/policy"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/scope"
	"github.com/urfave/cli/v3"
)

// shellMetaReason is the consumer-agnostic Reasoner why-line for a
// policy_denied from the shell-metacharacter gate.
const shellMetaReason = "the shell-metacharacter gate refuses argv a downstream shell could reinterpret; " +
	"some wrapped verbs forward argv into a remote shell (ssh, ssm send-command) and the gate cannot tell them " +
	"apart from the safe ones, so it refuses metacharacters for all. Pass shell-bearing values through a file " +
	"instead of inline (a --body-file rather than an inline --body, a file:// URL rather than an inline JSON " +
	"batch, an external pipe rather than an inline --jq), or opt a known-safe verb out with allow_metacharacters " +
	"in its declaring config (the audit row stamps policy_skipped so forensics still see it)"

// lockdownAxisReason is the Reasoner why-line for a policy_denied raised
// when the active lockdown profile refuses a verb on its axis.
const lockdownAxisReason = "a lockdown profile is the boundary that stops an agent operating under it from " +
	"widening its own permissions; a denied axis can only be relaxed from outside the agent by editing the " +
	"verb's profile in its declaring config, never by the caller bypassing the gate"

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

	// OnComplete, if set, runs inside writer.Wrap after Action returns and
	// before the audit record is appended. Receives a pointer to the record
	OnComplete func(*audit.Record)

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

// Wrap returns a cli.ActionFunc that runs the full verb pipeline.
func Wrap(spec Spec, writer *audit.Writer) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		// os.Args is what the user typed. Better for audit than trying to
		// reconstruct from cli.Command state (which requires a fully-
		argv := append([]string{}, os.Args...)
		if !spec.SkipPolicy {
			args, positional := extractArgs(spec, cmd)
			if err := policy.ValidateArgs(args); err != nil {
				coded := exitcode.New(exitcode.PolicyDenied, "policy_denied", err,
					"move the flag value with the metacharacter into a file and pass it by path "+
						"(a --<flag>-file or a file:// URL), or set allow_metacharacters on the verb if it is known-safe").
					WithReason(shellMetaReason)
				logReject(writer, spec.Name, argv, coded)
				return coded
			}
			if err := policy.ValidateArgSlice("positional", positional); err != nil {
				coded := exitcode.New(exitcode.PolicyDenied, "policy_denied", err,
					"move the positional argument with the metacharacter into a file and pass it by path, "+
						"or set allow_metacharacters on the verb if it is known-safe").
					WithReason(shellMetaReason)
				logReject(writer, spec.Name, argv, coded)
				return coded
			}
		}

		base := buildBaseRecord(spec, argv)

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

// buildBaseRecord composes the per-invocation Record that writer.Wrap fills
// in with Decision/ExitCode/DurationMS. Stamps RepoRoot best-effort from cwd.
func buildBaseRecord(spec Spec, argv []string) audit.Record {
	cwd := scope.CWD()
	invokeCWD := ""
	if spec.ResolveInvokeCWD != nil {
		invokeCWD = spec.ResolveInvokeCWD()
	}
	return audit.Record{
		ID:              spec.IDOverride,
		Verb:            spec.Name,
		Argv:            argv,
		RepoRoot:        scope.RepoRoot(cwd), // forensic-only: where cwd was, "" outside any repo
		CWDSubprocess:   cwd,
		CWDAtInvocation: invokeCWD,
	}
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
			"relax the denied axis in the verb's profile config, or run from a context the active profile allows").
			WithReason(lockdownAxisReason)
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
