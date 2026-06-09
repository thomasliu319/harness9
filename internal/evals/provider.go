// Package evals 提供 harness9 的自动化评估框架。
//
// 核心组件：
//   - ScriptedProvider：确定性 mock LLMProvider，支持脚本化响应序列
//   - EvalHarness：评估运行器，管理 Case 生命周期
//   - Assertion：断言接口，验证 Agent 行为
//   - Suite：批量运行多个 Case 并生成报告
package evals

import (
	"context"
	"fmt"
	"sync"

	"github.com/harness9/internal/schema"
)

// ScriptedTurn 代表 ScriptedProvider 在某一轮的预设回复。
type ScriptedTurn struct {
	// Text 是 LLM 的文本回复。工具调用时可为空。
	Text string
	// ToolCalls 是 LLM 发起的工具调用列表。非空时表示本轮执行工具。
	ToolCalls []schema.ToolCall
	// Err 如果非 nil，模拟 LLM 调用失败（用于测试引擎的 self-healing）。
	Err error
}

// RecordedCall 记录一次实际发生的 LLM 调用（用于 Assertion 验证）。
type RecordedCall struct {
	Messages []schema.Message
	Tools    []schema.ToolDefinition
}

// ScriptedProvider 是 LLMProvider 的确定性实现，按 Turns 序列返回预设回复。
// 所有 Turn 耗尽后，默认返回「任务完成。」文本回复（模拟自然终止）。
// 线程安全：并发调用无竞争。
type ScriptedProvider struct {
	Turns []ScriptedTurn
	mu    sync.Mutex
	idx   int
	calls []RecordedCall
}

// NewScriptedProvider 构造一个预设了响应序列的 ScriptedProvider。
func NewScriptedProvider(turns ...ScriptedTurn) *ScriptedProvider {
	return &ScriptedProvider{Turns: turns}
}

// Generate 按序列返回预设 Turn 的回复；超出序列时返回默认终止回复。
func (p *ScriptedProvider) Generate(ctx context.Context, messages []schema.Message, tools []schema.ToolDefinition) (*schema.Message, *schema.Usage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls = append(p.calls, RecordedCall{Messages: messages, Tools: tools})

	if p.idx >= len(p.Turns) {
		return &schema.Message{Role: schema.RoleAssistant, Content: "任务完成。"}, nil, nil
	}
	turn := p.Turns[p.idx]
	p.idx++

	if turn.Err != nil {
		return nil, nil, turn.Err
	}
	return &schema.Message{
		Role:      schema.RoleAssistant,
		Content:   turn.Text,
		ToolCalls: turn.ToolCalls,
	}, &schema.Usage{InputTokens: 100, OutputTokens: 50}, nil
}

// GenerateStream 委托给 Generate（eval 场景不需要流式）。
func (p *ScriptedProvider) GenerateStream(ctx context.Context, messages []schema.Message, tools []schema.ToolDefinition) (<-chan schema.StreamChunk, error) {
	msg, usage, err := p.Generate(ctx, messages, tools)
	ch := make(chan schema.StreamChunk, 2)
	if err != nil {
		ch <- schema.StreamChunk{Type: schema.StreamChunkError, Error: err.Error()}
		close(ch)
		return ch, nil
	}
	ch <- schema.StreamChunk{Type: schema.StreamChunkDone, Message: msg, Usage: usage}
	close(ch)
	return ch, nil
}

// Calls 返回所有已记录的 LLM 调用列表（线程安全）。
func (p *ScriptedProvider) Calls() []RecordedCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]RecordedCall, len(p.calls))
	copy(result, p.calls)
	return result
}

// TurnIndex 返回当前已消耗的 Turn 数量（线程安全）。
func (p *ScriptedProvider) TurnIndex() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.idx
}

// Reset 重置 provider 状态（可复用于多次运行）。
func (p *ScriptedProvider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.idx = 0
	p.calls = nil
}

// MakeToolCall 构造 schema.ToolCall 的辅助函数，简化测试数据准备。
// args 应为合法的 JSON 字符串，如 `{"command":"ls"}`。
func MakeToolCall(id, name, args string) schema.ToolCall {
	return schema.ToolCall{
		ID:        id,
		Name:      name,
		Arguments: []byte(args),
	}
}

// MakeBashCall 构造调用 bash 工具的 ToolCall。
func MakeBashCall(id, command string) schema.ToolCall {
	return MakeToolCall(id, "bash", fmt.Sprintf(`{"command":%q}`, command))
}
