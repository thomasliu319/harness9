package permission

import (
	"context"
	"encoding/json"
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
	argStr := extractArgString(tc)
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

// extractArgString 为规则匹配提取可读的参数字符串。
// bash 工具提取 command 字段；其他工具使用原始 JSON 字节。
func extractArgString(tc schema.ToolCall) string {
	if tc.Name == "bash" {
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(tc.Arguments, &args); err == nil && args.Command != "" {
			return args.Command
		}
	}
	return string(tc.Arguments)
}

// AfterExecute 是空操作，原样返回结果。
func (h *Hook) AfterExecute(_ context.Context, _ schema.ToolCall, result schema.ToolResult) schema.ToolResult {
	return result
}
