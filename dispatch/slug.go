package dispatch

import "strings"

// maxBranchSlugLen caps the slug portion of a worktree/branch name so a long
// issue title cannot produce an unwieldy git ref or blow filesystem path
// limits on the worktree directory.
const maxBranchSlugLen = 40

// branchSlug normalizes an issue title into a git-branch-safe slug: lowercase
// ASCII letters and digits pass through, every other byte (spaces,
// punctuation, shell metacharacters, unicode) collapses to a single dash, and
// leading/trailing dashes are trimmed. The result is capped at
// maxBranchSlugLen on a dash boundary. Returns "" when the title has no
// sluggable content, in which case callers fall back to the bare issue-<N>
// form.
//
// The ruleset is derived from the sink (git refname constraints plus
// readability), not from the shell-metacharacter set. Stdlib only, no
// transliteration: "café" slugs to "caf", which is fine for a branch name.
func branchSlug(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	prevDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) <= maxBranchSlugLen {
		return slug
	}
	slug = slug[:maxBranchSlugLen]
	// Trim a trailing partial word so the cap lands on a dash boundary.
	if i := strings.LastIndexByte(slug, '-'); i > 0 {
		slug = slug[:i]
	}
	return strings.Trim(slug, "-")
}
