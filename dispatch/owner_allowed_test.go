package dispatch

import "testing"

func TestOwnerAllowed(t *testing.T) {
	single := Config{AllowedOwner: "primary"}
	if !single.ownerAllowed("primary") {
		t.Error("primary owner should be allowed")
	}
	if single.ownerAllowed("other") {
		t.Error("non-primary owner should be refused with no AllowedOwners")
	}

	multi := Config{AllowedOwner: "primary", AllowedOwners: []string{"sib-a", "sib-b"}}
	for _, o := range []string{"primary", "sib-a", "sib-b"} {
		if !multi.ownerAllowed(o) {
			t.Errorf("%q should be allowed in the multi-owner set", o)
		}
	}
	if multi.ownerAllowed("outsider") {
		t.Error("owner outside the set should be refused")
	}
}

func TestAllowedOwnersLabel(t *testing.T) {
	if got := (Config{AllowedOwner: "primary"}).allowedOwnersLabel(); got != "primary/*" {
		t.Errorf("single-owner label = %q, want primary/*", got)
	}
	got := (Config{AllowedOwner: "primary", AllowedOwners: []string{"sib-a", "sib-b"}}).allowedOwnersLabel()
	if got != "{primary, sib-a, sib-b}/*" {
		t.Errorf("multi-owner label = %q", got)
	}
}
