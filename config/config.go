// Package config carries the layered-config primitives shared across
// cli-guard consumers: path helpers, repo-slug derivation, the Audit
// rotation knobs, ExpandHome, and a generic OverlayFile helper.
//
// The consumer owns its own schema. cli-guard does not. Wire your
// schema struct through OverlayFile twice (global then local) and you
// get the same default-then-global-then-local precedence rule that
// every cli-guard consumer uses today.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// Audit controls where the JSONL audit log lives and how lumberjack
// rotates it. LogPath defaults to ~/.coily/audit/<slug>.jsonl when left
// blank; callers fill that in via DefaultAuditPath after the overlay
// passes if they want the per-repo behavior.
type Audit struct {
	LogPath    string `yaml:"log_path"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAgeDays int    `yaml:"max_age_days"`
	Compress   bool   `yaml:"compress"`
}

// OverlayFile reads path (if it exists) and yaml-unmarshals onto dst.
// Missing file is not an error: the consumer's prior layer keeps its
// values. yaml.Unmarshal into an existing struct already does field-level
// merge - fields absent from the file keep their previous value, fields
// present overwrite. That is the per-key precedence rule consumers want.
//
// Generic over the consumer's schema. Call once per layer (global, then
// local) to assemble a layered config.
func OverlayFile[T any](dst *T, path string) error {
	b, err := os.ReadFile(path) // #nosec G304 -- caller-controlled config path is the intended input
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := yaml.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	}
	return nil
}
