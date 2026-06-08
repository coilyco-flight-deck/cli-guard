package allowlist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, yaml, mk string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	yp := filepath.Join(dir, "ward.yaml")
	mp := filepath.Join(dir, "Makefile")
	if err := os.WriteFile(yp, []byte(yaml), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if err := os.WriteFile(mp, []byte(mk), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	return yp, mp
}

const cleanYAML = `commands:
  build:
    run: make build
    description: Build all packages.
`

const cleanMK = "build: ## Build all packages.\n\techo build\n"

func TestLint_Clean(t *testing.T) {
	yp, mp := writeFiles(t, cleanYAML, cleanMK)
	problems, err := Lint(yp, mp)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("want clean, got %+v", problems)
	}
}

func TestLint_RunMismatch(t *testing.T) {
	yaml := `commands:
  build:
    run: bash build.sh
    description: Build all packages.
`
	yp, mp := writeFiles(t, yaml, cleanMK)
	problems, err := Lint(yp, mp)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Msg, ".run = ") {
		t.Fatalf("want one run-mismatch, got %+v", problems)
	}
	if problems[0].File != yp {
		t.Errorf("File should be yamlPath, got %q", problems[0].File)
	}
}

func TestLint_MissingTarget(t *testing.T) {
	yp, mp := writeFiles(t, cleanYAML, "vet: ## Run vet.\n")
	problems, err := Lint(yp, mp)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Msg, "no matching Makefile target") {
		t.Fatalf("want missing-target, got %+v", problems)
	}
}

func TestLint_DescriptionDrift(t *testing.T) {
	yp, mp := writeFiles(t, cleanYAML, "build: ## Compile every package.\n")
	problems, err := Lint(yp, mp)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Msg, "description") {
		t.Fatalf("want description-drift, got %+v", problems)
	}
}

func TestLint_MalformedYAML(t *testing.T) {
	yp, mp := writeFiles(t, "::: not yaml :::", cleanMK)
	_, err := Lint(yp, mp)
	if err == nil {
		t.Fatal("want parse error, got nil")
	}
}

func TestLint_MissingYAML(t *testing.T) {
	dir := t.TempDir()
	mp := filepath.Join(dir, "Makefile")
	if err := os.WriteFile(mp, []byte(cleanMK), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	_, err := Lint(filepath.Join(dir, "missing.yaml"), mp)
	if err == nil {
		t.Fatal("want read error, got nil")
	}
}

func TestLint_MissingMakefile(t *testing.T) {
	dir := t.TempDir()
	yp := filepath.Join(dir, "ward.yaml")
	if err := os.WriteFile(yp, []byte(cleanYAML), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	_, err := Lint(yp, filepath.Join(dir, "Makefile"))
	if err == nil {
		t.Fatal("want read error, got nil")
	}
}

func TestLint_TopLevelMissingCommands(t *testing.T) {
	yp, mp := writeFiles(t, "other: {}\n", cleanMK)
	_, err := Lint(yp, mp)
	if err == nil || !strings.Contains(err.Error(), "commands") {
		t.Fatalf("want missing-commands error, got %v", err)
	}
}
