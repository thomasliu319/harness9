package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/harness9/internal/planning"
	"github.com/harness9/internal/schema"
)

// TodoWriteTool 允许 LLM 维护当前会话的任务列表。
// 传入 todos 时全量替换；省略 todos 时读取当前列表。
type TodoWriteTool struct {
	store *planning.TodoStore
}

// NewTodoWriteTool 创建绑定到指定 TodoStore 的工具实例。
func NewTodoWriteTool(store *planning.TodoStore) *TodoWriteTool {
	return &TodoWriteTool{store: store}
}

func (t *TodoWriteTool) Name() string { return "todo_write" }

func (t *TodoWriteTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name: "todo_write",
		Description: "维护当前会话的任务清单。" +
			"提供 todos 数组时全量替换（atomic replace）；省略 todos 时读取当前列表。\n" +
			"当任务涉及 3 个或以上独立步骤时，在开始前调用此工具记录任务列表，" +
			"并在每完成一步后立即更新对应条目的 status 为 in_progress 或 completed。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"todos": map[string]interface{}{
					"type":        "array",
					"description": "完整的任务列表（全量替换）。省略此字段则仅读取当前列表。",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"id":      map[string]interface{}{"type": "string"},
							"content": map[string]interface{}{"type": "string"},
							"status": map[string]interface{}{
								"type": "string",
								"enum": []string{"pending", "in_progress", "completed", "cancelled"},
							},
						},
						"required": []string{"id", "content", "status"},
					},
				},
			},
		},
	}
}

type todoWriteArgs struct {
	Todos []planning.TodoItem `json:"todos"`
}

func (t *TodoWriteTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var input todoWriteArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	var current []planning.TodoItem
	if len(input.Todos) > 0 {
		current = t.store.Write(input.Todos)
	} else {
		current = t.store.Read()
	}

	if current == nil {
		current = []planning.TodoItem{}
	}

	b, err := json.Marshal(current)
	if err != nil {
		return "", fmt.Errorf("序列化任务列表失败: %w", err)
	}
	return string(b), nil
}
