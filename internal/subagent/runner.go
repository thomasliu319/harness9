package subagent

import (
	"context"
	"fmt"
	"time"

	"github.com/harness9/internal/engine"
	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/memory"
	"github.com/harness9/internal/permission"
	"github.com/harness9/internal/provider"
	"github.com/harness9/internal/schema"
	"github.com/harness9/internal/skills"
	"github.com/harness9/internal/tools"
)

// Runner 构建并运行子代理引擎。从 main.go 注入一次，运行期只读。
type Runner struct {
	baseTools          []tools.BaseTool // 全部基础工具实例（可安全跨引擎共享）
	sharedHooks        []hooks.ToolHook // danger + offload（permission 单独派生）
	settingsPath       string           // .harness9/settings.json（权限继承源）
	skillsIndex        *skills.Index    // 预加载 skill 正文（可为 nil）
	workDir            string
	defaultMaxTurns    int
	toolTimeout        time.Duration
	maxConcurrentTools int
	providerFor        func(model string) (provider.LLMProvider, int, error)
	compactorFor       func(p provider.LLMProvider, ctxWin int) memory.Compactor
	baseCtx            context.Context // 会话级 ctx，后台任务从此派生
}

// SubAgentResult 是子代理一次执行的结果。
type SubAgentResult struct {
	AgentID   string
	FinalText string
}

// denyTaskHook 是纵深防御 hook：始终拒绝子代理调用 task 工具（防递归）。
type denyTaskHook struct{}

func (denyTaskHook) BeforeExecute(ctx context.Context, tc schema.ToolCall) (context.Context, hooks.HookDecision, error) {
	if tc.Name == "task" {
		return ctx, hooks.Deny("子代理不允许再派生子代理"), nil
	}
	return ctx, hooks.Allow(), nil
}

func (denyTaskHook) AfterExecute(_ context.Context, _ schema.ToolCall, r schema.ToolResult) schema.ToolResult {
	return r
}

// buildChildRegistry 构造子代理的隔离工具注册表：仅注册定义允许的基础工具
// （永不含 task），再包权限派生 hook + denyTaskHook + sharedHooks（danger/offload）。
func (r *Runner) buildChildRegistry(def SubAgentDefinition) (tools.Registry, error) {
	allNames := make([]string, 0, len(r.baseTools))
	byName := make(map[string]tools.BaseTool, len(r.baseTools))
	for _, t := range r.baseTools {
		allNames = append(allNames, t.Name())
		byName[t.Name()] = t
	}
	resolved := def.ResolveTools(allNames)

	base := tools.NewRegistry()
	for _, name := range resolved {
		if t, ok := byName[name]; ok {
			if err := base.Register(t); err != nil {
				return nil, fmt.Errorf("注册子代理工具 %q 失败: %w", name, err)
			}
		}
	}

	hookChain := []hooks.ToolHook{permission.NewFileHook(r.settingsPath), denyTaskHook{}}
	hookChain = append(hookChain, r.sharedHooks...)
	return hooks.NewHookRegistry(base, hookChain...), nil
}

// Run 同步执行一个子代理：构建隔离子引擎，调用 RunStream，消费事件流，
// 转发进度、桥接审批、累积最终文本。background 控制审批策略与执行 context。
func (r *Runner) Run(ctx context.Context, def SubAgentDefinition, prompt string, background bool) (SubAgentResult, error) {
	childReg, err := r.buildChildRegistry(def)
	if err != nil {
		return SubAgentResult{}, err
	}

	p, ctxWin, err := r.providerFor(def.Model)
	if err != nil {
		return SubAgentResult{}, fmt.Errorf("解析子代理模型失败: %w", err)
	}

	var loader skillLoader
	if r.skillsIndex != nil {
		loader = r.skillsIndex.GetFullContent
	}
	spb := newPromptBuilder(def.SystemPrompt, r.workDir, def.Skills, loader)

	maxTurns := r.defaultMaxTurns
	if def.MaxTurns > 0 {
		maxTurns = def.MaxTurns
	}

	childID := fmt.Sprintf("subagent-%s", def.Name)
	childSession := memory.NewMemorySession(childID)

	opts := []engine.Option{
		engine.WithPromptBuilder(spb),
		engine.WithSession(childSession),
		engine.WithMaxTurns(maxTurns),
		engine.WithContextWindow(ctxWin),
		engine.WithToolTimeout(r.toolTimeout),
		engine.WithMaxConcurrentTools(r.maxConcurrentTools),
	}
	if comp := r.compactorFor(p, ctxWin); comp != nil {
		opts = append(opts, engine.WithCompactor(comp))
	}
	sub := engine.NewAgentEngine(p, childReg, r.workDir, opts...)

	// 执行 context：前台用调用方 ctx；后台用会话级 base ctx 派生（独立于父 turn）。
	execCtx := ctx
	if background {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithCancel(r.baseCtx)
		defer cancel()
	}

	progress := hooks.SubAgentProgressFromContext(ctx)
	emit := func(u schema.SubAgentUpdate) {
		u.AgentName = def.Name
		if progress != nil {
			progress(u)
		}
	}
	parentApproval := hooks.ApprovalFnFromContext(ctx)

	emit(schema.SubAgentUpdate{Kind: schema.SubAgentStart})

	stream, err := sub.RunStream(execCtx, prompt)
	if err != nil {
		return SubAgentResult{}, err
	}

	var currentTurnText string
	curTurn := -1
	for evt := range stream {
		switch evt.Type {
		case engine.EventActionDelta:
			if evt.Turn != curTurn {
				curTurn = evt.Turn
				currentTurnText = ""
			}
			s, _ := evt.Data.(string)
			currentTurnText += s
			emit(schema.SubAgentUpdate{Kind: schema.SubAgentDelta, Text: s})
		case engine.EventThinkingDelta:
			s, _ := evt.Data.(string)
			emit(schema.SubAgentUpdate{Kind: schema.SubAgentThinking, Text: s})
		case engine.EventToolStart:
			currentTurnText = "" // 工具前文本是中间产物，丢弃
			if tc, ok := evt.Data.(schema.ToolCall); ok {
				emit(schema.SubAgentUpdate{Kind: schema.SubAgentToolStart, ToolName: tc.Name})
			}
		case engine.EventToolResult:
			if d, ok := evt.Data.(engine.ToolResultData); ok {
				emit(schema.SubAgentUpdate{Kind: schema.SubAgentToolResult, IsError: d.Result.IsError})
			}
		case engine.EventApprovalRequired:
			req, ok := evt.Data.(engine.ApprovalRequest)
			if !ok {
				continue
			}
			if background || parentApproval == nil {
				req.ResponseCh <- hooks.ApprovalResponse{Approved: false,
					Feedback: "子代理无可用审批通道，已自动拒绝"}
			} else {
				req.ResponseCh <- parentApproval(execCtx, req.ToolCall, req.Reason, req.RiskLevel)
			}
		case engine.EventError:
			msg, _ := evt.Data.(string)
			emit(schema.SubAgentUpdate{Kind: schema.SubAgentError, Text: msg})
			return SubAgentResult{}, fmt.Errorf("子代理执行失败: %s", msg)
		case engine.EventDone:
			// 循环将随 channel 关闭自然结束。
		}
	}

	emit(schema.SubAgentUpdate{Kind: schema.SubAgentDone, Text: currentTurnText})
	return SubAgentResult{AgentID: childID, FinalText: currentTurnText}, nil
}
