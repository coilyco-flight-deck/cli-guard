// Package audit writes one JSONL record per CLI invocation to an
// append-only log outside the working tree. Per SECURITY.md, the
// audit log is the forensic trail if an agent (or a confused human) invokes
// something destructive.
//
// Rotation is handled by lumberjack: when the active file reaches MaxSizeMB
// it is renamed with a timestamp suffix and a fresh file is started. Old
// backups past MaxBackups or MaxAgeDays are pruned. All files are written
// with 0600 perms. If the target directory does not exist it is created
// with 0700.
//
// Failure mode: per-call Append errors propagate up. The runtime is expected
// to call Preflight at startup so an unwritable audit dir fails loudly there
// instead of being swallowed across hundreds of invocations.
package audit

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Record is one line in the audit log. Timestamp is unix seconds (int64),
// JSON-encoded as a number. Easier to sort and diff than RFC3339 strings.
//
// Decision is "accept" if the verb was allowed to run (regardless of whether
// the underlying tool then succeeded or failed) or "reject" if coily's policy
// blocked the invocation before any subprocess or SDK call. Both outcomes are
// audited; "reject" lets a forensic reader cleanly distinguish a metacharacter
// scrub from an AccessDenied at the AWS edge.
type Record struct {
	// ID is a UUID v7 (time-ordered) populated on Append if unset. Used as
	// the stable identifier in commit trailers (`coily://<ts>/<short>`),
	// where short is the first 8 base32 chars of the raw bytes.
	ID        string   `json:"id,omitempty"`
	Timestamp int64    `json:"ts"`
	Version   string   `json:"version,omitempty"`
	Decision  string   `json:"decision"`
	Verb      string   `json:"verb"`
	Argv      []string `json:"argv"`
	ExitCode  int      `json:"exit_code"`
	Error     string   `json:"error,omitempty"`
	// StderrTail is a bounded last-N-bytes capture of the wrapped tool's
	// stderr, populated by pass-through verbs on non-zero exit so the audit
	// row carries enough context to reconstruct what the tool said. Bare
	// "exit status 1" is the failure mode this fixes (issue #63). Bounded
	// at MaxStderrTailBytes to keep rows small. Empty when the verb did
	// not run a captured subprocess, when exit was zero, or when the
	// subprocess wrote nothing to stderr.
	StderrTail string `json:"stderr_tail,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	// RepoRoot is git rev-parse --show-toplevel of cwd at invocation time,
	// or empty if cwd was not inside a git repo. Forensic only: tells the
	// reader where the operator actually was, independent of CommitScope.
	RepoRoot string `json:"repo_root,omitempty"`
	// CWDSubprocess is os.Getwd() captured by buildBaseRecord at the
	// moment the subprocess saw the world. Differs from CWDAtInvocation
	// when an agent did `cd <repo> && bin ...` to enter a target tree
	// before invoking - the subprocess sees the post-cd path. Always
	// populated.
	CWDSubprocess string `json:"cwd_subprocess,omitempty"`
	// CWDAtInvocation is the consumer-resolved operator cwd, populated
	// from verb.Spec.ResolveInvokeCWD when set. Empty when the consumer
	// has not wired the resolver. When non-empty AND it differs from
	// CWDSubprocess, audit reviewers can flag the drift surfaced in
	// coilysiren/coily#109. Env-var conventions ($COILY_INVOKE_CWD,
	// $OLDPWD) live in the consumer; cli-guard only carries the value.
	CWDAtInvocation string `json:"cwd_at_invocation,omitempty"`
	// CommitScope binds this row to a commit-trailer query. Resolved from
	// --commit-scope (default "auto" = cwd's git toplevel). Empty means the
	// op was deliberately not bound to any commit and will not appear in
	// any trailer.
	CommitScope string `json:"commit_scope,omitempty"`
	// SessionID joins this row to the Claude Code (or other agent harness)
	// session that produced the invocation. Resolution order at write time:
	// (1) context value via WithSessionID, (2) env var CLAUDE_SESSION_ID,
	// (3) empty. Long-lived in-process servers should plumb ctx; spawn-shaped
	// flows can inherit the env var from the parent. Empty when neither
	// source carried a value.
	SessionID string `json:"session_id,omitempty"`
	// AuditOverride is set true when a repo verb ran with
	// --audit-override-dirty: the clean+synced gate refused but the
	// operator forced through. Companion field WorkingTreeStatus carries
	// the porcelain snapshot at refusal time so the run can still be
	// reconstructed after the fact.
	AuditOverride bool `json:"audit_override,omitempty"`
	// WorkingTreeStatus is the truncated `git status --porcelain` output
	// captured when a repo verb ran with --audit-override-dirty. Empty for
	// every other audit row.
	WorkingTreeStatus string `json:"working_tree_status,omitempty"`
	// Egress carries one row per host contacted by the wrapped subprocess
	// when the verb runs through the per-invocation HTTP CONNECT proxy
	// (see pkg/egress). Aggregated by host: a `coily npm install` that
	// opens 200 connections to registry.npmjs.org produces one row, not 200.
	// Empty when the verb did not run through the proxy.
	Egress []EgressRow `json:"egress,omitempty"`
	// ProfileDecision captures the per-session lockdown-profile evaluation
	// for this verb, when a consumer has wired verb.Spec.OnEvaluate. Absent
	// in default-off rows. Phase 4 plumbing for coilysiren/coily#150;
	// phase 5 fills in real per-axis branching.
	ProfileDecision *ProfileDecision `json:"profile_decision,omitempty"`
	// PolicySkipped is true when the shell-metacharacter validator was
	// bypassed for this invocation. Set by consumers whose verb wiring
	// runs with verb.Spec.SkipPolicy = true for a non-paranoid reason
	// (notably a repocfg.Command opted in via allow_metacharacters: true
	// in the committed .coily/coily.yaml). Forensics can still see that
	// the row ran under the relaxed policy even though the argv was
	// accepted. cli-guard#81.
	PolicySkipped bool `json:"policy_skipped,omitempty"`
	// AuditParent is the audit-row ID of an enclosing invocation when this
	// invocation was spawned by another coily process across a host boundary
	// (typically `coily ssh <alias> -- <args>` shipping the local id to the
	// remote via --audit-parent / COILY_AUDIT_PARENT). Forensic walking:
	// forward = search any audit log for rows with audit_parent == <id>;
	// backward = look up the row whose id == this audit_parent. Empty for
	// the common single-host case. coilysiren/coily#187 phase 2.
	AuditParent string `json:"audit_parent,omitempty"`
}

// ProfileDecision is the structured outcome of a per-session
// lockdown-profile evaluation. Allowed=false plus a non-nil verb.Spec
// OnEvaluate error short-circuits the action with PolicyDenied. Profile
// names the active sentinel profile, Source matches the consumer's
// resolver-status vocabulary (e.g. "override" / "missing_file" /
// "unknown_profile" / "unset"), and Coordinate captures the resolved
// tier-per-axis Coordinate so a forensic reader sees exactly which
// tiers were in effect at decision time. The JSON key is
// declared_coordinate to make clear this is the static profile coordinate,
// not observed runtime behavior.
type ProfileDecision struct {
	Allowed    bool       `json:"allowed"`
	Profile    string     `json:"profile,omitempty"`
	Source     string     `json:"source,omitempty"`
	Coordinate Coordinate `json:"declared_coordinate"`
	Reason     string     `json:"reason,omitempty"`
}

// UnmarshalJSON accepts old audit rows that used "coordinate" before the
// static-label field was renamed to "declared_coordinate".
func (p *ProfileDecision) UnmarshalJSON(data []byte) error {
	type profileDecision ProfileDecision
	var wire struct {
		profileDecision
		LegacyCoordinate Coordinate `json:"coordinate,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*p = ProfileDecision(wire.profileDecision)
	if p.Coordinate == (Coordinate{}) {
		p.Coordinate = wire.LegacyCoordinate
	}
	return nil
}

