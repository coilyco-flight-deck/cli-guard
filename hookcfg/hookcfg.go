// Package hookcfg bridges repocfg.Security into the hook engine's
// Protected shape, so consumers (cmd/cli-guard-hook, ward, future shell
// consumers via go.mod) share one mapping rather than each carrying
// their own copy.
//
// repocfg and hook stay independent of each other; this small bridge
// owns the direction repocfg.Security -> []hook.Protected.
package hookcfg

import (
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/hook"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/repocfg"
)

// ProtectedFor maps a parsed repocfg.Security block into the hook engine's
// Protected list. Merges two sources:
//
//   - Every protected_binaries entry becomes a Protected. The engine
//     matches by basename so bare / absolute / relative spellings all
//     trigger the same deny.
//   - Every hooks.deny_bare_binaries entry that is NOT already covered
//     by a protected_binaries entry becomes a hint-only Protected (no
//     wrappers), so a downstream config can deny additional bare
//     invocations without declaring the full protected schema.
//
// Hint precedence: hooks.route_hints[name] when set, else the engine
// synthesizes from Wrappers, else a bare deny.
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
