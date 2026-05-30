package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/harness9/internal/memory"
	"github.com/harness9/internal/provider"
	"github.com/harness9/internal/provider/providertest"
	"github.com/harness9/internal/schema"
)

func newTaskToolForTest(t *testing.T, p provider.LLMProvider) *TaskTool {
	t.Helper()
	reg := NewRegistry()
	_ = reg.Register(SubAgentDefinition{Name: "reviewer", Description: "审查代码", SystemPrompt: "p"})
	runner := &Runner{
		workDir:         t.TempDir(),
		defaultMaxTurns: 5,
		providerFor: func(string) (provider.LLMProvider, int, error) {
			return p, 128_000, nil
		},
		compactorFor: func(provider.LLMProvider, int) memory.Compactor { return nil },
		baseCtx:      context.Background(),
	}
	return NewTaskTool(reg, runner, NewMailbox())
}

func TestTaskToolDefinitionEnumeratesAgents(t *testing.T) {
	tt := newTaskToolForTest(t, providertest.NewMock())
	def := tt.Definition()
	if def.Name != "task" {
		t.Fatalf("Name=%q", def.Name)
	}
	blob, _ := json.Marshal(def.InputSchema)
	if !strings.Contains(string(blob), "reviewer") {
		t.Errorf("schema 应枚举 reviewer: %s", blob)
	}
	if !strings.Contains(def.Description, "审查代码") {
		t.Errorf("description 应含子代理用途: %s", def.Description)
	}
}

func TestTaskToolForegroundReturnsResult(t *testing.T) {
	mock := providertest.NewMockWithCallback(func(_ []schema.Message, _ []schema.ToolDefinition) schema.Message {
		return schema.Message{Role: schema.RoleAssistant, Content: "REVIEW-DONE"}
	})
	tt := newTaskToolForTest(t, mock)
	args, _ := json.Marshal(map[string]any{
		"subagent_type": "reviewer", "description": "审查", "prompt": "看看 main.go",
	})
	out, err := tt.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "REVIEW-DONE") || !strings.Contains(out, "completed") {
		t.Fatalf("前台返回应含结果与 completed 状态: %s", out)
	}
}

func TestTaskToolUnknownAgent(t *testing.T) {
	tt := newTaskToolForTest(t, providertest.NewMock())
	args, _ := json.Marshal(map[string]any{
		"subagent_type": "ghost", "prompt": "x",
	})
	if _, err := tt.Execute(context.Background(), args); err == nil {
		t.Fatal("未知子代理类型应返回 error")
	}
}

func TestTaskToolBackgroundReturnsRunning(t *testing.T) {
	mock := providertest.NewMockWithCallback(func(_ []schema.Message, _ []schema.ToolDefinition) schema.Message {
		return schema.Message{Role: schema.RoleAssistant, Content: "bg"}
	})
	tt := newTaskToolForTest(t, mock)
	args, _ := json.Marshal(map[string]any{
		"subagent_type": "reviewer", "prompt": "x", "background": true,
	})
	out, err := tt.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "running") {
		t.Fatalf("后台应立即返回 running 状态: %s", out)
	}
}
