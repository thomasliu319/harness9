# Test · Eval · Observability

harness9 的测试、评估与可观测体系——三个相互独立但协同工作的子系统，共同回答一个核心问题：**这个 Agent 是否真的在正确地工作？**

---

## 一、核心思想与设计哲学

### 1.1 为什么 Agent 需要专门的测试体系？

传统软件的单元测试假设：给定相同输入，总得到相同输出。Agent 系统打破了这一假设——LLM 的输出是非确定性的，同一个 prompt 在不同温度、不同会话下可能产生截然不同的行为路径。

这带来三个核心挑战：

| 挑战 | 传统测试的失效原因 | harness9 的解法 |
|------|-----------------|----------------|
| **非确定性** | Mock 不了真实 LLM 行为 | `ScriptedProvider` 将行为脚本化，使测试确定可重复 |
| **行为验证** | 断言返回值不够，要验证「做了什么」 | `recordingHook` + `Assertion` 框架验证工具调用轨迹 |
| **性能退化** | 没有 baseline 就发现不了退化 | 黄金数据集 + CI Quality Gate，每次 PR 自动对比 |

### 1.2 三层可观测性金字塔

```
          ┌─────────────────────────────┐
          │   Observability（可观测）    │  ← 生产环境：OTEL Traces + Metrics
          │   看清 Agent 在做什么        │
          └──────────────┬──────────────┘
                         │
          ┌──────────────▼──────────────┐
          │   Eval（评估）               │  ← CI/CD：黄金数据集 Quality Gate
          │   量化 Agent 能力边界        │
          └──────────────┬──────────────┘
                         │
          ┌──────────────▼──────────────┐
          │   Test（测试）               │  ← 开发阶段：ScriptedProvider + Assertion
          │   验证 Agent 行为的正确性    │
          └─────────────────────────────┘
```

**Test** 在开发阶段运行，使用确定性 mock 验证具体行为路径。
**Eval** 在 CI 阶段运行，使用黄金数据集检测能力退化。
**Observability** 在生产阶段运行，持续收集真实链路数据供分析和告警。

### 1.3 非侵入设计原则

harness9 的核心引擎（engine/provider/hooks）不感知任何测试或可观测逻辑。所有能力通过三个已有的扩展点无缝接入：

```
┌─────────────────────────────────────────────────────────────┐
│                     AgentEngine（核心引擎）                   │
│                                                              │
│  EngineObserver ← 唯一新增接口（4 处生命周期回调）            │
│       ↑                                                      │
│  [接入点 1]                                                   │
└──────────────────────────────────────────────────────────────┘
         ↑ WithEngineObserver 注入
         │
┌────────┴──────────┐   ┌─────────────────────────┐
│ OTELEngineObserver│   │   TracingProvider        │
│ Interaction Span  │   │ 包装 LLMProvider [接入点2]│
│ Turn Span         │   │ LLM Request Span          │
└───────────────────┘   └──────────────────────────┘
                                   ↑ 替换原始 provider
         ┌─────────────────────────┘
         │
┌────────┴──────────┐
│ ObservabilityHook │
│ 实现 ToolHook     │
│ Tool Span [接入点3]│
└───────────────────┘
         ↑ 注册到 HookRegistry（洋葱模型）
```

---

## 二、Test 子系统：确定性评估框架

### 2.1 架构总览

```
internal/evals/
├── provider.go      ScriptedProvider — 确定性 LLM mock
├── assertions.go    Assertion 接口 + Case/Result 类型 + 8 种断言
├── harness.go       RunCase / Suite / recordingHook
├── testenv.go       SetupHermeticEnv — CI 隔离环境
├── report.go        SuiteReport / BuildReport / WriteJSON / WriteMarkdown
└── dataset/
    ├── tool_calling_test.go   工具调用准确性（4 用例）
    ├── planning_test.go       Planning 完成率（2 用例）
    └── memory_test.go         Memory 持久化（2 用例）
```

