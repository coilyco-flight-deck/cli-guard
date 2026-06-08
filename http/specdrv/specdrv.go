// Package specdrv is the no-code driver behind cmd/specverb-gen: the uv-style
// verb surface (gen / lock / skew / run) over a Guardfile. See docs/specverb.md.
package specdrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/specgen"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/specverb"
)

// Options are the inputs shared by every driver verb.
type Options struct {
	GuardfilePath   string   // path to the consumer's KDL Guardfile
	Out             string   // gen: main.go output path (debug; cache when empty). build: binary output dir or path
	Args            []string // run: arguments passed through to the materialized binary
	CLIGuardRef     string   // lock: cli-guard module query to pin (version/commit); empty = auto
	CLIGuardReplace string   // lock: local cli-guard checkout to replace with (dev locks only)
}

// ErrNoLock is returned by run and skew when a required committed lock is
// absent, so the caller can point the user at `specverb-gen lock`.
var ErrNoLock = errors.New("missing committed lock; run 'specverb-gen lock' first")

// ErrSkew is returned by skew when the committed spec lock drifts from upstream.
var ErrSkew = errors.New("spec skew detected")

// load reads and parses the Guardfile and derives its Params, the common prelude
// of every verb.
func load(opts Options) (*guardfile.Guardfile, specgen.Params, []byte, error) {
	if opts.GuardfilePath == "" {
		return nil, specgen.Params{}, nil, errors.New("specdrv: no guardfile (set --guardfile)")
	}
	gfBytes, err := os.ReadFile(opts.GuardfilePath) //nolint:gosec // operator-supplied policy input
	if err != nil {
		return nil, specgen.Params{}, nil, fmt.Errorf("specdrv: read guardfile: %w", err)
	}
	gf, err := guardfile.Parse(gfBytes)
	if err != nil {
		return nil, specgen.Params{}, nil, fmt.Errorf("specdrv: parse guardfile: %w", err)
	}
	p, err := specgen.Plan(gf, filepath.Base(opts.GuardfilePath))
	if err != nil {
		return nil, specgen.Params{}, nil, err
	}
	return gf, p, gfBytes, nil
}

// Gen renders the consumer main.go. With no --out it writes into the cache and
// prints the path; --out is the debug escape hatch for inspecting the source.
func Gen(opts Options) error {
	gf, p, _, err := load(opts)
	if err != nil {
		return err
	}
	main, err := specgen.Render(gf, p.GuardfileName)
	if err != nil {
		return err
	}
	out := opts.Out
	if out == "" {
		dir, err := cacheDir(opts.GuardfilePath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("specdrv: create cache dir: %w", err)
		}
		out = filepath.Join(dir, "main.go")
	}
	if err := os.WriteFile(out, main, 0o600); err != nil {
		return fmt.Errorf("specdrv: write %s: %w", out, err)
	}
	fmt.Fprintf(os.Stderr, "specverb-gen: wrote %s\n", out)
	return emitReferenceDocFromLock(filepath.Dir(opts.GuardfilePath), gf, p)
}

// emitReferenceDocFromLock writes the reference doc from the committed spec lock,
// or no-ops with a note when the lock is absent (so a pre-lock gen still succeeds).
func emitReferenceDocFromLock(dir string, gf *guardfile.Guardfile, p specgen.Params) error {
	specBytes, err := os.ReadFile(filepath.Join(dir, p.SpecLockName)) //nolint:gosec // committed spec snapshot
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "specverb-gen: skipped reference doc (no spec lock %s; run lock)\n", p.SpecLockName)
			return nil
		}
		return fmt.Errorf("specdrv: read spec lock for reference doc: %w", err)
	}
	return writeReferenceDoc(dir, gf, p, specBytes)
}