// Coordinate mirrors cli-guard/profile.Coordinate as a JSON-stable
// snapshot. Duplicated here so audit.Record (the wire format) does not
// take a hard dependency on the profile package, and so the JSON tag
// shape is owned by the audit package.
type Coordinate struct {
	DataSecurity    string `json:"data_security,omitempty"`
	BlastRadius     string `json:"blast_radius,omitempty"`
	NetworkEgress   string `json:"network_egress,omitempty"`
	FilesystemReach string `json:"filesystem_reach,omitempty"`
}

// EgressRow is one (parent-invocation, host) pair from the egress proxy.
// Decision is "allow" or "deny"; deny rows are produced when the host fails
// the per-binary allowlist in enforce mode and the underlying CONNECT
// returned 403 instead of being forwarded.
type EgressRow struct {
	Host       string `json:"host"`
	Decision   string `json:"decision"`
	BytesUp    int64  `json:"bytes_up"`
	BytesDown  int64  `json:"bytes_down"`
	DurationMS int64  `json:"duration_ms"`
}

// Egress decision values.
const (
	EgressAllow = "allow"
	EgressDeny  = "deny"
)

// MaxStderrTailBytes caps Record.StderrTail so the audit row stays small
// even when a wrapped tool spews. 2 KiB is enough to carry the last few
// lines of a typical stderr (kubectl error, aws sdk error, gh API error)
// without bloating each line of the JSONL log.
const MaxStderrTailBytes = 2048

