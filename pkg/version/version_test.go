package version_test

import (
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/version"
)

func TestLooksReleased(t *testing.T) {
	cases := map[string]bool{
		"v1.2.3": true,
		"1.2.3":  true,
		"dev":    false,
		"":       false,
		"   ":    false,
		" v0.1 ": true,
	}
	for in, want := range cases {
		if got := version.LooksReleased(in); got != want {
			t.Errorf("LooksReleased(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in    string
		want  [3]int
		wantO bool
	}{
		{"v1.2.3", [3]int{1, 2, 3}, true},
		{"1.2.3", [3]int{1, 2, 3}, true},
		{"v0.5.2-rc1", [3]int{0, 5, 2}, true},
		{"v0.5.2+build.7", [3]int{0, 5, 2}, true},
		{"v1.2", [3]int{1, 2, 0}, true},
		{"v1", [3]int{1, 0, 0}, true},
		{"v1.2.3.4", [3]int{1, 2, 3}, true},
		{" v2.0.0 ", [3]int{2, 0, 0}, true},
		{"", [3]int{}, false},
		{"v", [3]int{}, false},
		{"vX.2.3", [3]int{}, false},
		{"v1.-2.3", [3]int{}, false},
	}
	for _, c := range cases {
		got, ok := version.Parse(c.in)
		if ok != c.wantO || got != c.want {
			t.Errorf("Parse(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.wantO)
		}
	}
}

func TestBehind(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.0", "v1.1.0", true},
		{"v1.0.0", "v2.0.0", true},
		{"v1.0.1", "v1.0.0", false}, // ahead
		{"v1.0.0", "v1.0.0", false}, // equal
		{"dev", "v9.9.9", false},    // dev never nags
		{"", "v1.0.0", false},
		{"v1.0.0", "garbage", false}, // unparseable latest
		{"garbage", "v1.0.0", false}, // unparseable current
		{"v0.5.2-rc1", "v0.5.2", false},
		{"v0.5.1", "v0.5.2-rc1", true},
	}
	for _, c := range cases {
		if got := version.Behind(c.current, c.latest); got != c.want {
			t.Errorf("Behind(%q,%q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}
