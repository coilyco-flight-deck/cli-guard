// Package profile declares the categorical operating-model axes that
// cli-guard exposes for consumers (today: coily) to build per-session
// profiles on top of. cli-guard owns the typed surface; the mapping
// from named profiles ("mobile", "mac-tower", ...) to coordinates in
// this axis space lives in the consumer.
//
// Phase 1 of coilysiren/coily#150. Types only, no gate enforcement.
// Enforcement wires in later phases once consumers have settled on
// the coordinate semantics.
//
// Design notes:
//
//   - Four axes only: data security, blast radius, network egress,
//     filesystem reach. Anything else (automation level, prompt
//     behavior, input modality) is agent-side posture and stays in
//     the consumer's execution-mode rules, not in cli-guard.
//   - Each axis has a strictest tier; the "Strictest" coordinate is
//     the headless-default per the design resolution in coily#150.
//   - Tiers are ordered by strictness within an axis. Helpers compare
//     tiers; consumers do not parse tier strings.
package profile

import "fmt"

// Axis names a categorical dimension cli-guard exposes for profile
// declarations. Stable string values so consumers can persist them
// (config files, audit rows) without coupling to the Go iota order.
type Axis string

const (
	// AxisDataSecurity governs secret access (direct vs proxy-only vs
	// full), identifier surfacing, vault visibility, and leak-check
	// stringency.
	AxisDataSecurity Axis = "data_security"

	// AxisBlastRadius governs which destructive verbs are gated:
	// cluster mutations, force-pushes, mass-deletes, etc.
	AxisBlastRadius Axis = "blast_radius"

	// AxisNetworkEgress governs outbound network reach. air-gapped
	// and loopback-only are stubbed in the initial implementation
	// per coily#150; the distinction is deferred.
	AxisNetworkEgress Axis = "network_egress"

	// AxisFilesystemReach governs which paths a session can read and
	// write outside the current repo.
	AxisFilesystemReach Axis = "filesystem_reach"
)

// AllAxes returns the four axes in the canonical declaration order.
// Useful for iteration in config validators and digest renderers.
func AllAxes() []Axis {
	return []Axis{
		AxisDataSecurity,
		AxisBlastRadius,
		AxisNetworkEgress,
		AxisFilesystemReach,
	}
}

// Tier is a string label naming a strictness level on a given axis.
// Stable values so consumer config files and audit rows do not
// depend on Go iota ordering.
type Tier string

// DataSecurity tiers, strictest last.
const (
	DataSecurityLow    Tier = "low"
	DataSecurityMedium Tier = "medium"
	DataSecurityHigh   Tier = "high"
	DataSecurityMax    Tier = "max"
)

// BlastRadius tiers, strictest last.
const (
	BlastRadiusHigh   Tier = "high"
	BlastRadiusMedium Tier = "medium"
	BlastRadiusLow    Tier = "low"
)

// NetworkEgress tiers, strictest last.
const (
	NetworkEgressOpen         Tier = "open"
	NetworkEgressAllowlisted  Tier = "allowlisted"
	NetworkEgressLoopbackOnly Tier = "loopback-only"
	NetworkEgressAirGapped    Tier = "air-gapped"
)

// FilesystemReach tiers, strictest last.
const (
	FilesystemReachUnrestricted Tier = "unrestricted"
	FilesystemReachRepoPlusHome Tier = "repo-plus-home"
	FilesystemReachRepoOnly     Tier = "repo-only"
)

// tierOrder maps each axis to its tiers in *strictness-ascending*
// order. Index 0 is the most permissive; the last index is the
// strictest. IsStricter / Strictest read from this.
var tierOrder = map[Axis][]Tier{
	AxisDataSecurity: {
		DataSecurityLow,
		DataSecurityMedium,
		DataSecurityHigh,
		DataSecurityMax,
	},
	AxisBlastRadius: {
		BlastRadiusHigh,
		BlastRadiusMedium,
		BlastRadiusLow,
	},
	AxisNetworkEgress: {
		NetworkEgressOpen,
		NetworkEgressAllowlisted,
		NetworkEgressLoopbackOnly,
		NetworkEgressAirGapped,
	},
	AxisFilesystemReach: {
		FilesystemReachUnrestricted,
		FilesystemReachRepoPlusHome,
		FilesystemReachRepoOnly,
	},
}

// TiersFor returns the tiers defined on axis a, ordered most
// permissive to strictest. Returns nil for an unknown axis so
// callers can detect typos in config parsing.
func TiersFor(a Axis) []Tier {
	tiers, ok := tierOrder[a]
	if !ok {
		return nil
	}
	out := make([]Tier, len(tiers))
	copy(out, tiers)
	return out
}

// IsStricter reports whether tier a is strictly stricter than tier b
// on axis ax. Returns an error if either tier is not defined on ax
// (caller's config likely has a typo).
func IsStricter(ax Axis, a, b Tier) (bool, error) {
	tiers, ok := tierOrder[ax]
	if !ok {
		return false, fmt.Errorf("profile: unknown axis %q", ax)
	}
	ai, bi := -1, -1
	for i, t := range tiers {
		if t == a {
			ai = i
		}
		if t == b {
			bi = i
		}
	}
	if ai < 0 {
		return false, fmt.Errorf("profile: tier %q not defined on axis %q", a, ax)
	}
	if bi < 0 {
		return false, fmt.Errorf("profile: tier %q not defined on axis %q", b, ax)
	}
	return ai > bi, nil
}

// StrictestTier returns the tier at the strict end of axis a's
// ordering. Used by Strictest() to build the max-paranoia coordinate
// and by consumers reasoning about deny defaults. Panics on an
// unknown axis since AllAxes is the canonical input and adding a new
// axis without populating tierOrder is a programmer error.
func StrictestTier(a Axis) Tier {
	tiers, ok := tierOrder[a]
	if !ok || len(tiers) == 0 {
		panic(fmt.Sprintf("profile: no tiers registered for axis %q", a))
	}
	return tiers[len(tiers)-1]
}

// Coordinate is a tier-per-axis assignment. Profiles declared by
// consumers (coily's "mobile", "mac-tower", ...) resolve to a
// Coordinate. The lockdown evaluator reads Coordinate, not profile
// name.
type Coordinate struct {
	DataSecurity    Tier
	BlastRadius     Tier
	NetworkEgress   Tier
	FilesystemReach Tier
}

// Get returns the tier on the named axis. Returns the empty string
// if the axis name does not match a known field; callers can treat
// that as a typo signal.
func (c Coordinate) Get(a Axis) Tier {
	switch a {
	case AxisDataSecurity:
		return c.DataSecurity
	case AxisBlastRadius:
		return c.BlastRadius
	case AxisNetworkEgress:
		return c.NetworkEgress
	case AxisFilesystemReach:
		return c.FilesystemReach
	}
	return ""
}

// Strictest returns the coordinate where every axis is at its
// strictest tier. Per coily#150, this is the headless-default and
// the implicit baseline absent a consumer config file.
func Strictest() Coordinate {
	return Coordinate{
		DataSecurity:    StrictestTier(AxisDataSecurity),
		BlastRadius:     StrictestTier(AxisBlastRadius),
		NetworkEgress:   StrictestTier(AxisNetworkEgress),
		FilesystemReach: StrictestTier(AxisFilesystemReach),
	}
}
