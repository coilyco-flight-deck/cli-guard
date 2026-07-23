package kdlspecs

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxDecodedSpecLockBytes = 128 << 20

// encodeSpecLock returns a deterministic gzip representation of the pruned JSON
// contract. The zero-value gzip header carries no timestamp or source filename.
func encodeSpecLock(decoded []byte) ([]byte, error) {
	var out bytes.Buffer
	zw, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("create gzip writer: %w", err)
	}
	if _, err := zw.Write(decoded); err != nil {
		_ = zw.Close()
		return nil, fmt.Errorf("compress spec lock: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finish spec lock compression: %w", err)
	}
	return out.Bytes(), nil
}

// decodeSpecLock expands a committed gzip lock into the JSON contract consumed
// by skew, reference generation, cache hashing, and materialization.
func decodeSpecLock(encoded []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("open gzip stream: %w", err)
	}
	decoded, readErr := io.ReadAll(io.LimitReader(zr, maxDecodedSpecLockBytes+1))
	closeErr := zr.Close()
	if readErr != nil {
		return nil, fmt.Errorf("decompress spec lock: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close gzip stream: %w", closeErr)
	}
	if len(decoded) > maxDecodedSpecLockBytes {
		return nil, fmt.Errorf("decoded spec lock exceeds %d bytes", maxDecodedSpecLockBytes)
	}
	return decoded, nil
}

// writeSpecLock stores the generated contract in its machine-owned encoded
// form. Callers retain the decoded JSON for the rest of the lock operation.
func writeSpecLock(path string, decoded []byte) (int, error) {
	encoded, err := encodeSpecLock(decoded)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil { //nolint:gosec // committed generated lock, not a secret
		return 0, err
	}
	legacyPath := strings.TrimSuffix(path, ".gz")
	if legacyPath != path {
		if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
			return 0, fmt.Errorf("remove legacy spec lock: %w", err)
		}
	}
	return len(encoded), nil
}

// readSpecLock prefers the encoded artifact and falls back to the former plain
// JSON filename. The fallback lets existing consumers build before migration.
func readSpecLock(dir string, m member) ([]byte, error) {
	name := m.Params.SpecLockName
	path := filepath.Join(dir, name)
	encoded, err := os.ReadFile(path) //nolint:gosec // committed generated lock
	if err == nil {
		decoded, decodeErr := decodeSpecLock(encoded)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode %s: %w", name, decodeErr)
		}
		return decoded, nil
	}
	if !os.IsNotExist(err) || !strings.HasSuffix(name, ".gz") {
		return nil, err
	}

	legacyName := strings.TrimSuffix(name, ".gz")
	legacy, legacyErr := os.ReadFile(filepath.Join(dir, legacyName)) //nolint:gosec // legacy committed lock
	if legacyErr == nil {
		return legacy, nil
	}
	if os.IsNotExist(legacyErr) {
		return nil, err
	}
	return nil, legacyErr
}
