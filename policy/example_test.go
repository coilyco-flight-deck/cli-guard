package policy_test

import (
	"fmt"

	"github.com/coilysiren/cli-guard/policy"
)

// Safe input: a positional argument with no shell metacharacters.
func ExampleValidateArgSlice() {
	err := policy.ValidateArgSlice("positional", []string{"hello", "world"})
	fmt.Println("err:", err)
	// Output: err: <nil>
}

// Unsafe input: a shell metacharacter (`;`) in a positional argument is
// rejected before the value can reach `execve`.
func ExampleValidateArgSlice_rejected() {
	err := policy.ValidateArgSlice("positional", []string{"hello; rm -rf /"})
	fmt.Println("err:", err)
	// Output: err: positional[0]: shell metacharacter ";" refused
}