// writeReferenceDoc renders Surface.Markdown() beside the Guardfile as <name>.md,
// the committed artifact refreshed alongside main.go and the locks.
func writeReferenceDoc(dir string, gf *guardfile.Guardfile, p specgen.Params, specBytes []byte) error {
	surface, err := specverb.Describe(specverb.Config{Guardfile: gf, Spec: specBytes})
	if err != nil {
		return fmt.Errorf("specdrv: build reference surface: %w", err)
	}
	name := strings.TrimSuffix(p.GuardfileName, filepath.Ext(p.GuardfileName)) + ".md"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(surface.Markdown()), 0o644); err != nil { //nolint:gosec // human-facing committed reference
		return fmt.Errorf("specdrv: write reference doc: %w", err)
	}
	fmt.Fprintf(os.Stderr, "specverb-gen: wrote %s (%d verbs)\n", path, len(surface.Verbs))
	return nil
}

// Lock refreshes both committed locks beside the Guardfile: the spec lock (the
// fetched Swagger) and specverb.lock (the frozen build module). The `uv lock` analog.
func Lock(opts Options) error {
	gf, p, gfBytes, err := load(opts)
	if err != nil {
		return err
	}
	specBytes, err := fetchSpec(p.SpecURL)
	if err != nil {
		return fmt.Errorf("specdrv: fetch spec: %w", err)
	}
	dir := filepath.Dir(opts.GuardfilePath)
	specLockPath := filepath.Join(dir, p.SpecLockName)
	if err := os.WriteFile(specLockPath, specBytes, 0o644); err != nil { //nolint:gosec // committed spec snapshot, not a secret
		return fmt.Errorf("specdrv: write spec lock: %w", err)
	}
	main, err := specgen.Render(gf, p.GuardfileName)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "specverb-lock-")
	if err != nil {
		return fmt.Errorf("specdrv: temp build dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := materializeModuleDir(tmp, p, main, gfBytes, specBytes); err != nil {
		return err
	}
	dl, err := resolveDepLock(tmp, opts.CLIGuardRef, opts.CLIGuardReplace)
	if err != nil {
		return err
	}
	if err := writeDepLock(filepath.Join(dir, LockName), dl); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "specverb-gen: locked %s (%d bytes) + %s (cli-guard %s)\n", p.SpecLockName, len(specBytes), LockName, dl.CLIGuard)
	return writeReferenceDoc(dir, gf, p, specBytes)
}

// Skew reports operation-level drift between the committed spec lock and live
// upstream, never writing. ErrSkew signals drift; a fetch failure is a plain error.
func Skew(opts Options) error {
	_, p, _, err := load(opts)
	if err != nil {
		return err
	}
	dir := filepath.Dir(opts.GuardfilePath)
	committed, err := os.ReadFile(filepath.Join(dir, p.SpecLockName)) //nolint:gosec // committed spec snapshot
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("specdrv: no spec lock %s: %w", p.SpecLockName, ErrNoLock)
		}
		return fmt.Errorf("specdrv: read spec lock: %w", err)
	}
	live, err := fetchSpec(p.SpecURL)
	if err != nil {
		return fmt.Errorf("specdrv: fetch spec: %w", err)
	}
	drift, err := diffSpecs(committed, live)
	if err != nil {
		return err
	}
	if len(drift) > 0 {
		fmt.Fprintf(os.Stderr, "specverb-gen: %d spec change(s) since lock:\n", len(drift))
		for _, d := range drift {
			fmt.Fprintf(os.Stderr, "  %s\n", d)
		}
		return ErrSkew
	}
	fmt.Fprintln(os.Stderr, "specverb-gen: no skew; committed spec lock matches upstream")
	return nil
}

// Run materializes the consumer binary out-of-band (building only when stale)
// and execs it. It refuses to run without committed locks rather than auto-locking.
func Run(opts Options) error {
	binPath, _, err := materialize(opts)
	if err != nil {
		return err
	}
	return execBinary(binPath, opts.Args)
}

