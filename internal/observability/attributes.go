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
