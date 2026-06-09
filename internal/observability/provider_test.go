package observability_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/harness9/internal/observability"
	"github.com/harness9/internal/provider/providertest"
	"github.com/harness9/internal/schema"
)

// errorProvider 是仅用于测试的最小 LLMProvider 实现，Generate 和 GenerateStream 始终返回错误。
type errorProvider struct{ err error }

func (e *errorProvider) Generate(_ context.Context, _ []schema.Message, _ []schema.ToolDefinition) (*schema.Message, *schema.Usage, error) {
	return nil, nil, e.err
}
func (e *errorProvider) GenerateStream(_ context.Context, _ []schema.Message, _ []schema.ToolDefinition) (<-chan schema.StreamChunk, error) {
	return nil, e.err
}

func TestTracingProvider_Generate_Success(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()

	// MockWithCallback 返回固定内容的 assistant 消息
	mock := providertest.NewMockWithCallback(func(_ []schema.Message, _ []schema.ToolDefinition) schema.Message {
		return schema.Message{Role: schema.RoleAssistant, Content: "done"}
	})
	tp, err := observability.NewTracingProvider(mock, p, "")
	if err != nil {
		t.Fatalf("NewTracingProvider: %v", err)
	}

	msg, _, err := tp.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if msg.Content != "done" {
		t.Errorf("expected 'done', got %q", msg.Content)
	}
}

func TestTracingProvider_Generate_Error(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()

	inner := &errorProvider{err: errors.New("api error")}
	tp, err := observability.NewTracingProvider(inner, p, "")
	if err != nil {
		t.Fatalf("NewTracingProvider: %v", err)
	}

	_, _, err = tp.Generate(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "api error" {
		t.Errorf("expected 'api error', got %q", err.Error())
	}
}

func TestTracingProvider_GenerateStream_Success(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()

	mock := providertest.NewMockWithCallback(func(_ []schema.Message, _ []schema.ToolDefinition) schema.Message {
		return schema.Message{Role: schema.RoleAssistant, Content: "streamed"}
	})
	tp, err := observability.NewTracingProvider(mock, p, "")
	if err != nil {
		t.Fatalf("NewTracingProvider: %v", err)
	}

	ch, err := tp.GenerateStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}

	var gotContent string
	var gotDone bool
	for chunk := range ch {
		switch chunk.Type {
		case schema.StreamChunkTextDelta:
			gotContent += chunk.Delta
		case schema.StreamChunkDone:
			gotDone = true
		}
	}
	if !gotDone {
		t.Error("expected StreamChunkDone, not received")
	}
	if gotContent != "streamed" {
		t.Errorf("expected 'streamed', got %q", gotContent)
	}
}

func TestTracingProvider_GenerateStream_Error(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()

	inner := &errorProvider{err: errors.New("stream error")}
	tp, err := observability.NewTracingProvider(inner, p, "")
	if err != nil {
		t.Fatalf("NewTracingProvider: %v", err)
	}

	_, err = tp.GenerateStream(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "stream error" {
		t.Errorf("expected 'stream error', got %q", err.Error())
	}
}

func TestTracingProvider_Generate_WithUsage(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()

	// MockWithCallback 不返回 Usage，但我们可以用自定义 inner 验证 usage 路径
	inner := &usageProvider{
		msg:   schema.Message{Role: schema.RoleAssistant, Content: "ok"},
		usage: &schema.Usage{InputTokens: 100, OutputTokens: 50},
	}
	tp, err := observability.NewTracingProvider(inner, p, "")
	if err != nil {
		t.Fatalf("NewTracingProvider: %v", err)
	}

	msg, usage, err := tp.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if msg.Content != "ok" {
		t.Errorf("expected 'ok', got %q", msg.Content)
	}
	if usage == nil {
		t.Fatal("expected non-nil usage")
	}
	if usage.InputTokens != 100 || usage.OutputTokens != 50 {
		t.Errorf("usage mismatch: got %+v", usage)
	}
}

// usageProvider 是携带 Usage 的测试 stub。
type usageProvider struct {
	msg   schema.Message
	usage *schema.Usage
}

func (u *usageProvider) Generate(_ context.Context, _ []schema.Message, _ []schema.ToolDefinition) (*schema.Message, *schema.Usage, error) {
	return &u.msg, u.usage, nil
}

func (u *usageProvider) GenerateStream(ctx context.Context, _ []schema.Message, _ []schema.ToolDefinition) (<-chan schema.StreamChunk, error) {
	ch := make(chan schema.StreamChunk, 2)
	go func() {
		defer close(ch)
		if u.msg.Content != "" {
			select {
			case <-ctx.Done():
				return
			case ch <- schema.StreamChunk{Type: schema.StreamChunkTextDelta, Delta: u.msg.Content}:
			}
		}
		select {
		case <-ctx.Done():
		case ch <- schema.StreamChunk{Type: schema.StreamChunkDone, Message: &u.msg, Usage: u.usage}:
		}
	}()
	return ch, nil
}