// Build materializes the consumer binary out-of-band (same cache + staleness
// path as Run) and copies it to opts.Out instead of execing it. See specverb-driver.md.
func Build(opts Options) error {
	binPath, p, err := materialize(opts)
	if err != nil {
		return err
	}
	dest, err := resolveBuildDest(opts.Out, p.Binary)
	if err != nil {
		return err
	}
	if err := copyExecutable(binPath, dest); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "specverb-gen: built %s\n", dest)
	return nil
}

// materialize is the shared prelude of Run and Build: it builds the consumer
// binary into the cache when stale and returns its path. Refuses without locks.
func materialize(opts Options) (string, specgen.Params, error) {
	gf, p, gfBytes, err := load(opts)
	if err != nil {
		return "", specgen.Params{}, err
	}
	dir := filepath.Dir(opts.GuardfilePath)
	specBytes, err := os.ReadFile(filepath.Join(dir, p.SpecLockName)) //nolint:gosec // committed spec snapshot
	if err != nil {
		if os.IsNotExist(err) {
			return "", p, fmt.Errorf("specdrv: no spec lock %s: %w", p.SpecLockName, ErrNoLock)
		}
		return "", p, fmt.Errorf("specdrv: read spec lock: %w", err)
	}
	depLockPath := filepath.Join(dir, LockName)
	depRaw, err := os.ReadFile(depLockPath) //nolint:gosec // committed dep lock
	if err != nil {
		if os.IsNotExist(err) {
			return "", p, fmt.Errorf("specdrv: no %s: %w", LockName, ErrNoLock)
		}
		return "", p, fmt.Errorf("specdrv: read %s: %w", LockName, err)
	}
	dl, err := readDepLock(depLockPath)
	if err != nil {
		return "", p, err
	}
	cdir, err := cacheDir(opts.GuardfilePath)
	if err != nil {
		return "", p, err
	}
	binPath := filepath.Join(cdir, "bin", p.Binary)
	want := stamp{
		GuardfileHash:    hashBytes(gfBytes),
		SpecLockHash:     hashBytes(specBytes),
		DepLockHash:      hashBytes(depRaw),
		GeneratorVersion: generatorVersion(),
		BuiltAt:          time.Now().UTC().Format(time.RFC3339),
	}
	if err := materializeIfStale(cdir, binPath, gf, p, gfBytes, specBytes, dl, want); err != nil {
		return "", p, err
	}
	return binPath, p, nil
}

// resolveBuildDest turns Build's --out into the binary's destination, following
// go build -o: an existing dir (or trailing separator) takes the binary name.
func resolveBuildDest(out, binary string) (string, error) {
	if out == "" {
		return "", fmt.Errorf("specdrv: build needs an output path (--out)")
	}
	dest := out
	if strings.HasSuffix(out, string(os.PathSeparator)) {
		dest = filepath.Join(out, binary)
	} else if info, err := os.Stat(out); err == nil && info.IsDir() {
		dest = filepath.Join(out, binary)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return "", fmt.Errorf("specdrv: create output dir: %w", err)
	}
	return dest, nil
}

