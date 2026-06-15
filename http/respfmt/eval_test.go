package respfmt

import "testing"

// TestEvalScopeInjection asserts an action input reaches the condition as a
// native `$variable` and the CI-watch terminal predicate settles correctly.
func TestEvalScopeInjection(t *testing.T) {
	expr := "length([?run_number==$run && status!='success' && status!='failure' && status!='cancelled' && status!='skipped']) == `0`"
	vars := map[string]any{"run": 5.0}

	pending := []any{
		map[string]any{"run_number": 5.0, "status": "success"},
		map[string]any{"run_number": 5.0, "status": "pending"}, // still running
		map[string]any{"run_number": 6.0, "status": "pending"}, // other run, ignored
	}
	if ok, err := EvalBool(pending, expr, vars); err != nil || ok {
		t.Errorf("pending run: ok=%v err=%v, want ok=false", ok, err)
	}

	done := []any{
		map[string]any{"run_number": 5.0, "status": "success"},
		map[string]any{"run_number": 5.0, "status": "failure"},
	}
	if ok, err := EvalBool(done, expr, vars); err != nil || !ok {
		t.Errorf("terminal run: ok=%v err=%v, want ok=true", ok, err)
	}
}

// TestEvalUnboundVariableFailsClosed asserts an expression naming a variable no
// input supplies errors rather than silently treating it as null.
func TestEvalUnboundVariableFailsClosed(t *testing.T) {
	if _, err := Eval(nil, "$missing == `1`", map[string]any{}); err == nil {
		t.Error("expected an error for an unbound variable, got nil")
	}
}

func TestTruthy(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{nil, false},
		{false, false},
		{true, true},
		{"", false},
		{"x", true},
		{[]any{}, false},
		{[]any{1}, true},
		{map[string]any{}, false},
		{map[string]any{"a": 1}, true},
		{0.0, true}, // JMESPath: the number 0 is truthy
	}
	for _, c := range cases {
		if got := Truthy(c.v); got != c.want {
			t.Errorf("Truthy(%#v) = %v, want %v", c.v, got, c.want)
		}
	}
}
