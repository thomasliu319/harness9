# Human-in-the-Loop + Hooks 权限控制：主流 Agent Harness 框架深度调研

> 调研日期：2026-05-27 | 框架：DeepAgents · OpenHarness · OpenCode · OpenClaw · HermesAgent · Claude Agent SDK

---

## 1. 各框架核心设计速览（对比表格）

| 维度 | DeepAgents | OpenHarness | OpenCode | OpenClaw | HermesAgent | Claude Agent SDK |
|------|-----------|-------------|----------|----------|-------------|-----------------|
| **语言** | Python | Python | TypeScript | TypeScript | Python | Python / TypeScript |
| **权限级别** | allow / deny（FilesystemPermission） | DEFAULT / PLAN / FULL_AUTO | allow / ask / deny（三级） | allow / deny（DM pairing） | allow / block（Plugin + Guardrail） | allow / ask / deny / defer（四级） |
| **中断机制** | LangGraph interrupt_on（工具名映射 → interrupt()） | 交互式 y/n 对话框 | Effect Deferred（异步挂起） | 未确认 | 线程 flag + clarify_callback | canUseTool 回调（无限期挂起） |
| **恢复机制** | LangGraph 检查点续跑（Command(resume=...)） | /resume 会话恢复 | once/always/reject 三路回复 | 未确认 | _pending_steer 注入（软中断） | resume=session_id 跨进程恢复 |
| **状态序列化** | DeltaChannel + 检查点（可配置 SQLite/Redis） | AppStateStore（格式未确认） | InstanceState Map（内存）+ 规则写 settings.json | 未确认 | JSONL 持久化 | JSONL（~/.claude/projects/<encoded-cwd>/） |
| **Hook 模型** | 线性中间件栈（before_agent） | 事件分发（fnmatch 匹配，四种执行器） | Effect 事件总线（Bus） | 未确认 | Plugin + Guardrail 双轨 | 并行回调（deny 优先） |
| **白名单粒度** | 路径级（glob，FilesystemPermission） | 工具名 + 路径规则 + 命令正则 | Rule 数组（glob/exact） | DM allowFrom 列表 | approval patterns（函数名 + 参数） | 工具名 + 参数模式（"Bash(rm *)"） |
| **动态更新** | SubAgent interrupt_on 覆盖 | PermissionMode 切换 | always 决策自动写入规则集 | dmPolicy 配置 | steer 注入 | setPermissionMode() 流中动态切换 |
| **审批 UI** | 无（由外层框架实现） | 交互式 y/n 对话框 | Bus 事件推送前端渲染 | 无 | terminal_tool 审批 callback | canUseTool 回调（调用方实现 UI） |

---

## 2. 维度一：执行权限模式

### Claude Agent SDK（最完善）

**权限评估管线（有序 5 步）**：

```
Hooks → deny 规则 → 权限模式 → allow 规则 → canUseTool 回调
```

六种权限模式：
- `default`：不自动批准，未匹配工具触发 canUseTool
- `dontAsk`：未预批准的工具直接拒绝，不调用 canUseTool
- `acceptEdits`：自动批准文件操作（Edit/Write/mkdir/rm/mv/cp/sed）
- `bypassPermissions`：绕过所有权限检查（危险）
- `plan`：只读模式
- `auto`（TypeScript 专有）：模型分类器自动决策

工具绑定三种粒度：
- 工具名：`allowed_tools=["Read"]`
- 参数模式：`disallowed_tools=["Bash(rm *)"]`（保留工具但拒绝匹配参数）
- 完全禁用：`disallowed_tools=["Bash"]`（从工具列表移除）

> 重要警告：`allowed_tools` 不约束 `bypassPermissions`。如需在 bypassPermissions 模式下阻止特定工具，必须使用 `disallowed_tools`。

### OpenCode（三级 allow/ask/deny）

