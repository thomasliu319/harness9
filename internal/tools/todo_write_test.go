package tools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/harness9/internal/planning"
	"github.com/harness9/internal/tools"
)

func TestTodoWriteTool_Name(t *testing.T) {
	store := planning.NewTodoStore()
	tool := tools.NewTodoWriteTool(store)
	if tool.Name() != "todo_write" {
		t.Errorf("Name() = %q, want todo_write", tool.Name())
	}
}

func TestTodoWriteTool_Write(t *testing.T) {
	store := planning.NewTodoStore()
	tool := tools.NewTodoWriteTool(store)

	args, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "1", "content": "step one", "status": "pending"},
			{"id": "2", "content": "step two", "status": "in_progress"},
		},
	})

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	// Result should be JSON of the current list
	var got []planning.TodoItem
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("result not valid JSON: %v — got %q", err, result)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 items, got %d", len(got))
	}
	if got[0].ID != "1" || got[1].ID != "2" {
		t.Errorf("unexpected items: %+v", got)
	}

	// Store should be updated
	stored := store.Read()
	if len(stored) != 2 {
		t.Fatalf("store has %d items, want 2", len(stored))
	}
}

func TestTodoWriteTool_Read_WhenNoTodos(t *testing.T) {
	store := planning.NewTodoStore()
	tool := tools.NewTodoWriteTool(store)

	// Omit todos field → read current (empty) list
	args, _ := json.Marshal(map[string]interface{}{})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	// Should return "[]" for empty list
	var got []planning.TodoItem
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("result not valid JSON: %v — got %q", err, result)
	}
	if len(got) != 0 {
		t.Errorf("want empty list, got %+v", got)
	}
}

func TestTodoWriteTool_Write_Replaces(t *testing.T) {
	store := planning.NewTodoStore()
	tool := tools.NewTodoWriteTool(store)

	first, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "1", "content": "old", "status": "pending"},
		},
	})
	tool.Execute(context.Background(), first) //nolint:errcheck

	second, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "2", "content": "new", "status": "in_progress"},
		},
	})
	tool.Execute(context.Background(), second) //nolint:errcheck

	stored := store.Read()
	if len(stored) != 1 || stored[0].ID != "2" {
		t.Errorf("second Write should replace first: %+v", stored)
	}
}

func TestTodoWriteTool_InvalidJSON(t *testing.T) {
	store := planning.NewTodoStore()
	tool := tools.NewTodoWriteTool(store)

	_, err := tool.Execute(context.Background(), []byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// TestTodoWriteTool_PendingToCompleted 验证 pending→completed 被拒绝（LLM 作弊防护）。
func TestTodoWriteTool_PendingToCompleted(t *testing.T) {
	store := planning.NewTodoStore()
	tool := tools.NewTodoWriteTool(store)

	// 初始化：两个 pending 任务
	init, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "1", "content": "task one", "status": "pending"},
			{"id": "2", "content": "task two", "status": "pending"},
		},
	})
	if _, err := tool.Execute(context.Background(), init); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// 尝试直接将 pending 标记为 completed（绕过 in_progress）
	cheat, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "1", "content": "task one", "status": "completed"},
			{"id": "2", "content": "task two", "status": "completed"},
		},
	})
	_, err := tool.Execute(context.Background(), cheat)
	if err == nil {
		t.Error("expected error when jumping pending→completed, got nil")
	}

	// store 应保持未变
	stored := store.Read()
	for _, item := range stored {
		if item.Status == planning.TodoCompleted {
			t.Errorf("store should not have completed items after rejected write, got %+v", stored)
		}
	}
}

// TestTodoWriteTool_InProgressToCompleted 验证 in_progress→completed 允许通过。
func TestTodoWriteTool_InProgressToCompleted(t *testing.T) {
	store := planning.NewTodoStore()
	tool := tools.NewTodoWriteTool(store)

	// 初始化：item1 in_progress
	init, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "1", "content": "task one", "status": "in_progress"},
		},
	})
	if _, err := tool.Execute(context.Background(), init); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// in_progress → completed 合法
	complete, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "1", "content": "task one", "status": "completed"},
		},
	})
	if _, err := tool.Execute(context.Background(), complete); err != nil {
		t.Errorf("in_progress→completed should be allowed, got error: %v", err)
	}
}

// TestTodoWriteTool_NewItemCompleted 验证新条目不能直接创建为 completed。
func TestTodoWriteTool_NewItemCompleted(t *testing.T) {
	store := planning.NewTodoStore()
	tool := tools.NewTodoWriteTool(store)

	args, _ := json.Marshal(map[string]interface{}{
		"todos": []map[string]string{
			{"id": "1", "content": "brand new", "status": "completed"},
		},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Error("expected error when creating new item as completed, got nil")
	}
}
