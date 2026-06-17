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
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/valuesource"
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
	env  []string
}

func (cp *capture) run(_ context.Context, bin string, argv, env []string) error {
	cp.bin = bin
	cp.argv = argv
	cp.env = env
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
		gate aws-read
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

// TestAWSReadGatePassesWrites proves write verbs skip the read gate.
func TestAWSReadGatePassesWrites(t *testing.T) {
	var cp capture
	if err := runArgv(t, awsGuardfile, &cp, "ops", "aws", "ssm", "put-parameter", "--name", "/x/secret-thing"); err != nil {
		t.Fatalf("write verb must pass the read gate: %v", err)
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
		}
		describe "open aws passthrough; sensitive reads denied pre-send"
	}
}`

// TestDenyWhenScopesToSensitiveReads proves the deny-when guard refuses a
// sensitive read and passes writes (only-reads).
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

// agentGuardfile exercises the per-grant `argv` override: a friendly leaf name
// (launch/headless/whoami) decoupled from the executed invocation.
const agentGuardfile = `wrap ward-kdl agents claude {
	exec claude

	can run launch {
		argv
		describe "interactive session (bare binary)"
	}
	can run headless {
		argv "-p"
		describe "non-interactive print mode"
	}
	can run whoami {
		argv "login" "status"
	}
	can run doctor
}`

func TestArgvOverrideBareLaunch(t *testing.T) {
	var cp capture
	if err := runArgv(t, agentGuardfile, &cp, "agents", "claude", "launch"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if cp.bin != "claude" {
		t.Errorf("bin = %q, want claude", cp.bin)
	}
	// the friendly leaf name `launch` must NOT leak into argv; an empty
	// override runs the bare binary.
	if len(cp.argv) != 0 {
		t.Errorf("argv = %v, want empty (bare launch)", cp.argv)
	}
}

func TestArgvOverrideBareLaunchForwardsArgs(t *testing.T) {
	var cp capture
	if err := runArgv(t, agentGuardfile, &cp, "agents", "claude", "launch", "--model", "opus"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.Join(cp.argv, " "); got != "--model opus" {
		t.Errorf("argv = %q, want %q", got, "--model opus")
	}
}

func TestArgvOverrideFlagShape(t *testing.T) {
	var cp capture
	if err := runArgv(t, agentGuardfile, &cp, "agents", "claude", "headless", "write a test"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.Join(cp.argv, " "); got != "-p write a test" {
		t.Errorf("argv = %q, want %q", got, "-p write a test")
	}
}

func TestArgvOverrideMultiToken(t *testing.T) {
	var cp capture
	if err := runArgv(t, agentGuardfile, &cp, "agents", "claude", "whoami"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.Join(cp.argv, " "); got != "login status" {
		t.Errorf("argv = %q, want %q", got, "login status")
	}
}

func TestNoArgvOverrideKeepsSubcommand(t *testing.T) {
	var cp capture
	if err := runArgv(t, agentGuardfile, &cp, "agents", "claude", "doctor"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.Join(cp.argv, " "); got != "doctor" {
		t.Errorf("argv = %q, want %q (subcommand kept when no override)", got, "doctor")
	}
}

func TestArgvOverrideParseFailsClosed(t *testing.T) {
	cases := []string{
		`wrap ward agents claude { exec claude; can run "*" { argv "-p" } }`,          // argv on wildcard
		`wrap ward agents claude { exec claude; can run launch { argv; argv "-p" } }`, // duplicate argv
	}
	for _, src := range cases {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("expected parse failure for %q", src)
		}
	}
}

// envGuardfile injects an opaque host via a provider plus a literal: the
// OLLAMA_HOST-to-tower shape.
const envGuardfile = `wrap ward-kdl agents ollama {
	exec ollama {
		env "OLLAMA_HOST" { value ssm "/coilysiren/ollama/host" }
		env "OLLAMA_KEEP_ALIVE" "30m"
	}
	can run list
}`

func TestEnvResolvesProviderAndLiteral(t *testing.T) {
	gf, err := Parse([]byte(envGuardfile))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var cp capture
	root := &cli.Command{Name: "ward-kdl"}
	provs := map[string]valuesource.Provider{
		"ssm": func(_ context.Context, addr string) (string, error) {
			if addr != "/coilysiren/ollama/host" {
				t.Fatalf("ssm address = %q", addr)
			}
			return "http://tower:11434", nil
		},
	}
	if err := Mount(root, Config{Guardfile: gf, Run: cp.run, Providers: provs}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if err := root.Run(context.Background(), []string{"ward-kdl", "agents", "ollama", "list"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := map[string]string{}
	for _, kv := range cp.env {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	if got["OLLAMA_HOST"] != "http://tower:11434" {
		t.Errorf("OLLAMA_HOST = %q (resolved provider value), env = %v", got["OLLAMA_HOST"], cp.env)
	}
	if got["OLLAMA_KEEP_ALIVE"] != "30m" {
		t.Errorf("OLLAMA_KEEP_ALIVE = %q (literal), env = %v", got["OLLAMA_KEEP_ALIVE"], cp.env)
	}
}

func TestEnvUnresolvedProviderFailsClosed(t *testing.T) {
	gf, err := Parse([]byte(envGuardfile))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var cp capture
	root := &cli.Command{Name: "ward-kdl"}
	// No `ssm` provider registered -> resolution must fail before any exec.
	if err := Mount(root, Config{Guardfile: gf, Run: cp.run}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	err = root.Run(context.Background(), []string{"ward-kdl", "agents", "ollama", "list"})
	if err == nil {
		t.Fatal("expected an env-resolution failure, got nil")
	}
	if cp.bin != "" {
		t.Errorf("exec ran despite unresolved env: %s %v", cp.bin, cp.argv)
	}
}

func TestEnvParseFailsClosed(t *testing.T) {
	cases := []string{
		`wrap ward x { exec foo { env "BAR" } can run baz }`,                        // neither literal nor value
		`wrap ward x { exec foo { env "BAR" { value ssm } } can run baz }`,          // value missing address
		`wrap ward x { exec foo { env { value ssm "/p" } } can run baz }`,           // env missing name
		`wrap ward x { exec foo { env "BAR" "lit" { value ssm "/p" } } can run b }`, // literal and block both
	}
	for _, src := range cases {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("expected parse failure for %q", src)
		}
	}
}
