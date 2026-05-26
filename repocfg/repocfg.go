// Package repocfg loads a per-repo command allowlist from a coily.yaml file
// discovered by walking up from the current working directory. Each command
package repocfg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coilysiren/cli-guard/policy"
	"gopkg.in/yaml.v3"
)

// Filename is the name repocfg looks for during discovery.
const Filename = "coily.yaml"

// LocalDirName is the per-repo overlay directory the loader prefers. The
// canonical home for the repo allowlist is ./.coily/coily.yaml.
const LocalDirName = ".coily"

// LegacyFilename is the pre-overlay name (./coily.yaml) that we reject with
// a pointer at the new location. Kept around so the error message can be
const LegacyFilename = "coily.yaml"

// EnvOverride, when set, is treated as the absolute path to the repo config
// and skips directory walking. Primarily for tests and advanced users.
const EnvOverride = "COILY_REPO_CONFIG"

// ErrLegacyLocation is wrapped by Discover when a coily.yaml is found at the
// repo root rather than under ./.coily/. The new convention is to put it
var ErrLegacyLocation = errors.New("repocfg: coily.yaml at repo root is no longer supported, move it to .coily/coily.yaml")

// Command is one parsed and validated entry from the commands: map.
type Command struct {
	// Name is the key from the yaml map, e.g. "test".
	Name string
	// Description is the optional human-readable blurb shown in help/--list.
	Description string
	// Argv is the command split into tokens. argv[0] is the binary name as
	// resolved via $PATH at exec time. Every token has been run through
	Argv []string
	// Egress, when true, opts the command into the per-invocation CONNECT
	// proxy that consumers (coily) wire around exec. The audit row picks up
	Egress bool
	// AllowMetacharacters opts this command out of the shell-metacharacter
	// validator for both the YAML-declared argv tokens (skipped at load)
	AllowMetacharacters bool
}

// SSHTarget describes one named ssh destination for the
// `coily ssh <alias> -- <args>` passthrough. All three fields are
type SSHTarget struct {
	// Name is the host alias, e.g. "kai-server". Matches the map key it
	// was loaded under. Validated with the same rules as command names.
	Name string
	// User is the remote ssh user. Plain string; ssh handles the rest.
	User string
	// Host is the remote hostname (or hostname:port). Resolved by the
	// caller's ssh client; repocfg does not DNS-validate.
	Host string
	// WorkingDir is the absolute path of the working directory the remote
	// coily binds its --commit-scope to. Must be absolute (POSIX leading
	WorkingDir string
}

// Config is the result of a successful Load.
type Config struct {
	// Path is the absolute path to the coily.yaml that produced this Config.
	Path string
	// Commands are sorted by Name. Safe to iterate directly for help output.
	Commands []Command
	// SSHTargets is the named ssh-passthrough target map keyed by alias.
	// Nil/empty when the file has no `ssh:` block. coilysiren/coily#187.
	SSHTargets map[string]SSHTarget
}

// ErrNoConfig is returned by LoadDefault when no coily.yaml is found in the
// cwd ancestry. Callers treat this as "no repo commands to register."
var ErrNoConfig = errors.New("repocfg: no coily.yaml found")

// Discover walks up from start looking for the repo config. Prefers
// ./.coily/coily.yaml at each level. If no overlay file is found but a
func Discover(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("repocfg: abs %s: %w", start, err)
	}
	for {
		path, err := discoverAtLevel(dir)
		if err != nil {
			return "", err
		}
		if path != "" {
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoConfig
		}
		dir = parent
	}
}

// discoverAtLevel checks one directory for a repo config. Returns the path if
// the preferred overlay exists, an ErrLegacyLocation-wrapped error if only
func discoverAtLevel(dir string) (string, error) {
	preferred := filepath.Join(dir, LocalDirName, Filename)
	if ok, err := isFile(preferred); err != nil {
		return "", err
	} else if ok {
		return preferred, nil
	}
	legacy := filepath.Join(dir, LegacyFilename)
	if ok, err := isFile(legacy); err != nil {
		return "", err
	} else if ok {
		return "", fmt.Errorf("%w (found %s)", ErrLegacyLocation, legacy)
	}
	return "", nil
}

