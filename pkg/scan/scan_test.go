package scan_test

import (
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/scan"
)

func TestDiffVendored(t *testing.T) {
	got := scan.Diff([]scan.Entry{
		{Path: "node_modules/left-pad/index.js", Bytes: 10},
		{Path: "src/app/vendor/lib.go", Bytes: 10},
		{Path: "src/main.go", Bytes: 10},
	})
	if len(got) != 2 {
		t.Fatalf("want 2 findings, got %d: %+v", len(got), got)
	}
	if got[0].Path != "node_modules/left-pad/index.js" || got[1].Path != "src/app/vendor/lib.go" {
		t.Errorf("unexpected findings: %+v", got)
	}
}

func TestDiffSecrets(t *testing.T) {
	cases := []struct {
		path string
		flag bool
	}{
		{".env", true},
		{"config/.env.production", true},
		{".env.example", false},
		{".env.sample", false},
		{"deploy/id_rsa", true},
		{"certs/server.pem", true},
		{"certs/server.key", true},
		{"src/main.go", false},
		{"keystore.jks", true},
	}
	for _, c := range cases {
		got := scan.Diff([]scan.Entry{{Path: c.path, Bytes: 10}})
		if (len(got) == 1) != c.flag {
			t.Errorf("Diff(%q) flagged=%v, want %v (%+v)", c.path, len(got) == 1, c.flag, got)
		}
	}
}

func TestDiffSizes(t *testing.T) {
	got := scan.Diff([]scan.Entry{
		{Path: "small.txt", Bytes: 100},
		{Path: "big.txt", Bytes: scan.OversizedBlobBytes},
		{Path: "small.bin", Bytes: scan.BinaryBlobBytes, Binary: true},
		{Path: "text-under-bin-bar.txt", Bytes: scan.BinaryBlobBytes}, // text, under 5 MiB
	})
	if len(got) != 2 {
		t.Fatalf("want 2 size findings, got %d: %+v", len(got), got)
	}
	if got[0].Path != "big.txt" || got[1].Path != "small.bin" {
		t.Errorf("unexpected size findings: %+v", got)
	}
}

func TestDiffFirstMatchWins(t *testing.T) {
	// A path that is both vendored and oversized reports the vendored reason.
	got := scan.Diff([]scan.Entry{{Path: "vendor/blob.bin", Bytes: scan.OversizedBlobBytes, Binary: true}})
	if len(got) != 1 || got[0].Reason != "vendored/generated tree (vendor/)" {
		t.Errorf("first-match-wins broken: %+v", got)
	}
}

func TestDiffClean(t *testing.T) {
	if got := scan.Diff([]scan.Entry{{Path: "src/main.go", Bytes: 200}}); len(got) != 0 {
		t.Errorf("clean diff should yield no findings, got %+v", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		512:        "512 B",
		2048:       "2.0 KiB",
		5 << 20:    "5.0 MiB",
		1536 << 10: "1.5 MiB",
	}
	for n, want := range cases {
		if got := scan.HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
