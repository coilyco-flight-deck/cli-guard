package kdlspecs

import (
	"fmt"
	"os"
	"strings"
)

// readSpecSource loads an operator-vendored API contract. A gzip suffix marks
// encoded input, while plain JSON and YAML remain backward compatible.
func readSpecSource(path string) ([]byte, error) {
	source, err := os.ReadFile(path) //nolint:gosec // operator-vendored spec beside the guardfile
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(path, ".gz") {
		return source, nil
	}
	decoded, err := decodeGzipSpec(source, "vendored spec", maxDecodedSpecBytes)
	if err != nil {
		return nil, fmt.Errorf("decode vendored spec: %w", err)
	}
	return decoded, nil
}