规则评估：按声明顺序，第一个匹配规则决定结果：
- 匹配 deny → 抛出 DeniedError（直接拒绝，不问用户）
- 匹配 allow 且覆盖全部 patterns → 直接通过
- 无匹配 → 创建 Deferred，发布 Bus 事件，等待用户决策

三路回复：`"once"`（本次批准）/ `"always"`（永久批准，写规则集）/ `"reject"`（可携带 feedback 文字 → CorrectedError）

### OpenHarness（三模式枚举）

```python
class PermissionMode(str, Enum):
    DEFAULT = "default"      # 写操作要求确认
    PLAN = "plan"            # 阻止所有变更操作
    FULL_AUTO = "full_auto"  # 允许一切
```

PermissionChecker 评估顺序（优先级由高到低）：
1. 硬编码敏感路径（`~/.ssh/*`、`~/.aws/credentials`、`~/.kube/config`——不可覆盖）
2. 显式拒绝列表（工具名/路径）
3. 显式允许列表
4. 路径规则（用户配置的 glob）
5. 命令模式（正则匹配危险命令）
6. 权限模式决策

### DeepAgents（双层：工具中断 + 文件系统权限）

```python
@dataclass
class FilesystemPermission:
    operations: list[FilesystemOperation]  # "read" | "write"
    paths: list[str]                       # glob 模式
    mode: Literal["allow", "deny"] = "allow"

# 按声明顺序匹配，第一个匹配规则胜出；无匹配默认允许
```

### HermesAgent（双轨制：Plugin + Guardrail）

- Plugin 轨道：`get_pre_tool_call_block_message(function_name, function_args)` 返回非 None → 阻塞
- Guardrail 轨道：`ToolGuardrailDecision`（allow / warn / block / halt）
  - warn 不阻止执行，仅提示（区别于其他框架）
  - block 阻止本次
  - halt 终止整个循环

---

## 3. 维度二：Human-in-the-Loop 实现方式

### 3.1 中断原理

**Claude Agent SDK：回调异步挂起**

```python
async def can_use_tool(tool_name, input_data, context):
    # 可无限期等待用户响应
    response = await show_ui_dialog(tool_name, input_data)
    if response == "allow":
        return PermissionResultAllow(updated_input=input_data)
    return PermissionResultDeny(message="用户拒绝")

# 特殊：defer 模式允许进程退出，稍后 resume 恢复
return {"permissionDecision": "defer"}  # Hook 返回 defer
```

`defer` 机制：hook 返回 defer → SDK 保存挂起点到 session 文件 → 进程退出 → 之后 `resume=session_id` 重新触发 canUseTool。这是解决"用户长时间未响应"问题的最优方案。

**DeepAgents：LangGraph 声明式中断（协程级暂停）**

```python
agent = create_deep_agent(
    interrupt_on={"edit_file": True}  # 每次调用 edit_file 前暂停
)
# 内部：HumanInTheLoopMiddleware → LangGraph interrupt()
# 整个 graph 执行冻结在工具调用前的节点
# 等待 Command(resume=user_decision) 注入才恢复
```

子 Agent 继承语义：
- `SubAgent`（声明式）：默认继承父的 interrupt_on，可用自身字段覆盖
- `CompiledSubAgent`：不继承，内部配置
- `AsyncSubAgent`：不继承，远程系统管理

**OpenCode：Effect Deferred 挂起**

```typescript
// 挂起：发布 Bus 事件，携带 PermissionID 和 patterns
const deferred = yield* Deferred.make<void, RejectedError>()
yield* Bus.publish(permissionRequestEvent({ id, patterns, deferred }))
yield* Deferred.await(deferred)  // 挂起在此

// 恢复：UI 层调用 reply()
// "once" → Deferred.succeed(void)
// "always" → Deferred.succeed(void) + 追加规则
// "reject" → Deferred.fail(RejectedError)
```

**OpenHarness：同步 y/n 对话框**