### 2.2 ScriptedProvider：把 LLM 行为脚本化

`ScriptedProvider` 是 eval 框架的基石。它实现 `provider.LLMProvider` 接口，按预设的 `ScriptedTurn` 序列返回确定性回复，不发起任何网络请求。

```go
// 脚本：第一轮发起 bash 工具调用，第二轮返回文本结论
p := evals.NewScriptedProvider(
    evals.ScriptedTurn{
        ToolCalls: []schema.ToolCall{
            evals.MakeToolCall("tc1", "bash", `{"command":"ls -la"}`),
        },
    },
    evals.ScriptedTurn{Text: "目录中有 3 个文件。"},
)
```

**设计要点：**

| 机制 | 说明 |
|------|------|
| **Turn 序列** | 每次 `Generate` 消费一个 `ScriptedTurn`，耗尽后返回默认终止回复 |
| **录制调用** | 所有 LLM 调用都被记录到 `calls []RecordedCall`，供 Assertion 验证 |
| **Err 注入** | `ScriptedTurn{Err: err}` 模拟 LLM API 失败，测试引擎自愈能力 |
| **线程安全** | 内部互斥锁，goroutine 并发调用无竞争 |

### 2.3 Assertion 框架：验证行为轨迹

断言分为 **Hard**（失败则 Case 不通过）和 **Soft**（仅记警告，用于效率指标）两类：

```
Assertion
├── Hard Assertions（失败 → Passed=false）
│   ├── ToolCalledAssertion      工具被调用 >= N 次
│   ├── ToolNotCalledAssertion   工具一次都没被调用
│   ├── OutputContainsAssertion  最终输出包含期望字符串
│   ├── OutputExcludesAssertion  最终输出不含禁止字符串
│   ├── NoErrorAssertion         RunError == nil
│   └── ErrorAssertion           RunError != nil（测试错误路径）
└── Soft Assertions（失败 → Warnings，不影响 Passed）
    ├── MaxTurnsAssertion         Turn 数 <= Max（效率告警）
    └── MaxToolCallsAssertion     工具调用次数 <= Max（效率告警）
```

**关键设计**：`recordingHook` 在 `HookRegistry` 的 `BeforeExecute` 阶段记录工具名——在 registry 查找之前触发，无论工具是否实际存在都能正确记录 ScriptedProvider 预设的工具调用序列。

### 2.4 EvalHarness：最小化引擎环境

`RunCase` 为每个 Case 构建一个完全隔离的最小化 `AgentEngine`：

```
RunCase(c *Case) Result
    │
    ├── 创建临时工作目录（c.WorkDir 为空时）
    ├── 注册基础工具（read_file / write_file / bash / edit_file）
    ├── 挂载 recordingHook（记录工具名）
    ├── engine.NewAgentEngine(c.Provider, hookReg, workDir, WithMaxTurns(50))
    │       ← 使用 ScriptedProvider，不持久化 Session，不启用压缩
    ├── eng.Run(ctx, c.Prompt)
    └── 逐一执行 c.Assertions → 聚合 Failures / Warnings → Result.Passed
```

**不使用 Session 和 Compactor** 是 eval 的关键决策——排除持久化和压缩带来的非确定性，保证相同脚本总产生相同结果。

### 2.5 Hermetic 测试隔离

```go
func TestMyFeature(t *testing.T) {
    evals.SetupHermeticEnv(t)  // 必须首先调用
    // ...
}
```

`SetupHermeticEnv` 在测试开始时清除所有 `_API_KEY`、`_TOKEN`、`_SECRET` 后缀的环境变量，并设置 `HARNESS9_EVAL_HERMETIC=1`。这防止 eval 测试因环境中存在真实 API Key 而意外调用付费服务，也保证本地与 CI 环境完全一致（仿 HermesAgent 的 Hermetic 隔离模式）。

### 2.6 编写自定义 Eval 用例

