package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "coily.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return p
}

const cleanCatalog = `catalog:
  kind: component
  type: library
  system: cli-guard
  owner: coilyco-flight-deck
  lifecycle: production
  description: A security-boundary framework.
  dependsOn: []
commands:
  build:
    run: make build
`

func TestCheck_Clean(t *testing.T) {
	p := writeConfig(t, cleanCatalog)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("want clean, got %+v", problems)
	}
}

func TestCheck_NonEmptyDependsOn(t *testing.T) {
	body := `catalog:
  kind: component
  type: library
  system: cli-guard
  owner: coilyco-flight-deck
  lifecycle: production
  description: A security-boundary framework.
  dependsOn:
    - repocfg
    - hook
`
	p := writeConfig(t, body)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("want clean, got %+v", problems)
	}
}

func TestCheck_MissingCatalogBlock(t *testing.T) {
	p := writeConfig(t, "commands:\n  build:\n    run: make build\n")
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Msg, "missing top-level `catalog:` block") {
		t.Fatalf("want one missing-block problem, got %+v", problems)
	}
	if problems[0].File != p {
		t.Errorf("File should be the config path, got %q", problems[0].File)
	}
}

func TestCheck_CatalogNotMapping(t *testing.T) {
	p := writeConfig(t, "catalog: just-a-scalar\n")
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Msg, "must be a mapping") {
		t.Fatalf("want catalog-not-mapping problem, got %+v", problems)
	}
}

func TestCheck_MissingRequiredKeys(t *testing.T) {
	body := `catalog:
  kind: component
  dependsOn: []
`
	p := writeConfig(t, body)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	// kind + dependsOn present; the other five required keys are missing.
	if len(problems) != 5 {
		t.Fatalf("want 5 missing-key problems, got %d: %+v", len(problems), problems)
	}
	for _, want := range []string{"type", "system", "owner", "lifecycle", "description"} {
		found := false
		for _, p := range problems {
			if strings.Contains(p.Msg, "\""+want+"\"") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected a problem naming missing key %q, got %+v", want, problems)
		}
	}
}

func TestCheck_DependsOnNotList(t *testing.T) {
	body := `catalog:
  kind: component
  type: library
  system: cli-guard
  owner: coilyco-flight-deck
  lifecycle: production
  description: A security-boundary framework.
  dependsOn: nope
`
	p := writeConfig(t, body)
	problems, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Msg, "must be a list") {
		t.Fatalf("want dependsOn-not-list problem, got %+v", problems)
	}
}

func TestCheck_FirstExistingWins(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "ward.yaml")
	fallback := filepath.Join(dir, "coily.yaml")
	// Only the fallback exists; Check should probe past the missing primary.
	if err := os.WriteFile(fallback, []byte(cleanCatalog), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	problems, err := Check(primary, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("want clean from fallback, got %+v", problems)
	}
}

func TestCheck_PrimaryPreferredOverFallback(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "ward.yaml")
	fallback := filepath.Join(dir, "coily.yaml")
	// Primary is broken, fallback is clean. Primary exists, so it wins and
	// its problem surfaces - the fallback is never consulted.
	if err := os.WriteFile(primary, []byte("commands: {}\n"), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, []byte(cleanCatalog), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	problems, err := Check(primary, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || problems[0].File != primary {
		t.Fatalf("want one problem anchored to the primary, got %+v", problems)
	}
}

func TestCheck_NoConfigFound(t *testing.T) {
	dir := t.TempDir()
	_, err := Check(filepath.Join(dir, "ward.yaml"), filepath.Join(dir, "coily.yaml"))
	if err == nil || !strings.Contains(err.Error(), "no config found") {
		t.Fatalf("want no-config error, got %v", err)
	}
}

func TestCheck_NoCandidates(t *testing.T) {
	_, err := Check()
	if err == nil || !strings.Contains(err.Error(), "no candidate config paths") {
		t.Fatalf("want no-candidates error, got %v", err)
	}
}

func TestCheck_MalformedYAML(t *testing.T) {
	p := writeConfig(t, "catalog: [unterminated\n")
	_, err := Check(p)
	if err == nil || !strings.Contains(err.Error(), "not valid YAML") {
		t.Fatalf("want parse error, got %v", err)
	}
}

func TestCheck_TopLevelNotMapping(t *testing.T) {
	p := writeConfig(t, "- just\n- a\n- list\n")
	_, err := Check(p)
	if err == nil || !strings.Contains(err.Error(), "top level must be a mapping") {
		t.Fatalf("want top-level error, got %v", err)
	}
}
