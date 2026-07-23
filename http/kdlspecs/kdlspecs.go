// Package kdlspecs is the no-code driver behind cmd/specgen: the uv-style
// verb surface (gen / lock / skew / run) over a Guardfile. See docs/specverb.md.
package kdlspecs

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

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/execverb"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/guardfile"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/kdlspecs/codegen"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/specverb"
	kdl "github.com/calico32/kdl-go"
)

// Options are the inputs shared by every driver verb.
type Options struct {
	GuardfilePath   string   // path to the consumer's KDL Guardfile
	ProjectRoot     string   // explicit recursive KDL project boundary (empty keeps legacy directory discovery)
	BinaryName      string   // gen/build/run: generated CLI/binary name (empty = Guardfile wrap binary)
	Out             string   // gen: main.go output path (debug; cache when empty). build: binary output dir or path
	Args            []string // run: arguments passed through to the materialized binary
	CLIGuardRef     string   // lock: cli-guard module query to pin (version/commit); empty = auto
	CLIGuardReplace string   // lock: local cli-guard checkout to replace with (dev locks only)
	Version         string   // build: release version stamped into the binary via -ldflags (empty = "dev")
}

// ErrNoLock is returned by run and skew when a required committed lock is
// absent, so the caller can point the user at `specgen lock`.
var ErrNoLock = errors.New("missing committed lock; run 'specgen lock' first")

// ErrSkew is returned by skew when the committed spec lock drifts from upstream.
var ErrSkew = errors.New("spec skew detected")

// member is one guardfile in a merged build. A spec member carries GF (with a
// spec lock + doc); an exec member carries ExecGF (policy only). See driver doc.
type member struct {
	// Path is the slash-normalized path relative to group.Dir. It is the
	// member's stable identity; never use an absolute path in generated inputs.
	Path string
	// SourcePath is the validated on-disk path used only while loading source.
	SourcePath string
	GF         *guardfile.Guardfile // spec dialect; nil for exec members
	ExecGF     *execverb.Guardfile  // exec dialect; nil for spec members
	Params     codegen.Params
	Bytes      []byte
}

// isExec reports whether the member speaks the exec dialect.
func (m member) isExec() bool { return m.Params.Transport == codegen.TransportExec }

// group is the operation members that compose one merged binary.
type group struct {
	Dir           string
	Binary        string
	RuntimeBinary string
	Members       []member // sorted by Path for a deterministic embed/build order
}

// runtimeBinary is the generated app/file name. It defaults to the source
// Guardfile wrap binary while letting a consumer publish it under another name.
func (g *group) runtimeBinary() string {
	if g.RuntimeBinary != "" {
		return g.RuntimeBinary
	}
	return g.Binary
}

// normalizeBinaryName checks a caller-supplied generated binary name before it
// becomes both a cli.Command name and a cached file path.
func normalizeBinaryName(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if strings.TrimSpace(name) != name {
		return "", fmt.Errorf("specgen: --binary must not have leading or trailing whitespace")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("specgen: --binary must be a binary name, not a path")
	}
	return name, nil
}

func newGroup(dir, selector string, members []member, binaryName string) (*group, error) {
	runtimeBinary, err := normalizeBinaryName(binaryName)
	if err != nil {
		return nil, err
	}
	if runtimeBinary == "" {
		runtimeBinary = selector
	}
	return &group{Dir: dir, Binary: selector, RuntimeBinary: runtimeBinary, Members: members}, nil
}

// sniffTransport reads a guardfile's dialect: an `exec` child of the `wrap`
// block is exec, otherwise spec. Lets the driver pick the right parser.
func sniffTransport(src []byte) (string, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return "", fmt.Errorf("specgen: parse KDL: %w", err)
	}
	wrap := doc.GetNode("wrap")
	if wrap == nil {
		return "", fmt.Errorf("specgen: missing top-level `wrap` node")
	}
	for _, n := range wrap.Children().Nodes {
		if n.Name() == "exec" {
			return codegen.TransportExec, nil
		}
	}
	return codegen.TransportSpec, nil
}

