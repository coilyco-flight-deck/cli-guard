package repocfg_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/coilysiren/cli-guard/repocfg"
)

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, repocfg.Filename)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoad_StringForm(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  test: go test ./...
  lint: golangci-lint run ./...
`)
	cfg, err := repocfg.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Path != path {
		t.Errorf("Path = %q, want %q", cfg.Path, path)
	}
	if got := len(cfg.Commands); got != 2 {
		t.Fatalf("got %d commands, want 2", got)
	}
	// Commands are sorted by name. "lint" < "test".
	if cfg.Commands[0].Name != "lint" || cfg.Commands[1].Name != "test" {
		t.Errorf("order = [%s, %s], want [lint, test]", cfg.Commands[0].Name, cfg.Commands[1].Name)
	}
	want := []string{"go", "test", "./..."}
	got := cfg.Commands[1].Argv
	if len(got) != len(want) {
		t.Fatalf("test argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("test argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoad_MappingForm(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  test:
    run: go test ./...
    description: Run the full unit suite.
`)
	cfg, err := repocfg.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c := cfg.Commands[0]
	if c.Description != "Run the full unit suite." {
		t.Errorf("Description = %q", c.Description)
	}
}

func TestLoad_AuditEgress(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  play:
    run: uv run python bot.py
    audit:
      egress: true
  test: go test ./...
`)
	cfg, err := repocfg.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var play, test repocfg.Command
	for _, c := range cfg.Commands {
		switch c.Name {
		case "play":
			play = c
		case "test":
			test = c
		}
	}
	if !play.Egress {
		t.Errorf("play.Egress = false, want true")
	}
	if test.Egress {
		t.Errorf("test.Egress = true, want false (default for scalar form)")
	}
}

func TestLoad_RejectsShellMetacharacter(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  bad: echo hi; rm -rf /tmp/foo
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Error("Load accepted a command with a shell metacharacter")
	}
}

func TestLoad_AllowMetacharactersOptIn(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  play:
    run: python bot.py --strategy={hunt:true}
    description: Drive the bot with a JSON-shaped strategy flag.
    allow_metacharacters: true
`)
	cfg, err := repocfg.Load(path)
	if err != nil {
		t.Fatalf("Load with allow_metacharacters=true: %v", err)
	}
	if len(cfg.Commands) != 1 {
		t.Fatalf("got %d commands, want 1", len(cfg.Commands))
	}
	c := cfg.Commands[0]
	if !c.AllowMetacharacters {
		t.Errorf("AllowMetacharacters = false, want true")
	}
	want := []string{"python", "bot.py", "--strategy={hunt:true}"}
	if len(c.Argv) != len(want) {
		t.Fatalf("argv = %v, want %v", c.Argv, want)
	}
	for i := range want {
		if c.Argv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, c.Argv[i], want[i])
		}
	}
}