在工具执行前调用 `PermissionChecker.check()`：
- `requires_confirmation=True` → UI 层展示 y/n 对话框
- `allowed=False` → 直接拒绝

**HermesAgent：线程 flag + clarify_callback**

```python
# 中断 flag
agent._interrupt_requested = False

# 循环内每次工具调用前检查
if agent._interrupt_requested:
    interrupted = True
    break

# LLM 主动发起的审批（clarify 工具）
result = clarify_tool(
    question="是否确认删除生产数据？",
    choices=["是", "否"],
    callback=agent.clarify_callback  # 由 CLI/Gateway 注入
)
```

> 重要设计：approval callback 被显式传播到 ThreadPoolExecutor worker 线程，防止 prompt_toolkit raw terminal 模式下的死锁。

### 3.2 恢复原理

**Claude Agent SDK（最完善）**：

| 恢复方式 | 机制 | 适用场景 |
|---------|------|---------|
| `continue=True` | 自动续接最近会话，无需跟踪 ID | 单进程多轮对话 |
| `resume=session_id` | 恢复特定会话，完整上下文 | 进程重启、跨机器 |
| `fork_session=True` | 创建会话分支，原会话不变 | 探索不同执行路径 |

会话文件位置：`~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`（完整对话历史，仅追加）

**DeepAgents**：LangGraph `Command(resume=user_decision)` 注入，检查点存储可配置（SQLite/Redis/内存）。`_DeepAgentState` 使用 `DeltaChannel` 增量编码，将检查点开销从 O(N²) 降为 O(N)。

**OpenCode**：`"always"` 决策持久化 allow 规则，级联满足同 session 中匹配 patterns 的其他 pending 请求。

**HermesAgent**：`_pending_steer`（软恢复）—— 用户在不中断 Agent 的情况下注入指导文字，附加到下一个工具结果之后（不打破 user/assistant 角色交替）。

### 3.3 状态序列化对比

| 框架 | 格式 | 位置 | 跨进程恢复 |
|------|------|------|-----------|
| Claude Agent SDK | JSONL（完整对话历史） | `~/.claude/projects/<cwd>/` | 是 |
| DeepAgents | 检查点（LangGraph） | 可配置（SQLite/Redis） | 是 |
| OpenCode | 内存 Map + 规则文件 | 内存 + settings.json | 部分 |
| OpenHarness | AppStateStore | 本地存储 | 是 |
| HermesAgent | JSONL + 检查点 | 本地文件 | 是 |
| OpenClaw | allowlist store | 本地存储 | 是（pairing） |

---

## 4. 维度三：Hooks 拦截高危命令

### 4.1 Hook 接口设计对比

**Claude Agent SDK（并行回调，deny 优先）**：

```python
# 完整事件列表
PreToolUse / PostToolUse / PostToolUseFailure / PostToolBatch（TS）
UserPromptSubmit / Stop / SubagentStart / SubagentStop
PreCompact / PermissionRequest / Notification
SessionStart / SessionEnd（TS 专有）

# Matcher：正则匹配工具名
hooks={
    "PreToolUse": [
        HookMatcher(matcher="Write|Edit|Delete", hooks=[security_hook]),
        HookMatcher(matcher="^mcp__", hooks=[mcp_audit]),
        HookMatcher(hooks=[global_logger]),  # 无 matcher 匹配全部
    ]
}

# Hook 输出结构
return {
    "systemMessage": "系统提示（用户可见）",
    "hookSpecificOutput": {
        "hookEventName": "PreToolUse",
        "permissionDecision": "deny",       # allow / deny / ask / defer
        "permissionDecisionReason": "...",   # 反馈给 LLM 的原因
        "updatedInput": {...},               # 可选：修改工具参数
    }
}
```

并发语义：多个 Hook 并行执行，**deny 优先于 defer 优先于 ask 优先于 allow**。

**DeepAgents（线性中间件栈）**：

