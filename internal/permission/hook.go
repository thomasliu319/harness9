package permission

import (
	"context"
	"fmt"

	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/schema"
)

// Hook 是基于 Rules 的 ToolHook 实现。
type Hook struct {
	rules *Rules
}

// NewHook 创建基于给定规则集的 PermissionHook。
func NewHook(rules *Rules) *Hook {
	return &Hook{rules: rules}
}

// BeforeExecute 评估工具调用并返回对应 HookDecision。
func (h *Hook) BeforeExecute(ctx context.Context, tc schema.ToolCall) (context.Context, hooks.HookDecision, error) {
	argStr := string(tc.Arguments)
	action := h.rules.Evaluate(tc.Name, argStr)
	switch action {
	case RuleDeny:
		return ctx, hooks.Deny(fmt.Sprintf("工具 %s 被权限规则拒绝", tc.Name)), nil
	case RuleAsk:
		return ctx, hooks.Ask(
			fmt.Sprintf("工具 %s 需要人类审批（规则配置）", tc.Name),
			"medium",
		), nil
	default:
		return ctx, hooks.Allow(), nil
	}
}

// AfterExecute 是空操作，原样返回结果。
func (h *Hook) AfterExecute(_ context.Context, _ schema.ToolCall, result schema.ToolResult) schema.ToolResult {
	return result
}
