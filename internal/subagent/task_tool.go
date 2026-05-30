package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/harness9/internal/schema"
)

// TaskTool 实现 tools.BaseTool（结构类型隐式满足，无需 import tools）。
// 由 main.go 注册进父代理 registry，是主代理委派子代理的唯一入口。
type TaskTool struct {
	reg     *Registry
	runner  *Runner
	mailbox *Mailbox
	idSeq   int // 后台任务 ID 序号
}

// NewTaskTool 创建 task 工具。
func NewTaskTool(reg *Registry, runner *Runner, mailbox *Mailbox) *TaskTool {
	return &TaskTool{reg: reg, runner: runner, mailbox: mailbox}
}

// Name 返回工具标识符 "task"。
func (t *TaskTool) Name() string { return "task" }

// Definition 动态生成工具定义：subagent_type 枚举所有已注册子代理，
// description 拼接各子代理用途，作为 LLM 的调度依据。
func (t *TaskTool) Definition() schema.ToolDefinition {
	defs := t.reg.List()
	names := make([]string, 0, len(defs))
	var sb strings.Builder
	sb.WriteString("把一个边界清晰的任务委派给专门的子代理执行。子代理拥有独立上下文与受限工具集。\n可用子代理：\n")
	for _, d := range defs {
		names = append(names, d.Name)
		fmt.Fprintf(&sb, "- %s: %s\n", d.Name, d.Description)
	}

	return schema.ToolDefinition{
		Name:        "task",
		Description: sb.String(),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"subagent_type": map[string]any{
					"type":        "string",
					"enum":        names,
					"description": "要调用的子代理类型名称",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "任务的简短标题（3-5 词，用于 UI 展示）",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "传给子代理的完整任务描述。子代理看不到主对话历史，所有必要信息（文件路径、背景、要求）都要写在这里。",
				},
				"background": map[string]any{
					"type":        "boolean",
					"description": "是否后台异步运行。true 时立即返回，结果稍后注入；false（默认）阻塞直到完成。",
				},
			},
			"required": []string{"subagent_type", "prompt"},
		},
	}
}

type taskArgs struct {
	SubAgentType string `json:"subagent_type"`
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
	Background   bool   `json:"background"`
}

// Execute 解析参数、查找子代理定义并执行（前台阻塞 / 后台异步）。
func (t *TaskTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a taskArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if a.SubAgentType == "" {
		return "", fmt.Errorf("subagent_type 不能为空")
	}
	if a.Prompt == "" {
		return "", fmt.Errorf("prompt 不能为空")
	}
	def, ok := t.reg.Get(a.SubAgentType)
	if !ok {
		return "", fmt.Errorf("未知子代理类型 %q，可用: %s", a.SubAgentType, t.agentNames())
	}

	if a.Background {
		t.idSeq++
		taskID := fmt.Sprintf("task-%s-%d", def.Name, t.idSeq)
		go func() {
			res, err := t.runner.Run(ctx, def, a.Prompt, true)
			ct := CompletedTask{TaskID: taskID, AgentName: def.Name}
			if err != nil {
				ct.IsError = true
				ct.FinalText = err.Error()
			} else {
				ct.FinalText = res.FinalText
			}
			t.mailbox.Deliver(ct)
		}()
		return fmt.Sprintf(`<task id=%q state="running"/>`, taskID), nil
	}

	res, err := t.runner.Run(ctx, def, a.Prompt, false)
	if err != nil {
		return fmt.Sprintf(`<task state="error">%s</task>`, err.Error()), nil
	}
	return fmt.Sprintf(`<task state="completed"><task_result>%s</task_result></task>`, res.FinalText), nil
}

func (t *TaskTool) agentNames() string {
	defs := t.reg.List()
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return strings.Join(names, ", ")
}
