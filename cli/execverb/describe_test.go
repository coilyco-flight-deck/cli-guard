package execverb

import (
	"strings"
	"testing"
)

const describeFixture = `wrap ward-kdl ops aws {
	exec aws
	can run sts get-caller-identity
	can run s3 ls {
		describe "list a bucket"
		deny-when arg0 matches "*tfstate*" "*-backup*"
	}
	can run secretsmanager get-secret-value {
		allow-flag "--secret-id"
	}
}`

func TestDescribeBuildsSurface(t *testing.T) {
	gf, err := Parse([]byte(describeFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := Describe(gf)
	if s.Bin != "aws" {
		t.Errorf("bin: got %q want aws", s.Bin)
	}
	if len(s.Grants) != 3 {
		t.Fatalf("want 3 grants, got %d", len(s.Grants))
	}
	// Dotted audit name composes group + subcommand.
	if s.Grants[1].Name != "ward-kdl.ops.aws.s3.ls" {
		t.Errorf("grant name: got %q", s.Grants[1].Name)
	}
	if s.Grants[1].Describe != "list a bucket" {
		t.Errorf("describe note dropped: %q", s.Grants[1].Describe)
	}
}

func TestDescribeMarkdown(t *testing.T) {
	gf, err := Parse([]byte(describeFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	md := Describe(gf).Markdown()
	for _, want := range []string{
		"# ward-kdl ops aws",
		"Exec-dialect CLI",
		"## ward-kdl ops aws sts get-caller-identity",
		"## ward-kdl ops aws s3 ls - list a bucket",
		"denies when arg0 matches *tfstate* or *-backup*",
		"only `--secret-id` allowed (strict allowlist)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
}

func TestDescribeArgvOverride(t *testing.T) {
	gf, err := Parse([]byte(`wrap ward-kdl agents claude {
		exec claude
		can run launch { argv; describe "interactive" }
		can run headless { argv "-p" }
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := Describe(gf)
	// the leaf name (Subcommand) and the executed argv (Exec) diverge.
	if got := strings.Join(s.Grants[0].Exec, " "); got != "" {
		t.Errorf("launch Exec: got %q want empty (bare)", got)
	}
	if got := strings.Join(s.Grants[1].Exec, " "); got != "-p" {
		t.Errorf("headless Exec: got %q want -p", got)
	}
	md := s.Markdown()
	for _, want := range []string{
		"## ward-kdl agents claude launch - interactive",
		"`claude`\n",    // bare launch renders just the binary
		"`claude -p`\n", // headless renders the override, not the leaf name
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
}

func TestDescribeWildcard(t *testing.T) {
	gf, err := Parse([]byte(`wrap ward ops docker { exec docker; can run * }`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := Describe(gf)
	if len(s.Grants) != 1 || !s.Grants[0].Wildcard {
		t.Fatalf("want one wildcard grant, got %+v", s.Grants)
	}
	if !strings.Contains(s.Markdown(), "* (open passthrough)") {
		t.Error("wildcard markdown should mark open passthrough")
	}
}