```python
class AgentMiddleware(ABC):
    async def before_agent(self, state: AgentState) -> dict | None:
        # 返回 None → 透传
        # 返回 dict(Overwrite(messages)) → 替换整个消息历史
```

FilesystemMiddleware 集成权限：
```python
if _check_fs_permission(rules, "read", path) == "deny":
    return ToolMessage(content=f"Error: permission denied for read on {path}")
```

**OpenHarness（事件分发，四种执行器）**：

Hook 执行器类型：
- **command**：子进程执行，支持超时和环境变量注入，fnmatch 模式匹配
- **http**：异步 HTTP POST 到配置端点
- **prompt**：通过 LLM 评估条件（解析 JSON 决策）
- **agent**：调用子 Agent 处理

`AggregatedHookResult`：任意 Hook 返回 `blocked=True` → 整体阻塞。

**HermesAgent（Plugin + Guardrail 双轨）**：

```python
# 轨道一：Plugin 系统
block_message = get_pre_tool_call_block_message(
    function_name, function_args, task_id=task_id
)
# block_message 非 None → 跳过实际工具执行

# 轨道二：内置循环检测 Guardrail
guardrail_decision = agent._tool_guardrails.before_call(
    function_name, function_args
)
# ToolGuardrailDecision: allow / warn（不阻止）/ block / halt
```

### 4.2 高危命令识别策略

| 框架 | 识别层次 | 实现 |
|------|---------|------|
| Claude Agent SDK | 参数模式 | `disallowed_tools=["Bash(rm *)"]`；Hook 内检查 `tool_input.command` |
| DeepAgents | 路径 glob + 操作类型 | `FilesystemPermission(mode="deny", paths=["/etc/*"])` |
| OpenHarness | 多层次：硬编码路径 + denied_commands 正则 + 路径规则 | 硬编码：`~/.ssh/*`等；配置：`denied_commands: ["rm -rf /"]` |
| HermesAgent | 破坏性命令启发式 + Plugin | `_is_destructive_command()` 触发 checkpointing |
| OpenCode | 规则集顺序匹配（deny 优先） | `Rule { action: "deny", patterns: ["/etc/**"] }` |
| OpenClaw | 沙箱工具白名单 | Docker 沙箱中 denied: [browser, canvas, nodes, cron] |

**OpenHarness 不可覆盖的硬编码敏感路径**（即使 FULL_AUTO 模式也生效）：
```
~/.ssh/*
~/.aws/credentials
~/.kube/config
~/.gnupg/*
~/Library/Keychains/*
```

这是**防御纵深**的最后一层，任何权限模式配置都无法绕过。

### 4.3 触发审批的流程对比

**Claude Agent SDK**：
1. Hook 返回 `ask` 或无 Hook 决策
2. 评估 allow 规则无匹配
3. 调用 `canUseTool` 回调（调用方实现任意 UI）
4. 可在 `PermissionRequest` Hook 中发送外部通知（Slack/Email）
5. 回调返回 allow/deny/修改后输入

**OpenCode**：
1. 规则评估无匹配 → 创建 Deferred + 发布 Bus 事件
2. UI 层收到事件，展示审批界面（含 patterns 列表）
3. 用户选择 once/always/reject
4. `reply()` 写入 Deferred 结果，恢复执行
5. `reject` 自动级联拒绝同 session 的所有 pending 请求

**超时处理差异**：
- Claude Agent SDK：`defer` 允许进程退出并稍后 resume（最优解）
- OpenCode：无超时，Deferred 永久等待
- HermesAgent：`_interrupt_requested` flag 强制退出等待循环

---

## 5. 维度四：命令白名单机制

### 5.1 白名单数据结构

**Claude Agent SDK**（最细粒度）：
```json
{
  "permissions": {
    "allow": [
      "Bash(git *)",
      "Bash(go test *)",
      "Read",
      "Write(src/**)"
    ],
    "deny": [
      "Bash(rm -rf *)",
      "Bash(curl * | bash)",
      "Write(/etc/*)"
    ]
  }
}
```

