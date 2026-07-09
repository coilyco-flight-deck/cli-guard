package issueref_test

import (
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/issueref"
)

const base = "https://forgejo.example.me"

func TestParseShort(t *testing.T) {
	r, err := issueref.Parse("coilyco-flight-deck/cli-guard"+"#"+"166", base)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Owner != "coilyco-flight-deck" || r.Repo != "cli-guard" || r.Number != 166 {
		t.Errorf("unexpected ref: %+v", r)
	}
	if r.String() != "coilyco-flight-deck/cli-guard"+"#"+"166" {
		t.Errorf("String = %q", r.String())
	}
	if r.RepoSlug() != "coilyco-flight-deck/cli-guard" {
		t.Errorf("RepoSlug = %q", r.RepoSlug())
	}
}

func TestParseURL(t *testing.T) {
	r, err := issueref.Parse(base+"/owner/repo/issues/42", base)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if r.Owner != "owner" || r.Repo != "repo" || r.Number != 42 {
		t.Errorf("unexpected ref: %+v", r)
	}
	if got := r.URL(base); got != base+"/owner/repo/issues/42" {
		t.Errorf("URL = %q", got)
	}
}

func TestParseURLWithTrailer(t *testing.T) {
	for _, in := range []string{
		base + "/owner/repo/issues/42/",
		base + "/owner/repo/issues/42?tab=comments",
		base + "/owner/repo/issues/42#issuecomment-7",
	} {
		r, err := issueref.Parse(in, base)
		if err != nil || r.Number != 42 || r.Repo != "repo" {
			t.Errorf("Parse(%q) = %+v, %v", in, r, err)
		}
	}
}

func TestParseBare(t *testing.T) {
	for _, in := range []string{"#" + "7", "7"} {
		r, err := issueref.Parse(in, base)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if r.Owner != "" || r.Repo != "" || r.Number != 7 {
			t.Errorf("Parse(%q) = %+v, want bare issue 7", in, r)
		}
	}
}

func TestParseURLDisabledWithoutBase(t *testing.T) {
	if _, err := issueref.Parse(base+"/owner/repo/issues/42", ""); err == nil {
		t.Error("URL form should not parse when baseURL is empty")
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", "  ", "not-a-ref", "owner/repo"+"#"+"0", "owner/repo"+"#"+"-1", "#" + "0"} {
		if _, err := issueref.Parse(in, base); err == nil {
			t.Errorf("Parse(%q) should error", in)
		}
	}
}
