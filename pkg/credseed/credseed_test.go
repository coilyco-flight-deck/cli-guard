package credseed

import (
	"bufio"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestEnvLinesTokenOnly(t *testing.T) {
	got := Creds{ForgejoToken: "tok"}.EnvLines()
	want := []string{"FORGEJO_TOKEN=tok"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEnvLinesAllBlobsBase64dInOrder(t *testing.T) {
	got := Creds{
		ForgejoToken:    "tok",
		Claude:          "claude-secret",
		Codex:           "codex-secret",
		GooseOllamaHost: "http://ollama",
	}.EnvLines()

	if len(got) != 4 {
		t.Fatalf("want 4 lines, got %d: %v", len(got), got)
	}
	if got[0] != "FORGEJO_TOKEN=tok" {
		t.Fatalf("token line first, got %q", got[0])
	}
	// The blobs must be base64'd, never raw, so multi-line secrets survive.
	for _, line := range got[1:] {
		if strings.Contains(line, "secret") || strings.Contains(line, "ollama") {
			t.Fatalf("blob leaked un-encoded: %q", line)
		}
	}
	// Round-trip the Claude blob back through the shared decoder.
	claudeVal := valueOf(t, got, EnvClaudeCredsB64)
	decoded, err := DecodeBlob(claudeVal)
	if err != nil {
		t.Fatalf("DecodeBlob: %v", err)
	}
	if decoded != "claude-secret" {
		t.Fatalf("round-trip mismatch: %q", decoded)
	}
}

func TestEnvLinesOmitsEmptyBlobs(t *testing.T) {
	got := Creds{ForgejoToken: "tok", Codex: "x"}.EnvLines()
	for _, line := range got {
		if strings.HasPrefix(line, EnvClaudeCredsB64) || strings.HasPrefix(line, EnvGooseOllamaHostB64) {
			t.Fatalf("absent blob emitted: %q", line)
		}
	}
}

func TestWriteEnvFile(t *testing.T) {
	c := Creds{ForgejoToken: "tok", Claude: "claude-secret"}
	path, cleanup, err := c.WriteEnvFile()
	if err != nil {
		t.Fatalf("WriteEnvFile: %v", err)
	}
	defer cleanup()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("want 0600 env-file, got %o", perm)
	}

	lines := readLines(t, path)
	if !reflect.DeepEqual(lines, c.EnvLines()) {
		t.Fatalf("file contents diverge from EnvLines:\n got %v\nwant %v", lines, c.EnvLines())
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cleanup did not remove file: %v", err)
	}
}

func TestDecodeBlobEmpty(t *testing.T) {
	got, err := DecodeBlob("")
	if err != nil || got != "" {
		t.Fatalf("empty decode: got %q err %v", got, err)
	}
}

func TestDecodeBlobInvalid(t *testing.T) {
	if _, err := DecodeBlob("not!base64!"); err == nil {
		t.Fatal("want error on invalid base64")
	}
}

// valueOf returns the value of the KEY=VALUE line with the given key.
func valueOf(t *testing.T, lines []string, key string) string {
	t.Helper()
	for _, line := range lines {
		if k, v, ok := strings.Cut(line, "="); ok && k == key {
			return v
		}
	}
	t.Fatalf("key %q not found in %v", key, lines)
	return ""
}

// readLines returns the non-empty lines of the file at path.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			out = append(out, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}
