package evals_test

import (
	"errors"
	"testing"

	"github.com/harness9/internal/evals"
)

func TestToolCalledAssertion_Pass(t *testing.T) {
	r := &evals.Result{ToolCallsExecuted: []string{"bash", "read_file"}}
	a := &evals.ToolCalledAssertion{ToolName: "bash"}
	if f := a.Check(r); f != nil {
		t.Errorf("expected pass, got: %s", f.Error())
	}
}

func TestToolCalledAssertion_Fail(t *testing.T) {
	r := &evals.Result{ToolCallsExecuted: []string{"read_file"}}
	a := &evals.ToolCalledAssertion{ToolName: "bash"}
	if f := a.Check(r); f == nil {
		t.Error("expected failure, got nil")
	}
}

func TestToolCalledAssertion_MinTimes(t *testing.T) {
	r := &evals.Result{ToolCallsExecuted: []string{"bash", "bash", "bash"}}
	if f := (&evals.ToolCalledAssertion{ToolName: "bash", MinTimes: 3}).Check(r); f != nil {
		t.Errorf("expected pass with 3 calls: %s", f.Error())
	}
	if f := (&evals.ToolCalledAssertion{ToolName: "bash", MinTimes: 4}).Check(r); f == nil {
		t.Error("expected fail with only 3 calls but MinTimes=4")
	}
}

func TestToolNotCalledAssertion(t *testing.T) {
	r := &evals.Result{ToolCallsExecuted: []string{"read_file"}}
	if f := (&evals.ToolNotCalledAssertion{ToolName: "bash"}).Check(r); f != nil {
		t.Errorf("bash not called: %s", f.Error())
	}
	r2 := &evals.Result{ToolCallsExecuted: []string{"bash"}}
	if f := (&evals.ToolNotCalledAssertion{ToolName: "bash"}).Check(r2); f == nil {
		t.Error("expected failure when bash is called")
	}
}

func TestOutputContainsAssertion(t *testing.T) {
	r := &evals.Result{FinalOutput: "Hello, world!"}
	if f := (&evals.OutputContainsAssertion{Expected: "Hello"}).Check(r); f != nil {
		t.Errorf("expected pass: %s", f.Error())
	}
	if f := (&evals.OutputContainsAssertion{Expected: "missing"}).Check(r); f == nil {
		t.Error("expected failure for missing text")
	}
}

func TestOutputExcludesAssertion(t *testing.T) {
	r := &evals.Result{FinalOutput: "Hello, world!"}
	if f := (&evals.OutputExcludesAssertion{Forbidden: "missing"}).Check(r); f != nil {
		t.Errorf("expected pass: %s", f.Error())
	}
	if f := (&evals.OutputExcludesAssertion{Forbidden: "Hello"}).Check(r); f == nil {
		t.Error("expected failure when forbidden text is present")
	}
}

func TestNoErrorAssertion(t *testing.T) {
	r := &evals.Result{RunError: nil}
	if f := (&evals.NoErrorAssertion{}).Check(r); f != nil {
		t.Errorf("expected pass: %s", f.Error())
	}
	r2 := &evals.Result{RunError: errors.New("test error")}
	if f := (&evals.NoErrorAssertion{}).Check(r2); f == nil {
		t.Error("expected failure when RunError is set")
	}
}

func TestErrorAssertion(t *testing.T) {
	r := &evals.Result{RunError: errors.New("some error")}
	if f := (&evals.ErrorAssertion{}).Check(r); f != nil {
		t.Errorf("expected pass when RunError is set: %s", f.Error())
	}
	r2 := &evals.Result{RunError: nil}
	if f := (&evals.ErrorAssertion{}).Check(r2); f == nil {
		t.Error("expected failure when RunError is nil")
	}
}

func TestMaxTurnsAssertion_IsSoft(t *testing.T) {
	r := &evals.Result{TurnCount: 10}
	f := (&evals.MaxTurnsAssertion{Max: 5}).Check(r)
	if f == nil {
		t.Fatal("expected failure for exceeding max turns")
	}
	if !f.IsSoft {
		t.Error("MaxTurnsAssertion should produce soft failure")
	}
}

