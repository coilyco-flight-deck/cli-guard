package shim_test

import (
	"fmt"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/hook"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/cli/shim"
)

// A consumer builds shims from the same protected set it feeds the engine,
// so the shim and deny sets cannot drift. Production: hookcfg.ProtectedFor.
func ExampleFor() {
	specs, err := shim.For([]hook.Protected{
		{Name: "gcloud", Hint: "Use kap for cloud ops."},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("shim:", specs[0].Name)
	// Output: shim: gcloud
}
