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

// TracingProvider 包装 LLMProvider，为每次调用创建 OTEL Span 并写入完整的
// input/output/token/model 属性，使 Langfuse 能展示对话内容和 Token 用量。
// 实现 provider.LLMProvider 接口，对引擎层完全透明。
type TracingProvider struct {
	inner          provider.LLMProvider
	tracer         trace.Tracer
	modelName      string // gen_ai.request.model 属性值，来自启动配置
	llmDuration    metric.Float64Histogram
	tokensInTotal  metric.Int64Counter
	tokensOutTotal metric.Int64Counter
}

// NewTracingProvider 构造 TracingProvider，初始化三个 metrics 仪器。
// modelName 对应 gen_ai.request.model 属性，用于 Langfuse 模型展示。
func NewTracingProvider(inner provider.LLMProvider, p *Providers, modelName string) (*TracingProvider, error) {
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
		modelName:      modelName,
		llmDuration:    llmDuration,
		tokensInTotal:  tokensInTotal,
		tokensOutTotal: tokensOutTotal,
	}, nil
}

// Generate 阻塞式 LLM 调用，用 SpanLLMRequest Span 包裹完整请求周期。
// 在 Span 上写入：
//   - langfuse.input  = 序列化的消息列表（Langfuse Input 字段）
//   - langfuse.output = LLM 响应文本或工具调用（Langfuse Output 字段）
//   - gen_ai.request.model / gen_ai.usage.input_tokens / gen_ai.usage.output_tokens
func (p *TracingProvider) Generate(ctx context.Context, messages []schema.Message, tools []schema.ToolDefinition) (*schema.Message, *schema.Usage, error) {
	ctx, span := p.tracer.Start(ctx, SpanLLMRequest)
	defer span.End()

	p.setInputAttrs(span, messages)

	start := time.Now()
	msg, usage, err := p.inner.Generate(ctx, messages, tools)
	dur := time.Since(start).Seconds()

	if msg != nil && err == nil {
		span.SetAttributes(attribute.String(AttrLangfuseObsOutput, serializeOutput(msg)))
	}
	p.recordMetrics(ctx, span, usage, dur, err)
	return msg, usage, err
}

// GenerateStream 流式 LLM 调用，Span 在 channel 关闭后结束。
// langfuse.input 在流开始前写入；langfuse.output 在 StreamChunkDone 后写入。
func (p *TracingProvider) GenerateStream(ctx context.Context, messages []schema.Message, tools []schema.ToolDefinition) (<-chan schema.StreamChunk, error) {
	ctx, span := p.tracer.Start(ctx, SpanLLMRequest)
	start := time.Now()

	p.setInputAttrs(span, messages)

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
		var lastMsg *schema.Message
		for chunk := range ch {
			if chunk.Type == schema.StreamChunkDone {
				lastMsg = chunk.Message
				if chunk.Usage != nil {
					lastUsage = chunk.Usage
				}
			}
			wrapped <- chunk
		}
		if lastMsg != nil {
			span.SetAttributes(attribute.String(AttrLangfuseObsOutput, serializeOutput(lastMsg)))
		}
		p.recordMetrics(ctx, span, lastUsage, time.Since(start).Seconds(), nil)
	}()
	return wrapped, nil
}

// setInputAttrs 在 Span 上写入 langfuse.observation.input 和模型相关属性。
// Langfuse v4 要求 observation 级别的 input 使用 langfuse.observation.input。
func (p *TracingProvider) setInputAttrs(span trace.Span, messages []schema.Message) {
	span.SetAttributes(attribute.String(AttrLangfuseObsInput, serializeMessages(messages)))
	if p.modelName != "" {
		span.SetAttributes(attribute.String(AttrGenAIRequestModel, p.modelName))
	}
}

// recordMetrics 将耗时、token 用量写入 Span 属性和 OTEL Metrics。
// token 用量同时写入 harness9 自定义属性和 GenAI 语义约定属性，确保 Langfuse 正确识别。
func (p *TracingProvider) recordMetrics(ctx context.Context, span trace.Span, usage *schema.Usage, dur float64, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.String(AttrErrorMsg, err.Error()))
	}
	p.llmDuration.Record(ctx, dur)
	if usage != nil {
		span.SetAttributes(
			// harness9 内部属性（用于 OTEL Metrics 维度）
			attribute.Int(AttrInputTokens, usage.InputTokens),
			attribute.Int(AttrOutputTokens, usage.OutputTokens),
			// GenAI 语义约定属性（Langfuse 用于 Token 用量展示与费用估算）
			attribute.Int(AttrGenAIInputTokens, usage.InputTokens),
			attribute.Int(AttrGenAIOutputTokens, usage.OutputTokens),
		)
		p.tokensInTotal.Add(ctx, int64(usage.InputTokens))
		p.tokensOutTotal.Add(ctx, int64(usage.OutputTokens))
	}
}
