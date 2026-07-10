//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// jailArgv0 is the internal subcommand the consumer's main routes to RunJail
// before any normal CLI parsing.
const jailArgv0 = "__jail"

// Wrap rewrites cmd to re-exec the consumer binary as the jail helper in a fresh
// user+mount namespace. No-op when jailed, opted out (EnvNoSandbox), or spec nil.
func Wrap(cmd *exec.Cmd, realPath string, argv []string, spec *Spec) {
	if spec == nil || spec.SelfExe == "" || Jailed() || NoSandbox() {
		return
	}
	execTool := filepath.Base(realPath)
	tools := withTool(spec.Tools, execTool)

	helper := []string{jailArgv0}
	for _, t := range tools {
		helper = append(helper, "--tool", t)
	}
	helper = append(helper, "--exec", execTool, "--")
	helper = append(helper, argv...)

	cmd.Path = spec.SelfExe
	cmd.Args = append([]string{spec.SelfExe}, helper...)
	cmd.SysProcAttr = jailSysProcAttr()
}

// withTool returns tools guaranteed to contain t.
func withTool(tools []string, t string) []string {
	for _, x := range tools {
		if x == t {
			return tools
		}
	}
	return append(append([]string(nil), tools...), t)
}

// jailSysProcAttr clones a user+mount namespace with an identity uid map (brew
// refuses root); CAP_SYS_ADMIN rides through as an ambient cap for the mounts.
func jailSysProcAttr() *syscall.SysProcAttr {
	uid, gid := os.Getuid(), os.Getgid()
	return &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: 1}},
		GidMappingsEnableSetgroups: false,
		AmbientCaps:                []uintptr{unix.CAP_SYS_ADMIN},
	}
}

// IsJailInvocation reports whether argv is the internal jail-helper re-exec;
// the consumer's main calls RunJail when true.
func IsJailInvocation(argv []string) bool {
	return len(argv) > 1 && argv[1] == jailArgv0
}

// SetupDenied reports whether a jailed cmd failed to *start* on a clone denial
// (EPERM/EACCES, child never ran), so the caller may retry it. See docs/sandbox.md.
func SetupDenied(cmd *exec.Cmd, err error) bool {
	if err == nil || cmd == nil || !IsJailInvocation(cmd.Args) {
		return false
	}
	if cmd.ProcessState != nil {
		return false
	}
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES)
}

// RunJail is the in-namespace helper: it shims the wrapped tools, installs the
// seccomp denylist, and execs the real tool (args is os.Args[2:]).
func RunJail(args []string) error {
	// Pin this OS thread: the thread that installs the non-TSYNC seccomp filter
	// must be the one that execs, which collapses the other Go threads.
	runtime.LockOSThread()

	tools, execTool, rest, err := parseJailArgs(args)
	if err != nil {
		return err
	}
	if execTool == "" {
		return fmt.Errorf("sandbox: jail missing --exec target")
	}

	// Make our mount view private so binds never propagate back to the host.
	if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("sandbox: make-rprivate: %w", err)
	}

	// A private tmpfs stashes real binaries for the compiled-tool case, where
	// masking the canonical path would also hide the file the gate must exec.
	stash := "/tmp/.cliguard-jail"
	if err := os.MkdirAll(stash, 0o700); err != nil {
		return fmt.Errorf("sandbox: mkdir stash: %w", err)
	}
	if err := unix.Mount("tmpfs", stash, "tmpfs", 0, "mode=0700"); err != nil {
		return fmt.Errorf("sandbox: mount stash tmpfs: %w", err)
	}

	shim := spec().SelfExe
	for _, t := range tools {
		if err := installToolShim(t, stash, shim); err != nil {
			return err
		}
	}

	if err := os.Setenv(EnvJailed, "1"); err != nil {
		return fmt.Errorf("sandbox: set jailed env: %w", err)
	}

	// All bind-mounts are done; the tool itself must not keep CAP_SYS_ADMIN.
	// Clear the ambient set so the cap does not leak into the exec'd tool.
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return fmt.Errorf("sandbox: clear ambient caps: %w", err)
	}

	// Drop the ability to gain privileges, then install the syscall denylist.
	// Order matters: all bind-mounts are done above, so mount() can be denied.
	if err := lockdownSyscalls(); err != nil {
		return err
	}

	target := os.Getenv(RealBinEnv(execTool))
	if target == "" {
		return fmt.Errorf("sandbox: exec target %q was not stashed (not on PATH?)", execTool)
	}
	fullArgv := append([]string{execTool}, rest...)
	// Replaces this image; target is our stashed real path, not user input.
	return syscall.Exec(target, fullArgv, os.Environ()) // #nosec G702 -- target is the resolved real binary, not user input
}