// isFile returns true when path exists and is a regular file. Missing path
// is not an error.
func isFile(path string) (bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		return !info.IsDir(), nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("repocfg: stat %s: %w", path, err)
}

// DiscoverChildren scans direct children of parentDir for a
// ./.coily/coily.yaml overlay, loads each one, and returns the resulting
func DiscoverChildren(parentDir string) ([]*Config, error) {
	abs, err := filepath.Abs(parentDir)
	if err != nil {
		return nil, fmt.Errorf("repocfg: abs %s: %w", parentDir, err)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("repocfg: readdir %s: %w", abs, err)
	}
	var configs []*Config
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		candidate := filepath.Join(abs, e.Name(), LocalDirName, Filename)
		ok, statErr := isFile(candidate)
		if statErr != nil || !ok {
			continue
		}
		cfg, loadErr := Load(candidate)
		if loadErr != nil {
			continue
		}
		configs = append(configs, cfg)
	}
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Path < configs[j].Path
	})
	return configs, nil
}

// DiscoverAll returns every coily.yaml reachable from start in a single
// deterministic pool: every level of the ancestor walk from start up to
func DiscoverAll(start string) ([]*Config, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return nil, fmt.Errorf("repocfg: abs %s: %w", start, err)
	}
	var configs []*Config
	seen := map[string]bool{}
	dir := abs
	for {
		if path, _ := discoverAtLevel(dir); path != "" && !seen[path] {
			if cfg, loadErr := Load(path); loadErr == nil {
				configs = append(configs, cfg)
				seen[path] = true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	children, _ := DiscoverChildren(abs)
	for _, c := range children {
		if !seen[c.Path] {
			configs = append(configs, c)
			seen[c.Path] = true
		}
	}
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Path < configs[j].Path
	})
	return configs, nil
}

// LoadDefault resolves the config path from $COILY_REPO_CONFIG or by walking
// up from the current working directory, then parses it. Returns nil,
func LoadDefault() (*Config, error) {
	if override := os.Getenv(EnvOverride); override != "" {
		return Load(override)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("repocfg: getwd: %w", err)
	}
	path, err := Discover(cwd)
	if err != nil {
		return nil, err
	}
	return Load(path)
}

// Load parses the yaml at path. Every command is validated against
// policy.ValidateArg. A single bad token fails the whole load.
func Load(path string) (*Config, error) {
	path = filepath.Clean(path)
	b, err := os.ReadFile(path) // #nosec G304 -- caller-controlled config path is the intended input
	if err != nil {
		return nil, fmt.Errorf("repocfg: read %s: %w", path, err)
	}
	var raw struct {
		Commands map[string]yaml.Node `yaml:"commands"`
		SSH      *struct {
			Targets map[string]yaml.Node `yaml:"targets"`
		} `yaml:"ssh"`
	}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("repocfg: parse %s: %w", path, err)
	}

	cfg := &Config{Path: path}
	for name, node := range raw.Commands {
		cmd, err := decodeCommand(name, node)
		if err != nil {
			return nil, fmt.Errorf("repocfg: %s: command %q: %w", path, name, err)
		}
		cfg.Commands = append(cfg.Commands, cmd)
	}
	sort.Slice(cfg.Commands, func(i, j int) bool {
		return cfg.Commands[i].Name < cfg.Commands[j].Name
	})

	if raw.SSH != nil {
		cfg.SSHTargets = map[string]SSHTarget{}
		for name, node := range raw.SSH.Targets {
			t, err := decodeSSHTarget(name, node)
			if err != nil {
				return nil, fmt.Errorf("repocfg: %s: ssh target %q: %w", path, name, err)
			}
			cfg.SSHTargets[name] = t
		}
	}
	return cfg, nil
}

