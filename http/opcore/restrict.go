package opcore

import (
	"fmt"
	"path/filepath"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/exitcode"
)

// CheckRestrictions enforces every wrap-level restriction against a leaf's bound
// path values, failing closed on an out-of-scope value. See docs/specverb.md.
func (rt *Runtime) CheckRestrictions(pathParams, values []string) error {
	for _, r := range rt.Restrict {
		for i, p := range pathParams {
			if p != r.Param || i >= len(values) {
				continue
			}
			if !matchesAnyGlob(values[i], r.Globs) {
				return exitcode.New(exitcode.PolicyDenied, "policy_denied",
					fmt.Errorf("argument %s=%q is outside the allowed scope (restrict %s matches %v)", p, values[i], r.Param, r.Globs),
					"supply a value within the restricted scope, or widen the `restrict` clause")
			}
		}
	}
	return nil
}

// matchesAnyGlob reports whether val matches at least one filepath.Match glob.
// A malformed pattern matches nothing (fail closed), never errors out.
func matchesAnyGlob(val string, globs []string) bool {
	for _, g := range globs {
		if ok, err := filepath.Match(g, val); err == nil && ok {
			return true
		}
	}
	return false
}
