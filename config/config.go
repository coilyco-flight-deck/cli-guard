// Package config carries the layered-config primitives shared across
// cli-guard consumers: path helpers, repo-slug derivation, the Audit
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
type Audit struct {
	LogPath    string `yaml:"log_path"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAgeDays int    `yaml:"max_age_days"`
	Compress   bool   `yaml:"compress"`
}

// OverlayFile reads path (if it exists) and yaml-unmarshals onto dst.
// Missing file is not an error: the consumer's prior layer keeps its
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