// decodeSSHTarget parses one entry from the `ssh.targets:` map. user, host,
// and working_dir are all mandatory; working_dir must be absolute since it
func decodeSSHTarget(name string, node yaml.Node) (SSHTarget, error) {
	if err := validateName(name); err != nil {
		return SSHTarget{}, err
	}
	if node.Kind != yaml.MappingNode {
		return SSHTarget{}, errors.New("must be a {user, host, working_dir} mapping")
	}
	var obj struct {
		User       string `yaml:"user"`
		Host       string `yaml:"host"`
		WorkingDir string `yaml:"working_dir"`
	}
	if err := node.Decode(&obj); err != nil {
		return SSHTarget{}, fmt.Errorf("decode mapping: %w", err)
	}
	obj.User = strings.TrimSpace(obj.User)
	obj.Host = strings.TrimSpace(obj.Host)
	obj.WorkingDir = strings.TrimSpace(obj.WorkingDir)
	if obj.User == "" {
		return SSHTarget{}, errors.New("user is empty")
	}
	if obj.Host == "" {
		return SSHTarget{}, errors.New("host is empty")
	}
	if obj.WorkingDir == "" {
		return SSHTarget{}, errors.New("working_dir is empty")
	}
	if !strings.HasPrefix(obj.WorkingDir, "/") {
		return SSHTarget{}, fmt.Errorf("working_dir %q must be absolute (start with /)", obj.WorkingDir)
	}
	for label, val := range map[string]string{"user": obj.User, "host": obj.Host, "working_dir": obj.WorkingDir} {
		if err := policy.ValidateArg(label, val); err != nil {
			return SSHTarget{}, err
		}
	}
	return SSHTarget{Name: name, User: obj.User, Host: obj.Host, WorkingDir: obj.WorkingDir}, nil
}

func decodeCommand(name string, node yaml.Node) (Command, error) {
	if err := validateName(name); err != nil {
		return Command{}, err
	}
	var (
		runStr, desc        string
		allowMetacharacters bool
		egress              bool
	)
	switch node.Kind {
	case yaml.ScalarNode:
		if err := node.Decode(&runStr); err != nil {
			return Command{}, fmt.Errorf("decode scalar: %w", err)
		}
	case yaml.MappingNode:
		var obj struct {
			Run                 string `yaml:"run"`
			Description         string `yaml:"description"`
			AllowMetacharacters bool   `yaml:"allow_metacharacters"`
			Audit               struct {
				Egress bool `yaml:"egress"`
			} `yaml:"audit"`
		}
		if err := node.Decode(&obj); err != nil {
			return Command{}, fmt.Errorf("decode mapping: %w", err)
		}
		runStr = obj.Run
		desc = obj.Description
		allowMetacharacters = obj.AllowMetacharacters
		egress = obj.Audit.Egress
	case yaml.DocumentNode, yaml.SequenceNode, yaml.AliasNode:
		return Command{}, fmt.Errorf("must be a string or a {run, description, allow_metacharacters, audit} mapping")
	default:
		return Command{}, fmt.Errorf("must be a string or a {run, description, allow_metacharacters, audit} mapping")
	}
	runStr = strings.TrimSpace(runStr)
	if runStr == "" {
		return Command{}, errors.New("run is empty")
	}
	argv := strings.Fields(runStr)
	if len(argv) == 0 {
		return Command{}, errors.New("run parsed to zero tokens")
	}
	if err := validateArgvTokens(argv, allowMetacharacters); err != nil {
		return Command{}, err
	}
	return Command{Name: name, Description: desc, Argv: argv, AllowMetacharacters: allowMetacharacters, Egress: egress}, nil
}

// validateArgvTokens runs policy.ValidateArg over each declared argv token
// unless the command opted in to allow_metacharacters: true. Split out of
func validateArgvTokens(argv []string, allowMetacharacters bool) error {
	if allowMetacharacters {
		return nil
	}
	for i, tok := range argv {
		if err := policy.ValidateArg(fmt.Sprintf("argv[%d]", i), tok); err != nil {
			return err
		}
	}
	return nil
}

// validateName rejects command names that would confuse cli parsing or help
// output. Keep it tight: lowercase letters, digits, and single-dashes.
func validateName(name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return fmt.Errorf("name %q cannot start or end with '-'", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("name %q contains illegal character %q (allowed: a-z, 0-9, -)", name, r)
		}
	}
	return nil
}
