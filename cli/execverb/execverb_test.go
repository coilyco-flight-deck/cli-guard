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