// installToolShim records one wrapped tool's real target for the gate to exec,
// then masks its canonical PATH entry with the consumer shim.
func installToolShim(tool, stash, shim string) error {
	canonical, err := exec.LookPath(tool)
	if err != nil {
		// Tool not present on this host: nothing to shim.
		return nil //nolint:nilerr // absence is not an error; just skip
	}
	realPath := canonical
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		realPath = resolved
	}

	realTarget := realPath
	if realPath == canonical {
		// Compiled binary at its own path: stash a bind before masking. argv0/$0
		// are irrelevant for an ELF, so exec'ing the stash copy is fine.
		stashed := filepath.Join(stash, tool)
		if err := touch(stashed); err != nil {
			return fmt.Errorf("sandbox: stash placeholder %s: %w", tool, err)
		}
		if err := unix.Mount(realPath, stashed, "", unix.MS_BIND, ""); err != nil {
			return fmt.Errorf("sandbox: stash bind %s: %w", tool, err)
		}
		realTarget = stashed
	} else {
		// Symlinked $0-sensitive tool (brew derives HOMEBREW_PREFIX from $0's
		// grandparent): exec a canonical-dir symlink so the prefix resolves right.
		link := filepath.Join(filepath.Dir(canonical), "."+tool+".cliguard")
		_ = os.Remove(link)
		if err := os.Symlink(realPath, link); err == nil {
			realTarget = link
		}
	}
	if err := os.Setenv(RealBinEnv(tool), realTarget); err != nil {
		return fmt.Errorf("sandbox: set realbin env %s: %w", tool, err)
	}

	// Mask the canonical PATH/symlink entry with the shim (O_NOFOLLOW so the
	// bind lands on the symlink itself, leaving realTarget reachable).
	if err := maskWithShim(canonical, shim); err != nil {
		return fmt.Errorf("sandbox: shim mask %s: %w", tool, err)
	}
	return nil
}

// spec resolves the consumer binary (the jail helper itself) for the shim
// source; stable even after canonical tool paths are masked.
func spec() *Spec {
	exe, err := os.Executable()
	if err != nil {
		exe = "/proc/self/exe"
	}
	return &Spec{SelfExe: exe}
}

// parseJailArgs reads the `--tool <t>...  --exec <tool>  -- <argv...>` form.
func parseJailArgs(args []string) (tools []string, execTool string, rest []string, err error) {
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--tool":
			if i+1 >= len(args) {
				return nil, "", nil, fmt.Errorf("sandbox: --tool needs a value")
			}
			tools = append(tools, args[i+1])
			i += 2
		case "--exec":
			if i+1 >= len(args) {
				return nil, "", nil, fmt.Errorf("sandbox: --exec needs a value")
			}
			execTool = args[i+1]
			i += 2
		case "--":
			rest = args[i+1:]
			return tools, execTool, rest, nil
		default:
			return nil, "", nil, fmt.Errorf("sandbox: unexpected jail arg %q", args[i])
		}
	}
	return tools, execTool, rest, nil
}

// touch creates an empty file (the bind-mount target placeholder).
func touch(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G302 -- placeholder, masked by bind-mount
	if err != nil {
		return err
	}
	return f.Close()
}

// maskWithShim bind-mounts shim over canonical via an O_PATH|O_NOFOLLOW fd at
// /proc/self/fd/N, so a symlink entry is masked without following to its target.
func maskWithShim(canonical, shim string) error {
	fd, err := unix.Open(canonical, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open canonical: %w", err)
	}
	defer func() { _ = unix.Close(fd) }()
	target := "/proc/self/fd/" + strconv.Itoa(fd)
	if err := unix.Mount(shim, target, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind shim: %w", err)
	}
	return nil
}