规则语法：裸工具名（整体控制）、参数模式（`"Bash(rm *)"`）、路径模式（`"Write(src/**)"`)

**OpenCode**（Effect Rule 数组）：
```typescript
interface Rule {
    action: "allow" | "deny"
    patterns: string[]   // glob 匹配路径列表
}
// 评估：按数组顺序，第一个匹配的 Rule 决定结果
```

**DeepAgents**（Python dataclass）：
```python
permissions = [
    FilesystemPermission(operations=["read"], paths=["/**"]),          # 允许所有读
    FilesystemPermission(operations=["write"], paths=["/etc/**"], mode="deny"),
    FilesystemPermission(operations=["write"], paths=["/tmp/**"]),      # 允许写 /tmp
]
```

**OpenHarness**（settings.json）：
```json
{
  "permission_mode": "default",
  "denied_commands": ["rm -rf /", "DROP TABLE *"],
  "path_rules": [
    {"pattern": "/etc/*", "allow": false},
    {"pattern": "/tmp/**", "allow": true}
  ]
}
```

### 5.2 动态更新机制

| 框架 | 动态更新能力 | 实现方式 |
|------|------------|---------|
| Claude Agent SDK | 最完整 | `setPermissionMode()` 流中切换；`updatedPermissions` 写 settings.local.json |
| OpenCode | 规则追加 | `"always"` 决策自动 append allow 规则到规则集 |
| DeepAgents | SubAgent 级覆盖 | 声明式 SubAgent 的 `interrupt_on` 字段覆盖父级配置 |
| OpenHarness | PermissionMode 热切换 | `app_state.permission_mode = PermissionMode.FULL_AUTO` |
| HermesAgent | 会话内 steer 注入 | `_pending_steer` 附加指导，不修改白名单本身 |

**Claude Agent SDK 的分级持久化**（最佳实践）：
- `settings.json`：项目级基线（提交 git，团队共享）
- `settings.local.json`：本地覆盖（不提交 git，个人偏好）
- 会话级：通过 `canUseTool` 返回 `updatedPermissions` 写入 local

---

## 6. 最佳实践提炼

### 6.1 权限评估管线设计原则

**关键结论**：采用有序多层级评估，deny 永远优先。

```
Hooks（最先，可修改请求/拒绝/允许）
  ↓
deny 规则（任何匹配立即拒绝，不可被权限模式绕过）
  ↓
权限模式（全局模式决策）
  ↓
allow 规则（白名单匹配）
  ↓
用户审批回调（最后手段）
```

Claude Agent SDK 明确规定：即使在 `bypassPermissions` 模式下，通过 `disallowed_tools=["Bash(rm *)"]` 设置的参数模式 deny 规则仍然生效。这是"绝对安全边界"的关键设计。

### 6.2 状态序列化选择

**最佳实践**：JSONL（JSON Lines）格式

优点：追加写入 O(1)、每行独立不互相依赖、人类可读、便于调试、跨进程只需传 session_id。

关键设计要点：
- 会话文件路径包含 cwd（避免跨目录混淆）
- 工具调用和结果成对存储（支持孤立工具对检测和修复）
- 元数据与内容分离

### 6.3 Hook 系统设计原则

1. **并行执行，deny 优先**：多个 Hook 同时运行，任意 deny 阻止操作
2. **副作用 Hook 异步化**：日志/通知 Hook 不需要阻塞主流程
3. **Hook 返回原因**：`permissionDecisionReason` 让 LLM 了解被拒绝的原因，可自适应调整策略
4. **Hook 可修改输入**：`updatedInput` 允许路径重写、参数清洗等（例如将所有写操作重定向到 sandbox 目录）
5. **每个 Hook 职责单一**：安全检查 Hook 不应关心日志 Hook 的结果

### 6.4 白名单粒度设计

