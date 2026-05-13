package egress

// Allowlists is the per-binary set of upstreams each wrapped binary is
// allowed to reach in enforce mode. Empty by default - consumers populate
// this map (or call New with their own allowlist) before mounting any
// enforced passthrough.
//
// The keys are binary names; the values are hostname strings matched against
// the CONNECT host. Hostnames are exact-match for now (no wildcards). See
// proxy.go for the matching contract.
var Allowlists = map[string][]string{}
