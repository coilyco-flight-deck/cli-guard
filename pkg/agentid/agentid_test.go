package agentid_test

import (
	_ "embed"
	"encoding/json"
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/agentid"
)

// vectorsJSON is the shared cross-language contract, vendored byte-for-byte from
// aos. TestVectorContract reproduces every seed->id, so any drift fails CI.

//go:embed agent_id_vectors.json
var vectorsJSON []byte

type vectorFile struct {
	IDLetters   string `json:"id_letters"`
	IDDigits    string `json:"id_digits"`
	IDLen       int    `json:"id_len"`
	IDLetterLen int    `json:"id_letter_len"`
	Vectors     []struct {
		Seed string `json:"seed"`
		ID   string `json:"id"`
	} `json:"vectors"`
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	var vf vectorFile
	if err := json.Unmarshal(vectorsJSON, &vf); err != nil {
		t.Fatalf("unmarshal agent_id_vectors.json: %v", err)
	}
	return vf
}

// TestVectorContract is the drift guard: the vendored vector's alphabet and
// every seed->id must match this port exactly, or CI fails.
func TestVectorContract(t *testing.T) {
	vf := loadVectors(t)

	if vf.IDLetters != agentid.Letters {
		t.Errorf("vector id_letters = %q, port Letters = %q", vf.IDLetters, agentid.Letters)
	}
	if vf.IDDigits != agentid.Digits {
		t.Errorf("vector id_digits = %q, port Digits = %q", vf.IDDigits, agentid.Digits)
	}
	if vf.IDLen != agentid.Len {
		t.Errorf("vector id_len = %d, port Len = %d", vf.IDLen, agentid.Len)
	}
	if vf.IDLetterLen != agentid.LetterLen {
		t.Errorf("vector id_letter_len = %d, port LetterLen = %d", vf.IDLetterLen, agentid.LetterLen)
	}
	if len(vf.Vectors) == 0 {
		t.Fatal("no vectors loaded")
	}
	for _, v := range vf.Vectors {
		if got := agentid.SeededID(v.Seed); got != v.ID {
			t.Errorf("SeededID(%q) = %q, want %q", v.Seed, got, v.ID)
		}
		if !agentid.IsValid(v.ID) {
			t.Errorf("vector id %q from seed %q is not IsValid", v.ID, v.Seed)
		}
	}
}

func TestSeededIDDeterministic(t *testing.T) {
	want := agentid.SeededID("kai-server")
	for i := 0; i < 3; i++ {
		if got := agentid.SeededID("kai-server"); got != want {
			t.Errorf("SeededID not deterministic: got %q, want %q", got, want)
		}
	}
	if !agentid.IsValid(agentid.SeededID("anything")) {
		t.Error("SeededID produced an invalid id")
	}
}

func TestNewIDShape(t *testing.T) {
	for i := 0; i < 500; i++ {
		id, err := agentid.NewID()
		if err != nil {
			t.Fatalf("NewID: %v", err)
		}
		if !agentid.IsValid(id) {
			t.Fatalf("NewID produced invalid id %q", id)
		}
	}
}

func TestIsValid(t *testing.T) {
	// Excluded digits (0-3) fail even in the right shape, so the issue's
	// illustrative "ab81"/"cd92" examples are themselves invalid ids.
	cases := map[string]bool{
		"ab85": true,
		"ab81": false, // '1' is an excluded digit
		"AB85": false, // uppercase is not canonical
		"ab8":  false, // too short
		"abcd": false, // no digits
		"8945": false, // no leading letters
		"ao85": false, // confusable 'o' rejected
		"a8b5": false, // interleaved
		"":     false,
	}
	for in, want := range cases {
		if got := agentid.IsValid(in); got != want {
			t.Errorf("IsValid(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"  AB85 ": "ab85",
		"Cd97":    "cd97",
	}
	for in, want := range cases {
		got, err := agentid.Normalize(in)
		if err != nil {
			t.Errorf("Normalize(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := agentid.Normalize("nope"); err == nil {
		t.Error("Normalize(\"nope\") should have errored")
	}
}

func TestAlphabetExcludesConfusables(t *testing.T) {
	for _, bad := range []byte("ilno0123") {
		if idx := indexByte(agentid.Alphabet, bad); idx >= 0 {
			t.Errorf("confusable %q present in alphabet", string(bad))
		}
	}
	if agentid.Letters != "abcdefghjkmpqrstuvwxyz" {
		t.Errorf("Letters drifted: %q", agentid.Letters)
	}
	if agentid.Digits != "456789" {
		t.Errorf("Digits drifted: %q", agentid.Digits)
	}
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
