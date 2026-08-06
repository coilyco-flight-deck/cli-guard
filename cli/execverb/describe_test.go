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

// TestDescriptionParsesAndSurfaces checks the top-level `description` node parses
// into Guardfile.Description and renders as prose under the reference-doc header.
func TestDescriptionParsesAndSurfaces(t *testing.T) {
	src := `description "AWS ops launcher: read-mostly, tfstate reads denied."` + "\n" + describeFixture
	gf, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if gf.Description != "AWS ops launcher: read-mostly, tfstate reads denied." {
		t.Errorf("Description = %q", gf.Description)
	}
	md := Describe(gf).Markdown()
	// The description prose lands between the H1 and the exec-dialect sentence.
	hdr := strings.Index(md, "# ward-kdl ops aws")
	desc := strings.Index(md, "AWS ops launcher")
	dialect := strings.Index(md, "Exec-dialect CLI")
	if hdr >= desc || desc >= dialect {
		t.Errorf("description prose misplaced: hdr=%d desc=%d dialect=%d\n%s", hdr, desc, dialect, md)
	}
}

// TestDescriptionEmptyFailsClosed checks a bare `description ""` is rejected.
func TestDescriptionEmptyFailsClosed(t *testing.T) {
	src := `description ""` + "\n" + describeFixture
	if _, err := Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("want non-empty error, got %v", err)
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

func TestDescribeSealed(t *testing.T) {
	gf, err := Parse([]byte(`wrap ward-kdl ops forgejo {
		exec kubectl
		can run "read runner-token" { argv "get" "secret" "forgejo-runner-secrets"; sealed }
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := Describe(gf)
	if !s.Grants[0].Sealed {
		t.Error("grant Sealed: got false, want true")
	}
	md := s.Markdown()
	if !strings.Contains(md, "Sealed: the pinned command forwards exactly") {
		t.Errorf("markdown missing the sealed note\n---\n%s", md)
	}
}

func TestDescribeInspectList(t *testing.T) {
	gf, err := Parse([]byte(`wrap ward-kdl inspect {
		allow ls cat grep
		deny-when any-arg matches "*secret*"
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := Describe(gf)
	if !s.Inspect {
		t.Error("inspect-list surface should set Inspect")
	}
	if s.Bin != "" {
		t.Errorf("inspect list has no single Bin, got %q", s.Bin)
	}
	if len(s.Grants) != 3 {
		t.Fatalf("want 3 funnels, got %d", len(s.Grants))
	}
	// each funnel names its own binary and the per-binary dotted audit name
	if s.Grants[1].Bin != "cat" || s.Grants[1].Name != "ward-kdl.inspect.cat" {
		t.Errorf("funnel[1]: got bin=%q name=%q", s.Grants[1].Bin, s.Grants[1].Name)
	}
	md := s.Markdown()
	for _, want := range []string{
		"# ward-kdl inspect",
		"Inspect-list CLI: 3 read-only binaries",
		"## ward-kdl inspect ls",
		"`grep <args...>` (open passthrough)",
		"denies when any-arg matches *secret*", // wrap guard rendered on every leaf
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