```go
func TestPlanningQuality(t *testing.T) {
    evals.SetupHermeticEnv(t)

    c := &evals.Case{
        ID:       "planning/three_steps",
        Category: "planning",
        Prompt:   "帮我制定一个三步实现计划",
        Provider: evals.NewScriptedProvider(
            // Turn 1：LLM 调用 todo_write 写入计划
            evals.ScriptedTurn{
                ToolCalls: []schema.ToolCall{
                    evals.MakeToolCall("tc1", "todo_write", `{"todos":[
                        {"id":"1","content":"分析需求","status":"pending"},
                        {"id":"2","content":"实现功能","status":"pending"},
                        {"id":"3","content":"编写测试","status":"pending"}
                    ]}`),
                },
            },
            // Turn 2：LLM 输出结论
            evals.ScriptedTurn{Text: "已生成包含 3 个步骤的实现计划。"},
        ),
        Assertions: []evals.Assertion{
            &evals.ToolCalledAssertion{ToolName: "todo_write"},
            &evals.ToolNotCalledAssertion{ToolName: "write_file"}, // 规划阶段不应写文件
            &evals.NoErrorAssertion{},
            &evals.MaxTurnsAssertion{Max: 3}, // soft：效率告警
        },
    }

    result := evals.RunCase(context.Background(), c)
    if !result.Passed {
        for _, f := range result.Failures {
            t.Errorf("❌ %s", f.Error())
        }
    }
    for _, w := range result.Warnings {
        t.Logf("⚠️ %s", w.Error())
    }
}
```

---

## 三、Observability 子系统：OpenTelemetry 链路追踪

### 3.1 Span 层次结构与数据流

harness9 的每一次 Agent 运行产生一棵完整的 Span 树：

```
harness9.interaction   [session.id="abc123"]
│   duration: 12.4s
│
├── harness9.turn   [agent.turn=1]
│   │   duration: 3.2s
│   │
│   ├── harness9.llm_request   [llm.tokens.input=4821, llm.tokens.output=312]
│   │       duration: 2.1s
│   │
│   ├── harness9.tool   [tool.name="bash", tool.success=true]
│   │       duration: 0.8s
│   │
│   └── harness9.tool   [tool.name="read_file", tool.success=true]
│           duration: 0.1s
│
└── harness9.turn   [agent.turn=2]
    │   duration: 2.8s  (turn.has_tool_calls=false)
    │
    └── harness9.llm_request   [llm.tokens.input=5134, llm.tokens.output=89]
            duration: 2.6s
```

每个 Span 携带的属性：

| Span | 关键属性 |
|------|---------|
| `harness9.interaction` | `session.id`，总 `agent.turn` 数 |
| `harness9.turn` | `agent.turn`（轮次编号），`turn.has_tool_calls` |
| `harness9.llm_request` | `llm.tokens.input`，`llm.tokens.output`，失败时 `error.message` |
| `harness9.tool` | `tool.name`，`tool.success`，失败时 `error.message` |

### 3.2 三组件实现原理

#### OTELEngineObserver — Interaction + Turn Span

`EngineObserver` 是 harness9 为可观测性新增的唯一引擎接口。`runLoop` 在 4 个生命周期点回调它：

```
runLoop 入口
  → OnInteractionStart(ctx, sessionID, prompt)
    返回携带 interaction Span 的增强 ctx
  
  for 每个 Turn:
    → OnTurnStart(ctx, turn)
        返回携带 turn Span 的增强 turnCtx
      em.generate(turnCtx, ...)       ← LLM 调用继承 turn Span
      e.executeTools(turnCtx, ...)    ← 工具执行继承 turn Span
    → OnTurnEnd(turnCtx, turn, hasToolCalls)
  
runLoop 退出（defer 保证任何路径均执行）
  → OnInteractionEnd(ctx, turns, err)
```

**ctx 继承链**是 OTEL 自动嵌套 Span 的关键：`turnCtx` 携带 turn Span，当 `TracingProvider.Generate` 在 `turnCtx` 上调用 `tracer.Start(turnCtx, SpanLLMRequest)` 时，OTEL 自动将新 Span 设为 turn Span 的子节点。

