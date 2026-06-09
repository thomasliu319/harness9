package observability

// Span 名称常量（参考 Claude Agent SDK 命名规范）。
const (
	SpanInteraction   = "harness9.interaction" // 一次完整 Agent 运行
	SpanTurn          = "harness9.turn"        // 单个 ReAct Turn
	SpanLLMRequest    = "harness9.llm_request" // 单次 LLM API 调用
	SpanToolExecution = "harness9.tool"        // 工具执行
)

// Span / Metric 属性键常量。
const (
	AttrSessionID    = "session.id"
	AttrModel        = "llm.model"
	AttrInputTokens  = "llm.tokens.input"
	AttrOutputTokens = "llm.tokens.output"
	AttrTurnNumber   = "agent.turn"
	AttrToolName     = "tool.name"
	AttrToolSuccess  = "tool.success"
	AttrAgentType    = "agent.type" // "main" | "sub"
	AttrErrorMsg     = "error.message"
)

// Metric 名称常量。
const (
	MetricLLMDuration  = "harness9.llm.request.duration"    // histogram, seconds
	MetricTokensInput  = "harness9.llm.tokens.input"        // counter
	MetricTokensOutput = "harness9.llm.tokens.output"       // counter
	MetricToolCalls    = "harness9.tool.calls.total"        // counter, by name+status
	MetricToolDuration = "harness9.tool.execution.duration" // histogram, seconds
	MetricTurnsTotal   = "harness9.agent.turns.total"       // counter
)

// Langfuse 富内容属性——Langfuse OTEL ingestion 将这两个属性映射到 UI 的 Input / Output 字段。
const (
	AttrLangfuseInput  = "langfuse.input"
	AttrLangfuseOutput = "langfuse.output"
)

// GenAI 语义约定属性（OTEL 标准）——Langfuse 以这些属性识别 LLM Generation 并展示 Token 用量与模型信息。
const (
	AttrGenAISystem       = "gen_ai.system"              // LLM 提供商（openai / anthropic 等）
	AttrGenAIRequestModel = "gen_ai.request.model"       // 请求使用的模型名称
	AttrGenAIInputTokens  = "gen_ai.usage.input_tokens"  // 输入 token 数（Langfuse 用于费用估算）
	AttrGenAIOutputTokens = "gen_ai.usage.output_tokens" // 输出 token 数
)
