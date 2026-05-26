package egress

// Allowlists is the per-binary set of upstreams each wrapped binary is
// allowed to reach in enforce mode. Empty by default - consumers populate
var Allowlists = map[string][]string{}