// readMember reads a single guardfile, sniffs its transport, and parses+plans
// it with the matching dialect.
func readMember(path, identity string) (member, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-supplied policy input
	if err != nil {
		return member{}, fmt.Errorf("specgen: read guardfile: %w", err)
	}
	transport, err := sniffTransport(b)
	if err != nil {
		return member{}, fmt.Errorf("specgen: sniff %s: %w", path, err)
	}
	if transport == codegen.TransportExec {
		egf, err := execverb.Parse(b)
		if err != nil {
			return member{}, fmt.Errorf("specgen: parse exec guardfile %s: %w", path, err)
		}
		p, err := codegen.PlanExec(egf.Group, egf.Providers(), identity)
		if err != nil {
			return member{}, err
		}
		return member{Path: identity, SourcePath: path, ExecGF: egf, Params: p, Bytes: b}, nil
	}
	// Resolve `inherit` into one self-contained document before the typed parse,
	// so every downstream stage sees the merged grant set (docs/specverb-inherit.md).
	flat, err := guardfile.Flatten(path)
	if err != nil {
		return member{}, fmt.Errorf("specgen: resolve guardfile %s: %w", path, err)
	}
	gf, err := guardfile.Parse(flat)
	if err != nil {
		return member{}, fmt.Errorf("specgen: parse guardfile %s: %w", path, err)
	}
	p, err := codegen.Plan(gf, identity)
	if err != nil {
		return member{}, err
	}
	// A lock stays beside its member's root-relative source identity. This keeps
	// identically named members in separate folders from sharing an artifact.
	p.SpecLockName = filepath.ToSlash(filepath.Join(filepath.Dir(identity), p.SpecLockName))
	return member{Path: identity, SourcePath: path, GF: gf, Params: p, Bytes: flat}, nil
}

// operationIntent reports whether malformed KDL is clearly an operation member.
// Such source fails rather than being silently skipped as unrelated project KDL.
func operationIntent(src []byte) bool {
	for _, line := range strings.Split(string(src), "\n") {
		line = strings.TrimSpace(line)
		if line == "wrap" || strings.HasPrefix(line, "wrap ") || strings.HasPrefix(line, "wrap{") {
			return true
		}
	}
	return false
}

func projectRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("specgen: resolve project root: %w", err)
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("specgen: resolve project root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("specgen: stat project root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("specgen: project root %s is not a directory", path)
	}
	return root, nil
}

func memberIdentity(root, path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("specgen: resolve member %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("specgen: member %s escapes project root %s", path, root)
	}
	return filepath.ToSlash(rel), nil
}

// discoverProjectMembers recursively finds operation KDL in root.
// Parsed KDL without wrap is unrelated; malformed source mentioning wrap fails.
func discoverProjectMembers(root string) ([]member, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("specgen: walk project: %w", err)
		}
		if d.Type()&os.ModeSymlink != 0 {
			if _, err := memberIdentity(root, path); err != nil {
				return err
			}
		}
		if d.IsDir() || filepath.Ext(path) != ".kdl" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var members []member
	seenSource := map[string]string{}
	for _, path := range paths {
		m, found, err := projectMember(root, path, seenSource)
		if err != nil {
			return nil, err
		}
		if found {
			members = append(members, m)
		}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Path < members[j].Path })
	return members, nil
}

func projectMember(root, path string, seenSource map[string]string) (member, bool, error) {
	identity, err := memberIdentity(root, path)
	if err != nil {
		return member{}, false, err
	}
	if prior, ok := seenSource[identity]; ok {
		return member{}, false, fmt.Errorf("specgen: duplicate logical member %s (%s and %s)", identity, prior, path)
	}
	seenSource[identity] = path
	src, err := os.ReadFile(path) //nolint:gosec // operator-supplied policy input
	if err != nil {
		return member{}, false, fmt.Errorf("specgen: read member %s: %w", identity, err)
	}
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		if operationIntent(src) {
			return member{}, false, fmt.Errorf("specgen: parse intended operation member %s: %w", identity, err)
		}
		return member{}, false, nil
	}
	if doc.GetNode("wrap") == nil {
		return member{}, false, nil
	}
	m, err := readMember(path, identity)
	return m, err == nil, err
}

