// Package embedfile materializes specgen-embedded build inputs for one guarded
// process lifetime.
package embedfile

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Source is one generated binary asset. Path is the project-relative embed
// identity and Data is its compile-time content.
type Source struct {
	Path string
	Data []byte
}

// Materialize writes sources beneath one private absolute temporary directory.
// The first map key identifies an exec member and the second is the
// guardfile-relative source reference that execverb resolves.
func Materialize(prefix string, sources map[int]map[string]Source) (map[int]map[string]string, func() error, error) {
	root, err := os.MkdirTemp("", prefix+"-embedded-*")
	if err != nil {
		return nil, nil, fmt.Errorf("materialize embedded files: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(root) }
	absRoot, err := filepath.Abs(root)
	if err != nil {
		_ = cleanup()
		return nil, nil, fmt.Errorf("resolve embedded-file directory: %w", err)
	}
	root = absRoot
	cleanup = func() error { return os.RemoveAll(root) }

	resolved := map[int]map[string]string{}
	written := map[string]string{}
	for member, files := range sources {
		resolved[member] = map[string]string{}
		for source, embedded := range files {
			if err := validatePath(embedded.Path); err != nil {
				_ = cleanup()
				return nil, nil, fmt.Errorf("materialize embedded file %s: %w", source, err)
			}
			dest := filepath.Join(root, filepath.FromSlash(embedded.Path))
			if prior, exists := written[dest]; exists {
				_ = cleanup()
				return nil, nil, fmt.Errorf("materialize embedded file %s: destination conflicts with %s", source, prior)
			}
			written[dest] = source
			if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
				_ = cleanup()
				return nil, nil, fmt.Errorf("materialize embedded file %s: %w", source, err)
			}
			if err := os.WriteFile(dest, embedded.Data, 0o600); err != nil {
				_ = cleanup()
				return nil, nil, fmt.Errorf("materialize embedded file %s: %w", source, err)
			}
			resolved[member][source] = dest
		}
	}
	return resolved, cleanup, nil
}

func validatePath(name string) error {
	if name == "" || path.IsAbs(name) || strings.Contains(name, "\\") || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return fmt.Errorf("invalid generated path %q", name)
	}
	return nil
}