// ShortID returns the 8-char base32 prefix of the raw UUID bytes. Used in
// the trailer suffix. Returns empty if ID is unset or unparseable.
func (r Record) ShortID() string {
	return shortIDFromUUID(r.ID)
}

// Trailer returns the canonical Audit-log trailer value for this record:
// `coily://<unix-ts>/<short-id>`. Empty if ID is unset. This is the form
// ParseTrailer round-trips; for the human-readable line that also includes
// the argv summary, use TrailerLine.
func (r Record) Trailer() string {
	short := r.ShortID()
	if short == "" {
		return ""
	}
	return fmt.Sprintf("coily://%d/%s", r.Timestamp, short)
}

// TrailerLine returns the full Audit-log trailer value rendered for a
// commit: `coily://<unix-ts>/<short-id> - <argv summary>`. The argv summary
// is argv[0] (basename) plus any subsequent positional tokens up to but
// not including the first flag-shaped token (anything starting with `-`).
// Flags and their values are dropped, because flag values can be sensitive
// (e.g. an SSM parameter name encoding a GPG key id) and have no business
// in commit history. The full argv is still in the per-scope JSONL audit
// log on disk for forensic recovery; the commit message gets the verb chain
// and nothing more. Empty if ID is unset (matches Trailer's behavior).
func (r Record) TrailerLine() string {
	url := r.Trailer()
	if url == "" {
		return ""
	}
	if len(r.Argv) == 0 {
		return url
	}
	parts := make([]string, len(r.Argv))
	copy(parts, r.Argv)
	parts[0] = filepath.Base(parts[0])
	end := len(parts)
	for i, p := range parts {
		if strings.HasPrefix(p, "-") {
			end = i
			break
		}
	}
	parts = parts[:end]
	if len(parts) == 0 {
		return url
	}
	return url + " - " + strings.Join(parts, " ")
}

// ParseTrailer extracts the unix timestamp and short ID from a coily://
// trailer value. Returns (ts, short, true) on a well-formed input. Accepts
// either the bare URL form (`coily://<ts>/<short>`) or the full TrailerLine
// form with an argv summary appended (`coily://<ts>/<short> - <argv...>`);
// the suffix after ` - ` is informational and is discarded.
func ParseTrailer(s string) (int64, string, bool) {
	const prefix = "coily://"
	if !strings.HasPrefix(s, prefix) {
		return 0, "", false
	}
	rest := s[len(prefix):]
	if i := strings.Index(rest, " - "); i >= 0 {
		rest = rest[:i]
	}
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 || slash == len(rest)-1 {
		return 0, "", false
	}
	var ts int64
	if _, err := fmt.Sscanf(rest[:slash], "%d", &ts); err != nil {
		return 0, "", false
	}
	return ts, rest[slash+1:], true
}

