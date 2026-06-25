package hook

import (
	"os"
	"path/filepath"
)

// Guard describes one detectable guard: its binary Name, the config Marker
// that identifies a governed tree, and the Route / IntegrityRule tables.
type Guard struct {
	// Name is the guard's binary name, e.g. "ward". Detect returns it and
	// PreToolUse stamps it as the deny-message source label.
	Name string
	// Marker is the repo-root-relative config file marking a governed tree,
	// e.g. ".ward/ward.yaml". Detect walks up from a directory looking for it.
	Marker string
	// Routes is the bare-token recovery table for this guard.
	Routes []Route
	// Integrity is the guard-binary path allow-list for this guard.
	Integrity []IntegrityRule
}

// Registry holds the guards a hook can detect plus a Default name for when
// no marker is found. Guards are checked in registration order (first wins).
type Registry struct {
	// Default is the guard name Detect returns when no marker is found, and
	// the fallback Guard lookup uses for an unknown name.
	Default string
	// Guards is the registered guard set, checked in order.
	Guards []Guard
}

// Detect walks up from start and returns the name of the first guard whose
// marker is found. An empty start, an unresolvable path, or no marker: Default.
func (r Registry) Detect(start string) string {
	if start == "" {
		return r.Default
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return r.Default
	}
	for {
		for _, g := range r.Guards {
			if g.Marker != "" && fileExists(filepath.Join(dir, g.Marker)) {
				return g.Name
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return r.Default
		}
		dir = parent
	}
}

// Guard returns the registered guard for name, falling back to Default; ok
// is false only when neither name nor Default resolves.
func (r Registry) Guard(name string) (Guard, bool) {
	if g, ok := r.find(name); ok {
		return g, true
	}
	return r.find(r.Default)
}

// find returns the registered guard with exactly this name.
func (r Registry) find(name string) (Guard, bool) {
	for _, g := range r.Guards {
		if g.Name == name {
			return g, true
		}
	}
	return Guard{}, false
}

// fileExists reports whether p is an existing regular file (not a directory).
func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