// copyExecutable copies the cached binary to dest via temp file + rename, so an
// older copy running at dest is replaced atomically, not truncated ("text file busy").
func copyExecutable(src, dest string) error {
	in, err := os.Open(src) //nolint:gosec // driver-built cache binary
	if err != nil {
		return fmt.Errorf("specdrv: open built binary: %w", err)
	}
	defer func() { _ = in.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".specverb-build-*")
	if err != nil {
		return fmt.Errorf("specdrv: create temp binary: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("specdrv: copy binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("specdrv: close temp binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil { //nolint:gosec // executable output
		return fmt.Errorf("specdrv: chmod binary: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("specdrv: install binary: %w", err)
	}
	return nil
}

// materializeIfStale rebuilds the binary under the cache lock when its inputs
// changed, releasing the lock before return so Run can exec the fresh image.
func materializeIfStale(cdir, binPath string, gf *guardfile.Guardfile, p specgen.Params, gfBytes, specBytes []byte, dl *DepLock, want stamp) error {
	if err := os.MkdirAll(cdir, 0o750); err != nil {
		return fmt.Errorf("specdrv: create cache dir: %w", err)
	}
	lf, err := os.OpenFile(filepath.Join(cdir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // driver-owned cache dir
	if err != nil {
		return fmt.Errorf("specdrv: open cache lock: %w", err)
	}
	defer func() { _ = lf.Close() }()
	if err := lockFile(lf); err != nil {
		return fmt.Errorf("specdrv: lock cache: %w", err)
	}
	defer func() { _ = unlockFile(lf) }()

	if !stale(cdir, binPath, want) {
		return nil
	}
	main, err := specgen.Render(gf, p.GuardfileName)
	if err != nil {
		return err
	}
	if err := materializeModuleDir(cdir, p, main, gfBytes, specBytes); err != nil {
		return err
	}
	if err := writeModuleFiles(cdir, dl); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(cdir, "bin"), 0o750); err != nil {
		return fmt.Errorf("specdrv: create bin dir: %w", err)
	}
	if err := runGo(cdir, "build", "-mod=readonly", "-o", binPath, "."); err != nil {
		return err
	}
	return writeStamp(cdir, want)
}

// materializeModuleDir writes the build module's inputs into dir: the rendered
// main.go plus the two //go:embed files (Guardfile + spec lock) beside it.
func materializeModuleDir(dir string, p specgen.Params, main, gfBytes, specBytes []byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("specdrv: create module dir: %w", err)
	}
	files := map[string][]byte{
		"main.go":       main,
		p.GuardfileName: gfBytes,
		p.SpecLockName:  specBytes,
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return fmt.Errorf("specdrv: write %s: %w", name, err)
		}
	}
	return nil
}

// fetchSpec GETs the upstream Swagger document, the source for both lock and
// skew.
func fetchSpec(specURL string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(specURL) //nolint:gosec // URL derived from the Guardfile base-url
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s -> %s", specURL, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// diffSpecs reports operation-level drift between two Swagger documents by the
// paths/definitions keys, normalized through JSON to ignore key reordering.
func diffSpecs(committed, live []byte) ([]string, error) {
	c, err := normalizeSpec(committed)
	if err != nil {
		return nil, fmt.Errorf("specdrv: parse committed spec lock: %w", err)
	}
	l, err := normalizeSpec(live)
	if err != nil {
		return nil, fmt.Errorf("specdrv: parse live spec: %w", err)
	}
	var drift []string
	for _, section := range []string{"paths", "definitions"} {
		drift = append(drift, diffSection(section, mapOf(c[section]), mapOf(l[section]))...)
	}
	sort.Strings(drift)
	return drift, nil
}

// normalizeSpec unmarshals a Swagger document into a generic map for structural
// comparison.
func normalizeSpec(b []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// mapOf coerces a decoded JSON value to a string-keyed map, or nil.
func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// diffSection compares one section's keys, emitting "+ key" for additions,
// "- key" for removals, and "~ key" for entries whose canonical JSON changed.
func diffSection(section string, committed, live map[string]any) []string {
	var out []string
	for k := range live {
		if _, ok := committed[k]; !ok {
			out = append(out, fmt.Sprintf("%s: + %s", section, k))
		}
	}
	for k, cv := range committed {
		lv, ok := live[k]
		if !ok {
			out = append(out, fmt.Sprintf("%s: - %s", section, k))
			continue
		}
		if canonical(cv) != canonical(lv) {
			out = append(out, fmt.Sprintf("%s: ~ %s", section, k))
		}
	}
	return out
}

// canonical renders v as a stable JSON string (Go marshals map keys sorted), so
// two structurally equal values compare equal regardless of source ordering.
func canonical(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
