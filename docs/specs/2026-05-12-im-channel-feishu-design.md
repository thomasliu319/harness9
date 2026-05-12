# IM Channel 接入设计文档

**日期**: 2026-05-12  
**状态**: 已确认，待实施  
**范围**: 第一期 — 飞书长连接接入

---

## 1. 背景与目标

当前 harness9 以纯 CLI 方式运行：用户通过命令行提交 prompt，Agent 执行后将结果输出到 stdout，进程退出。

本期目标：将 harness9 接入飞书 IM 平台，使用户可直接通过飞书私聊与 Agent 交互，并在 Agent 执行过程中实时看到工具调用进度。

**核心约束**：
- 采用飞书长连接（WebSocket），无需公网 IP 或内网穿透
- 现有 `engine/`、`provider/`、`tools/`、`schema/` 包完全不动
- 预留足够扩展性，后续可接入 Slack、钉钉等其他 IM 平台

---

## 2. 关键决策

| 维度 | 决策 | 理由 |
|------|------|------|
| 会话模型 | 每条消息独立启动 AgentEngine 循环 | 无状态、易扩展，避免跨消息 context 管理复杂度 |
| 回复方式 | 事件驱动进度推送 + 单条最终回复 | 满足工具调用进度展示需求，UX 友好 |
| 工作目录 | `.env` 配置 `WORK_DIR` | 适配服务化部署，与 CLI 模式解耦 |
| 触发场景 | 仅私聊消息 | 避免群聊噪音，第一期聚焦核心链路 |
| 进度展示 | 占位消息 + PatchMessage 逐步更新 | 飞书原生支持，无消息堆积，UX 简洁 |

---

## 3. 包结构

```
internal/
  imchannel/
    channel.go      # IMChannel + Session 接口 + IncomingMessage + MessageHandler
    feishu/
      client.go     # FeishuChannel：WebSocket 长连接 + 消息收发
      session.go    # FeishuSession：占位消息管理 + 进度更新 + 最终回复
cmd/harness9/
  server.go         # Server：IMChannel ↔ RunStream 事件 ↔ Session 进度推送
  main.go           # 改造：常驻服务模式
```

新包单向依赖 `engine` 和 `schema`，不反向耦合现有任何包。

---

## 4. 接口定义

### 4.1 IMChannel

```go
// IMChannel 是 IM 平台的统一适配接口。
type IMChannel interface {
    // Start 建立与 IM 平台的连接并开始接收消息，阻塞直到 ctx 取消。
    Start(ctx context.Context) error

    // SetMessageHandler 注册用户消息到达时的回调。
    SetMessageHandler(handler MessageHandler)

    // NewSession 为一条入站消息创建独立的交互会话。
    NewSession(chatID, messageID string) Session
}
```

### 4.2 Session

```go
// Session 代表一条用户消息触发的 Agent 执行上下文的"IM 侧视图"。
type Session interface {
    // NotifyThinking 发送"思考中"占位消息（Agent 开始处理时调用）。
    NotifyThinking(ctx context.Context) error

    // NotifyToolStart 推送工具开始执行的进度。
    NotifyToolStart(ctx context.Context, tc schema.ToolCall) error

    // NotifyToolDone 推送工具执行完成的进度。
    NotifyToolDone(ctx context.Context, tc schema.ToolCall, result schema.ToolResult, d time.Duration) error

    // SendReply 发送 Agent 的最终回复（成功或错误均通过此方法）。
    SendReply(ctx context.Context, text string) error
}
```

### 4.3 消息类型

```go
// MessageHandler 是用户消息到达时的回调签名。
type MessageHandler func(ctx context.Context, msg IncomingMessage)

// IncomingMessage 代表从 IM 平台收到的一条用户消息。
type IncomingMessage struct {
    ChatID    string // 会话 ID（飞书的 chat_id）
    SenderID  string // 发送者 ID
    Text      string // 消息文本内容
    MessageID string // 平台消息 ID（用于回复线程或消息更新）
}
```

---

## 5. 飞书实现

### 5.1 SDK

官方 Go SDK：`github.com/larksuite/oapi-sdk-go/v3`，内置 WebSocket 长连接客户端，无需公网 IP。

### 5.2 FeishuChannel（client.go）

- 使用 `larkws.NewClient(appID, appSecret)` 建立 WebSocket 长连接
- 注册 `im.message.receive_v1` 事件处理器
- 过滤条件：`event.Message.ChatType == "p2p"`（仅私聊，群聊忽略）
- 解析 `text` 类型消息体，提取纯文本后调用 `MessageHandler`
- 发消息通过 `lark.NewClient(appID, appSecret).Im().Message().Create(ctx, req)` 实现
- 更新消息通过 `PatchMessage` API 实现

### 5.3 FeishuSession（session.go）

进度展示采用"占位消息 + 逐步更新"策略：