func legacyMembers(dir, selected string) ([]member, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.guardfile.kdl"))
	if err != nil {
		return nil, fmt.Errorf("specgen: discover guardfiles: %w", err)
	}
	if selected != "" {
		found := false
		for _, p := range matches {
			if p == selected {
				found = true
			}
		}
		if !found {
			matches = append(matches, selected)
		}
	}
	sort.Strings(matches)
	var members []member
	for _, path := range matches {
		m, err := readMember(path, filepath.Base(path))
		if err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, nil
}

func validateArtifacts(members []member) error {
	seenLocks := map[string]string{}
	for _, m := range members {
		if m.isExec() {
			continue
		}
		if prior, ok := seenLocks[m.Params.SpecLockName]; ok {
			return fmt.Errorf("specgen: conflicting spec lock %s for %s and %s", m.Params.SpecLockName, prior, m.Path)
		}
		seenLocks[m.Params.SpecLockName] = m.Path
	}
	return nil
}

// loadGroup discovers the operation members that make up one merged binary.
// --project-root is recursive and content-driven; absent it, legacy discovery remains.
func loadGroup(opts Options) (*group, error) {
	dir, selector, members, err := discover(opts)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		if opts.ProjectRoot != "" {
			return nil, fmt.Errorf("specgen: no operation KDL members in project root %s", dir)
		}
		return nil, errors.New("specgen: no *.guardfile.kdl in cwd (set --guardfile or --project-root)")
	}
	byBinary := map[string][]member{}
	order := []string{}
	for _, mem := range members {
		if _, seen := byBinary[mem.Params.Binary]; !seen {
			order = append(order, mem.Params.Binary)
		}
		byBinary[mem.Params.Binary] = append(byBinary[mem.Params.Binary], mem)
	}
	if selector == "" {
		if len(byBinary) != 1 {
			sort.Strings(order)
			return nil, fmt.Errorf("specgen: %d binaries in %s (%s); pass --guardfile to pick one", len(byBinary), dir, strings.Join(order, ", "))
		}
		selector = order[0]
	}
	members, ok := byBinary[selector]
	if !ok {
		return nil, fmt.Errorf("specgen: no guardfile for binary %q in %s", selector, dir)
	}
	if err := validateArtifacts(members); err != nil {
		return nil, err
	}
	return newGroup(dir, selector, members, opts.BinaryName)
}

func discover(opts Options) (string, string, []member, error) {
	if opts.ProjectRoot == "" {
		return discoverLegacy(opts.GuardfilePath)
	}
	return discoverRoot(opts.ProjectRoot, opts.GuardfilePath)
}

func discoverLegacy(selected string) (string, string, []member, error) {
	dir := "."
	selector := ""
	if selected != "" {
		dir = filepath.Dir(selected)
		sel, err := readMember(selected, filepath.Base(selected))
		if err != nil {
			return "", "", nil, err
		}
		selector = sel.Params.Binary
	}
	members, err := legacyMembers(dir, selected)
	return dir, selector, members, err
}

func discoverRoot(root, selected string) (string, string, []member, error) {
	dir, err := projectRoot(root)
	if err != nil {
		return "", "", nil, err
	}
	selector, err := selectedBinary(dir, selected)
	if err != nil {
		return "", "", nil, err
	}
	members, err := discoverProjectMembers(dir)
	return dir, selector, members, err
}

func selectedBinary(root, selected string) (string, error) {
	if selected == "" {
		return "", nil
	}
	identity, err := memberIdentity(root, selected)
	if err != nil {
		return "", err
	}
	sel, err := readMember(selected, identity)
	if err != nil {
		return "", err
	}
	return sel.Params.Binary, nil
}