```go
// observer.go — 核心实现摘要
func (o *OTELEngineObserver) OnInteractionStart(ctx context.Context, sessionID, prompt string) context.Context {
    ctx, span := o.tracer.Start(ctx, SpanInteraction,
        trace.WithAttributes(attribute.String(AttrSessionID, sessionID)),
    )
    return context.WithValue(ctx, interactionSpanKey{}, span)  // 注入 ctx
}

func (o *OTELEngineObserver) OnTurnStart(ctx context.Context, turn int) context.Context {
    ctx, span := o.tracer.Start(ctx, SpanTurn, ...)  // 自动成为 interaction 的子 Span
    return context.WithValue(ctx, turnSpanKey{}, span)
}
```

#### TracingProvider — LLM Request Span

`TracingProvider` 是 `provider.LLMProvider` 的装饰器，引擎无需做任何改动：

```go
// provider.go — Generate 调用链
func (p *TracingProvider) Generate(ctx context.Context, ...) {
    ctx, span := p.tracer.Start(ctx, SpanLLMRequest)  // ctx 携带 turnCtx 的 Span，自动嵌套
    defer span.End()
    
    msg, usage, err := p.inner.Generate(ctx, ...)     // 委托给真实 provider
    
    // 记录 Token Metrics
    p.llmDuration.Record(ctx, elapsed)
    p.tokensInTotal.Add(ctx, int64(usage.InputTokens))
    p.tokensOutTotal.Add(ctx, int64(usage.OutputTokens))
    // 设置 Span 属性
    span.SetAttributes(attribute.Int(AttrInputTokens, usage.InputTokens), ...)
}
```

`GenerateStream` 在 goroutine 中监听 channel，从 `StreamChunkDone` 中提取 Usage，在 channel 关闭时结束 Span。

#### ObservabilityHook — Tool Execution Span

`ObservabilityHook` 实现 `hooks.ToolHook` 接口，注册到 `HookRegistry` 的洋葱模型中：

```go
// hook.go — BeforeExecute / AfterExecute
func (h *ObservabilityHook) BeforeExecute(ctx context.Context, tc schema.ToolCall) (...) {
    ctx, span := h.tracer.Start(ctx, SpanToolExecution,
        trace.WithAttributes(attribute.String(AttrToolName, tc.Name)),
    )
    ctx = context.WithValue(ctx, obsSpanKey{}, span)      // 保存 Span
    ctx = context.WithValue(ctx, obsStartKey{}, time.Now()) // 保存开始时间
    return ctx, hooks.Allow(), nil
}

func (h *ObservabilityHook) AfterExecute(ctx context.Context, tc schema.ToolCall, result schema.ToolResult) schema.ToolResult {
    span := ctx.Value(obsSpanKey{}).(trace.Span)
    span.SetAttributes(attribute.Bool(AttrToolSuccess, !result.IsError))
    if result.IsError {
        span.RecordError(errors.New(result.Output))
    }
    span.End()
    
    // 记录 Tool Metrics（by name + status）
    h.toolCallsTotal.Add(ctx, 1, metric.WithAttributeSet(...))
    h.toolDuration.Record(ctx, elapsed, ...)
    
    return result  // 原样透传，不修改工具结果
}
```

### 3.3 Metrics 体系

harness9 定义了 6 个关键指标，覆盖 LLM 调用、工具执行和 Agent 行为三个维度：

| 指标名 | 类型 | 维度 | 说明 |
|--------|------|------|------|
| `harness9.llm.request.duration` | Histogram | — | LLM API 请求延迟（秒），P50/P95/P99 分析慢查询 |
| `harness9.llm.tokens.input` | Counter | — | 累计输入 Token，直接对应 API 费用 |
| `harness9.llm.tokens.output` | Counter | — | 累计输出 Token |
| `harness9.tool.calls.total` | Counter | `tool.name`, `status` | 工具调用次数，按工具名和成功/失败分维度 |
| `harness9.tool.execution.duration` | Histogram | `tool.name`, `status` | 工具执行耗时，识别慢工具 |
| `harness9.agent.turns.total` | Counter | — | Agent Turn 总数，衡量整体任务复杂度 |

