// Package attribution renders the identity a driving agent signs its work
// with: a one-line label ("Claude (she/her)"), an idempotent markdown
// footer appended to bodies an agent posts, and a git Co-Authored-By
// trailer. It is consumer-agnostic - the caller supplies the identity and
// the tool-specific text, so any harness can sign with its own agents
// rather than a baked-in roster.
package attribution

import (
	"fmt"
	"strings"
)

// Identity is who an agent signs as: a display name and optional pronouns.
type Identity struct {
	// Name is the agent's display name, e.g. "Claude" or "Goose".
	Name string
	// Pronouns is optional; when set it renders parenthesised after the name.
	Pronouns string
}

// Label renders the one-line identity: "Claude (she/her)" when pronouns are
// known, otherwise just "Goose".
func (i Identity) Label() string {
	if strings.TrimSpace(i.Pronouns) != "" {
		return fmt.Sprintf("%s (%s)", i.Name, i.Pronouns)
	}
	return i.Name
}

// Signer signs bodies and commits with an Identity, using consumer-supplied
// text: an idempotency Marker, a footer tail Via, and a commit-trailer Email.
type Signer struct {
	Identity Identity
	Marker   string
	Via      string
	Email    string
}

// SignBody idempotently appends the attribution footer to a body. A body
// already carrying Marker (or a Signer with no Marker) is returned unchanged.
func (s Signer) SignBody(body string) string {
	if s.Marker == "" {
		return body
	}
	if strings.Contains(body, s.Marker) {
		return body
	}
	footer := s.footer()
	if strings.TrimSpace(body) == "" {
		return footer
	}
	return strings.TrimRight(body, "\n") + "\n\n" + footer
}

// footer renders the marker line plus the visible attribution line.
func (s Signer) footer() string {
	line := "— " + s.Identity.Label()
	if v := strings.TrimSpace(s.Via); v != "" {
		line += ", " + v
	}
	return s.Marker + "\n" + line
}

// CommitTrailer renders the git Co-Authored-By trailer tagging a commit
// with the signing agent.
func (s Signer) CommitTrailer() string {
	return fmt.Sprintf("Co-Authored-By: %s <%s>", s.Identity.Label(), s.Email)
}