// render emits the merged main.go from the members' pre-planned params, mixing
// spec and exec mounts onto the one binary.
func (g *group) render() ([]byte, error) {
	sp := codegen.SetParams{Binary: g.runtimeBinary()}
	for _, m := range g.Members {
		sp.Mounts = append(sp.Mounts, m.Params)
		if m.isExec() {
			sp.HasExec = true
		} else {
			sp.HasSpec = true
		}
	}
	return codegen.RenderParams(sp)
}

// orderedSpecs returns the spec-member spec bytes in member order, the
// deterministic input to the staleness hash. Exec members contribute nothing.
func orderedSpecs(mems []member, byPath map[string][]byte) [][]byte {
	var out [][]byte
	for _, m := range mems {
		if b, ok := byPath[m.Path]; ok {
			out = append(out, b)
		}
	}
	return out
}

// Gen renders the merged consumer main.go (cache, or --out for inspection) and
// refreshes each member's reference doc from its committed spec lock.
func Gen(opts Options) error {
	g, err := loadGroup(opts)
	if err != nil {
		return err
	}
	main, err := g.render()
	if err != nil {
		return err
	}
	out := opts.Out
	if out == "" {
		dir := cacheDirForGroup(g)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("specgen: create cache dir: %w", err)
		}
		out = filepath.Join(dir, "main.go")
	}
	if err := os.WriteFile(out, main, 0o600); err != nil {
		return fmt.Errorf("specgen: write %s: %w", out, err)
	}
	fmt.Fprintf(os.Stderr, "specgen: wrote %s\n", out)
	for _, m := range g.Members {
		if m.isExec() {
			if err := emitExecReferenceDoc(g.Dir, m); err != nil {
				return err
			}
			continue
		}
		if err := emitReferenceDocFromLock(g.Dir, m); err != nil {
			return err
		}
	}
	return nil
}

// refDocName is the committed reference-doc filename for a member: the
// guardfile name with its extension swapped for .md.
func refDocName(m member) string {
	return strings.TrimSuffix(m.Params.GuardfileName, filepath.Ext(m.Params.GuardfileName)) + ".md"
}

// emitExecReferenceDoc writes an exec member's reference doc from its parsed
// policy (execverb.Describe), since it has no upstream spec to render from.
func emitExecReferenceDoc(dir string, m member) error {
	surface := execverb.Describe(m.ExecGF)
	path := filepath.Join(dir, refDocName(m))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("specgen: create reference doc dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(surface.Markdown()), 0o644); err != nil { //nolint:gosec // human-facing committed reference
		return fmt.Errorf("specgen: write exec reference doc: %w", err)
	}
	fmt.Fprintf(os.Stderr, "specgen: wrote %s (%d grants)\n", path, len(surface.Grants))
	return nil
}

// emitReferenceDocFromLock writes one member's reference doc from its committed
// spec lock, or no-ops with a note when the lock is absent (pre-lock gen).
func emitReferenceDocFromLock(dir string, m member) error {
	specBytes, err := os.ReadFile(filepath.Join(dir, m.Params.SpecLockName)) //nolint:gosec // committed spec snapshot
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "specgen: skipped reference doc (no spec lock %s; run lock)\n", m.Params.SpecLockName)
			return nil
		}
		return fmt.Errorf("specgen: read spec lock for reference doc: %w", err)
	}
	return writeReferenceDoc(dir, m, specBytes)
}

// writeReferenceDoc renders Surface.Markdown() beside the member's Guardfile as
// <name>.md, the committed artifact refreshed alongside main.go and the locks.
func writeReferenceDoc(dir string, m member, specBytes []byte) error {
	surface, err := specverb.Describe(specverb.Config{Guardfile: m.GF, Spec: specBytes})
	if err != nil {
		return fmt.Errorf("specgen: build reference surface: %w", err)
	}
	path := filepath.Join(dir, refDocName(m))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("specgen: create reference doc dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(surface.Markdown()), 0o644); err != nil { //nolint:gosec // human-facing committed reference
		return fmt.Errorf("specgen: write reference doc: %w", err)
	}
	fmt.Fprintf(os.Stderr, "specgen: wrote %s (%d verbs)\n", path, len(surface.Verbs))
	return nil
}

