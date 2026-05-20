package mcporter_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/coilysiren/cli-guard/mcporter"
)

func TestScanConfig_FindsRefsInNestedStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcporter.json")
	body := `{
	  "mcpServers": {
	    "repo-recall": {
	      "baseUrl": "https://repo-recall.${COILYSIREN_TAILNET_DOMAIN}/mcp"
	    },
	    "luca": {
	      "command": "uv",
	      "env": {
	        "LUCA_REPO_RECALL_URL": "https://repo-recall.${COILYSIREN_TAILNET_DOMAIN}/mcp",
	        "OTHER": "${OTHER_NAME}"
	      }
	    },
	    "staging": {
	      "baseUrl": "http://127.0.0.1:7777/mcp"
	    }
	  }
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := mcporter.ScanConfig(path)
	if err != nil {
		t.Fatalf("ScanConfig: %v", err)
	}
	want := []string{"COILYSIREN_TAILNET_DOMAIN", "OTHER_NAME"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ScanConfig = %v, want %v", got, want)
	}
}

func TestScanConfig_MissingFileReturnsEmpty(t *testing.T) {
	got, err := mcporter.ScanConfig(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("ScanConfig: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ScanConfig on missing file = %v, want empty", got)
	}
}

func TestScanConfig_ParseErrorSurfacesPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := mcporter.ScanConfig(path)
	if err == nil {
		t.Fatal("ScanConfig: want err on bad json, got nil")
	}
	// Error must name the file path so the operator can find it.
	if !contains(err.Error(), path) {
		t.Errorf("err = %q, want to contain path %q", err.Error(), path)
	}
}

func TestScanConfig_NoRefsReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.json")
	if err := os.WriteFile(path, []byte(`{"a":"b","c":["d","e"]}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := mcporter.ScanConfig(path)
	if err != nil {
		t.Fatalf("ScanConfig: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ScanConfig with no refs = %v, want empty", got)
	}
}

func TestResolveAll_HappyPath(t *testing.T) {
	r := mcporter.ResolverFunc(func(name string) (string, error) {
		return "v:" + name, nil
	})
	got, err := mcporter.ResolveAll(r, []string{"A", "B"})
	if err != nil {
		t.Fatalf("ResolveAll: %v", err)
	}
	want := map[string]string{"A": "v:A", "B": "v:B"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveAll = %v, want %v", got, want)
	}
}

func TestResolveAll_FirstErrorNamesVariable(t *testing.T) {
	boom := errors.New("expired sso")
	r := mcporter.ResolverFunc(func(name string) (string, error) {
		if name == "B" {
			return "", boom
		}
		return "v:" + name, nil
	})
	_, err := mcporter.ResolveAll(r, []string{"A", "B", "C"})
	if err == nil {
		t.Fatal("ResolveAll: want err, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want wrap of %v", err, boom)
	}
	if !contains(err.Error(), "B") {
		t.Errorf("err = %q, want to name failing var B", err.Error())
	}
}

func TestConfigPath_OverrideWins(t *testing.T) {
	t.Setenv("MCPORTER_CONFIG", "/etc/somewhere/mcporter.json")
	got, err := mcporter.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	if got != "/etc/somewhere/mcporter.json" {
		t.Errorf("ConfigPath = %q, want override", got)
	}
}

func TestConfigPath_DefaultUnderHome(t *testing.T) {
	t.Setenv("MCPORTER_CONFIG", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir: %v", err)
	}
	got, err := mcporter.ConfigPath()
	if err != nil {
		t.Fatalf("ConfigPath: %v", err)
	}
	want := filepath.Join(home, ".mcporter", "mcporter.json")
	if got != want {
		t.Errorf("ConfigPath = %q, want %q", got, want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
