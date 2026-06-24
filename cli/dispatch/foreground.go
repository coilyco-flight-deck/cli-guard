package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// spawnForegroundClaude runs `claude -p` in the foreground with inherited
// stdio, blocking until exit and returning the child's exit code. Bound to
func spawnForegroundClaude(ctx context.Context, repoPath, bin string, argv, env []string) (int, error) {
	binPath, err := exec.LookPath(bin)
	if err != nil {
		return 0, fmt.Errorf("resolve %s on PATH: %w", bin, err)
	}
	cmd := exec.CommandContext(ctx, binPath, argv...)
	cmd.Dir = repoPath
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// claude ran and exited nonzero - report the code, not an error,
			// so the caller can surface it as a clean job failure.
			return ee.ExitCode(), nil
		}
		return 0, fmt.Errorf("run foreground claude: %w", err)
	}
	return 0, nil
}