// lockSpecs fetches each spec member's upstream spec, prunes it, writes the
// per-member lock, and returns the pruned bytes by path. Exec members skipped.
func lockSpecs(g *group) (map[string][]byte, error) {
	specs := map[string][]byte{}
	for _, m := range g.Members {
		if m.isExec() {
			continue
		}
		full, err := loadFullSpec(m)
		if err != nil {
			return nil, fmt.Errorf("specgen: load spec %s: %w", m.Params.GuardfileName, err)
		}
		// Commit only the granted slice, not the full upstream dump: the lock
		// becomes the consumer's own contract. See docs/specgen.md.
		specBytes, err := specverb.Prune(full, m.GF)
		if err != nil {
			return nil, fmt.Errorf("specgen: prune spec %s: %w", m.Params.GuardfileName, err)
		}
		specLockPath := filepath.Join(g.Dir, m.Params.SpecLockName)
		if err := os.MkdirAll(filepath.Dir(specLockPath), 0o750); err != nil {
			return nil, fmt.Errorf("specgen: create spec lock dir: %w", err)
		}
		if err := os.WriteFile(specLockPath, specBytes, 0o644); err != nil { //nolint:gosec // committed spec snapshot, not a secret
			return nil, fmt.Errorf("specgen: write spec lock: %w", err)
		}
		specs[m.Path] = specBytes
		fmt.Fprintf(os.Stderr, "specgen: locked %s (%d bytes, pruned from %d)\n", m.Params.SpecLockName, len(specBytes), len(full))
	}
	return specs, nil
}

// loadFullSpec returns the member's full upstream spec: a spec vendored beside
// the guardfile is read directly, else fetched from the derived URL.
func loadFullSpec(m member) ([]byte, error) {
	if m.GF != nil && m.GF.Spec != "" {
		local := filepath.Join(filepath.Dir(m.SourcePath), m.GF.Spec)
		if b, rerr := os.ReadFile(local); rerr == nil { //nolint:gosec // operator-vendored spec beside the guardfile
			fmt.Fprintf(os.Stderr, "specgen: read vendored spec %s\n", m.GF.Spec)
			return b, nil
		}
	}
	return fetchSpec(m.Params.SpecURL)
}

// Lock refreshes both committed locks: each member's pruned spec lock and the
// one specverb.lock (the frozen build module). The `uv lock` analog.
func Lock(opts Options) error {
	g, err := loadGroup(opts)
	if err != nil {
		return err
	}
	specs, err := lockSpecs(g)
	if err != nil {
		return err
	}
	// One merged dep lock for the whole binary - the module graph is the union.
	main, err := g.render()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "specverb-lock-")
	if err != nil {
		return fmt.Errorf("specgen: temp build dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := materializeModuleDir(tmp, main, g.Members, specs); err != nil {
		return err
	}
	dl, err := resolveDepLock(tmp, opts.CLIGuardRef, opts.CLIGuardReplace)
	if err != nil {
		return err
	}
	if err := writeDepLock(filepath.Join(g.Dir, LockName), dl); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "specgen: locked %s (cli-guard %s)\n", LockName, dl.CLIGuard)
	for _, m := range g.Members {
		if m.isExec() {
			if err := emitExecReferenceDoc(g.Dir, m); err != nil {
				return err
			}
			continue
		}
		if err := writeReferenceDoc(g.Dir, m, specs[m.Path]); err != nil {
			return err
		}
	}
	return nil
}