// shortB32 is the base32 alphabet used for short IDs. Standard RFC 4648
// uppercase, no padding. Matches the trailer doc.
var shortB32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func shortIDFromUUID(id string) string {
	raw, err := decodeUUID(id)
	if err != nil {
		return ""
	}
	return shortB32.EncodeToString(raw)[:8]
}

// NewUUIDv7 returns a UUID v7 string (time-ordered) using crypto/rand.
// Same generator that Append uses internally for unset Record.ID;
// exported so callers can pre-allocate an id, embed it in a side
// channel (e.g. --audit-parent on a child process), and then feed it
// back as Spec.IDOverride for the local row. coilysiren/coily#187
// phase 2.
func NewUUIDv7() (string, error) {
	return newUUIDv7(time.Now())
}

// newUUIDv7 returns a UUID v7 string (time-ordered) using crypto/rand.
// 48-bit unix-millis prefix, version=7, variant=10, rest random.
func newUUIDv7(now time.Time) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("audit: rand: %w", err)
	}
	ms := uint64(now.UnixMilli()) //nolint:gosec // UnixMilli fits in 48 bits for the next ~8000 years
	b[0] = byte(ms >> 40 & 0xff)  //nolint:gosec // mask makes overflow inert
	b[1] = byte(ms >> 32 & 0xff)  //nolint:gosec
	b[2] = byte(ms >> 24 & 0xff)  //nolint:gosec
	b[3] = byte(ms >> 16 & 0xff)  //nolint:gosec
	b[4] = byte(ms >> 8 & 0xff)   //nolint:gosec
	b[5] = byte(ms & 0xff)        //nolint:gosec
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func decodeUUID(s string) ([]byte, error) {
	if len(s) != 36 {
		return nil, fmt.Errorf("audit: uuid length %d, want 36", len(s))
	}
	hex := strings.ReplaceAll(s, "-", "")
	if len(hex) != 32 {
		return nil, fmt.Errorf("audit: uuid hyphenation invalid")
	}
	out := make([]byte, 16)
	for i := 0; i < 16; i++ {
		var n int
		if _, err := fmt.Sscanf(hex[2*i:2*i+2], "%02x", &n); err != nil {
			return nil, fmt.Errorf("audit: uuid hex: %w", err)
		}
		out[i] = byte(n & 0xff) //nolint:gosec // %02x bounds n to [0,255]
	}
	return out, nil
}

// Decision values for Record.Decision.
const (
	DecisionAccept = "accept"
	DecisionReject = "reject"
)

// Writer appends records to a JSONL file. The zero value is unusable. Use
// NewWriter or set Path explicitly.
type Writer struct {
	// Path is the JSONL file. Must be set.
	Path string
	// Now is used for timestamps. Tests override. Defaults to time.Now.
	Now func() time.Time
	// MaxSizeMB is the rotation trigger. Zero uses lumberjack's default (100).
	MaxSizeMB int
	// MaxBackups caps the number of rotated files retained. Zero keeps all.
	MaxBackups int
	// MaxAgeDays prunes rotated files older than this. Zero disables.
	MaxAgeDays int
	// Compress gzips rotated files.
	Compress bool

	mu     sync.Mutex
	log    *lumberjack.Logger
	redact RedactPolicy
}

// NewWriter returns a Writer with Now set to time.Now. Rotation fields
// default to zero (lumberjack defaults apply) and can be set by the caller.
func NewWriter(path string) *Writer {
	return &Writer{
		Path: path,
		Now:  time.Now,
	}
}

// ErrPathUnset is returned when Append is called on a Writer with empty Path.
var ErrPathUnset = errors.New("audit: log path not configured")

