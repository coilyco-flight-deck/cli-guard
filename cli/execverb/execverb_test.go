package execverb

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/verb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/audit"
	"github.com/urfave/cli/v3"
)

const gitGuardfile = `wrap ward git {
	exec git

	can run status
	can run log
	can run commit {
		deny-flag "--no-verify"
		describe "record staged changes"
	}
	can run push {
		allow-flag "--force-with-lease"
	}
	never run "reflog expire"
}`

const adminGuardfile = `wrap ward ops forgejo admin {
	exec ssh {
		argv-prefix "kai@kai-server" "k3s" "kubectl" "-n" "forgejo" "exec" "deploy/forgejo" "--" "forgejo"
	}
	can run "admin user list"
	can run "doctor check" {
		allow-flag "--run"
	}
}`

// capture is a Runner recording the resolved invocation.
type capture struct {
	bin  string
	argv []string
}

func (cp *capture) run(_ context.Context, bin string, argv []string) error {
	cp.bin = bin
	cp.argv = argv
	return nil
}

func runArgv(t *testing.T, src string, cp *capture, argv ...string) error {
	t.Helper()
	gf, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	root := &cli.Command{Name: "ward"}
	if err := Mount(root, Config{Guardfile: gf, Run: cp.run}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return root.Run(context.Background(), append([]string{"ward"}, argv...))
}

func TestMountsOnlyGrantedSubcommands(t *testing.T) {
	gf, err := Parse([]byte(gitGuardfile))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	root, err := Build(Config{Guardfile: gf})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []string{"status", "log", "commit", "push"} {
		if findChild(root, want) == nil {
			t.Errorf("missing granted leaf %q", want)
		}
	}
	// deny-by-default: rebase was never granted, reflog is a `never` denial
	if findChild(root, "rebase") != nil || findChild(root, "reflog") != nil {
		t.Error("ungranted subcommand mounted")
	}
}

func TestExecsFixedBinAndArgs(t *testing.T) {
	var cp capture
	if err := runArgv(t, gitGuardfile, &cp, "git", "log", "--oneline", "-5"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if cp.bin != "git" {
		t.Errorf("bin = %q, want git", cp.bin)
	}
	if got := strings.Join(cp.argv, " "); got != "log --oneline -5" {
		t.Errorf("argv = %q, want log --oneline -5", got)
	}
}

func TestDenyFlagRefused(t *testing.T) {
	var cp capture
	err := runArgv(t, gitGuardfile, &cp, "git", "commit", "-m", "x", "--no-verify")
	if err == nil {
		t.Fatal("expected a deny-flag refusal, got nil")
	}
	if cp.bin != "" {
		t.Errorf("denied invocation still executed: %s %v", cp.bin, cp.argv)
	}
}

func TestAllowlistMode(t *testing.T) {
	var cp capture
	if err := runArgv(t, gitGuardfile, &cp, "git", "push", "--force-with-lease"); err != nil {
		t.Fatalf("allowlisted flag refused: %v", err)
	}
	err := runArgv(t, gitGuardfile, &cp, "git", "push", "--force")
	if err == nil {
		t.Fatal("expected an allowlist refusal for --force, got nil")
	}
}

func TestArgvPrefixIsFixed(t *testing.T) {
	var cp capture
	if err := runArgv(t, adminGuardfile, &cp, "ops", "forgejo", "admin", "admin", "user", "list"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if cp.bin != "ssh" {
		t.Errorf("bin = %q, want ssh", cp.bin)
	}
	want := "kai@kai-server k3s kubectl -n forgejo exec deploy/forgejo -- forgejo admin user list"
	if got := strings.Join(cp.argv, " "); got != want {
		t.Errorf("argv = %q,\nwant   %q", got, want)
	}
}

func TestMultiWordLeafWithFlag(t *testing.T) {
	var cp capture
	if err := runArgv(t, adminGuardfile, &cp, "ops", "forgejo", "admin", "doctor", "check", "--run", "storages"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.Join(cp.argv, " "); !strings.HasSuffix(got, "forgejo doctor check --run storages") {
		t.Errorf("argv = %q, want the doctor check suffix", got)
	}
}

func TestComposesWithVerbWrap(t *testing.T) {
	w := &audit.Writer{
		Path: filepath.Join(t.TempDir(), "audit.jsonl"),
		Now:  func() time.Time { return time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC) },
	}
	t.Cleanup(func() { _ = w.Close() })
	gf, err := Parse([]byte(gitGuardfile))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var cp capture
	root := &cli.Command{Name: "ward"}
	cfg := Config{
		Guardfile: gf,
		Run:       cp.run,
		Wrap:      func(s verb.Spec) cli.ActionFunc { return verb.Wrap(s, w) },
	}
	if err := Mount(root, cfg); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if err := root.Run(context.Background(), []string{"ward", "git", "status"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, _ := os.ReadFile(w.Path)
	if !strings.Contains(string(data), "ward.git.status") {
		t.Errorf("audit row missing the dotted verb name; got:\n%s", string(data))
	}
}

const awsGuardfile = `wrap ward ops aws {
	exec aws

	can run "*" {
		gate aws-read {
			allow-env "WARD_AWS_ALLOW_SENSITIVE_READ"
		}
		describe "open aws passthrough behind the sensitive-read gate"
	}
}`

// TestWildcardPassthrough proves `can run *` mounts the group itself as one
// open passthrough: any service/operation reaches the binary verbatim.
func TestWildcardPassthrough(t *testing.T) {
	var cp capture
	if err := runArgv(t, awsGuardfile, &cp, "ops", "aws", "sts", "get-caller-identity"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if cp.bin != "aws" || strings.Join(cp.argv, " ") != "sts get-caller-identity" {
		t.Errorf("invocation = %s %v, want aws sts get-caller-identity", cp.bin, cp.argv)
	}
}

// TestAWSReadGateDeniesSensitiveRead proves the declared gate refuses a
// read-only verb touching a sensitive token before any exec happens.
func TestAWSReadGateDeniesSensitiveRead(t *testing.T) {
	var cp capture
	err := runArgv(t, awsGuardfile, &cp, "ops", "aws", "s3", "ls", "s3://prod-secrets-bucket")
	if err == nil {
		t.Fatal("expected the sensitive read to be denied, got nil")
	}
	if cp.bin != "" {
		t.Errorf("denied invocation still executed: %s %v", cp.bin, cp.argv)
	}
	if !strings.Contains(err.Error(), "*secret*") {
		t.Errorf("denial should name the matched pattern: %v", err)
	}
}

// TestAWSReadGatePassesWritesAndEscapes proves write verbs skip the read gate
// and the env escape allows a deliberate sensitive read.
func TestAWSReadGatePassesWritesAndEscapes(t *testing.T) {
	var cp capture
	if err := runArgv(t, awsGuardfile, &cp, "ops", "aws", "ssm", "put-parameter", "--name", "/x/secret-thing"); err != nil {
		t.Fatalf("write verb must pass the read gate: %v", err)
	}
	t.Setenv("WARD_AWS_ALLOW_SENSITIVE_READ", "1")
	if err := runArgv(t, awsGuardfile, &cp, "ops", "aws", "s3", "ls", "s3://prod-secrets-bucket"); err != nil {
		t.Fatalf("env escape must allow the read: %v", err)
	}
}

// awsWhenGuardfile is the shipped shape: `gate aws-read` replaced by a
// self-describing `deny-when` over the aws CLI's read convention.
const awsWhenGuardfile = `wrap ward ops aws {
	exec aws

	can run "*" {
		deny-when any-arg matches \
			"*secret*" "*tfstate*" "arn:aws:iam::*:role/*admin*" \
		{
			only-reads
			allow-env "WARD_AWS_ALLOW_SENSITIVE_READ"
		}
		describe "open aws passthrough; sensitive reads denied pre-send"
	}
}`

// TestDenyWhenScopesToSensitiveReads proves the deny-when guard refuses a
// sensitive read, passes writes (only-reads), and honors the env escape.
func TestDenyWhenScopesToSensitiveReads(t *testing.T) {
	var cp capture
	err := runArgv(t, awsWhenGuardfile, &cp, "ops", "aws", "s3", "ls", "s3://prod-secrets-bucket")
	if err == nil {
		t.Fatal("expected the sensitive read to be denied, got nil")
	}
	if cp.bin != "" {
		t.Errorf("denied invocation still executed: %s %v", cp.bin, cp.argv)
	}
	if !strings.Contains(err.Error(), "*secret*") {
		t.Errorf("denial should name the matched pattern: %v", err)
	}

	// a write naming the same sensitive token passes: the guard is read-scoped
	if err := runArgv(t, awsWhenGuardfile, &cp, "ops", "aws", "ssm", "put-parameter", "--name", "/x/secret-thing"); err != nil {
		t.Fatalf("write verb must skip the read-scoped guard: %v", err)
	}

	// the env escape allows a deliberate sensitive read
	t.Setenv("WARD_AWS_ALLOW_SENSITIVE_READ", "1")
	if err := runArgv(t, awsWhenGuardfile, &cp, "ops", "aws", "s3", "ls", "s3://prod-secrets-bucket"); err != nil {
		t.Fatalf("env escape must allow the read: %v", err)
	}
}

// TestWhenKwargAllowGuard proves `when <flag> matches`: the call passes only
// when the named kwarg's value matches an allowed glob.
func TestWhenKwargAllowGuard(t *testing.T) {
	const src = `wrap ward ops aws {
		exec aws
		can run secretsmanager get-secret-value {
			when secret-id matches "readonly-*"
		}
	}`
	var cp capture
	if err := runArgv(t, src, &cp, "ops", "aws", "secretsmanager", "get-secret-value", "--secret-id", "readonly-keys"); err != nil {
		t.Fatalf("allowed kwarg value refused: %v", err)
	}
	if got := strings.Join(cp.argv, " "); got != "secretsmanager get-secret-value --secret-id readonly-keys" {
		t.Errorf("argv = %q, want the get-secret-value passthrough", got)
	}
	err := runArgv(t, src, &cp, "ops", "aws", "secretsmanager", "get-secret-value", "--secret-id", "prod-db")
	if err == nil {
		t.Fatal("expected a non-matching kwarg to be denied, got nil")
	}
}

// TestWhenPositionalIndexSelector proves `argN` reads one positional by index,
// over the caller args after the matched subcommand path.
func TestWhenPositionalIndexSelector(t *testing.T) {
	const src = `wrap ward ops aws {
		exec aws
		can run "s3 ls" {
			when arg0 matches "s3://public-*"
		}
	}`
	var cp capture
	if err := runArgv(t, src, &cp, "ops", "aws", "s3", "ls", "s3://public-assets"); err != nil {
		t.Fatalf("allowed positional refused: %v", err)
	}
	if err := runArgv(t, src, &cp, "ops", "aws", "s3", "ls", "s3://prod-secrets"); err == nil {
		t.Fatal("expected a non-matching positional to be denied, got nil")
	}
}

// TestUnknownGateFailsClosed proves a typo'd gate name refuses to build.
func TestUnknownGateFailsClosed(t *testing.T) {
	gf, err := Parse([]byte(`wrap ward ops aws {
		exec aws
		can run "*" { gate aws-reed {} }
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Build(Config{Guardfile: gf}); err == nil {
		t.Fatal("expected an unknown-gate build error, got nil")
	}
}

// TestWildcardMustBeOnlyGrant proves mixing `can run *` with named grants
// refuses to build.
func TestWildcardMustBeOnlyGrant(t *testing.T) {
	gf, err := Parse([]byte(`wrap ward ops aws {
		exec aws
		can run "*"
		can run s3
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := Build(Config{Guardfile: gf}); err == nil {
		t.Fatal("expected a wildcard-exclusivity build error, got nil")
	}
}

func TestParseFailsClosed(t *testing.T) {
	cases := []string{
		`wrap ward git { can run status }`,                           // no exec block
		`wrap ward git { exec git }`,                                 // no grants
		`wrap ward git { exec git; can status }`,                     // missing `run`
		`wrap ward git { exec git; can run commit { env "X=1" } }`,   // unknown policy node
		`wrap ward git { exec git; unknown-node x; can run status }`, // unknown wrap child
	}
	for _, src := range cases {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("expected parse failure for %q", src)
		}
	}
}