// Skew reports operation-level drift between the committed spec lock and live
// upstream, never writing. ErrSkew signals drift; a fetch failure is a plain error.
func Skew(opts Options) error {
	g, err := loadGroup(opts)
	if err != nil {
		return err
	}
	var drift []string
	for _, m := range g.Members {
		if m.isExec() {
			continue // exec members have no upstream spec to drift against
		}
		committed, err := os.ReadFile(filepath.Join(g.Dir, m.Params.SpecLockName)) //nolint:gosec // committed spec snapshot
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("specgen: no spec lock %s: %w", m.Params.SpecLockName, ErrNoLock)
			}
			return fmt.Errorf("specgen: read spec lock: %w", err)
		}
		live, err := fetchSpec(m.Params.SpecURL)
		if err != nil {
			return fmt.Errorf("specgen: fetch spec %s: %w", m.Params.GuardfileName, err)
		}
		// Prune live to the same granted slice the committed lock holds, so
		// drift is reported only for operations this consumer exposes.
		livePruned, err := specverb.Prune(live, m.GF)
		if err != nil {
			return fmt.Errorf("specgen: prune live spec %s: %w", m.Params.GuardfileName, err)
		}
		d, err := diffSpecs(committed, livePruned)
		if err != nil {
			return err
		}
		// Prefix each line with the member so a merged binary's drift is
		// attributable to the API that moved.
		for _, line := range d {
			drift = append(drift, m.Params.GuardfileName+": "+line)
		}
	}
	if len(drift) > 0 {
		fmt.Fprintf(os.Stderr, "specgen: %d spec change(s) since lock:\n", len(drift))
		for _, d := range drift {
			fmt.Fprintf(os.Stderr, "  %s\n", d)
		}
		return ErrSkew
	}
	fmt.Fprintln(os.Stderr, "specgen: no skew; committed spec locks match upstream")
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
// path as Run) and copies it to opts.Out instead of execing it. See specgen.md.
func Build(opts Options) error {
	binPath, g, err := materialize(opts)
	if err != nil {
		return err
	}
	dest, err := resolveBuildDest(opts.Out, g.runtimeBinary())
	if err != nil {
		return err
	}
	if err := copyExecutable(binPath, dest); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "specgen: built %s\n", dest)
	return nil
}

// materialize is the shared prelude of Run and Build: it builds the merged
// binary into the cache when stale and returns its path. Refuses without locks.
func materialize(opts Options) (string, *group, error) {
	g, err := loadGroup(opts)
	if err != nil {
		return "", nil, err
	}
	specByPath := map[string][]byte{}
	for _, m := range g.Members {
		if m.isExec() {
			continue
		}
		specBytes, err := os.ReadFile(filepath.Join(g.Dir, m.Params.SpecLockName)) //nolint:gosec // committed spec snapshot
		if err != nil {
			if os.IsNotExist(err) {
				return "", g, fmt.Errorf("specgen: no spec lock %s: %w", m.Params.SpecLockName, ErrNoLock)
			}
			return "", g, fmt.Errorf("specgen: read spec lock: %w", err)
		}
		specByPath[m.Path] = specBytes
	}
	depLockPath := filepath.Join(g.Dir, LockName)
	depRaw, err := os.ReadFile(depLockPath) //nolint:gosec // committed dep lock
	if err != nil {
		if os.IsNotExist(err) {
			return "", g, fmt.Errorf("specgen: no %s: %w", LockName, ErrNoLock)
		}
		return "", g, fmt.Errorf("specgen: read %s: %w", LockName, err)
	}
	dl, err := readDepLock(depLockPath)
	if err != nil {
		return "", g, err
	}
	cdir := cacheDirForGroup(g)
	main, err := g.render()
	if err != nil {
		return "", g, err
	}
	binPath := filepath.Join(cdir, "bin", g.runtimeBinary())
	want := stamp{
		GuardfileHash:    hashMembers(g.Members),
		SpecLockHash:     hashConcat(orderedSpecs(g.Members, specByPath)...),
		DepLockHash:      hashBytes(depRaw),
		GeneratorVersion: DriverVersion(),
		LDVersion:        opts.Version,
		BuiltAt:          time.Now().UTC().Format(time.RFC3339),
	}
	if err := materializeIfStale(cdir, binPath, main, g.Members, specByPath, dl, want); err != nil {
		return "", g, err
	}
	return binPath, g, nil
}

// hashMembers combines the raw guardfile bytes of a group (members are
// pre-sorted by path) into one staleness hash.
func hashMembers(mems []member) string {
	bss := make([][]byte, 0, len(mems)*2)
	for _, m := range mems {
		bss = append(bss, []byte(m.Path+"\x00"), m.Bytes)
	}
	return hashConcat(bss...)
}