// Append writes one record as a JSON line. Timestamp is populated from the
// Writer if unset on the Record (zero).
func (w *Writer) Append(r Record) error {
	if w.Path == "" {
		return ErrPathUnset
	}
	now := w.now()
	if r.Timestamp == 0 {
		r.Timestamp = now.Unix()
	}
	if r.ID == "" {
		id, err := newUUIDv7(now)
		if err != nil {
			return err
		}
		r.ID = id
	}

	w.applyRedaction(&r)

	if err := os.MkdirAll(filepath.Dir(w.Path), 0o700); err != nil {
		return fmt.Errorf("audit: mkdir %s: %w", filepath.Dir(w.Path), err)
	}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(&r); err != nil {
		return fmt.Errorf("audit: encode: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.log == nil {
		w.log = &lumberjack.Logger{
			Filename:   w.Path,
			MaxSize:    w.MaxSizeMB,
			MaxBackups: w.MaxBackups,
			MaxAge:     w.MaxAgeDays,
			Compress:   w.Compress,
			LocalTime:  true,
		}
	}
	if _, err := w.log.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("audit: write %s: %w", w.Path, err)
	}
	return nil
}

// Close releases the underlying log file. Safe to call multiple times and
// on a Writer that was never used. Call at process exit if you want to be
// tidy; coily's short-lived CLI model doesn't require it.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.log == nil {
		return nil
	}
	err := w.log.Close()
	w.log = nil
	return err
}

// Wrap records an invocation by running fn and logging the result. base
// supplies caller-set fields (Verb, Argv, RepoRoot, CommitScope, Version);
// Wrap fills in Decision, ExitCode, DurationMS, and Error. The returned
// error is whatever fn returned, unmodified. Audit append failures are
// written to stderr so the operator notices, but they do not mask fn's
// error.
func (w *Writer) Wrap(ctx context.Context, base Record, fn func() error) error {
	return w.WrapHook(ctx, base, fn, nil)
}

// WrapHook is Wrap with an optional onComplete callback. The hook runs
// after fn returns and before the record is appended; it gets a pointer
// to the record so the caller can attach side-channel data (e.g. egress
// rows). Decision/ExitCode/DurationMS/Error are already populated when the
// hook fires.
func (w *Writer) WrapHook(ctx context.Context, base Record, fn func() error, onComplete func(*Record)) error {
	start := w.now()
	err := fn()
	rec := base
	if rec.SessionID == "" {
		rec.SessionID = resolveSessionID(ctx)
	}
	rec.Decision = DecisionAccept
	rec.ExitCode = 0
	rec.DurationMS = w.now().Sub(start).Milliseconds()
	if err != nil {
		rec.ExitCode = 1
		rec.Error = err.Error()
	}
	if onComplete != nil {
		onComplete(&rec)
	}
	if aerr := w.Append(rec); aerr != nil {
		fmt.Fprintf(os.Stderr, "audit: %v\n", aerr)
	}
	return err
}

// Preflight ensures the audit directory exists with 0700 perms and that the
// target path is writable. Call at startup so a broken config blows up
// immediately instead of dropping records silently across the session.
func (w *Writer) Preflight() error {
	if w.Path == "" {
		return ErrPathUnset
	}
	dir := filepath.Dir(w.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("audit: mkdir %s: %w", dir, err)
	}
	// Open in append+create mode to verify the path is writable. Don't write
	// anything; just touch the file so a permission failure surfaces here.
	f, err := os.OpenFile(w.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304 -- caller-controlled audit path
	if err != nil {
		return fmt.Errorf("audit: open %s: %w", w.Path, err)
	}
	return f.Close()
}

func (w *Writer) now() time.Time {
	if w.Now == nil {
		return time.Now()
	}
	return w.Now()
}

// ReadAll decodes every record from r. Useful for tests and for `coily audit
// tail`-style verbs.
func ReadAll(r io.Reader) ([]Record, error) {
	dec := json.NewDecoder(r)
	var out []Record
	for dec.More() {
		var rec Record
		if err := dec.Decode(&rec); err != nil {
			return out, fmt.Errorf("audit: decode: %w", err)
		}
		out = append(out, rec)
	}
	return out, nil
}
