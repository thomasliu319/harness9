package hooks_test

import (
	"context"
	"testing"

	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/schema"
)

func TestWithApprovalFn_RoundTrip(t *testing.T) {
	called := false
	fn := hooks.ApprovalFunc(func(ctx context.Context, tc schema.ToolCall, _, _ string) hooks.ApprovalResponse {
		called = true
		return hooks.ApprovalResponse{Approved: true}
	})
	ctx := hooks.WithApprovalFn(context.Background(), fn)
	got := hooks.ApprovalFnFromContext(ctx)
	if got == nil {
		t.Fatal("ApprovalFnFromContext returned nil")
	}
	got(context.Background(), schema.ToolCall{}, "", "")
	if !called {
		t.Error("ApprovalFunc not called")
	}
}

func TestApprovalFnFromContext_NilWhenAbsent(t *testing.T) {
	got := hooks.ApprovalFnFromContext(context.Background())
	if got != nil {
		t.Error("expected nil ApprovalFunc when not set")
	}
}

// TestDecisionConstructors 验证 Allow/Deny/Ask 快捷构造函数的字段设置正确。
func TestDecisionConstructors(t *testing.T) {
	a := hooks.Allow()
	if a.Action != hooks.HookActionAllow {
		t.Errorf("Allow().Action = %q, want %q", a.Action, hooks.HookActionAllow)
	}

	d := hooks.Deny("blocked")
	if d.Action != hooks.HookActionDeny {
		t.Errorf("Deny().Action = %q, want %q", d.Action, hooks.HookActionDeny)
	}
	if d.Reason != "blocked" {
		t.Errorf("Deny().Reason = %q, want %q", d.Reason, "blocked")
	}

	ask := hooks.Ask("risky", "high")
	if ask.Action != hooks.HookActionAsk {
		t.Errorf("Ask().Action = %q, want %q", ask.Action, hooks.HookActionAsk)
	}
	if ask.Reason != "risky" {
		t.Errorf("Ask().Reason = %q, want %q", ask.Reason, "risky")
	}
	if ask.RiskLevel != "high" {
		t.Errorf("Ask().RiskLevel = %q, want %q", ask.RiskLevel, "high")
	}
}
