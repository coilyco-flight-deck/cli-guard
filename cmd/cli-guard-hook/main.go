// Command cli-guard-hook is a single-purpose Claude Code PreToolUse binary for
// shell-only consumers. See docs/architecture.md for payload flow and flags.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/hook"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/hookcfg"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/repocfg"
)

const (
	exitBlock         = 2
	envConfig         = "CLI_GUARD_HOOK_CONFIG"
	defaultSourceName = "cli-guard-hook"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stderr, os.Getenv, exec.LookPath))
}

// run is the testable core. Returns the process exit code rather than
// calling os.Exit so tests can drive it without spawning processes.
func run(args []string, stdin io.Reader, stderr io.Writer, getenv func(string) string, lookup hook.LookPath) int {
	configPath, source := parseFlags(args, getenv)

	payload := hook.ReadPayload(stdin)
	if payload.ToolName == "" {
		return 0
	}

	protected := loadProtected(configPath)
	if len(protected) == 0 {
		return 0
	}

	d := hook.PreToolUse(payload, source, nil, nil, lookup, protected...)
	if d.Block {
		_, _ = fmt.Fprintln(stderr, d.Message)
		return exitBlock
	}
	return 0
}

// parseFlags walks args looking for --config / --source, keeping the binary's
// surface to what the install script writes into settings.json.
func parseFlags(args []string, getenv func(string) string) (configPath, source string) {
	source = defaultSourceName
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--config" && i+1 < len(args):
			configPath = args[i+1]
			i++
		case len(args[i]) > len("--config=") && args[i][:len("--config=")] == "--config=":
			configPath = args[i][len("--config="):]
		case args[i] == "--source" && i+1 < len(args):
			source = args[i+1]
			i++
		case len(args[i]) > len("--source=") && args[i][:len("--source=")] == "--source=":
			source = args[i][len("--source="):]
		}
	}
	if configPath == "" {
		configPath = getenv(envConfig)
	}
	return configPath, source
}

// loadProtected reads the config file and returns the mapped Protected list.
// Best-effort: empty path, missing file, or any parse error returns nil.
func loadProtected(path string) []hook.Protected {
	if path == "" {
		return nil
	}
	cfg, err := repocfg.Load(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return nil
	}
	return hookcfg.ProtectedFor(cfg.Security)
}
