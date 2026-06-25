package attribution_test

import (
	"strings"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/attribution"
)

func TestLabel(t *testing.T) {
	if got := (attribution.Identity{Name: "Claude", Pronouns: "she/her"}).Label(); got != "Claude (she/her)" {
		t.Errorf("Label = %q", got)
	}
	if got := (attribution.Identity{Name: "Goose"}).Label(); got != "Goose" {
		t.Errorf("Label = %q", got)
	}
	if got := (attribution.Identity{Name: "Qwen", Pronouns: "  "}).Label(); got != "Qwen" {
		t.Errorf("blank pronouns should drop parens, got %q", got)
	}
}

func newSigner() attribution.Signer {
	return attribution.Signer{
		Identity: attribution.Identity{Name: "Claude", Pronouns: "she/her"},
		Marker:   "<!-- ward-agent-signature -->",
		Via:      "via `ward agent`",
		Email:    "claude@ward.agent",
	}
}

func TestSignBodyAppendsFooter(t *testing.T) {
	s := newSigner()
	got := s.SignBody("Some body text.")
	if !strings.HasPrefix(got, "Some body text.\n\n") {
		t.Errorf("body not preserved with blank line: %q", got)
	}
	if !strings.Contains(got, s.Marker) {
		t.Errorf("marker missing: %q", got)
	}
	if !strings.Contains(got, "— Claude (she/her), via `ward agent`") {
		t.Errorf("attribution line missing: %q", got)
	}
}

func TestSignBodyIdempotent(t *testing.T) {
	s := newSigner()
	once := s.SignBody("Body.")
	twice := s.SignBody(once)
	if once != twice {
		t.Errorf("SignBody not idempotent:\n%q\n%q", once, twice)
	}
}

func TestSignBodyEmpty(t *testing.T) {
	s := newSigner()
	got := s.SignBody("")
	if strings.TrimSpace(got) == "" {
		t.Error("empty body should become the footer, not stay empty")
	}
	if strings.HasPrefix(got, "\n") {
		t.Errorf("empty body footer should not lead with newline: %q", got)
	}
}

func TestSignBodyNoMarkerIsNoOp(t *testing.T) {
	s := attribution.Signer{Identity: attribution.Identity{Name: "Claude"}, Via: "x"}
	if got := s.SignBody("hi"); got != "hi" {
		t.Errorf("no-marker signer should be a no-op, got %q", got)
	}
}

func TestSignBodyNoVia(t *testing.T) {
	s := newSigner()
	s.Via = ""
	got := s.SignBody("Body.")
	if !strings.Contains(got, "— Claude (she/her)") || strings.Contains(got, ", via") {
		t.Errorf("blank Via should drop the tail: %q", got)
	}
}

func TestCommitTrailer(t *testing.T) {
	got := newSigner().CommitTrailer()
	want := "Co-Authored-By: Claude (she/her) <claude@ward.agent>"
	if got != want {
		t.Errorf("CommitTrailer = %q, want %q", got, want)
	}
}