func TestLoad_AllowMetacharactersDefaultsFalse(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  test:
    run: go test ./...
`)
	cfg, err := repocfg.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Commands[0].AllowMetacharacters {
		t.Error("AllowMetacharacters = true on a mapping with no opt-in, want false")
	}
}

func TestLoad_RejectsPipeRedirect(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  bad: cat file | grep foo
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Error("Load accepted a piped command")
	}
}

func TestLoad_RejectsEmptyRun(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  empty: ""
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Error("Load accepted an empty run value")
	}
}

func TestLoad_RejectsIllegalName(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  "--flag": go test
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Error("Load accepted a command name beginning with -")
	}
}

func TestDiscover_FindsInParentOverlay(t *testing.T) {
	// Discover prefers ./.coily/coily.yaml. Place the file under the overlay
	// directory at root and walk from a deep child.
	root := t.TempDir()
	overlay := filepath.Join(root, repocfg.LocalDirName)
	if err := os.MkdirAll(overlay, 0o700); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}
	writeConfig(t, overlay, "commands: {test: go test ./...}\n")
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path, err := repocfg.Discover(deep)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := filepath.Join(overlay, repocfg.Filename)
	// Compare against evaluated symlinks because macOS TempDir returns /var,
	// which resolves to /private/var.
	gotR, _ := filepath.EvalSymlinks(path)
	wantR, _ := filepath.EvalSymlinks(want)
	if gotR != wantR {
		t.Errorf("Discover = %q, want %q", path, want)
	}
}

func TestDiscover_RejectsLegacyRootLocation(t *testing.T) {
	// A coily.yaml at the repo root (no .coily/ overlay) used to be the
	// canonical location. Now it's an error pointing at the new home.
	root := t.TempDir()
	writeConfig(t, root, "commands: {test: go test ./...}\n")
	_, err := repocfg.Discover(root)
	if !errors.Is(err, repocfg.ErrLegacyLocation) {
		t.Errorf("err = %v, want ErrLegacyLocation", err)
	}
}

func TestDiscover_OverlayWinsOverLegacy(t *testing.T) {
	// If both exist (during a partial migration), the overlay takes
	// precedence and the legacy file is ignored.
	root := t.TempDir()
	overlay := filepath.Join(root, repocfg.LocalDirName)
	if err := os.MkdirAll(overlay, 0o700); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}
	writeConfig(t, overlay, "commands: {modern: go version}\n")
	writeConfig(t, root, "commands: {legacy: echo nope}\n")
	path, err := repocfg.Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	want := filepath.Join(overlay, repocfg.Filename)
	gotR, _ := filepath.EvalSymlinks(path)
	wantR, _ := filepath.EvalSymlinks(want)
	if gotR != wantR {
		t.Errorf("Discover = %q, want %q", path, want)
	}
}

func TestDiscover_ReturnsErrNoConfig(t *testing.T) {
	dir := t.TempDir()
	_, err := repocfg.Discover(dir)
	if !errors.Is(err, repocfg.ErrNoConfig) {
		t.Errorf("err = %v, want ErrNoConfig", err)
	}
}

func TestDiscoverChildren_FindsOverlayInChild(t *testing.T) {
	// Layout: /parent/child/.coily/coily.yaml. Discovery from parent finds it.
	parent := t.TempDir()
	childOverlay := filepath.Join(parent, "child", repocfg.LocalDirName)
	if err := os.MkdirAll(childOverlay, 0o700); err != nil {
		t.Fatalf("mkdir child overlay: %v", err)
	}
	writeConfig(t, childOverlay, "commands: {test: go test ./...}\n")
	configs, err := repocfg.DiscoverChildren(parent)
	if err != nil {
		t.Fatalf("DiscoverChildren: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("len(configs) = %d, want 1", len(configs))
	}
	if configs[0].Commands[0].Name != "test" {
		t.Errorf("got %q, want test", configs[0].Commands[0].Name)
	}
}

func TestDiscoverChildren_SkipsLegacyRootForm(t *testing.T) {
	// A legacy /parent/child/coily.yaml (no .coily/ overlay) is intentionally
	// ignored. Child discovery is opt-in via the .coily/ overlay so unrelated
	parent := t.TempDir()
	childRoot := filepath.Join(parent, "legacy-child")
	if err := os.MkdirAll(childRoot, 0o700); err != nil {
		t.Fatalf("mkdir legacy child: %v", err)
	}
	writeConfig(t, childRoot, "commands: {test: go test}\n")
	configs, err := repocfg.DiscoverChildren(parent)
	if err != nil {
		t.Fatalf("DiscoverChildren: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("len(configs) = %d, want 0 (legacy form must be ignored)", len(configs))
	}
}

func TestDiscoverChildren_SkipsHiddenAndUnconfiguredChildren(t *testing.T) {
	// Hidden entries (.git, .vscode) are skipped. Children without a
	// .coily/coily.yaml are skipped. Files at parent level are skipped.
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, ".git"), 0o700); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "no-config"), 0o700); err != nil {
		t.Fatalf("mkdir no-config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parent, "stray-file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	configs, err := repocfg.DiscoverChildren(parent)
	if err != nil {
		t.Fatalf("DiscoverChildren: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("len(configs) = %d, want 0", len(configs))
	}
}

func TestDiscoverChildren_SkipsMalformedChild(t *testing.T) {
	// A child whose coily.yaml fails to parse must not abort the whole scan.
	// The good child is still returned.
	parent := t.TempDir()
	bad := filepath.Join(parent, "bad", repocfg.LocalDirName)
	good := filepath.Join(parent, "good", repocfg.LocalDirName)
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatalf("mkdir bad: %v", err)
	}
	if err := os.MkdirAll(good, 0o700); err != nil {
		t.Fatalf("mkdir good: %v", err)
	}
	writeConfig(t, bad, "commands: {oops: 'echo hi; rm -rf /'}\n")
	writeConfig(t, good, "commands: {test: go test}\n")
	configs, err := repocfg.DiscoverChildren(parent)
	if err != nil {
		t.Fatalf("DiscoverChildren: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("len(configs) = %d, want 1 (bad child must be silently skipped)", len(configs))
	}
	if configs[0].Commands[0].Name != "test" {
		t.Errorf("got %q, want test", configs[0].Commands[0].Name)
	}
}

func TestDiscoverChildren_SortedByPath(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"zebra", "apple", "mango"} {
		dir := filepath.Join(parent, name, repocfg.LocalDirName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		writeConfig(t, dir, "commands: {test: go test}\n")
	}
	configs, err := repocfg.DiscoverChildren(parent)
	if err != nil {
		t.Fatalf("DiscoverChildren: %v", err)
	}
	if len(configs) != 3 {
		t.Fatalf("len(configs) = %d, want 3", len(configs))
	}
	for i := 1; i < len(configs); i++ {
		if configs[i-1].Path >= configs[i].Path {
			t.Errorf("configs not sorted: %s >= %s", configs[i-1].Path, configs[i].Path)
		}
	}
}

func TestLoad_SSHTargets(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  test: go test ./...
ssh:
  targets:
    kai-server:
      user: kai
      host: kai-server.tail09a41b.ts.net
      working_dir: /home/kai/projects/coilysiren
`)
	cfg, err := repocfg.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	tgt, ok := cfg.SSHTargets["kai-server"]
	if !ok {
		t.Fatalf("missing kai-server target; got %v", cfg.SSHTargets)
	}
	if tgt.Name != "kai-server" || tgt.User != "kai" || tgt.Host != "kai-server.tail09a41b.ts.net" || tgt.WorkingDir != "/home/kai/projects/coilysiren" {
		t.Errorf("target = %+v", tgt)
	}
}

