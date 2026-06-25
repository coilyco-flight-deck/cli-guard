// Package credseed is the typed surface for seeding a child container's
// credentials through a private env-file: the forgejo push/dispatch token plus
// optional base64'd agent credential blobs, one line each.
//
// It extracts the token-seed shaping a credential broker needs - rendering and
// writing the env-file, and the env-var names both writer and reader share -
// without any docker, git, or container-lifecycle knowledge. The broker (or
// ward) writes the file with [Creds.WriteEnvFile] and hands the path to the
// container runtime as an --env-file; the in-container bootstrap reads the same
// env-var names back. Centralizing the names here keeps the two from drifting.
//
// The agent credential blobs ride base64-encoded so multi-line secrets (OAuth
// JSON, an auth.json) survive a flat KEY=VALUE env-file intact.
package credseed

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// Env-var names spanning the seed (writer) and bootstrap (reader) sides. The
// token is plain; the agent blobs are base64'd, marked by the B64 suffix.
const (
	// EnvForgejoToken carries the forgejo push/dispatch token, plain.
	EnvForgejoToken = "FORGEJO_TOKEN" //nolint:gosec // env-var name, not a credential value
	// EnvClaudeCredsB64 carries the base64'd Claude OAuth credential blob.
	EnvClaudeCredsB64 = "WARD_CLAUDE_CREDS_B64"
	// EnvCodexAuthB64 carries the base64'd Codex auth.json blob.
	EnvCodexAuthB64 = "WARD_CODEX_AUTH_B64"
	// EnvGooseOllamaHostB64 carries the base64'd goose ollama host.
	EnvGooseOllamaHostB64 = "WARD_GOOSE_OLLAMA_HOST_B64"
)

// Creds is the secrets seeded into a child container; hold raw values here as
// [Creds.EnvLines] does the base64 shaping. Only ForgejoToken is required.
type Creds struct {
	// ForgejoToken is the forgejo push/dispatch token. Required.
	ForgejoToken string
	// Claude is the raw Claude OAuth credential blob, if seeding Claude.
	Claude string
	// Codex is the raw Codex auth.json blob, if seeding Codex.
	Codex string
	// GooseOllamaHost is the goose ollama host, if seeding goose.
	GooseOllamaHost string
}

// EnvLines renders the env-file lines, stable order: plain token first, then a
// base64'd line per present agent blob. Pure - testable without disk.
func (c Creds) EnvLines() []string {
	lines := []string{EnvForgejoToken + "=" + c.ForgejoToken}
	if c.Claude != "" {
		lines = append(lines, EnvClaudeCredsB64+"="+b64(c.Claude))
	}
	if c.Codex != "" {
		lines = append(lines, EnvCodexAuthB64+"="+b64(c.Codex))
	}
	if c.GooseOllamaHost != "" {
		lines = append(lines, EnvGooseOllamaHostB64+"="+b64(c.GooseOllamaHost))
	}
	return lines
}

// WriteEnvFile writes the creds to a new private (0600) temp env-file, returning
// its path and a cleanup func (always safe to call). See docs/broker.md.
func (c Creds) WriteEnvFile() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "credseed-env-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("credseed: create env-file: %w", err)
	}
	path = f.Name()
	cleanup = func() { _ = os.Remove(path) }
	if cherr := f.Chmod(0o600); cherr != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("credseed: secure env-file: %w", cherr)
	}
	if _, werr := f.WriteString(strings.Join(c.EnvLines(), "\n") + "\n"); werr != nil {
		_ = f.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("credseed: write env-file: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("credseed: close env-file: %w", cerr)
	}
	return path, cleanup, nil
}

// b64 base64-encodes a credential blob for an env-file line.
func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// DecodeBlob reverses the base64 shaping [Creds.EnvLines] applies, for a reader
// decoding a *_B64 value back. Empty input decodes to "" with no error.
func DecodeBlob(b64Value string) (string, error) {
	if b64Value == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(b64Value)
	if err != nil {
		return "", fmt.Errorf("credseed: decode credential blob: %w", err)
	}
	return string(raw), nil
}
