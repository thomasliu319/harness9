package evals_test

import (
	"context"
	"errors"
	"testing"

	"github.com/harness9/internal/evals"
	"github.com/harness9/internal/schema"
)

func TestScriptedProvider_ReturnsScriptedTurns(t *testing.T) {
	p := evals.NewScriptedProvider(
		evals.ScriptedTurn{Text: "first"},
		evals.ScriptedTurn{Text: "second"},
	)
	ctx := context.Background()

	msg1, _, _ := p.Generate(ctx, nil, nil)
	if msg1.Content != "first" {
		t.Errorf("turn 1: want 'first', got %q", msg1.Content)
	}
	msg2, _, _ := p.Generate(ctx, nil, nil)
	if msg2.Content != "second" {
		t.Errorf("turn 2: want 'second', got %q", msg2.Content)
	}
	// 超出序列后自然终止
	msg3, _, _ := p.Generate(ctx, nil, nil)
	if msg3 == nil || msg3.Content == "" {
		t.Error("turn 3: expected non-empty default reply after exhausting turns")
	}
}

func TestScriptedProvider_RecordsCalls(t *testing.T) {
	p := evals.NewScriptedProvider(evals.ScriptedTurn{Text: "ok"})
	p.Generate(context.Background(), []schema.Message{{Role: schema.RoleUser, Content: "hi"}}, nil)
	if len(p.Calls()) != 1 {
		t.Errorf("expected 1 recorded call, got %d", len(p.Calls()))
	}
}

func TestScriptedProvider_ToolCalls(t *testing.T) {
	tc := evals.MakeToolCall("id1", "bash", `{"command":"ls"}`)
	p := evals.NewScriptedProvider(
		evals.ScriptedTurn{ToolCalls: []schema.ToolCall{tc}},
		evals.ScriptedTurn{Text: "done"},
	)
	msg, _, _ := p.Generate(context.Background(), nil, nil)
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "bash" {
		t.Errorf("expected bash tool call, got %+v", msg.ToolCalls)
	}
}

func TestScriptedProvider_ErrorTurn(t *testing.T) {
	p := evals.NewScriptedProvider(
		evals.ScriptedTurn{Err: errors.New("llm error")},
	)
	_, _, err := p.Generate(context.Background(), nil, nil)
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestScriptedProvider_TurnIndex(t *testing.T) {
	p := evals.NewScriptedProvider(
		evals.ScriptedTurn{Text: "a"},
		evals.ScriptedTurn{Text: "b"},
	)
	p.Generate(context.Background(), nil, nil)
	if p.TurnIndex() != 1 {
		t.Errorf("expected TurnIndex=1, got %d", p.TurnIndex())
	}
}

func TestScriptedProvider_Reset(t *testing.T) {
	p := evals.NewScriptedProvider(evals.ScriptedTurn{Text: "hello"})
	p.Generate(context.Background(), nil, nil)
	p.Reset()
	if p.TurnIndex() != 0 {
		t.Errorf("expected TurnIndex=0 after Reset, got %d", p.TurnIndex())
	}
}
