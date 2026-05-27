package hooks_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/schema"
	"github.com/harness9/internal/tools"
)

// echoTool 是测试用的简单工具，原样返回工具名。
type echoTool struct{ name string }

func (e *echoTool) Name() string { return e.name }
func (e *echoTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{Name: e.name}
}
func (e *echoTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "echo:" + e.name, nil
}

// recordHook 记录 Before/After 调用顺序。
type recordHook struct {
	id        string
	log       *[]string
	beforeDec hooks.HookDecision
	beforeErr error
}

func (r *recordHook) BeforeExecute(ctx context.Context, tc schema.ToolCall) (context.Context, hooks.HookDecision, error) {
	*r.log = append(*r.log, "before:"+r.id)
	if r.beforeErr != nil {
		return ctx, hooks.HookDecision{}, r.beforeErr
	}
	dec := r.beforeDec
	if dec.Action == "" {
		dec = hooks.Allow()
	}
	return ctx, dec, nil
}

func (r *recordHook) AfterExecute(ctx context.Context, tc schema.ToolCall, result schema.ToolResult) schema.ToolResult {
	*r.log = append(*r.log, "after:"+r.id)
	return result
}

func newInnerWithEcho(t *testing.T) tools.Registry {
	t.Helper()
	reg := tools.NewRegistry()
	if err := reg.Register(&echoTool{name: "echo"}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestHookRegistry_NoHooks_PassThrough(t *testing.T) {
	inner := newInnerWithEcho(t)
	hr := hooks.NewHookRegistry(inner)

	result := hr.Execute(context.Background(), schema.ToolCall{Name: "echo", ID: "1"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Output)
	}
	if result.Output != "echo:echo" {
		t.Errorf("got %q, want %q", result.Output, "echo:echo")
	}
}

func TestHookRegistry_BeforeShortCircuit(t *testing.T) {
	inner := newInnerWithEcho(t)
	var log []string
	h := &recordHook{id: "A", log: &log, beforeErr: errors.New("denied")}
	hr := hooks.NewHookRegistry(inner, h)

	result := hr.Execute(context.Background(), schema.ToolCall{Name: "echo", ID: "2"})
	if !result.IsError {
		t.Fatal("expected IsError=true after BeforeExecute error")
	}
	if result.Output != "denied" {
		t.Errorf("output should be error message, got %q", result.Output)
	}
	// AfterExecute must NOT be called when BeforeExecute short-circuits
	for _, entry := range log {
		if entry == "after:A" {
			t.Error("AfterExecute must not be called after BeforeExecute error")
		}
	}
}

func TestHookRegistry_OnionOrder(t *testing.T) {
	inner := newInnerWithEcho(t)
	var log []string
	hA := &recordHook{id: "A", log: &log}
	hB := &recordHook{id: "B", log: &log}
	hr := hooks.NewHookRegistry(inner, hA, hB)

	hr.Execute(context.Background(), schema.ToolCall{Name: "echo", ID: "3"})

	want := []string{"before:A", "before:B", "after:B", "after:A"}
	if len(log) != len(want) {
		t.Fatalf("log = %v, want %v", log, want)
	}
	for i, entry := range log {
		if entry != want[i] {
			t.Errorf("log[%d] = %q, want %q", i, entry, want[i])
		}
	}
}

func TestHookRegistry_Register_Delegates(t *testing.T) {
	inner := tools.NewRegistry()
	hr := hooks.NewHookRegistry(inner)
	if err := hr.Register(&echoTool{name: "x"}); err != nil {
		t.Fatal(err)
	}
	defs := hr.GetAvailableTools()
	if len(defs) != 1 || defs[0].Name != "x" {
		t.Errorf("GetAvailableTools = %v, want [{x}]", defs)
	}
}

func TestHookRegistry_BeforeDeny(t *testing.T) {
	inner := newInnerWithEcho(t)
	var log []string
	h := &recordHook{id: "A", log: &log, beforeDec: hooks.Deny("forbidden")}
	hr := hooks.NewHookRegistry(inner, h)

	result := hr.Execute(context.Background(), schema.ToolCall{Name: "echo", ID: "5"})
	if !result.IsError {
		t.Fatal("expected IsError=true on Deny")
	}
	if result.Output != "forbidden" {
		t.Errorf("output=%q, want %q", result.Output, "forbidden")
	}
	for _, entry := range log {
		if entry == "after:A" {
			t.Error("AfterExecute must not be called after Deny")
		}
	}
}

func TestHookRegistry_BeforeAsk_NoApprovalFn_Allows(t *testing.T) {
	inner := newInnerWithEcho(t)
	var log []string
	h := &recordHook{id: "A", log: &log, beforeDec: hooks.Ask("risky", "high")}
	hr := hooks.NewHookRegistry(inner, h)

	result := hr.Execute(context.Background(), schema.ToolCall{Name: "echo", ID: "6"})
	if result.IsError {
		t.Fatalf("expected success when no ApprovalFunc: %s", result.Output)
	}
}

func TestHookRegistry_BeforeAsk_WithApprovalFn_Deny(t *testing.T) {
	inner := newInnerWithEcho(t)
	var log []string
	h := &recordHook{id: "A", log: &log, beforeDec: hooks.Ask("risky", "high")}
	hr := hooks.NewHookRegistry(inner, h)

	ctx := hooks.WithApprovalFn(context.Background(), func(_ context.Context, _ schema.ToolCall, _, _ string) hooks.ApprovalResponse {
		return hooks.ApprovalResponse{Approved: false, Feedback: "user denied"}
	})
	result := hr.Execute(ctx, schema.ToolCall{Name: "echo", ID: "7"})
	if !result.IsError {
		t.Fatal("expected IsError=true when user denies")
	}
}

func TestHookRegistry_BeforeAsk_WithApprovalFn_Allow(t *testing.T) {
	inner := newInnerWithEcho(t)
	var log []string
	h := &recordHook{id: "A", log: &log, beforeDec: hooks.Ask("risky", "high")}
	hr := hooks.NewHookRegistry(inner, h)

	ctx := hooks.WithApprovalFn(context.Background(), func(_ context.Context, _ schema.ToolCall, _, _ string) hooks.ApprovalResponse {
		return hooks.ApprovalResponse{Approved: true}
	})
	result := hr.Execute(ctx, schema.ToolCall{Name: "echo", ID: "8"})
	if result.IsError {
		t.Fatalf("expected success when user approves: %s", result.Output)
	}
}
