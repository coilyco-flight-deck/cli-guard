// Package hookcfg bridges repocfg.Security into the hook engine's Protected
// shape so consumers share one mapping (Security -> []hook.Protected).
package hookcfg

import (
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/hook"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/repocfg"
)

// ProtectedFor maps a repocfg.Security block into the hook engine's Protected
// list, merging protected_binaries with uncovered hooks.deny_bare_binaries.
func ProtectedFor(sec repocfg.Security) []hook.Protected {
	if len(sec.ProtectedBinaries) == 0 && len(sec.Hooks.DenyBareBinaries) == 0 {
		return nil
	}
	covered := make(map[string]bool, len(sec.ProtectedBinaries))
	out := make([]hook.Protected, 0, len(sec.ProtectedBinaries)+len(sec.Hooks.DenyBareBinaries))
	for _, pb := range sec.ProtectedBinaries {
		if pb.Name == "" {
			continue
		}
		covered[pb.Name] = true
		out = append(out, hook.Protected{
			Name:     pb.Name,
			Hint:     sec.Hooks.RouteHints[pb.Name],
			Wrappers: pb.AllowedWrappers,
		})
	}
	for _, name := range sec.Hooks.DenyBareBinaries {
		if name == "" || covered[name] {
			continue
		}
		out = append(out, hook.Protected{
			Name: name,
			Hint: sec.Hooks.RouteHints[name],
		})
	}
	return out
}
