// Package hooks — HookDecision、ApprovalFunc 和 Human-in-the-Loop 审批类型。
// 本文件定义工具拦截器的决策机制（allow/deny/ask）以及人类审批（Human-in-the-Loop）的回调与上下文注入接口。
package hooks

import (
	"context"
	"encoding/json"

	"github.com/harness9/internal/schema"
)

// HookAction 表示 BeforeExecute 的决策类型。
type HookAction string

const (
	// HookActionAllow 允许工具执行，继续调用后续 hook。
	HookActionAllow HookAction = "allow"
	// HookActionDeny 立即拒绝工具执行，返回 IsError=true，跳过后续 hook。
	HookActionDeny HookAction = "deny"
	// HookActionAsk 要求人类审批；若 context 无 ApprovalFunc 则视为 allow。
	HookActionAsk HookAction = "ask"
)

// HookDecision 是 BeforeExecute 返回的结构化决策。
type HookDecision struct {
	Action       HookAction
	Reason       string          // 向用户和 LLM 展示的原因（Deny/Ask 时填写）
	RiskLevel    string          // "low" | "medium" | "high"（驱动 TUI 审批界面配色）
	ModifiedArgs json.RawMessage // 可选：hook 修改后的工具参数（路径重写等）
}

// Allow 返回默认允许决策。
func Allow() HookDecision { return HookDecision{Action: HookActionAllow} }

// Deny 返回拒绝决策，携带原因。
func Deny(reason string) HookDecision {
	return HookDecision{Action: HookActionDeny, Reason: reason}
}

// Ask 返回审批决策，携带原因和风险级别。
func Ask(reason, riskLevel string) HookDecision {
	return HookDecision{Action: HookActionAsk, Reason: reason, RiskLevel: riskLevel}
}

// ApprovalResponse 是用户对审批请求的回复。
type ApprovalResponse struct {
	Approved bool   // true = 允许执行
	Feedback string // 拒绝时携带的反馈文字，回传给 LLM 以辅助自适应调整
	Remember bool   // true = 将此决策写入白名单配置（"总是允许"）
}

// ApprovalFunc 是 Human-in-the-Loop 审批回调，由引擎注入 context，hook 从 context 中提取并调用。
// 调用者（engine）保证 tc 不为 nil；实现者可无限期阻塞等待用户响应。
type ApprovalFunc func(ctx context.Context, tc schema.ToolCall, reason, riskLevel string) ApprovalResponse

type approvalContextKey struct{}

// WithApprovalFn 将 ApprovalFunc 注入 context，供 HookRegistry 在 BeforeExecute 后调用。
func WithApprovalFn(ctx context.Context, fn ApprovalFunc) context.Context {
	return context.WithValue(ctx, approvalContextKey{}, fn)
}

// ApprovalFnFromContext 从 context 中提取 ApprovalFunc，未设置时返回 nil。
func ApprovalFnFromContext(ctx context.Context) ApprovalFunc {
	fn, _ := ctx.Value(approvalContextKey{}).(ApprovalFunc)
	return fn
}

type approvedContextKey struct{}

// withApproved 在 context 中标记当前工具调用已由前置 hook 经过人类审批（用户点击"允许"）。
// 后续的 HookActionAsk hook 检查到此标记后直接跳过，避免对同一次工具调用重复弹出审批对话框。
func withApproved(ctx context.Context) context.Context {
	return context.WithValue(ctx, approvedContextKey{}, true)
}

// isApproved 报告当前工具调用是否已在前置 hook 中获得人类审批。
func isApproved(ctx context.Context) bool {
	v, _ := ctx.Value(approvedContextKey{}).(bool)
	return v
}

type explicitlyAllowedContextKey struct{}

// withExplicitlyAllowed 在 context 中标记当前工具调用已被前置 hook 规则显式放行（如白名单命中）。
// 语义上区别于 withApproved（人类实时审批）：此处是规则系统的静默放行，无需人类介入。
// 后续 HookActionAsk hook 遇到此标记同样跳过审批，避免白名单写入后仍被危险模式拦截的问题。
func withExplicitlyAllowed(ctx context.Context) context.Context {
	return context.WithValue(ctx, explicitlyAllowedContextKey{}, true)
}

// isExplicitlyAllowed 报告当前工具调用是否已被前置 hook 规则显式放行。
func isExplicitlyAllowed(ctx context.Context) bool {
	v, _ := ctx.Value(explicitlyAllowedContextKey{}).(bool)
	return v
}