// resolveBuildDest turns Build's --out into the binary's destination, following
// go build -o: an existing dir (or trailing separator) takes the binary name.
func resolveBuildDest(out, binary string) (string, error) {
	if out == "" {
		return "", fmt.Errorf("specgen: build needs an output path (--out)")
	}
	dest := out
	if strings.HasSuffix(out, string(os.PathSeparator)) {
		dest = filepath.Join(out, binary)
	} else if info, err := os.Stat(out); err == nil && info.IsDir() {
		dest = filepath.Join(out, binary)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return "", fmt.Errorf("specgen: create output dir: %w", err)
	}
	return dest, nil
}

// copyExecutable copies the cached binary to dest via temp file + rename, so an
// older copy running at dest is replaced atomically, not truncated ("text file busy").
func copyExecutable(src, dest string) error {
	in, err := os.Open(src) //nolint:gosec // driver-built cache binary
	if err != nil {
		return fmt.Errorf("specgen: open built binary: %w", err)
	}
	defer func() { _ = in.Close() }()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".specverb-build-*")
	if err != nil {
		return fmt.Errorf("specgen: create temp binary: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("specgen: copy binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("specgen: close temp binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil { //nolint:gosec // executable output
		return fmt.Errorf("specgen: chmod binary: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("specgen: install binary: %w", err)
	}
	return nil
}

// materializeIfStale rebuilds the binary under the cache lock when its inputs
// changed, releasing the lock before return so Run can exec the fresh image.
func materializeIfStale(cdir, binPath string, main []byte, mems []member, specByPath map[string][]byte, dl *DepLock, want stamp) error {
	if err := os.MkdirAll(cdir, 0o750); err != nil {
		return fmt.Errorf("specgen: create cache dir: %w", err)
	}
	lf, err := os.OpenFile(filepath.Join(cdir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // driver-owned cache dir
	if err != nil {
		return fmt.Errorf("specgen: open cache lock: %w", err)
	}
	defer func() { _ = lf.Close() }()
	if err := lockFile(lf); err != nil {
		return fmt.Errorf("specgen: lock cache: %w", err)
	}
	defer func() { _ = unlockFile(lf) }()

	if !stale(cdir, binPath, want) {
		return nil
	}
	if err := materializeModuleDir(cdir, main, mems, specByPath); err != nil {
		return err
	}
	if err := writeModuleFiles(cdir, dl); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(cdir, "bin"), 0o750); err != nil {
		return fmt.Errorf("specgen: create bin dir: %w", err)
	}
	args := []string{"build", "-mod=readonly"}
	// Stamp the consumer's release version into main.Version, mirroring the way
	// ward's own formula sets it. Absent (dev build), the template's "dev" stands.
	if want.LDVersion != "" {
		args = append(args, "-ldflags", "-X main.Version="+want.LDVersion)
	}
	args = append(args, "-o", binPath, ".")
	if err := runGo(cdir, args...); err != nil {
		return err
	}
	return writeStamp(cdir, want)
}

// materializeModuleDir writes the build inputs into dir: the rendered main.go
// plus each member's embeds (guardfile always, spec lock for spec members).
func materializeModuleDir(dir string, main []byte, mems []member, specByPath map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("specgen: create module dir: %w", err)
	}
	files := map[string][]byte{"main.go": main}
	for _, m := range mems {
		files[m.Params.GuardfileName] = m.Bytes
		if !m.isExec() {
			files[m.Params.SpecLockName] = specByPath[m.Path]
		}
	}
	for name, b := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("specgen: create embed dir: %w", err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			return fmt.Errorf("specgen: write %s: %w", name, err)
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
		return nil, fmt.Errorf("specgen: parse committed spec lock: %w", err)
	}
	l, err := normalizeSpec(live)
	if err != nil {
		return nil, fmt.Errorf("specgen: parse live spec: %w", err)
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