### 3.4 OTEL SDK 初始化

harness9 通过 `observability.Setup(ctx, cfg)` 一次性完成 TracerProvider 和 MeterProvider 的初始化：

```
Setup(ctx, cfg)
├── cfg.Enabled=false 或 ExporterNoop → 立即返回 noopProviders()（零开销）
├── ExporterStdout → stdouttrace + stdoutmetric（本地调试）
└── ExporterOTLP   → otlptracehttp + otlpmetrichttp（生产 / Langfuse / Grafana）
        ↓
    otel.SetTracerProvider(tp)
    otel.SetMeterProvider(mp)
    otel.SetTextMapPropagator(TraceContext + Baggage)  ← W3C 标准传播
        ↓
    返回 *Providers{ Tracer, Meter, Shutdown }
```

`Providers.Shutdown` 应在进程退出时调用（`defer`），确保 buffer 中的 Span 都被 flush。

---

## 四、CI/CD 集成：自动化质量门控

### 4.1 流水线设计

```
PR 触发（push to master / pull_request）
       │
       ▼
  unit-tests job
  └── go test ./...  ← 全量单元测试，包含 observability + evals
       │
       ▼ needs: unit-tests
  eval job（Quality Gate）
  ├── 环境：OPENAI_API_KEY=""  ANTHROPIC_API_KEY=""  HARNESS9_EVAL_HERMETIC=1
  ├── go test ./internal/evals/... ./internal/evals/dataset/... -v
  ├── 结果上传为 Artifact（保留 30 天）
  └── 摘要写入 GitHub Step Summary
```

**Hermetic 环境**：CI 中清除所有 API Key，强制使用 `ScriptedProvider`，保证：
1. 不产生任何真实 LLM API 费用
2. 测试结果完全确定，无随机波动
3. 任何行为退化都来自代码变更而非 LLM 更新

**Quality Gate**：eval job 的 `continue-on-error` 设为 `false`——eval 失败则 CI 失败，PR 无法合并。

### 4.2 当前黄金数据集

共 8 个用例，按三个维度组织：

| 类别 | 用例 | 验证目标 |
|------|------|---------|
| `tool_calling` | `bash_basic` | bash 工具被正确调用 |
| `tool_calling` | `read_file` | read_file 工具被正确调用 |
| `tool_calling` | `write_then_read` | 多工具顺序调用（write → read） |
| `tool_calling` | `no_tool_conversation` | 纯对话不触发工具调用 |
| `planning` | `plan_generated` | todo_write 写入计划 |
| `planning` | `no_write_in_plan_mode` | 规划阶段不调用 write_file/edit_file |
| `memory` | `write_memory` | memory_write 工具被调用 |
| `memory` | `search_memory` | memory_search 工具被调用 |

扩展数据集只需在 `internal/evals/dataset/` 下新增 `_test.go` 文件，无需修改框架代码。

### 4.3 运行方式

```bash
# 运行全量 eval（包含黄金数据集）
go test ./internal/evals/... ./internal/evals/dataset/... -v

# 只运行特定类别
go test ./internal/evals/dataset/... -v -run TestToolCalling
go test ./internal/evals/dataset/... -v -run TestPlanning
go test ./internal/evals/dataset/... -v -run TestMemory

# 生成 JSON + Markdown 报告（自定义脚本中调用）
results := suite.Run(ctx)
report := evals.BuildReport(results)
evals.WriteJSON(report, "eval-report.json")
evals.WriteMarkdown(report, "eval-report.md")
```

---

## 五、可观测性配置与接入指南

### 5.1 配置一览

