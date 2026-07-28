// Package issueref parses an issue reference into its owner / repo /
// number parts. Any consumer that just needs to turn a ref string into
// parts can call Parse without extra wiring.
//
// Three forms parse:
//
//	owner/repo#N                              short form
//	#N  or  N                                 bare form (owner/repo empty)
//	<baseURL>/owner/repo/issues/N             Forgejo-style issue URL
//
// A trailing slash and any appended ?query / #fragment are tolerated, so a
// browser-copied URL parses unedited. The number must be a positive
// integer. The bare form carries only the number, leaving owner and repo
// empty for the caller to fill from context (e.g. the cwd's git origin).
package issueref

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Ref is a parsed issue reference. Owner and Repo are empty for a bare ref.
type Ref struct {
	Owner  string
	Repo   string
	Number int
}

// String renders the canonical owner/repo#N short form.
func (r Ref) String() string {
	return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number)
}

// RepoSlug renders the owner/repo pair without the issue number.
func (r Ref) RepoSlug() string {
	return r.Owner + "/" + r.Repo
}

// URL renders the canonical Forgejo issue URL under baseURL.
func (r Ref) URL(baseURL string) string {
	return fmt.Sprintf("%s/%s/%s/issues/%d", strings.TrimRight(baseURL, "/"), r.Owner, r.Repo, r.Number)
}

// trailerRE swallows an optional trailing slash plus any appended ?query
// and/or #fragment, so a browser-copied ref parses unedited.
const trailerRE = `/?(?:[?#].*)?$`

// shortRE matches owner/repo#N, ignoring any appended query/fragment.
var shortRE = regexp.MustCompile(`^([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)#(\d+)` + trailerRE)

// bareRE matches a bare `#N` or `N` ref (no owner/repo).
var bareRE = regexp.MustCompile(`^#?(\d+)` + trailerRE)

// Parse resolves owner/repo#N, a bare #N / N, or a Forgejo issue URL under
// baseURL (empty disables URL parsing). The number must be positive.
func Parse(s, baseURL string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("empty issue reference")
	}
	if m := shortRE.FindStringSubmatch(s); m != nil {
		return refFrom(m[1], m[2], m[3], s)
	}
	if baseURL != "" {
		if m := urlRE(baseURL).FindStringSubmatch(s); m != nil {
			return refFrom(m[1], m[2], m[3], s)
		}
	}
	if m := bareRE.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return Ref{}, fmt.Errorf("issue number must be a positive integer in %q", s)
		}
		return Ref{Number: n}, nil
	}
	return Ref{}, parseError(s, baseURL)
}

// urlRE matches <baseURL>/owner/repo/issues/N, query/fragment ignored. It
// is built per call because the base URL is host-configurable.
func urlRE(baseURL string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(strings.TrimRight(baseURL, "/")) +
		`/([A-Za-z0-9._-]+)/([A-Za-z0-9._-]+)/issues/(\d+)` + trailerRE)
}

// refFrom builds a Ref from regex captures, validating the number.
func refFrom(owner, repo, num, s string) (Ref, error) {
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return Ref{}, fmt.Errorf("issue number must be a positive integer in %q", s)
	}
	return Ref{Owner: owner, Repo: repo, Number: n}, nil
}

// parseError renders the "want X, Y, or Z" failure, naming the URL form
// only when a base URL was supplied.
func parseError(s, baseURL string) error {
	want := "owner/repo#N or a bare #N"
	if baseURL != "" {
		want += " or " + strings.TrimRight(baseURL, "/") + "/owner/repo/issues/N"
	}
	return fmt.Errorf("cannot parse issue ref %q: want %s", s, want)
}
