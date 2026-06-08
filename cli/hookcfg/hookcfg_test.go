package hookcfg

import (
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/repocfg"
)

func TestProtectedFor_Empty(t *testing.T) {
	if got := ProtectedFor(repocfg.Security{}); got != nil {
		t.Fatalf("empty Security -> nil, got %+v", got)
	}
}

func TestProtectedFor_ProtectedBinariesMapped(t *testing.T) {
	sec := repocfg.Security{
		ProtectedBinaries: []repocfg.ProtectedBinary{
			{Name: "gcloud", AllowedWrappers: []string{"kap"}},
		},
		Hooks: repocfg.HookPolicy{
			RouteHints: map[string]string{"gcloud": "Use kap for cloud operations."},
		},
	}
	got := ProtectedFor(sec)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d: %+v", len(got), got)
	}
	if got[0].Name != "gcloud" || got[0].Hint != "Use kap for cloud operations." {
		t.Errorf("wrong mapping: %+v", got[0])
	}
	if len(got[0].Wrappers) != 1 || got[0].Wrappers[0] != "kap" {
		t.Errorf("wrappers not threaded: %+v", got[0].Wrappers)
	}
}

func TestProtectedFor_DenyBareSuppliesHintOnlyEntries(t *testing.T) {
	sec := repocfg.Security{
		Hooks: repocfg.HookPolicy{
			DenyBareBinaries: []string{"terraform"},
			RouteHints:       map[string]string{"terraform": "use `coily tf ...`."},
		},
	}
	got := ProtectedFor(sec)
	if len(got) != 1 || got[0].Name != "terraform" || got[0].Hint != "use `coily tf ...`." {
		t.Fatalf("want one hint-only terraform entry, got %+v", got)
	}
	if len(got[0].Wrappers) != 0 {
		t.Errorf("hint-only entry should have no wrappers: %+v", got[0])
	}
}

func TestProtectedFor_DenyBareSkipsCoveredNames(t *testing.T) {
	sec := repocfg.Security{
		ProtectedBinaries: []repocfg.ProtectedBinary{{Name: "gcloud"}},
		Hooks:             repocfg.HookPolicy{DenyBareBinaries: []string{"gcloud"}},
	}
	got := ProtectedFor(sec)
	if len(got) != 1 {
		t.Fatalf("want 1 (no dup), got %d: %+v", len(got), got)
	}
}

func TestProtectedFor_SkipsEmptyNames(t *testing.T) {
	sec := repocfg.Security{
		ProtectedBinaries: []repocfg.ProtectedBinary{{Name: ""}, {Name: "gcloud"}},
		Hooks:             repocfg.HookPolicy{DenyBareBinaries: []string{"", "terraform"}},
	}
	got := ProtectedFor(sec)
	if len(got) != 2 {
		t.Fatalf("want 2 (skipping empties), got %d: %+v", len(got), got)
	}
}
