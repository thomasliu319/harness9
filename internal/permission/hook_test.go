package permission_test

import (
	"context"
	"testing"

	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/permission"
	"github.com/harness9/internal/schema"
)

func TestPermissionHook_Deny(t *testing.T) {
	r := permission.NewRules()
	r.AddRule(permission.RuleDeny, []string{"bash(rm -rf *)"})
	h := permission.NewHook(r)

	tc := schema.ToolCall{Name: "bash", Arguments: []byte(`{"command":"rm -rf /tmp"}`)}
	_, dec, err := h.BeforeExecute(context.Background(), tc)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Action != hooks.HookActionDeny {
		t.Errorf("expected Deny, got %s", dec.Action)
	}
}

func TestPermissionHook_Allow(t *testing.T) {
	r := permission.NewRules()
	r.AddRule(permission.RuleAllow, []string{"bash(git *)"})
	h := permission.NewHook(r)

	tc := schema.ToolCall{Name: "bash", Arguments: []byte(`{"command":"git status"}`)}
	_, dec, err := h.BeforeExecute(context.Background(), tc)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Action != hooks.HookActionAllow {
		t.Errorf("expected Allow, got %s", dec.Action)
	}
}

func TestPermissionHook_Ask_DefaultMediumRisk(t *testing.T) {
	r := permission.NewRules()
	h := permission.NewHook(r)

	tc := schema.ToolCall{Name: "bash", Arguments: []byte(`{"command":"echo hello"}`)}
	_, dec, err := h.BeforeExecute(context.Background(), tc)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Action != hooks.HookActionAsk {
		t.Errorf("expected Ask, got %s", dec.Action)
	}
	if dec.RiskLevel != "medium" {
		t.Errorf("expected medium risk level, got %s", dec.RiskLevel)
	}
}

func TestPermissionHook_AfterExecute_PassThrough(t *testing.T) {
	r := permission.NewRules()
	h := permission.NewHook(r)
	result := schema.ToolResult{Output: "ok"}
	got := h.AfterExecute(context.Background(), schema.ToolCall{}, result)
	if got.Output != "ok" {
		t.Error("AfterExecute should pass through")
	}
}
