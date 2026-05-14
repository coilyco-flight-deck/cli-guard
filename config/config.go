// Package config loads a layered configuration: a Go-literal default
// baked into the binary, ~/.coily/config.yaml (the global overlay), and
// ./.coily/config.yaml (the per-repo local overlay). Local always wins
// on a per-key basis.
//
// The default layer guarantees the binary can boot with no on-disk state
// at all. The global layer holds host-wide defaults (audit rotation knobs,
// AWS profile name). The local layer lets a repo carry its own overrides
// into a fresh checkout without touching ~/.
//
// The .coily directory name is the convention shared by every cli-guard
// consumer today. Override paths via GlobalConfigPath / LocalConfigPath
// callers if a different directory is wanted.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// AuditLogEnvVar is the environment variable that, when set, overrides
// Audit.LogPath wholesale. Documented as the orchestrator-friendly path
// override: an external runner can point a cli-guard consumer at its own
// log dir without writing a config file.
const AuditLogEnvVar = "COILY_AUDIT_LOG"

// defaults returns the baseline config used when no overlay layer fills a
// field. LogPath stays empty so applyDefaults() falls to the per-repo
// DefaultAuditPath under ~/.coily/audit/.
func defaults() Config {
	return Config{
		KaiServer: KaiServer{
			TailscaleHost: "kai-server",
			SSHUser:       "kai",
		},
		Audit: Audit{
			MaxSizeMB:  10,
			MaxBackups: 10,
			MaxAgeDays: 30,
			Compress:   false,
		},
		AWS: AWS{Profile: "default"},
		Eco: Eco{
			ServerDir: "/home/kai/Steam/steamapps/common/EcoServer",
		},
		Factorio: Factorio{
			ServerDir: "/home/kai/Steam/steamapps/common/FactorioServer",
		},
	}
}

// Config is the merged result of the three layered config sources. Loaded
// holds the moment Load returned, so callers can decide whether to refresh.
//
// The schema is the union of every field any current consumer (coily and
// its descendants) needs. Fields not used by a given consumer are zero
// and harmless.
type Config struct {
	KaiServer KaiServer `yaml:"kai_server"`
	Audit     Audit     `yaml:"audit"`
	AWS       AWS       `yaml:"aws"`
	Eco       Eco       `yaml:"eco"`
	Factorio  Factorio  `yaml:"factorio"`
	Loaded    time.Time `yaml:"-"`
}

// Eco is local-side + remote-side config for eco-server operator verbs.
// configs_dir points at a local checkout of the eco-configs repo;
// server_dir is the absolute path to the Eco dedicated server install on
// the host the consumer drives.
type Eco struct {
	ConfigsDir string `yaml:"configs_dir"`
	ServerDir  string `yaml:"server_dir"`
}

// Factorio is the remote-side config for factorio-server operator verbs.
// server_dir is the absolute path to the dedicated server install (the
// directory that contains saves/, mods/, bin/x64/factorio).
type Factorio struct {
	ServerDir string `yaml:"server_dir"`
}

// KaiServer is the connection config for the home server that ssh and
// the gaming/eco/factorio verbs target.
type KaiServer struct {
	TailscaleHost string `yaml:"tailscale_host"`
	SSHUser       string `yaml:"ssh_user"`
	// SSHKeyPath is an optional path to a PEM private key used for
	// ssh/sftp auth. ~ is expanded. When empty, callers fall back to
	// ssh-agent (SSH_AUTH_SOCK). On Windows the MSYS/cygwin agent is
	// not reachable from a Windows-native binary, so an explicit key
	// path is the working path there.
	SSHKeyPath string `yaml:"ssh_key_path"`
}

// Audit controls where the JSONL audit log lives and how lumberjack
// rotates it. LogPath defaults to ~/.coily/audit/<slug>.jsonl when left
// blank.
type Audit struct {
	LogPath    string `yaml:"log_path"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAgeDays int    `yaml:"max_age_days"`
	Compress   bool   `yaml:"compress"`
}

// AWS holds the named profile that an aws pass-through wrapper hands to
// the underlying aws CLI. Empty falls back to the aws CLI's own resolution.
type AWS struct {
	Profile string `yaml:"profile"`
}

// Load returns the layered config. The Go-literal default is the base,
// then ~/.coily/config.yaml is overlaid on top, then ./.coily/config.yaml.
// Any missing layer is silently skipped. The audit log path default is
// filled in from the homedir when the merged config left it blank, and
// any "~/" prefix is expanded to an absolute path.
func Load() (*Config, error) {
	c := defaults()

	globalPath, gerr := GlobalConfigPath()
	if gerr == nil {
		if err := overlayFromFile(&c, globalPath); err != nil {
			return nil, fmt.Errorf("global config %s: %w", globalPath, err)
		}
	}
	localPath, lerr := LocalConfigPath()
	if lerr == nil {
		if err := overlayFromFile(&c, localPath); err != nil {
			return nil, fmt.Errorf("local config %s: %w", localPath, err)
		}
	}

	if err := applyDefaults(&c); err != nil {
		return nil, err
	}
	c.Loaded = time.Now()
	return &c, nil
}

// overlayFromFile reads path (if it exists) and merges the parsed values
// onto dst. yaml.Unmarshal into an existing struct already does
// field-level merge: fields absent from the file keep their previous
// value, fields present overwrite. That is exactly the per-key
// precedence rule we want.
func overlayFromFile(dst *Config, path string) error {
	b, err := os.ReadFile(path) // #nosec G304 -- caller-controlled config path is the intended input
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := yaml.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	return nil
}

// applyDefaults fills in any path field that the embedded + overlay layers
// left blank, and expands ~/ on every path field. Defaults are computed
// from $HOME, so the binary works on a fresh laptop with no on-disk config.
//
// AuditLogEnvVar (COILY_AUDIT_LOG), if set, wins over both file config
// and the per-repo default.
func applyDefaults(c *Config) error {
	if envPath := os.Getenv(AuditLogEnvVar); envPath != "" {
		c.Audit.LogPath = expandHome(envPath)
		return nil
	}
	if c.Audit.LogPath == "" {
		p, err := DefaultAuditPath()
		if err != nil {
			return err
		}
		c.Audit.LogPath = p
	} else {
		c.Audit.LogPath = expandHome(c.Audit.LogPath)
	}
	return nil
}
