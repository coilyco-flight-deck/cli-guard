//go:build unix

package doctor

import (
	"os"
	"syscall"
)

// defaultCanExec reports whether the current user can exec info, matching the
// file's owner/group against the process uid/gids. Run it as the agent user.
func defaultCanExec(info os.FileInfo) (canExec bool, known bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, false
	}
	perm := info.Mode().Perm()
	const (
		ownerExec = 0o100
		groupExec = 0o010
		otherExec = 0o001
	)
	if uint32(os.Getuid()) == st.Uid { //nolint:gosec // uid fits; comparison only
		return perm&ownerExec != 0, true
	}
	if inGroup(st.Gid) {
		return perm&groupExec != 0, true
	}
	return perm&otherExec != 0, true
}

// inGroup reports whether gid is the process's primary or supplementary group.
func inGroup(gid uint32) bool {
	if uint32(os.Getgid()) == gid { //nolint:gosec // gid fits; comparison only
		return true
	}
	groups, err := os.Getgroups()
	if err != nil {
		return false
	}
	for _, g := range groups {
		if uint32(g) == gid { //nolint:gosec // gid fits; comparison only
			return true
		}
	}
	return false
}