最优设计采用三层：
- 工具名级别：整体允许/拒绝（`"Bash"`）
- 参数模式级别：精细控制（`"Bash(git commit *)"` 只允许 git commit）
- 路径级别：文件操作控制（`"Write(src/**)"` 只允许写 src）

白名单存储策略（分层）：
1. 硬编码（不可覆盖）：系统安全边界（`~/.ssh/*` 等）
2. 项目级（提交 git）：保守安全基线
3. 本地级（不提交 git）：个人偏好宽松规则
4. 会话级（内存）：临时授权

---

## 7. 针对 harness9 的具体设计建议

基于现有架构（`internal/hooks/` HookRegistry 洋葱模型、`internal/planning/` PlanMode、TUI reviewDialog），提出以下建议：

### 7.1 HookDecision 扩展（P0）

当前 `BeforeExecute` 仅返回 `error`，建议扩展为结构化决策：

```go
// internal/hooks/hook.go
type HookDecision struct {
    Action       string          // "allow" | "deny" | "ask" | "defer"
    Reason       string          // 展示给用户和回传给 LLM 的原因
    ModifiedArgs json.RawMessage // 可选：修改后的工具参数（路径重写等）
    RiskLevel    string          // "low" | "medium" | "high"（驱动 UI 展示）
}

type ToolHook interface {
    BeforeExecute(ctx context.Context, tc schema.ToolCall) (context.Context, HookDecision, error)
    AfterExecute(ctx context.Context, tc schema.ToolCall, result schema.ToolResult) schema.ToolResult
}
```

扩展后，`HookRegistry.Execute` 可在 BeforeExecute 返回 `"ask"` 时：
1. 发送 `EventApprovalRequired` 事件到 TUI
2. 等待 `approvalCh` 信道获取用户决策
3. 根据决策继续执行或返回 `IsError: true`

### 7.2 EventApprovalRequired 事件 + TUI 审批对话框（P1）

```go
// internal/engine/stream.go — 新增事件
const EventApprovalRequired EventType = "approval_required"

type ApprovalRequest struct {
    ToolCall  schema.ToolCall `json:"tool_call"`
    Reason    string          `json:"reason"`
    RiskLevel string          `json:"risk_level"`
}
```

TUI 审批对话框（基于现有 `confirmPlanReview` 机制）：

```
┌──────────────────────────────────────────────────────────────┐
│  ⚠ 工具审批请求                                               │
│                                                              │
│  工具: bash                                                   │
│  命令: rm -rf /tmp/build_cache                               │
│  风险: 中（删除文件操作）                                      │
│                                                              │
│  ▶ [1] 允许（仅本次）                                         │
│    [2] 允许（本会话不再提示）                                  │
│    [3] 总是允许（写入白名单）                                  │
│    [4] 拒绝                                                  │
│    [5] 拒绝并提供反馈...                                      │
└──────────────────────────────────────────────────────────────┘
```

> 关键点：选项 [5] 的反馈文字作为前置 context 注入下次 LLM dispatch（类似 OpenCode 的 CorrectedError，让 LLM 了解为何被拒绝并自适应调整策略）。

### 7.3 白名单配置格式（P1）

```toml
# .harness9/settings.toml — 项目级（可提交 git）
[permissions]
allow = [
    "bash(git *)",
    "bash(go test *)",
    "bash(go build *)",
    "read_file",
]

deny = [
    "bash(rm -rf *)",
    "bash(curl * | bash)",
    "bash(wget * | sh)",
]

ask = [
    "bash(sudo *)",
    "write_file(/etc/*)",
]

# .harness9/settings.local.toml — 本地级（不提交 git）
[permissions]
allow = ["bash(npm *)"]
```

### 7.4 硬编码敏感路径（P0）

扩展 `internal/tools/safe_path.go`：