| 方法 | 飞书操作 |
|------|---------|
| `NotifyThinking` | 发送富文本消息 `🤔 思考中...`，记录返回的 `message_id` |
| `NotifyToolStart` | 调用 `PatchMessage` 追加 `🔧 调用工具：{name}` |
| `NotifyToolDone` | 调用 `PatchMessage` 更新对应行为 `✅ {name}（{duration}）` 或 `❌ {name} 失败` |
| `SendReply` | 新发一条消息作为最终回复，随后删除占位进度消息 |

进度消息使用飞书富文本（`post` 类型）以支持多行内容追加。

### 5.4 .env 新增配置

```env
# ---- 飞书 Bot ----
FEISHU_APP_ID=cli_a923905332f91bb5
FEISHU_APP_SECRET=rMN707ziN7aT0emcI7DL9dLwYgzT1kTD

# ---- Agent 工作目录 ----
WORK_DIR=/path/to/your/workspace
```

---

## 6. 编排层（server.go）

```go
type Server struct {
    channel imchannel.IMChannel
    engine  *engine.AgentEngine
}

func NewServer(ch imchannel.IMChannel, eng *engine.AgentEngine) *Server

// Start 注册消息处理器并启动 IMChannel 长连接（阻塞）
func (s *Server) Start(ctx context.Context) error
```

**handleMessage 核心逻辑**（每条消息在独立 goroutine 中执行）：

```
新建带 5 分钟 timeout 的子 context
→ channel.NewSession(chatID, messageID)
→ session.NotifyThinking()
→ engine.RunStream(ctx, text)
→ 本地维护两个 map（goroutine 私有，无需加锁）：
    toolCalls      map[toolCallID]schema.ToolCall   // EventToolStart 时写入
    toolStartTimes map[toolCallID]time.Time          // EventToolStart 时写入
→ 消费事件流：
    EventActionDelta
        → 累积最终回复文本
    EventToolStart（Data: schema.ToolCall）
        → toolCalls[tc.ID] = tc
        → toolStartTimes[tc.ID] = time.Now()
        → session.NotifyToolStart(ctx, tc)
    EventToolResult（Data: schema.ToolResult）
        → tc = toolCalls[result.ToolCallID]
        → d  = time.Since(toolStartTimes[result.ToolCallID])
        → session.NotifyToolDone(ctx, tc, result, d)
    EventDone
        → session.SendReply(ctx, accumulatedText)
    EventError
        → session.SendReply(ctx, "❌ " + errMsg)
```

> `EventToolResult` 只携带 `schema.ToolResult`（含 `ToolCallID`），不含工具名和耗时；
> Server 在 `EventToolStart` 时缓存原始 `ToolCall` 和开始时间，在 `EventToolResult` 到达时查表补全，再传给 `Session`。

---

## 7. main.go 改造

**改造前**（CLI 单次执行）：
```
读 args/stdin → 创建 engine → Run/RunStream → 输出 stdout → 退出
```

**改造后**（常驻 IM Server）：
```
加载 .env
→ 读取 WORK_DIR / FEISHU_APP_ID / FEISHU_APP_SECRET
→ 创建 LLM Provider + ToolRegistry + AgentEngine
→ 创建 FeishuChannel
→ NewServer(channel, engine)
→ server.Start(ctx)   // 阻塞，Ctrl-C 优雅退出
```

---

## 8. 数据流

```
用户飞书私聊消息
    │
    ▼
FeishuChannel（WebSocket 长连接接收）
    │  MessageHandler
    ▼
Server.handleMessage（goroutine）
    │
    ├─ session.NotifyThinking()
    │
    ├─ engine.RunStream(ctx, text)
    │      │
    │      ├─ EventToolStart  → session.NotifyToolStart()
    │      ├─ EventToolResult → session.NotifyToolDone()
    │      ├─ EventActionDelta → 累积文本
    │      └─ EventDone / EventError
    │
    └─ session.SendReply(finalText)
           │
           ▼
    FeishuChannel（调用飞书消息 API 发送）
           │
           ▼
    用户看到最终回复
```

---

## 9. 扩展性说明

- **新增 IM 平台**：实现 `IMChannel` + `Session` 接口，在 `main.go` 中替换即可，不改动 `Server` 或 `engine`。
- **富进度展示**：`Session` 接口预留了 `NotifyToolDone` 方法，后续可在飞书实现中升级为交互卡片（Interactive Card）。
- **群聊支持**：`FeishuChannel` 中增加 `chat_type == "group"` + @ 过滤即可，`IMChannel` 接口无需改动。
- **会话记忆**：如需跨消息持久化 context，在 `Server` 层添加 `sessionStore map[chatID][]schema.Message`，`IMChannel` 接口无需改动。

---

## 10. 不在本期范围内

- 群聊 @ 触发
- 跨消息会话持久化（Roadmap）
- 飞书交互卡片（Interactive Card）进度展示
- 其他 IM 平台（Slack、钉钉）
