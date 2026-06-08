//go:build !unix

package doctor

import "os"

// defaultCanExec cannot inspect ownership on non-unix platforms, so it
// reports unknown; Check turns unknown into a Warn.
func defaultCanExec(info os.FileInfo) (canExec bool, known bool) {
	_ = info
	return false, false
}
