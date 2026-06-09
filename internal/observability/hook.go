package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/schema"
)

// obsSpanKey 是存储 Span 的 context key 私有类型，避免与其他包键冲突。
type obsSpanKey struct{}

// obsStartKey 是存储工具调用开始时间的 context key 私有类型。
type obsStartKey struct{}

// ObservabilityHook 实现 hooks.ToolHook 接口，为每次工具调用创建 OTEL Span 并记录 Metrics。
//
// BeforeExecute 启动 Span 并将 Span 与开始时间写入 context；
// AfterExecute 结束 Span，记录 MetricToolDuration（histogram）和 MetricToolCalls（counter）。
type ObservabilityHook struct {
	tracer         trace.Tracer
	toolDuration   metric.Float64Histogram
	toolCallsTotal metric.Int64Counter
}

// NewObservabilityHook 初始化 metrics 仪器，返回可用的 ObservabilityHook。
func NewObservabilityHook(p *Providers) (*ObservabilityHook, error) {
	toolDuration, err := p.Meter.Float64Histogram(
		MetricToolDuration,
		metric.WithDescription("工具执行耗时（秒）"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 %s histogram 失败: %w", MetricToolDuration, err)
	}

	toolCallsTotal, err := p.Meter.Int64Counter(
		MetricToolCalls,
		metric.WithDescription("工具调用次数（按工具名与状态分组）"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 %s counter 失败: %w", MetricToolCalls, err)
	}

	return &ObservabilityHook{
		tracer:         p.Tracer,
		toolDuration:   toolDuration,
		toolCallsTotal: toolCallsTotal,
	}, nil
}

// BeforeExecute 启动工具执行 Span，写入工具名和参数（langfuse.input），注入开始时间。
// 始终返回 HookActionAllow，不拦截工具执行。
func (h *ObservabilityHook) BeforeExecute(ctx context.Context, tc schema.ToolCall) (context.Context, hooks.HookDecision, error) {
	ctx, _ = h.tracer.Start(ctx, SpanToolExecution,
		trace.WithAttributes(attribute.String(AttrToolName, tc.Name)),
	)
	// langfuse.observation.input：工具调用参数 JSON，Langfuse v4 observation 级别 Input 字段。
	span := trace.SpanFromContext(ctx)
	if len(tc.Arguments) > 0 {
		span.SetAttributes(attribute.String(AttrLangfuseObsInput, truncateAttr(string(tc.Arguments))))
	}
	ctx = context.WithValue(ctx, obsStartKey{}, time.Now())
	return ctx, hooks.Allow(), nil
}

// AfterExecute 结束 Span，写入工具结果（langfuse.output），记录耗时与次数 metrics。
// 若 result.IsError 为 true，在 Span 上记录错误属性。
// 透传 result，不修改工具执行结果。
func (h *ObservabilityHook) AfterExecute(ctx context.Context, tc schema.ToolCall, result schema.ToolResult) schema.ToolResult {
	span := trace.SpanFromContext(ctx)

	// langfuse.observation.output：工具执行结果，Langfuse v4 observation 级别 Output 字段。
	span.SetAttributes(attribute.String(AttrLangfuseObsOutput, truncateAttr(result.Output)))

	// 计算耗时
	var elapsed float64
	if start, ok := ctx.Value(obsStartKey{}).(time.Time); ok {
		elapsed = time.Since(start).Seconds()
	}

	// 确定状态
	status := "ok"
	if result.IsError {
		status = "error"
		span.RecordError(fmt.Errorf("tool %s error: %s", tc.Name, result.Output))
	}

	// 设置 Span 属性并结束
	span.SetAttributes(
		attribute.String(AttrToolName, tc.Name),
		attribute.Bool(AttrToolSuccess, !result.IsError),
	)
	span.End()

	// 记录 metrics，带工具名和状态维度
	attrSet := attribute.NewSet(
		attribute.String(AttrToolName, tc.Name),
		attribute.String("tool.status", status),
	)
	h.toolDuration.Record(ctx, elapsed, metric.WithAttributeSet(attrSet))
	h.toolCallsTotal.Add(ctx, 1, metric.WithAttributeSet(attrSet))

	return result
}