func TestMaxTurnsAssertion_Pass(t *testing.T) {
	r := &evals.Result{TurnCount: 5}
	if f := (&evals.MaxTurnsAssertion{Max: 5}).Check(r); f != nil {
		t.Errorf("expected pass when TurnCount == Max: %s", f.Error())
	}
}

func TestMaxToolCallsAssertion_IsSoft(t *testing.T) {
	r := &evals.Result{ToolCallsExecuted: []string{"bash", "bash", "bash"}}
	f := (&evals.MaxToolCallsAssertion{Max: 2}).Check(r)
	if f == nil {
		t.Fatal("expected failure for exceeding max tool calls")
	}
	if !f.IsSoft {
		t.Error("MaxToolCallsAssertion should produce soft failure")
	}
}

func TestMaxToolCallsAssertion_Pass(t *testing.T) {
	r := &evals.Result{ToolCallsExecuted: []string{"bash", "read_file"}}
	if f := (&evals.MaxToolCallsAssertion{Max: 2}).Check(r); f != nil {
		t.Errorf("expected pass: %s", f.Error())
	}
}

func TestFailure_Error(t *testing.T) {
	f := &evals.Failure{AssertionName: "test_assertion", Message: "something went wrong"}
	expected := "[test_assertion] something went wrong"
	if f.Error() != expected {
		t.Errorf("expected %q, got %q", expected, f.Error())
	}
}

func TestAssertionNames(t *testing.T) {
	cases := []struct {
		assertion evals.Assertion
		wantName  string
	}{
		{&evals.ToolCalledAssertion{ToolName: "bash"}, "tool_called(bash)"},
		{&evals.ToolCalledAssertion{ToolName: "bash", MinTimes: 3}, "tool_called(bash)"},
		{&evals.ToolNotCalledAssertion{ToolName: "write_file"}, "tool_not_called(write_file)"},
		{&evals.OutputContainsAssertion{Expected: "hello"}, `output_contains("hello")`},
		{&evals.OutputExcludesAssertion{Forbidden: "error"}, `output_excludes("error")`},
		{&evals.NoErrorAssertion{}, "no_run_error"},
		{&evals.ErrorAssertion{}, "run_error"},
		{&evals.MaxTurnsAssertion{Max: 10}, "max_turns(10)"},
		{&evals.MaxToolCallsAssertion{Max: 5}, "max_tool_calls(5)"},
	}
	for _, tc := range cases {
		if got := tc.assertion.Name(); got != tc.wantName {
			t.Errorf("assertion.Name() = %q, want %q", got, tc.wantName)
		}
	}
}

func TestHardAssertions_IsSoftFalse(t *testing.T) {
	// Hard assertions must never set IsSoft=true on failure.
	r := &evals.Result{
		ToolCallsExecuted: []string{"read_file"},
		FinalOutput:       "bad output",
		RunError:          nil,
		TurnCount:         1,
	}
	hard := []evals.Assertion{
		&evals.ToolCalledAssertion{ToolName: "bash"},
		&evals.ToolNotCalledAssertion{ToolName: "read_file"},
		&evals.OutputContainsAssertion{Expected: "missing"},
		&evals.OutputExcludesAssertion{Forbidden: "bad"},
		&evals.NoErrorAssertion{},
	}
	for _, a := range hard {
		// Force a failure by using the result above, then verify IsSoft is false.
		f := a.Check(r)
		if f == nil {
			// ErrorAssertion needs different input; skip assertions that pass here.
			continue
		}
		if f.IsSoft {
			t.Errorf("hard assertion %q produced IsSoft=true", a.Name())
		}
	}

	// ErrorAssertion: needs RunError == nil to fail.
	r2 := &evals.Result{RunError: nil}
	if f := (&evals.ErrorAssertion{}).Check(r2); f != nil && f.IsSoft {
		t.Error("ErrorAssertion should produce hard failure")
	}
}
