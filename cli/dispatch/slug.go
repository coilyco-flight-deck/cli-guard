package dispatch

import "strings"

// maxBranchSlugLen caps the slug so a long title can't blow git refname or
// filesystem path limits on the worktree directory.
const maxBranchSlugLen = 40

// branchSlug normalizes a title into a git-branch-safe slug: [a-z0-9] pass
// through, every other byte collapses to a dash, capped on a dash boundary.
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
