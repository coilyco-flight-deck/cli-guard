package repocfg_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/repocfg"
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

func TestDiscoverChildren_FindsGrandchildOverlay(t *testing.T) {
	// Layout: /parent/group/repo/.coily/coily.yaml. The grouping dir has no
	// config of its own; discovery from parent must still reach the repo.
	parent := t.TempDir()
	grandchildOverlay := filepath.Join(parent, "group", "repo", repocfg.LocalDirName)
	if err := os.MkdirAll(grandchildOverlay, 0o700); err != nil {
		t.Fatalf("mkdir grandchild overlay: %v", err)
	}
	writeConfig(t, grandchildOverlay, "commands: {test: go test ./...}\n")
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

func TestDiscoverChildren_FindsChildAndGrandchildTogether(t *testing.T) {
	// A direct-child repo and a grandchild repo under a grouping directory are
	// both returned in one deterministic, path-sorted pool.
	parent := t.TempDir()
	child := filepath.Join(parent, "child", repocfg.LocalDirName)
	grandchild := filepath.Join(parent, "group", "repo", repocfg.LocalDirName)
	for _, dir := range []string{child, grandchild} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		writeConfig(t, dir, "commands: {test: go test}\n")
	}
	configs, err := repocfg.DiscoverChildren(parent)
	if err != nil {
		t.Fatalf("DiscoverChildren: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("len(configs) = %d, want 2", len(configs))
	}
}

func TestDiscoverChildren_RejectsDepth3(t *testing.T) {
	// A config three levels below the parent is past the default depth-2 reach
	// and must not be discovered, keeping recursion bounded.
	parent := t.TempDir()
	tooDeep := filepath.Join(parent, "a", "b", "c", repocfg.LocalDirName)
	if err := os.MkdirAll(tooDeep, 0o700); err != nil {
		t.Fatalf("mkdir too-deep overlay: %v", err)
	}
	writeConfig(t, tooDeep, "commands: {test: go test}\n")
	configs, err := repocfg.DiscoverChildren(parent)
	if err != nil {
		t.Fatalf("DiscoverChildren: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("len(configs) = %d, want 0 (depth-3 config is out of reach)", len(configs))
	}
}

func TestDiscoverChildrenDepth_HonorsExplicitDepth(t *testing.T) {
	// Depth-1 sees only the direct child; depth-2 also reaches the grandchild;
	// depth-3 reaches all three.
	parent := t.TempDir()
	for _, dir := range []string{
		filepath.Join(parent, "lvl1", repocfg.LocalDirName),
		filepath.Join(parent, "lvl1", "lvl2", repocfg.LocalDirName),
		filepath.Join(parent, "lvl1", "lvl2", "lvl3", repocfg.LocalDirName),
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		writeConfig(t, dir, "commands: {test: go test}\n")
	}
	for _, tc := range []struct {
		depth int
		want  int
	}{
		{depth: 0, want: 0},
		{depth: 1, want: 1},
		{depth: 2, want: 2},
		{depth: 3, want: 3},
	} {
		configs, err := repocfg.DiscoverChildrenDepth(parent, tc.depth)
		if err != nil {
			t.Fatalf("DiscoverChildrenDepth(depth=%d): %v", tc.depth, err)
		}
		if len(configs) != tc.want {
			t.Errorf("depth=%d: len(configs) = %d, want %d", tc.depth, len(configs), tc.want)
		}
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

func TestLoad_NoSecuritySection(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  test: go test ./...
`)
	cfg, err := repocfg.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Security.ProtectedBinaries) != 0 {
		t.Errorf("ProtectedBinaries = %v, want empty", cfg.Security.ProtectedBinaries)
	}
	if cfg.Security.Sudo.ForbidPasswordless {
		t.Error("ForbidPasswordless = true, want false on a config with no security:")
	}
	if cfg.Security.Hooks.RouteHints != nil {
		t.Errorf("RouteHints = %v, want nil", cfg.Security.Hooks.RouteHints)
	}
}

func TestLoad_SecurityFull(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  build: make build
security:
  protected_binaries:
    - name: gcloud
      mode: deny-direct
      allowed_wrappers: [kap, ward]
      expected_real_paths:
        - /opt/homebrew/bin/gcloud
        - /usr/local/bin/gcloud
      credential_env: [CLOUDSDK_CONFIG, GOOGLE_APPLICATION_CREDENTIALS]
    - name: clusterctl
      allowed_wrappers: [kap]
  sudo:
    forbid_passwordless: true
  hooks:
    deny_bare_binaries: [gcloud, clusterctl]
    route_hints:
      gcloud: "Use kap for cloud operations."
`)
	cfg, err := repocfg.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// commands: still parses alongside security:.
	if len(cfg.Commands) != 1 || cfg.Commands[0].Name != "build" {
		t.Fatalf("Commands = %+v, want one build command", cfg.Commands)
	}
	pbs := cfg.Security.ProtectedBinaries
	if len(pbs) != 2 {
		t.Fatalf("got %d protected binaries, want 2", len(pbs))
	}
	if pbs[0].Name != "gcloud" || pbs[0].EffectiveMode() != repocfg.ModeDenyDirect {
		t.Errorf("pb[0] = %+v, want gcloud/deny-direct", pbs[0])
	}
	if len(pbs[0].ExpectedRealPaths) != 2 || pbs[0].ExpectedRealPaths[0] != "/opt/homebrew/bin/gcloud" {
		t.Errorf("pb[0].ExpectedRealPaths = %v", pbs[0].ExpectedRealPaths)
	}
	// Empty mode defaults to deny-direct via EffectiveMode.
	if pbs[1].Mode != "" || pbs[1].EffectiveMode() != repocfg.ModeDenyDirect {
		t.Errorf("pb[1] mode = %q, EffectiveMode = %q", pbs[1].Mode, pbs[1].EffectiveMode())
	}
	if !cfg.Security.Sudo.ForbidPasswordless {
		t.Error("ForbidPasswordless = false, want true")
	}
	if got := cfg.Security.Hooks.RouteHints["gcloud"]; got != "Use kap for cloud operations." {
		t.Errorf("route hint = %q", got)
	}
	if len(cfg.Security.Hooks.DenyBareBinaries) != 2 {
		t.Errorf("DenyBareBinaries = %v", cfg.Security.Hooks.DenyBareBinaries)
	}
}

func TestLoad_SecurityRejectsPathName(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
security:
  protected_binaries:
    - name: /opt/homebrew/bin/gcloud
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Fatal("Load: want error for path-shaped name, got nil")
	}
}

func TestLoad_SecurityRejectsUnknownMode(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
security:
  protected_binaries:
    - name: gcloud
      mode: read-only
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Fatal("Load: want error for unsupported mode, got nil")
	}
}

func TestLoad_SecurityRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
security:
  protected_binaries:
    - name: gcloud
    - name: gcloud
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Fatal("Load: want error for duplicate name, got nil")
	}
}