```go
// hardDenyPaths 是永远拒绝的敏感路径，任何权限模式都无法覆盖
var hardDenyPaths = []string{
    "~/.ssh/*",
    "~/.aws/credentials",
    "~/.kube/config",
    "~/.gnupg/*",
    "~/.netrc",
    "~/.config/gcloud/*",
}
```

### 7.5 内置高危命令拦截 Hook（P1）

```go
// internal/hooks/danger_hook.go
var DefaultDangerPatterns = []struct {
    Pattern   string
    RiskLevel string
}{
    {"rm -rf *", "high"},
    {"rm -r /*", "high"},
    {"curl * | bash", "high"},
    {"wget * | sh", "high"},
    {"chmod -R 777 *", "high"},
    {":(){ :|:& };:", "high"},  // Fork bomb
    {"sudo *", "medium"},
    {"chmod 777 *", "medium"},
}
```

### 7.6 PermissionMode 扩展（P2）

扩展现有 `PlanMode` 或新增独立的 `PermissionMode`：

```go
type PermissionMode int

const (
    PermissionModeDefault     PermissionMode = iota  // 变更操作需确认
    PermissionModePlan                               // 阻止所有变更（当前 PlanModePlan）
    PermissionModeAutoApprove                        // 白名单内自动执行
    PermissionModeReadOnly                           // 禁止所有写操作
)
```

### 7.7 实现优先级

| 优先级 | 功能 | 说明 |
|-------|------|------|
| P0 | HookDecision 扩展（allow/deny/ask） | 现有 Hook 框架最小改动 |
| P0 | 硬编码敏感路径保护 | 扩展 safe_path.go |
| P1 | EventApprovalRequired + TUI 审批对话框 | 基于现有 reviewDialog 机制 |
| P1 | 白名单配置文件（TOML） | 新增 env 层扩展 |
| P1 | 内置高危命令拦截 Hook | 新增 hooks/danger_hook.go |
| P2 | 白名单动态更新（"总是允许" 写配置） | P1 完成后扩展 |
| P2 | PermissionMode 扩展 | 扩展现有 PlanMode |
| P3 | defer 模式（进程退出后恢复审批） | 需持久化 pending 状态到 SQLite |

---

## 8. 权威参考资料

1. **Claude Agent SDK — Configure Permissions**
   - https://code.claude.com/docs/en/agent-sdk/permissions
   - 权限评估管线（5 步）、六种权限模式、allow/deny/ask 规则语法、动态 setPermissionMode

2. **Claude Agent SDK — Hooks Guide**
   - https://code.claude.com/docs/en/agent-sdk/hooks
   - PreToolUse/PostToolUse 接口、Matcher 正则语法、deny 优先语义、updatedInput 修改工具参数

3. **Claude Agent SDK — Handle Approvals and User Input**
   - https://code.claude.com/docs/en/agent-sdk/user-input
   - canUseTool 回调详解、once/always/reject 三路响应、defer 跨进程恢复

4. **Claude Agent SDK — Sessions**
   - https://code.claude.com/docs/en/agent-sdk/sessions
   - JSONL 会话序列化、resume/continue/fork 三种恢复模式

5. **Building Effective Agents**（Anthropic Research，2024-12-19）
   - https://www.anthropic.com/research/building-effective-agents
   - Agent 设计哲学：简单可组合、人机协作检查点、在工具层而非模型层执行边界约束

6. **DeepAgents FilesystemMiddleware**
   - FilesystemPermission dataclass、`_check_fs_permission` glob 匹配评估逻辑

7. **OpenHarness PermissionChecker**
   - 评估顺序（敏感路径→deny→allow→路径规则→命令模式→权限模式）、硬编码敏感路径清单

8. **OpenCode Permission 权限系统**
   - Effect Deferred 挂起、三路回复（once/always/reject）、级联拒绝/允许的 InstanceState 管理

9. **HermesAgent tool_executor**
   - Plugin 系统、Guardrail 决策、approval callback 传播防死锁
