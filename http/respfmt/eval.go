// Binding-aware JMESPath evaluation: the engine `--query` uses, plus native
// `$variable` scope injection for action conditions. See docs/specverb-actions.md.

package respfmt

import (
	"fmt"

	"github.com/jmespath-community/go-jmespath/pkg/binding"
	"github.com/jmespath-community/go-jmespath/pkg/functions"
	"github.com/jmespath-community/go-jmespath/pkg/interpreter"
	"github.com/jmespath-community/go-jmespath/pkg/parsing"
)

// Eval evaluates expr against decoded data, with vars injected as native
// `$name` variables (keys are bare names; Eval adds the `$`). See specverb-actions.md.
func Eval(data any, expr string, vars map[string]any) (any, error) {
	ast, err := parsing.NewParser().Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("respfmt: parse jmespath %q: %w", expr, err)
	}
	b := binding.NewBindings()
	for name, v := range vars {
		b = b.Register("$"+name, v)
	}
	caller := interpreter.NewFunctionCaller(functions.GetDefaultFunctions()...)
	result, err := interpreter.NewInterpreter(data, caller, b).Execute(ast, data)
	if err != nil {
		return nil, fmt.Errorf("respfmt: eval jmespath %q: %w", expr, err)
	}
	return result, nil
}

// Validate reports whether expr parses as JMESPath, without evaluating it: the
// build-time check that fails closed on a malformed `until`/`fail-when`.
func Validate(expr string) error {
	if _, err := parsing.NewParser().Parse(expr); err != nil {
		return fmt.Errorf("respfmt: parse jmespath %q: %w", expr, err)
	}
	return nil
}

// EvalBool evaluates expr and reports its JMESPath truthiness, the predicate
// form `until` and `fail-when` need. A non-boolean result is reduced via Truthy.
func EvalBool(data any, expr string, vars map[string]any) (bool, error) {
	v, err := Eval(data, expr, vars)
	if err != nil {
		return false, err
	}
	return Truthy(v), nil
}

// Truthy reports JMESPath truthiness: null, false, "", [], and {} are false;
// everything else (including the number 0) is true, per the JMESPath spec.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) != 0
	case map[string]any:
		return len(x) != 0
	default:
		return true
	}
}
