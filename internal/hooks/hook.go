// Package hooks 提供通用的双向工具拦截器机制（Hooks）。
//
// HookRegistry 实现 tools.Registry 接口，在工具执行前后调用注册的 ToolHook 链。
// BeforeExecute 正向执行；AfterExecute 逆向执行（洋葱模型）。
//
// BeforeExecute 返回 HookDecision：
//   - Allow：继续调用后续 hook 和内层工具
//   - Deny：立即短路，不调用内层也不调用 AfterExecute
//   - Ask：从 context 提取 ApprovalFunc 请求人类审批；无 ApprovalFunc 时视为 Allow
package hooks

import (
	"context"
	"fmt"

	"github.com/harness9/internal/schema"
	"github.com/harness9/internal/tools"
)

// ToolHook 是双向工具拦截器接口。
// BeforeExecute 在工具调用前触发，返回结构化 HookDecision 指导后续流程。
// AfterExecute 在工具调用后触发，可修改返回的 ToolResult。
type ToolHook interface {
	BeforeExecute(ctx context.Context, tc schema.ToolCall) (context.Context, HookDecision, error)
	AfterExecute(ctx context.Context, tc schema.ToolCall, result schema.ToolResult) schema.ToolResult
}

// HookRegistry 用 hook 链包装原始 Registry，实现 tools.Registry 接口。
// 零 hook 时行为与原始 Registry 完全一致。
type HookRegistry struct {
	inner tools.Registry
	hooks []ToolHook
}

// NewHookRegistry 创建包装 inner 的 HookRegistry，依次应用给定的拦截器。
func NewHookRegistry(inner tools.Registry, hs ...ToolHook) *HookRegistry {
	return &HookRegistry{inner: inner, hooks: hs}
}

// Register 直接委托给内层 Registry。
func (r *HookRegistry) Register(tool tools.BaseTool) error {
	return r.inner.Register(tool)
}

// GetAvailableTools 直接委托给内层 Registry。
func (r *HookRegistry) GetAvailableTools() []schema.ToolDefinition {
	return r.inner.GetAvailableTools()
}

// Execute 按洋葱模型依次执行 hook 链，中间调用内层 Registry.Execute。
//
// 决策优先级（每个 hook 依次评估）：
//   - error           → 立即短路，返回 IsError=true
//   - HookActionDeny  → 立即拒绝，跳过后续 hook 和 AfterExecute
//   - HookActionAsk   → 调用 context 中的 ApprovalFunc；无 ApprovalFunc 时视为 Allow
//   - HookActionAllow → 继续
func (r *HookRegistry) Execute(ctx context.Context, call schema.ToolCall) schema.ToolResult {
	executed := 0

	for _, h := range r.hooks {
		newCtx, dec, err := h.BeforeExecute(ctx, call)
		if err != nil {
			return schema.ToolResult{
				ToolCallID: call.ID,
				Output:     err.Error(),
				IsError:    true,
			}
		}
		switch dec.Action {
		case HookActionDeny:
			return schema.ToolResult{
				ToolCallID: call.ID,
				Output:     dec.Reason,
				IsError:    true,
			}
		case HookActionAllow:
			// 规则显式放行（如白名单命中），标记为 explicitly-allowed。
			// 使用独立 key 区别于用户实时审批（withApproved），保留两类来源的可追溯性。
			// 不 fallthrough，Go switch 默认不 fallthrough。
			// 应用 ModifiedArgs（若 hook 同时携带了参数重写，例如路径沙箱归一化）。
			if dec.ModifiedArgs != nil {
				call.Arguments = dec.ModifiedArgs
			}
			newCtx = withExplicitlyAllowed(newCtx)
		case HookActionAsk:
			// 若此工具调用已被人类审批或规则显式放行，直接视为 Allow，不再弹出对话框。
			if isApproved(newCtx) || isExplicitlyAllowed(newCtx) {
				if dec.ModifiedArgs != nil {
					call.Arguments = dec.ModifiedArgs
				}
				break
			}
			if fn := ApprovalFnFromContext(newCtx); fn != nil {
				resp := fn(newCtx, call, dec.Reason, dec.RiskLevel)
				if !resp.Approved {
					reason := "操作被用户拒绝"
					if resp.Feedback != "" {
						reason = fmt.Sprintf("操作被用户拒绝: %s", resp.Feedback)
					}
					return schema.ToolResult{
						ToolCallID: call.ID,
						Output:     reason,
						IsError:    true,
					}
				}
				if dec.ModifiedArgs != nil {
					call.Arguments = dec.ModifiedArgs
				}
				// 标记此工具调用已被批准，后续 hook 无需再次询问
				newCtx = withApproved(newCtx)
			}
			// 无 ApprovalFunc 时视为 Allow，继续执行
		}
		ctx = newCtx
		executed++
	}

	result := r.inner.Execute(ctx, call)

	for i := executed - 1; i >= 0; i-- {
		result = r.hooks[i].AfterExecute(ctx, call, result)
	}
	return result
}