func TestLoad_SSHTargets_AbsentWhenBlockMissing(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "commands: {test: go test}\n")
	cfg, err := repocfg.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SSHTargets != nil {
		t.Errorf("SSHTargets = %v, want nil for missing block", cfg.SSHTargets)
	}
}

func TestLoad_SSHTargets_RejectsMissingFields(t *testing.T) {
	cases := map[string]string{
		"missing user":         "ssh: {targets: {kai-server: {host: h, working_dir: /home/kai}}}\n",
		"missing host":         "ssh: {targets: {kai-server: {user: kai, working_dir: /home/kai}}}\n",
		"missing working_dir":  "ssh: {targets: {kai-server: {user: kai, host: h}}}\n",
		"relative working_dir": "ssh: {targets: {kai-server: {user: kai, host: h, working_dir: home/kai}}}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeConfig(t, dir, body)
			if _, err := repocfg.Load(path); err == nil {
				t.Errorf("Load accepted invalid config (%s)", name)
			}
		})
	}
}

func TestLoad_SSHTargets_RejectsShellMetacharacter(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
ssh:
  targets:
    bad:
      user: kai
      host: "h; rm -rf /"
      working_dir: /home/kai
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Error("Load accepted ssh.targets entry with shell metacharacter")
	}
}

func TestLoad_SSHTargets_RejectsBadName(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
ssh:
  targets:
    -bad-name:
      user: kai
      host: h
      working_dir: /home/kai
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Error("Load accepted ssh target with leading-dash name")
	}
}

func TestLoadDefault_UsesEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, "commands: {test: go test}\n")
	t.Setenv(repocfg.EnvOverride, path)
	cfg, err := repocfg.LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if cfg.Commands[0].Name != "test" {
		t.Errorf("got %q, want test", cfg.Commands[0].Name)
	}
}