| 环境变量 | 默认值 | 说明 |
|---------|--------|------|
| `OTEL_ENABLED` | `false` | `true` 启用，其他值关闭（默认 noop 零开销） |
| `OTEL_SERVICE_NAME` | `harness9` | 服务名，在 Trace 平台上标识来源 |
| `OTEL_EXPORTER_TYPE` | `noop` | `noop` / `stdout` / `otlp` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | OTLP HTTP 端点，`otlp` 时必填 |

### 5.2 接入 Langfuse（推荐）

[Langfuse](https://langfuse.com) 是专为 LLM 应用设计的可观测平台，原生支持 OpenTelemetry，提供 Trace 可视化、Token 费用追踪、用户行为分析。

```bash
export OTEL_ENABLED=true
export OTEL_EXPORTER_TYPE=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=https://cloud.langfuse.com/api/public/otel
# Langfuse 通过 HTTP Basic Auth 认证，将 Public Key:Secret Key 编码为 Base64 传入
# 可在 OTEL_EXPORTER_OTLP_HEADERS 中设置（按平台文档配置）
harness9
```

### 5.3 接入 Jaeger（本地开发）

```bash
# 启动 Jaeger（all-in-one 模式，含 OTLP HTTP 接收端和 Web UI）
docker run --rm -p 16686:16686 -p 4318:4318 jaegertracing/all-in-one

export OTEL_ENABLED=true
export OTEL_EXPORTER_TYPE=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
harness9

# 打开 http://localhost:16686 → 搜索 Service: harness9 查看完整 Trace
```

### 5.4 接入 Grafana + Tempo

```bash
# Tempo 接收 OTLP，Grafana 展示 Trace
export OTEL_ENABLED=true
export OTEL_EXPORTER_TYPE=otlp
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318  # Tempo OTLP HTTP 端口
harness9
```

### 5.5 本地调试（stdout 导出器）

无需任何外部服务，直接将 Trace 数据打印到 stderr：

```bash
export OTEL_ENABLED=true
export OTEL_EXPORTER_TYPE=stdout
harness9
```

输出示例（JSON 格式，每个 Span 单独一行）：
```json
{
  "Name": "harness9.llm_request",
  "Attributes": [
    {"Key": "llm.tokens.input", "Value": {"Int64Value": 4821}},
    {"Key": "llm.tokens.output", "Value": {"Int64Value": 312}}
  ],
  "StartTime": "2026-06-09T10:23:41Z",
  "EndTime": "2026-06-09T10:23:43Z"
}
```

---

## 六、模块文件索引

| 文件 | 包 | 职责 |
|------|----|------|
| `internal/engine/observer.go` | `engine` | `EngineObserver` 接口 + `noopObserver` |
| `internal/observability/config.go` | `observability` | `Config` + `ConfigFromEnv()` |
| `internal/observability/attributes.go` | `observability` | Span 名称 + Metric 名称 + 属性键常量 |
| `internal/observability/setup.go` | `observability` | OTEL SDK 初始化（`Setup` + `NewNoopProviders`） |
| `internal/observability/observer.go` | `observability` | `OTELEngineObserver`（Interaction/Turn Span） |
| `internal/observability/provider.go` | `observability` | `TracingProvider`（LLM Request Span） |
| `internal/observability/hook.go` | `observability` | `ObservabilityHook`（Tool Execution Span） |
| `internal/evals/provider.go` | `evals` | `ScriptedProvider`（确定性 mock） |
| `internal/evals/assertions.go` | `evals` | `Assertion` 接口 + 8 种断言 + `Case`/`Result` 类型 |
| `internal/evals/harness.go` | `evals` | `RunCase` / `Suite` / `recordingHook` |
| `internal/evals/testenv.go` | `evals` | `SetupHermeticEnv()` |
| `internal/evals/report.go` | `evals` | `BuildReport` / `WriteJSON` / `WriteMarkdown` |
| `internal/evals/dataset/*.go` | `dataset` | 黄金数据集（8 个用例） |
| `.github/workflows/eval.yml` | CI | GitHub Actions Quality Gate |
