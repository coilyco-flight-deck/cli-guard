package hook_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/hook"
)

func testInstaller() hook.Installer {
	return hook.Installer{Matcher: "Bash", Command: "ward hook pre-tool-use"}
}

func TestEnsureFromEmpty(t *testing.T) {
	present, merged := testInstaller().Ensure(nil)
	if present {
		t.Error("empty settings should not report present")
	}
	hooks := merged["hooks"].(map[string]any)
	pre := hooks["PreToolUse"].([]any)
	entry := pre[0].(map[string]any)
	if entry["matcher"] != "Bash" {
		t.Errorf("matcher = %v", entry["matcher"])
	}
	inner := entry["hooks"].([]any)[0].(map[string]any)
	if inner["command"] != "ward hook pre-tool-use" || inner["type"] != "command" {
		t.Errorf("inner hook = %v", inner)
	}
}

func TestEnsureIdempotent(t *testing.T) {
	in := testInstaller()
	_, first := in.Ensure(nil)
	present, second := in.Ensure(first)
	if !present {
		t.Error("second Ensure should report present")
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("idempotent Ensure changed the map:\n%v\n%v", first, second)
	}
}

func TestEnsureDoesNotMutateInput(t *testing.T) {
	in := map[string]any{"hooks": map[string]any{"PreToolUse": []any{}}}
	testInstaller().Ensure(in)
	pre := in["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 0 {
		t.Errorf("input mutated: %v", in)
	}
}

func TestEnsurePreservesUnrelated(t *testing.T) {
	in := map[string]any{
		"otherKey": "keep me",
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks":   []any{map[string]any{"type": "command", "command": "some other hook"}},
				},
			},
		},
	}
	present, merged := testInstaller().Ensure(in)
	if present {
		t.Error("different command under same matcher should not read as present")
	}
	if merged["otherKey"] != "keep me" {
		t.Error("unrelated top-level key dropped")
	}
	entry := merged["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	if got := len(entry["hooks"].([]any)); got != 2 {
		t.Errorf("existing Bash hook should be kept beside the new one, got %d", got)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	m, err := hook.LoadSettings(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || len(m) != 0 {
		t.Errorf("missing file = %v,%v", m, err)
	}
}

func TestLoadMalformedErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := hook.LoadSettings(path); err == nil {
		t.Error("malformed JSON should error, not silently overwrite")
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", ".claude", "settings.json")
	_, merged := testInstaller().Ensure(nil)
	data, err := hook.MarshalSettings(merged)
	if err != nil {
		t.Fatal(err)
	}
	if data[len(data)-1] != '\n' {
		t.Error("marshal should end in newline")
	}
	if err := hook.WriteSettings(path, data); err != nil {
		t.Fatalf("WriteSettings: %v", err)
	}
	got, err := hook.LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	present, _ := testInstaller().Ensure(got)
	if !present {
		t.Error("round-tripped settings should report the hook present")
	}
	// Confirm it is valid JSON on disk.
	raw, _ := os.ReadFile(path)
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Errorf("on-disk settings not valid JSON: %v", err)
	}
}

func TestResolveExplicitPath(t *testing.T) {
	got, err := hook.ResolveSettingsPath("rel/settings.json")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("explicit path not made absolute: %q", got)
	}
}

func TestCustomType(t *testing.T) {
	in := hook.Installer{Matcher: "Bash", Command: "x", Type: "custom"}
	_, merged := in.Ensure(nil)
	inner := merged["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if inner["type"] != "custom" {
		t.Errorf("type = %v, want custom", inner["type"])
	}
}
