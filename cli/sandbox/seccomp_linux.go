//go:build linux

package sandbox

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// seccompRetData masks the 16-bit data field carried in SECCOMP_RET_ERRNO.
const seccompRetData = 0x0000ffff

// lockdownSyscalls sets no_new_privs and installs the seccomp denylist on the
// current locked thread, without TSYNC since execve tears down the others.
func lockdownSyscalls() error {
	// Belt-and-suspenders: the caller already locked the thread.
	runtime.LockOSThread()

	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("sandbox: set no_new_privs: %w", err)
	}

	arch := auditArch()
	if arch == 0 {
		// Unknown arch: skip the syscall filter rather than risk a wrong-arch
		// program. The namespace + shim enforcement still applies.
		return nil
	}

	prog := buildFilter(deniedSyscalls(), arch)
	fprog := unix.SockFprog{
		Len:    uint16(len(prog)), // #nosec G115 -- program is a fixed, small denylist; length fits uint16
		Filter: &prog[0],
	}
	if _, _, errno := unix.Syscall(
		unix.SYS_SECCOMP,
		uintptr(unix.SECCOMP_SET_MODE_FILTER),
		0,                               // flags: no TSYNC
		uintptr(unsafe.Pointer(&fprog)), // #nosec G103 -- required to pass the filter program to seccomp(2)
	); errno != 0 {
		return fmt.Errorf("sandbox: seccomp set filter: %w", errno)
	}
	return nil
}

// auditArch returns the AUDIT_ARCH_* matching the compiled GOARCH, or 0 if the
// arch is one we have not validated the syscall numbers for.
func auditArch() uint32 {
	switch runtime.GOARCH {
	case "amd64":
		return uint32(unix.AUDIT_ARCH_X86_64)
	case "arm64":
		return uint32(unix.AUDIT_ARCH_AARCH64)
	default:
		return 0
	}
}

// deniedSyscalls is the escape/escalation denylist (ptrace, module load,
// re-namespacing, remount, keyring, cross-process memory). Normal tools unaffected.
func deniedSyscalls() []uint32 {
	return []uint32{
		unix.SYS_PTRACE,
		unix.SYS_KEXEC_LOAD,
		unix.SYS_KEXEC_FILE_LOAD,
		unix.SYS_INIT_MODULE,
		unix.SYS_FINIT_MODULE,
		unix.SYS_DELETE_MODULE,
		unix.SYS_BPF,
		unix.SYS_MOUNT,
		unix.SYS_UMOUNT2,
		unix.SYS_PIVOT_ROOT,
		unix.SYS_SETNS,
		unix.SYS_UNSHARE,
		unix.SYS_KEYCTL,
		unix.SYS_ADD_KEY,
		unix.SYS_REQUEST_KEY,
		unix.SYS_PERF_EVENT_OPEN,
		unix.SYS_PROCESS_VM_READV,
		unix.SYS_PROCESS_VM_WRITEV,
	}
}

// buildFilter assembles a classic-BPF program: wrong arch -> KILL, a denied nr
// -> ERRNO(EPERM), else ALLOW. Offsets derive from instruction indices.
func buildFilter(deniedNRs []uint32, arch uint32) []unix.SockFilter {
	const (
		offArch = 4 // offsetof(struct seccomp_data, arch)
		offNR   = 0 // offsetof(struct seccomp_data, nr)
	)
	n := len(deniedNRs)
	allowIdx := 3 + n // RET ALLOW
	errnoIdx := allowIdx + 1
	killIdx := allowIdx + 2

	prog := make([]unix.SockFilter, 0, killIdx+1)
	// 0: A = arch
	prog = append(prog, stmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, offArch))
	// 1: if A == arch -> next; else -> KILL
	prog = append(prog, jump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, arch, 0, uint8(killIdx-2))) // #nosec G115 -- denylist is small; offset fits uint8
	// 2: A = nr
	prog = append(prog, stmt(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS, offNR))
	// 3..: for each denied nr, if A == nr -> ERRNO
	for i, nr := range deniedNRs {
		cur := 3 + i
		jt := uint8(errnoIdx - cur - 1) // #nosec G115 -- denylist is small; offset fits uint8
		prog = append(prog, jump(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K, nr, jt, 0))
	}
	prog = append(prog, stmt(unix.BPF_RET|unix.BPF_K, uint32(unix.SECCOMP_RET_ALLOW)))
	prog = append(prog, stmt(unix.BPF_RET|unix.BPF_K, uint32(unix.SECCOMP_RET_ERRNO)|(uint32(unix.EPERM)&seccompRetData)))
	prog = append(prog, stmt(unix.BPF_RET|unix.BPF_K, uint32(unix.SECCOMP_RET_KILL_PROCESS)))
	return prog
}

func stmt(code uint16, k uint32) unix.SockFilter {
	return unix.SockFilter{Code: code, K: k}
}

func jump(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
	return unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
}
