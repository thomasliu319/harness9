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
