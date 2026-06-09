package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/harness9/internal/provider"
	"github.com/harness9/internal/schema"
)

// TracingProvider 包装 LLMProvider，为每次调用创建 OTEL Span 并记录 Token Metrics。
// 实现 provider.LLMProvider 接口，对引擎层完全透明。
type TracingProvider struct {
	inner          provider.LLMProvider
	tracer         trace.Tracer
	llmDuration    metric.Float64Histogram
	tokensInTotal  metric.Int64Counter
	tokensOutTotal metric.Int64Counter
}

// NewTracingProvider 构造 TracingProvider，初始化三个 metrics 仪器：
//   - MetricLLMDuration（histogram，秒）
//   - MetricTokensInput（counter）
//   - MetricTokensOutput（counter）
func NewTracingProvider(inner provider.LLMProvider, p *Providers) (*TracingProvider, error) {
	llmDuration, err := p.Meter.Float64Histogram(
		MetricLLMDuration,
		metric.WithDescription("LLM 请求耗时（秒）"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 %s histogram 失败: %w", MetricLLMDuration, err)
	}

	tokensInTotal, err := p.Meter.Int64Counter(
		MetricTokensInput,
		metric.WithDescription("LLM 输入 token 累计消耗量"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 %s counter 失败: %w", MetricTokensInput, err)
	}

	tokensOutTotal, err := p.Meter.Int64Counter(
		MetricTokensOutput,
		metric.WithDescription("LLM 输出 token 累计生成量"),
	)
	if err != nil {
		return nil, fmt.Errorf("创建 %s counter 失败: %w", MetricTokensOutput, err)
	}

	return &TracingProvider{
		inner:          inner,
		tracer:         p.Tracer,
		llmDuration:    llmDuration,
		tokensInTotal:  tokensInTotal,
		tokensOutTotal: tokensOutTotal,
	}, nil
}

// Generate 阻塞式 LLM 调用，用 SpanLLMRequest Span 包裹完整请求周期。
// Span 在 inner.Generate 返回后立即结束，并记录 token 用量 metrics。
func (p *TracingProvider) Generate(ctx context.Context, messages []schema.Message, tools []schema.ToolDefinition) (*schema.Message, *schema.Usage, error) {
	ctx, span := p.tracer.Start(ctx, SpanLLMRequest)
	defer span.End()

	start := time.Now()
	msg, usage, err := p.inner.Generate(ctx, messages, tools)
	p.recordMetrics(ctx, span, usage, time.Since(start).Seconds(), err)
	return msg, usage, err
}

// GenerateStream 流式 LLM 调用，Span 在 channel 关闭后结束。
// goroutine 从 inner channel 消费 chunk，从 StreamChunkDone 中提取 Usage，
// 最终在 channel 关闭时调用 recordMetrics 并结束 Span。
func (p *TracingProvider) GenerateStream(ctx context.Context, messages []schema.Message, tools []schema.ToolDefinition) (<-chan schema.StreamChunk, error) {
	ctx, span := p.tracer.Start(ctx, SpanLLMRequest)
	start := time.Now()

	ch, err := p.inner.GenerateStream(ctx, messages, tools)
	if err != nil {
		span.RecordError(err)
		span.End()
		return nil, err
	}

	wrapped := make(chan schema.StreamChunk, 8)
	go func() {
		defer close(wrapped)
		defer span.End()

		var lastUsage *schema.Usage
		for chunk := range ch {
			// 从 Done chunk 提取 Usage
			if chunk.Type == schema.StreamChunkDone && chunk.Usage != nil {
				lastUsage = chunk.Usage
			}
			wrapped <- chunk
		}
		p.recordMetrics(ctx, span, lastUsage, time.Since(start).Seconds(), nil)
	}()
	return wrapped, nil
}

// recordMetrics 将耗时、token 用量记录到 Span 属性和 OTEL Metrics 中。
// 若 err 非 nil，在 Span 上记录错误并设置 AttrErrorMsg 属性。
func (p *TracingProvider) recordMetrics(ctx context.Context, span trace.Span, usage *schema.Usage, dur float64, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.String(AttrErrorMsg, err.Error()))
	}
	p.llmDuration.Record(ctx, dur)
	if usage != nil {
		span.SetAttributes(
			attribute.Int(AttrInputTokens, usage.InputTokens),
			attribute.Int(AttrOutputTokens, usage.OutputTokens),
		)
		p.tokensInTotal.Add(ctx, int64(usage.InputTokens))
		p.tokensOutTotal.Add(ctx, int64(usage.OutputTokens))
	}
}
