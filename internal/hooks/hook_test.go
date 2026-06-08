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

// capturingTool 是测试用工具，将 Execute 收到的参数记录到外部指针。
type capturingTool struct {
	name    string
	capture *[]byte
}

func (c *capturingTool) Name() string { return c.name }
func (c *capturingTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{Name: c.name}
}
func (c *capturingTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	*c.capture = append([]byte(nil), args...)
	return "captured", nil
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

// TestHookRegistry_AllowWithModifiedArgs 验证 HookActionAllow 携带 ModifiedArgs 时参数被正确应用。
// 场景：路径沙箱 hook 显式放行并重写参数，内层工具应收到重写后的参数。
func TestHookRegistry_AllowWithModifiedArgs(t *testing.T) {
	var receivedArgs []byte
	inner := tools.NewRegistry()
	captureTool := &capturingTool{name: "echo", capture: &receivedArgs}
	if err := inner.Register(captureTool); err != nil {
		t.Fatal(err)
	}

	modifiedArgs := []byte(`{"command":"safe-cmd"}`)
	allowHook := &recordHook{
		id:  "rewriter",
		log: new([]string),
		beforeDec: hooks.HookDecision{
			Action:       hooks.HookActionAllow,
			ModifiedArgs: modifiedArgs,
		},
	}
	hr := hooks.NewHookRegistry(inner, allowHook)
	hr.Execute(context.Background(), schema.ToolCall{
		Name:      "echo",
		ID:        "10",
		Arguments: []byte(`{"command":"dangerous-cmd"}`),
	})
	if string(receivedArgs) != string(modifiedArgs) {
		t.Errorf("tool received args %q, want %q", receivedArgs, modifiedArgs)
	}
}

// TestHookRegistry_AskApprovedOnce 验证：同一工具调用已由人类审批后，后续相同的 Ask hook 不再弹框。
// 场景：两个 Ask hook 串联，第一个触发审批（通过），第二个应检测到 withApproved 标记而跳过。
func TestHookRegistry_AskApprovedOnce(t *testing.T) {
	inner := newInnerWithEcho(t)
	var log []string
	askHook1 := &recordHook{id: "ask1", log: &log, beforeDec: hooks.Ask("risky1", "high")}
	askHook2 := &recordHook{id: "ask2", log: &log, beforeDec: hooks.Ask("risky2", "medium")}
	hr := hooks.NewHookRegistry(inner, askHook1, askHook2)

	callCount := 0
	ctx := hooks.WithApprovalFn(context.Background(), func(_ context.Context, _ schema.ToolCall, _, _ string) hooks.ApprovalResponse {
		callCount++
		return hooks.ApprovalResponse{Approved: true}
	})
	result := hr.Execute(ctx, schema.ToolCall{Name: "echo", ID: "11"})
	if result.IsError {
		t.Fatalf("expected success: %s", result.Output)
	}
	if callCount != 1 {
		t.Errorf("ApprovalFunc called %d times, expected 1 (second ask should be deduped)", callCount)
	}
}

// TestHookRegistry_AllowThenAsk 验证白名单修复：前置 hook 显式 Allow 后，后续 hook 的 Ask 不再弹出审批。
// 复现场景：permHook 返回 Allow（命中白名单），dangerHook 返回 Ask（命中危险模式），
// 用户不应被再次要求审批。
// 注意：此处使用 "echo" 而非 "bash"，因为 inner registry 仅注册了 echo stub；
// 行为上与真实 bash+permHook+dangerHook 链等价——允许/拒绝决策在工具分发前已完成。
func TestHookRegistry_AllowThenAsk(t *testing.T) {
	inner := newInnerWithEcho(t)
	var log []string
	allowHook := &recordHook{id: "perm", log: &log, beforeDec: hooks.Allow()}
	askHook := &recordHook{id: "danger", log: &log, beforeDec: hooks.Ask("dangerous", "high")}
	hr := hooks.NewHookRegistry(inner, allowHook, askHook)

	// ApprovalFunc 若被调用则代表 bug 仍存在。
	asked := false
	ctx := hooks.WithApprovalFn(context.Background(), func(_ context.Context, _ schema.ToolCall, _, _ string) hooks.ApprovalResponse {
		asked = true
		return hooks.ApprovalResponse{Approved: true}
	})
	result := hr.Execute(ctx, schema.ToolCall{Name: "echo", ID: "9"})
	if result.IsError {
		t.Fatalf("expected success: %s", result.Output)
	}
	if asked {
		t.Fatal("ApprovalFunc should not be called when a prior hook explicitly returned Allow")
	}
}
