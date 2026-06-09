package observability_test

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/harness9/internal/observability"
)

func TestOTELEngineObserver_LifecycleNoPanic(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()

	obs, err := observability.NewOTELEngineObserver(p)
	if err != nil {
		t.Fatalf("NewOTELEngineObserver: %v", err)
	}

	ctx := context.Background()
	ctx = obs.OnInteractionStart(ctx, "session-123", "hello")

	ctx = obs.OnTurnStart(ctx, 1)
	obs.OnTurnEnd(ctx, 1, false)

	ctx = obs.OnTurnStart(ctx, 2)
	obs.OnTurnEnd(ctx, 2, true)

	obs.OnInteractionEnd(ctx, 2, nil)
	// noop tracer 不 panic 即视为通过
}

func TestOTELEngineObserver_ErrorPropagation(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()
	obs, _ := observability.NewOTELEngineObserver(p)

	ctx := obs.OnInteractionStart(context.Background(), "s1", "task")
	obs.OnInteractionEnd(ctx, 0, fmt.Errorf("test error"))
	// noop tracer 对 RecordError 静默，不 panic
}

func TestOTELEngineObserver_NilSpanSafety(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()
	obs, err := observability.NewOTELEngineObserver(p)
	if err != nil {
		t.Fatalf("NewOTELEngineObserver: %v", err)
	}

	// 直接调用 End 方法，ctx 中没有存储 Span（模拟未调用 OnInteractionStart 的情况）
	obs.OnInteractionEnd(context.Background(), 0, nil)
	obs.OnTurnEnd(context.Background(), 0, false)
	// 不 panic 即通过
}

// TestOTELEngineObserver_SpanLinkage 用真实 SpanRecorder 验证 interaction/turn/llm_request
// 三层 span 的 trace_id 相同且父子关系正确。这是 Langfuse 分组显示的核心前提。
func TestOTELEngineObserver_SpanLinkage(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	tracer := tp.Tracer("harness9-test")

	p := &observability.Providers{
		Tracer:   tracer,
		Meter:    otel.GetMeterProvider().Meter("harness9-test"),
		Shutdown: func(_ context.Context) error { return nil },
	}

	obs, err := observability.NewOTELEngineObserver(p)
	if err != nil {
		t.Fatalf("NewOTELEngineObserver: %v", err)
	}

	// ---- 模拟 runLoop 行为 ----
	ctx := context.Background()
	ctx = obs.OnInteractionStart(ctx, "sess-1", "test prompt")

	// Turn 1：模拟 LLM 调用（TracingProvider 会在 turnCtx 上再开一个 llm_request span）
	turnCtx := obs.OnTurnStart(ctx, 1)
	_, llmSpan := tracer.Start(turnCtx, "harness9.llm_request") // 模拟 TracingProvider
	llmSpan.End()
	obs.OnTurnEnd(turnCtx, 1, false)

	obs.OnInteractionEnd(ctx, 1, nil)

	// ---- 验证 ----
	spans := sr.Ended()
	if len(spans) < 3 {
		t.Fatalf("期望至少 3 个 span，实际得到 %d 个", len(spans))
	}

	// 找到各层 span（ReadOnlySpan interface）
	var interactionSpan, turnSpan, llmReqSpan sdktrace.ReadOnlySpan
	for _, s := range spans {
		switch s.Name() {
		case "harness9.interaction":
			interactionSpan = s
		case "harness9.turn":
			turnSpan = s
		case "harness9.llm_request":
			llmReqSpan = s
		}
	}

	if interactionSpan == nil {
		t.Fatal("未找到 harness9.interaction span")
	}
	if turnSpan == nil {
		t.Fatal("未找到 harness9.turn span")
	}
	if llmReqSpan == nil {
		t.Fatal("未找到 harness9.llm_request span")
	}

	traceID := interactionSpan.SpanContext().TraceID()

	// 三个 span 必须共享同一个 trace_id（Langfuse 分组的依据）
	if turnSpan.SpanContext().TraceID() != traceID {
		t.Errorf("turn span 的 trace_id (%s) 与 interaction span (%s) 不一致",
			turnSpan.SpanContext().TraceID(), traceID)
	}
	if llmReqSpan.SpanContext().TraceID() != traceID {
		t.Errorf("llm_request span 的 trace_id (%s) 与 interaction span (%s) 不一致",
			llmReqSpan.SpanContext().TraceID(), traceID)
	}

	// 父子关系：turn → interaction，llm_request → turn
	if turnSpan.Parent().SpanID() != interactionSpan.SpanContext().SpanID() {
		t.Errorf("turn span 的 parent_span_id (%s) 应指向 interaction span (%s)",
			turnSpan.Parent().SpanID(), interactionSpan.SpanContext().SpanID())
	}
	if llmReqSpan.Parent().SpanID() != turnSpan.SpanContext().SpanID() {
		t.Errorf("llm_request span 的 parent_span_id (%s) 应指向 turn span (%s)",
			llmReqSpan.Parent().SpanID(), turnSpan.SpanContext().SpanID())
	}

	t.Logf("✅ 所有 span 共享 trace_id=%s，父子关系正确", traceID)
}

func TestOTELEngineObserver_Multipleturns(t *testing.T) {
	otel.SetTracerProvider(noop.NewTracerProvider())
	p := observability.NewNoopProviders()
	obs, err := observability.NewOTELEngineObserver(p)
	if err != nil {
		t.Fatalf("NewOTELEngineObserver: %v", err)
	}

	ctx := obs.OnInteractionStart(context.Background(), "session-multi", "multi-turn test")

	const numTurns = 5
	for i := 1; i <= numTurns; i++ {
		turnCtx := obs.OnTurnStart(ctx, i)
		obs.OnTurnEnd(turnCtx, i, i%2 == 0)
	}

	obs.OnInteractionEnd(ctx, numTurns, nil)
}
