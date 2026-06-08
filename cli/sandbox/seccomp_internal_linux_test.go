//go:build linux

package sandbox

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestBuildFilterLayout locks the jump-offset math: each denied nr branches to
// ERRNO, the arch guard to KILL, fall-through to ALLOW. A wrong offset leaks.
func TestBuildFilterLayout(t *testing.T) {
	const arch = uint32(unix.AUDIT_ARCH_X86_64)
	denied := []uint32{uint32(unix.SYS_PTRACE), uint32(unix.SYS_MOUNT), uint32(unix.SYS_BPF)}
	prog := buildFilter(denied, arch)

	n := len(denied)
	allowIdx := 3 + n
	errnoIdx := allowIdx + 1
	killIdx := allowIdx + 2

	if len(prog) != killIdx+1 {
		t.Fatalf("program length = %d, want %d", len(prog), killIdx+1)
	}

	// 1: arch guard branches to KILL on mismatch (jf), falls through on match.
	archJF := int(prog[1].Jf)
	if 1+1+archJF != killIdx {
		t.Errorf("arch guard jf lands at %d, want KILL at %d", 1+1+archJF, killIdx)
	}

	// 3..: each denied syscall branches forward to ERRNO.
	for i := range denied {
		cur := 3 + i
		jt := int(prog[cur].Jt)
		if cur+1+jt != errnoIdx {
			t.Errorf("denied[%d] jt lands at %d, want ERRNO at %d", i, cur+1+jt, errnoIdx)
		}
	}

	if got := prog[allowIdx].K; got != uint32(unix.SECCOMP_RET_ALLOW) {
		t.Errorf("allow return = %#x, want SECCOMP_RET_ALLOW", got)
	}
	wantErrno := uint32(unix.SECCOMP_RET_ERRNO) | (uint32(unix.EPERM) & seccompRetData)
	if got := prog[errnoIdx].K; got != wantErrno {
		t.Errorf("errno return = %#x, want %#x", got, wantErrno)
	}
	if got := prog[killIdx].K; got != uint32(unix.SECCOMP_RET_KILL_PROCESS) {
		t.Errorf("kill return = %#x, want SECCOMP_RET_KILL_PROCESS", got)
	}
}

// TestAuditArchKnown ensures the running arch resolves to a non-zero AUDIT_ARCH
// (a zero would silently skip the syscall filter on this platform).
func TestAuditArchKnown(t *testing.T) {
	if auditArch() == 0 {
		t.Skip("audit arch not mapped for this GOARCH; seccomp filter would be skipped")
	}
}
