package doctor_test

import (
	"fmt"
	"os"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/doctor"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/repocfg"
)

// A consumer runs Check against the parsed Security block, then decides
// whether a Fail blocks a step. Probes are injected here for determinism.
func ExampleCheck() {
	sec := repocfg.Security{
		ProtectedBinaries: []repocfg.ProtectedBinary{
			{Name: "gcloud", ExpectedRealPaths: []string{"/opt/homebrew/bin/gcloud"}},
		},
		Sudo: repocfg.SudoPolicy{ForbidPasswordless: true},
	}

	probes := doctor.Probes{
		Sudo:      func() (bool, string) { return false, "sudo: a password is required" },
		Stat:      func(string) (os.FileInfo, error) { return os.Stat(os.DevNull) },
		CanExec:   func(os.FileInfo) (bool, bool) { return false, true },
		LookupEnv: func(string) (string, bool) { return "", false },
	}

	report := doctor.Check(sec, probes)
	fmt.Println("ok:", report.OK())
	// Output: ok: true
}
