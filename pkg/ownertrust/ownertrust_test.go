package ownertrust_test

import (
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/ownertrust"
)

func TestAllowedSingle(t *testing.T) {
	l := ownertrust.List{Primary: "primary"}
	if !l.Allowed("primary") {
		t.Error("primary should be allowed")
	}
	if l.Allowed("other") {
		t.Error("non-primary should be refused with no Extra")
	}
	if l.Allowed("") {
		t.Error("empty owner should never be allowed")
	}
}

func TestAllowedMulti(t *testing.T) {
	l := ownertrust.List{Primary: "primary", Extra: []string{"sib-a", "sib-b"}}
	for _, o := range []string{"primary", "sib-a", "sib-b"} {
		if !l.Allowed(o) {
			t.Errorf("%q should be allowed", o)
		}
	}
	if l.Allowed("outsider") {
		t.Error("outsider should be refused")
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		list ownertrust.List
		want string
	}{
		{ownertrust.List{Primary: "primary"}, "primary/*"},
		{ownertrust.List{Primary: "primary", Extra: []string{"sib-a", "sib-b"}}, "{primary, sib-a, sib-b}/*"},
		{ownertrust.List{Primary: "primary", Extra: []string{"primary"}}, "primary/*"}, // dedup
		{ownertrust.List{}, "(no owners)/*"},
	}
	for _, c := range cases {
		if got := c.list.Label(); got != c.want {
			t.Errorf("Label(%+v) = %q, want %q", c.list, got, c.want)
		}
	}
}
