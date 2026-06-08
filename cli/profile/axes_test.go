package profile_test

import (
	"testing"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/profile"
)

func TestAllAxes_Order(t *testing.T) {
	want := []profile.Axis{
		profile.AxisDataSecurity,
		profile.AxisBlastRadius,
		profile.AxisNetworkEgress,
		profile.AxisFilesystemReach,
	}
	got := profile.AllAxes()
	if len(got) != len(want) {
		t.Fatalf("AllAxes len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AllAxes[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestTiersFor_KnownAxis(t *testing.T) {
	tiers := profile.TiersFor(profile.AxisDataSecurity)
	if len(tiers) != 4 {
		t.Fatalf("data_security tier count = %d, want 4", len(tiers))
	}
	if tiers[0] != profile.DataSecurityLow {
		t.Errorf("first tier = %s, want %s", tiers[0], profile.DataSecurityLow)
	}
	if tiers[len(tiers)-1] != profile.DataSecurityMax {
		t.Errorf("last tier = %s, want %s (strictest)", tiers[len(tiers)-1], profile.DataSecurityMax)
	}
}

func TestTiersFor_UnknownAxis(t *testing.T) {
	if tiers := profile.TiersFor("nope"); tiers != nil {
		t.Fatalf("unknown axis should return nil, got %v", tiers)
	}
}

func TestIsStricter_DataSecurity(t *testing.T) {
	yes, err := profile.IsStricter(profile.AxisDataSecurity, profile.DataSecurityMax, profile.DataSecurityLow)
	if err != nil {
		t.Fatalf("IsStricter: %v", err)
	}
	if !yes {
		t.Error("max should be stricter than low")
	}
	no, _ := profile.IsStricter(profile.AxisDataSecurity, profile.DataSecurityLow, profile.DataSecurityMax)
	if no {
		t.Error("low should not be stricter than max")
	}
	same, _ := profile.IsStricter(profile.AxisDataSecurity, profile.DataSecurityHigh, profile.DataSecurityHigh)
	if same {
		t.Error("equal tiers should not be stricter")
	}
}

func TestIsStricter_BlastRadiusOrdering(t *testing.T) {
	// low blast radius is *strictest* (fewest destructive verbs allowed).
	yes, err := profile.IsStricter(profile.AxisBlastRadius, profile.BlastRadiusLow, profile.BlastRadiusHigh)
	if err != nil {
		t.Fatalf("IsStricter: %v", err)
	}
	if !yes {
		t.Error("blast_radius low should be stricter than high")
	}
}

func TestIsStricter_NetworkEgressOrdering(t *testing.T) {
	yes, _ := profile.IsStricter(profile.AxisNetworkEgress, profile.NetworkEgressAirGapped, profile.NetworkEgressOpen)
	if !yes {
		t.Error("air-gapped should be stricter than open")
	}
}

func TestIsStricter_UnknownTier(t *testing.T) {
	if _, err := profile.IsStricter(profile.AxisDataSecurity, "nope", profile.DataSecurityMax); err == nil {
		t.Error("unknown tier should error")
	}
	if _, err := profile.IsStricter("bogus-axis", profile.DataSecurityMax, profile.DataSecurityLow); err == nil {
		t.Error("unknown axis should error")
	}
}

func TestStrictest_AllAxesAtStrictTier(t *testing.T) {
	c := profile.Strictest()
	if c.DataSecurity != profile.DataSecurityMax {
		t.Errorf("data_security = %s, want %s", c.DataSecurity, profile.DataSecurityMax)
	}
	if c.BlastRadius != profile.BlastRadiusLow {
		t.Errorf("blast_radius = %s, want %s", c.BlastRadius, profile.BlastRadiusLow)
	}
	if c.NetworkEgress != profile.NetworkEgressAirGapped {
		t.Errorf("network_egress = %s, want %s", c.NetworkEgress, profile.NetworkEgressAirGapped)
	}
	if c.FilesystemReach != profile.FilesystemReachRepoOnly {
		t.Errorf("filesystem_reach = %s, want %s", c.FilesystemReach, profile.FilesystemReachRepoOnly)
	}
}

func TestCoordinate_GetByAxis(t *testing.T) {
	c := profile.Strictest()
	for _, ax := range profile.AllAxes() {
		got := c.Get(ax)
		want := profile.StrictestTier(ax)
		if got != want {
			t.Errorf("Get(%s) = %s, want %s", ax, got, want)
		}
	}
	if c.Get("nope") != "" {
		t.Error("unknown axis should return empty string")
	}
}
