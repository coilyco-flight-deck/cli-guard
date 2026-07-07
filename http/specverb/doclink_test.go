package specverb

import (
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
)

// TestDescriptionSurfaces proves the guardfile's top-level `description` node
// flows through the describe surface into the reference-doc prose under the H1.
func TestDescriptionSurfaces(t *testing.T) {
	_, spec := loadFixtures(t)
	gf, err := guardfile.Parse([]byte(`description "Forgejo ops surface: scoped read/write over the coily* orgs."
wrap ward ops forgejo {
		spec forgejo.swagger.v1.json
		auth header-token { header Authorization; prefix "token "; value ssm "/forgejo/api-token" }
		can get repo { op "repoGet" }
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if gf.Description == "" {
		t.Fatal("guardfile Description was not parsed")
	}
	surface, err := Describe(Config{Guardfile: gf, Spec: spec})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if surface.Description != gf.Description {
		t.Errorf("Surface.Description = %q, want %q", surface.Description, gf.Description)
	}
	md := surface.Markdown()
	hdr := strings.Index(md, "# ward ops forgejo")
	desc := strings.Index(md, "Forgejo ops surface")
	driven := strings.Index(md, "Spec-driven CLI")
	if hdr >= desc || desc >= driven {
		t.Errorf("description prose misplaced: hdr=%d desc=%d driven=%d\n%s", hdr, desc, driven, md)
	}
}

// TestDocLinkDescribed proves the spec-dialect describe surface renders a
// `## See also` footer from the guardfile's `doc-link` nodes. See docs/doc-link.md.
func TestDocLinkDescribed(t *testing.T) {
	_, spec := loadFixtures(t)
	gf, err := guardfile.Parse([]byte(`wrap ward ops forgejo {
		spec forgejo.swagger.v1.json
		auth header-token { header Authorization; prefix "token "; value ssm "/forgejo/api-token" }
		can get repo { op "repoGet" }
		doc-link "../ward-kdl.md" "ward-kdl.md" "the build-time authoring layer"
		doc-link "ward-kdl-surface.md"
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(gf.DocLinks) != 2 || gf.DocLinks[1].Text != "ward-kdl-surface.md" {
		t.Fatalf("DocLinks = %+v", gf.DocLinks)
	}
	surface, err := Describe(Config{Guardfile: gf, Spec: spec})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	md := surface.Markdown()
	for _, want := range []string{
		"\n## See also\n\n",
		"- [ward-kdl.md](../ward-kdl.md) - the build-time authoring layer\n",
		"- [ward-kdl-surface.md](ward-kdl-surface.md)\n",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("describe prose missing %q:\n%s", want, md)
		}
	}
}

// TestNoDocLinkNoFooter proves the footer is absent when no doc-link is declared.
func TestNoDocLinkNoFooter(t *testing.T) {
	_, spec := loadFixtures(t)
	gf, err := guardfile.Parse([]byte(`wrap ward ops forgejo {
		spec forgejo.swagger.v1.json
		auth header-token { header Authorization; prefix "token "; value ssm "/forgejo/api-token" }
		can get repo { op "repoGet" }
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	surface, err := Describe(Config{Guardfile: gf, Spec: spec})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if strings.Contains(surface.Markdown(), "## See also") {
		t.Errorf("unexpected See also footer without a doc-link")
	}
}
