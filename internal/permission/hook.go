// Package permission — PermissionHook：基于 JSON 配置规则的工具执行权限拦截器。
// 本文件实现 Hook（permission.Hook），将 Rules 封装为 hooks.ToolHook 接口。
// NewFileHook 每次 BeforeExecute 时从磁盘重新加载规则文件，确保 TUI "总是允许"
// 动态更新白名单后在下次工具调用时立即生效，无需重启进程。
package permission

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/schema"
)

// Hook 是基于 Rules 的 ToolHook 实现。
// settingsPath 非空时每次 BeforeExecute 从磁盘重新加载规则，确保 writeApprovalToConfig 写入后立即生效。
type Hook struct {
	rules        *Rules
	settingsPath string
}

// NewHook 创建基于内存规则集的 PermissionHook（用于测试）。
func NewHook(rules *Rules) *Hook {
	return &Hook{rules: rules}
}

// NewFileHook 创建从文件按需加载规则的 PermissionHook。
// 每次工具调用时重新读取文件，使 writeApprovalToConfig 写入的白名单立即生效。
func NewFileHook(settingsPath string) *Hook {
	return &Hook{settingsPath: settingsPath}
}

// BeforeExecute 评估工具调用并返回对应 HookDecision。
func (h *Hook) BeforeExecute(ctx context.Context, tc schema.ToolCall) (context.Context, hooks.HookDecision, error) {
	rules := h.rules
	if h.settingsPath != "" {
		if loaded, err := LoadRules(h.settingsPath); err == nil {
			rules = loaded
		} else if rules == nil {
			rules = NewRules()
		}
	}
	if rules == nil {
		rules = NewRules()
	}
	argStr := extractArgString(tc)
	action := rules.Evaluate(tc.Name, argStr)
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