func TestLoad_ForbiddenArgvRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
commands:
  build: make build
security:
  forbidden_argv:
    - description: "gh writes"
      matches_glob_any:
        - "gh * create*"
        - "gh * delete*"
      hint: "use the relevant issue tracker, not gh."
    - description: "substring deny"
      matches_glob_any:
        - "gh *secret*"
`)
	cfg, err := repocfg.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	fa := cfg.Security.ForbiddenArgv
	if len(fa) != 2 {
		t.Fatalf("got %d forbidden_argv rules, want 2", len(fa))
	}
	if fa[0].Description != "gh writes" || fa[0].Hint != "use the relevant issue tracker, not gh." {
		t.Errorf("fa[0] = %+v", fa[0])
	}
	if len(fa[0].MatchesGlobAny) != 2 || fa[0].MatchesGlobAny[0] != "gh * create*" {
		t.Errorf("fa[0].MatchesGlobAny = %v", fa[0].MatchesGlobAny)
	}
	// Optional hint omitted leaves an empty string for the engine to synthesize.
	if fa[1].Hint != "" {
		t.Errorf("fa[1].Hint = %q, want empty", fa[1].Hint)
	}
}

func TestLoad_ForbiddenArgvRejectsMissingDescription(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
security:
  forbidden_argv:
    - matches_glob_any: ["gh * create*"]
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Fatal("Load: want error for missing description, got nil")
	}
}

func TestLoad_ForbiddenArgvRejectsEmptyGlobs(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
security:
  forbidden_argv:
    - description: "no globs"
      matches_glob_any: []
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Fatal("Load: want error for empty matches_glob_any, got nil")
	}
}

func TestLoad_ForbiddenArgvRejectsBadGlob(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `
security:
  forbidden_argv:
    - description: "malformed glob"
      matches_glob_any:
        - "gh [unterminated"
`)
	if _, err := repocfg.Load(path); err == nil {
		t.Fatal("Load: want error for invalid glob syntax, got nil")
	}
}
