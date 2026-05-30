# Sub-Agent 系统实现原理

harness9 的 Sub-Agent 系统让主代理可以把**边界清晰的子任务**委派给拥有独立上下文、受限工具集与可选模型覆盖的专门代理执行。子代理不是新的抽象——它就是一个运行在隔离 Session 上的普通 `engine.AgentEngine` 实例，复用现有 `RunStream` 流水线，不改动核心 `runLoop` 一行代码。

---

## 系统架构

```
internal/subagent/
├── definition.go   # SubAgentDefinition 结构体 + ResolveTools + Validate
├── registry.go     # Registry：Register / Get / List（启动阶段注册，运行期只读）
├── frontmatter.go  # parseAgentFile：YAML frontmatter + 正文 → SubAgentDefinition
├── loader.go       # Registry.LoadFromDir：扫描 .harness9/agents/*.md 文件式定义
├── prompt.go       # promptBuilder：子代理 system prompt + Skills 预加载 + workDir 注入
├── tracker.go      # TaskTracker：后台任务单一事实源（Start/AppendLog/Finish/DrainCompleted/List/Get）
├── runner.go       # Runner：构建隔离子引擎 + 运行 RunStream + 桥接审批与进度
└── task_tool.go    # TaskTool：主代理调用的唯一委派入口（tools.BaseTool）

cmd/harness9/
├── main.go         # 接线：注册内置 code-reviewer、LoadFromDir、NewRunner、NewTaskTool
├── tui_update.go   # EventSubAgent 渲染 + dispatch() 中 TaskTracker.DrainCompleted 注入 + @agent 直跑 + 任务面板按键
└── tui_view.go     # renderSubAgentProgress()、renderTaskPanel()、renderStatusBar() 后台任务状态栏
```

---

## 子代理定义

### 编程式定义

在 `main.go` 中直接构造并注册到 `subagent.Registry`：

```go
subAgentReg.Register(subagent.SubAgentDefinition{
    Name:         "code-reviewer",
    Description:  "代码审查专家。写完或修改代码后主动使用，检查安全、性能与最佳实践。",
    SystemPrompt: "你是一名资深代码审查专家。审查时聚焦：安全漏洞、性能问题、可维护性。...",
    Tools:        []string{"read_file", "bash"},
    MaxTurns:     20,
    Source:       "builtin",
})
```

harness9 内置了一个 `code-reviewer` 子代理，覆盖最常见的代码审查场景。

### SubAgentDefinition 字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| `Name` | `string` | 唯一标识，须匹配 `^[a-z0-9][a-z0-9-]*$` |
| `Description` | `string` | 写给 LLM 的"何时使用我"，是 `task` 工具调度依据的核心 |
| `SystemPrompt` | `string` | 子代理 system prompt 正文 |
| `Tools` | `[]string` | 工具白名单；nil/空 = 继承父全部可用工具 |
| `DisallowedTools` | `[]string` | 工具黑名单（先 deny 后 allow） |
| `Model` | `string` | 模型覆盖；`""` = 继承父代理模型 |
| `MaxTurns` | `int` | 最大轮数；`0` = 继承引擎默认值（当前 20） |
| `Skills` | `[]string` | 启动时预加载的 skill 名称（正文注入子代理 system prompt） |
| `Source` | `string` | 诊断字段：`"builtin"` 或文件路径 |

### 文件式定义

在工作目录的 `.harness9/agents/` 下创建 `*.md` 文件，harness9 启动时自动扫描加载。**文件定义覆盖同名编程式定义**（记录日志，不报错）。若文件未包含 `name` 字段，自动回退用文件名（去 `.md` 后缀）作为 Name。

**完整示例 `.harness9/agents/security-auditor.md`**：

```markdown
---
name: security-auditor
description: 安全审计专家。对涉及认证、鉴权、输入校验的代码变更后使用，检测 OWASP Top 10 漏洞。
tools: read_file, bash
disallowed_tools: write_file, edit_file
model: openai/gpt-4o
max_turns: 30
skills: security-review
---

你是一名应用安全工程师，专注于识别代码中的安全漏洞。
审查时按优先级输出：严重 > 高危 > 中危 > 低危，每条附上 CWE 编号与修复建议。
不要修改文件，只输出审查报告。
```

**frontmatter 字段速查**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 同 SubAgentDefinition.Name |
| `description` | string | 同 SubAgentDefinition.Description |
| `tools` | 逗号分隔字符串 | 白名单，如 `read_file, bash` |
| `disallowed_tools` | 逗号分隔字符串 | 黑名单 |
| `model` | string | 模型覆盖 |
| `max_turns` | int | 最大轮数 |
| `skills` | 逗号分隔字符串 | 预加载 skill 名称 |

---

## task 工具

`task` 是注册在父代理工具注册表中的普通工具（`tools.BaseTool`）。LLM 通过调用 `task` 工具委派子任务；子代理的 registry 永不包含 `task`，从根上禁止递归。

### 工具参数

| 参数 | 类型 | 必填 | 说明 |
|------|------|:----:|------|
| `subagent_type` | string（枚举） | ✅ | 已注册子代理的 Name，`Definition()` 动态枚举 |
| `prompt` | string | ✅ | 传给子代理的完整任务描述。子代理看不到父对话历史，所有必要信息都要写在这里 |
| `description` | string | ❌ | 3–5 词的简短标题（UI 展示用） |
| `background` | bool | ❌ | 是否后台异步运行（默认 `false`） |

`Definition()` 在每次被调用时**动态生成**，将所有已注册子代理的 Name 作为 `subagent_type` 的 `enum`，Description 拼入工具描述，是 LLM 选择"调用哪个子代理"的依据：

```
把一个边界清晰的任务委派给专门的子代理执行。子代理拥有独立上下文与受限工具集。
可用子代理：
- code-reviewer: 代码审查专家。写完或修改代码后主动使用，检查安全、性能与最佳实践。
- security-auditor: 安全审计专家。...
```

### 前台执行（`background=false`，默认）

```
task 工具调用
    │ execCtx = 父调用方 ctx
    ▼
Runner.Run(..., background=false)
    │ 构建隔离子引擎，调用 RunStream，消费事件流
    │ 审批请求 → parentApproval(ctx, ...) → 透传父 TUI 审批对话框
    ▼
阻塞直到子引擎 channel 关闭
    │
    ▼
返回 <task state="completed"><task_result>...最终文本...</task_result></task>
```

前台执行的 tool result 直接作为工具调用的 Output 注入父代理的上下文历史，主代理可立即读取子代理输出。

### 后台执行（`background=true`）

```
task 工具调用
    │
    ▼
task 工具立即返回 <task id="task-code-reviewer-1" state="running"/>
    │
    ▼ 同时：go func(){...}()
        execCtx 从会话级 baseCtx 派生（独立于父 turn，不受工具 60s 超时影响）
        审批请求 → 一律拒绝（fail-closed），返回"子代理无可用审批通道，已自动拒绝"
        子引擎事件流 → tracker.AppendLog(id, update)（全过程日志写入内存，加锁，不经 channel）
        子引擎执行完成 → tracker.Finish(id, finalText, isErr)
            │ 触发 SetNotify 回调 → tea.Program.Send(subAgentNotifyMsg) → TUI 即时显示完成提示

下一次 dispatch() 前：
    tracker.DrainCompleted() → 拼入 prompt 前缀 → 注入 LLM 上下文
```

---

## 执行模型与 Context 传递

### Runner 的两阶段执行

`Runner.Run` 是子代理执行的核心：

1. **构建隔离 registry**：`buildChildRegistry` 按 `ResolveTools`（白名单∩全集 - 黑名单 - task）筛选工具，包上 `permission.NewFileHook`（继承同一 `settings.json`）+ `denyTaskHook`（防递归） + sharedHooks（dangerHook + offloadHook）。
2. **解析 Provider**：`def.Model != ""` 时新建 OpenAI Provider 并查询对应 context window；`""` 时复用父代理模型。
3. **构建 PromptBuilder**：子代理 system prompt + workDir 注入 + def.Skills 列表中的 skill 正文（通过 `skills.Index.GetFullContent` 加载，失败静默忽略）。
4. **独立 MemorySession**：`memory.NewMemorySession(childID)`（纯内存，不含父对话历史，不含父 system prompt）。
5. **启动 RunStream**：`sub.RunStream(execCtx, prompt)`，消费事件流，转发进度、桥接审批，累积最终文本。

### Context 传递规则

```
父代理 ──► task 工具 ──► prompt 字符串 ──► 子代理（唯一信息来源）
子代理 ──► FinalText ──► tool result ──► 父代理上下文（前台）
子代理 ──► TaskTracker ──► DrainCompleted ──► 父代理下次 prompt 前缀（后台）
```

子代理**看不到**父代理的对话历史和 system prompt。文件路径、背景信息、需求细节必须通过 `task` 工具的 `prompt` 参数显式传递。

### 执行 Context 差异

| 维度 | 前台（`background=false`） | 后台（`background=true`） |
|------|--------------------------|--------------------------|
| execCtx 来源 | 父调用方 `ctx`（工具超时 60s 以内） | 会话级 `baseCtx` 派生（独立于父 turn） |
| 审批策略 | 透传父 `ApprovalFunc`，TUI 审批对话框可用 | 一律拒绝（fail-closed） |
| 结果交付 | tool result 同步返回 | `TaskTracker.Finish` 写入内存，下次 dispatch 时 `DrainCompleted` 注入 |
| 进度日志 | 经 `EventSubAgent` 实时渲染到 subAgentLines | `TaskTracker.AppendLog` 缓冲到内存，可通过 `/tasks` 面板查看 |
| 取消传播 | 父 ctx 取消 → 子代理随之取消 | baseCtx 取消（进程关闭）才取消 |

---

## TUI 实时进度渲染

前台子代理执行期间，TUI 在工具进度区下方实时追加 `[agent-name]` 前缀的暗青色进度行：

```
  [code-reviewer] 子代理启动…
  [code-reviewer] ▸ read_file
  [code-reviewer]   ✓
  [code-reviewer] ▸ bash
  [code-reviewer]   ✓
  [code-reviewer] 发现 3 处安全问题，已列出...
  [code-reviewer] ✓ 完成
```

进度行最多保留最近 `maxSubAgentLines = 12` 行，防止长时间运行的子代理无界增长。`SubAgentThinking`（推理增量）故意不展示，减少噪声。

进度数据流：`Runner.emit(SubAgentUpdate)` → `hooks.SubAgentProgressFunc`（注入 context）→ `RunStream` 转为 `EventSubAgent` 事件 → TUI `EventSubAgent` case → `m.subAgentLines` 追加。

---

## 安全保障

| 安全层 | 机制 | 说明 |
|--------|------|------|
| 禁止递归 | 子 registry 永不含 `task` 工具 | `ResolveTools` 硬编码 `denied["task"]=true` |
| 禁止递归（纵深） | `denyTaskHook.BeforeExecute` | 双重防御：即使未来代码引入 `task`，hook 也会拒绝 |
| 权限不升级 | 继承同一 `.harness9/settings.json` | `permission.NewFileHook(settingsPath)` 复用同一规则文件 |
| 权限只叠加更严 | 子代理额外叠加 DisallowedTools + denyTaskHook | 只能比父代理更受限，不能扩权 |
| Context 隔离 | 独立 `MemorySession`（纯内存） | 不含父对话历史，不含父 system prompt，无数据泄漏路径 |
| 工具隔离 | `ResolveTools`（白名单∩全集 - 黑名单 - task） | 仅注册显式允许的工具实例 |
| 后台审批 fail-closed | 后台子代理审批一律拒绝 | 无 TUI 通道时宁可拒绝，不自动放行危险操作 |
| 敏感路径 | sharedHooks 含 `dangerHook` | 19 条高危模式（`~/.ssh`、`~/.aws` 等）同样保护子代理 |

---

## TaskTracker — 后台任务单一事实源

`TaskTracker` 是后台子代理任务的线程安全单一事实源，替代旧版 `Mailbox`，同时承担全过程日志缓冲与结果注入两项职责：

### API 一览

| 方法 | 调用方 | 说明 |
|------|--------|------|
| `Start(agentName, prompt) string` | 后台 goroutine 启动时 | 注册 Running 任务，返回唯一 `id`（格式 `task-{agent}-{seq}`） |
| `AppendLog(id, SubAgentUpdate)` | 后台 goroutine 流式推进中 | 将进度事件追加到内存缓冲（加锁），不经任何 channel |
| `Finish(id, finalText, isErr)` | 后台 goroutine 完成时 | 标记 Done/Failed，触发 `SetNotify` 回调（锁外调用） |
| `DrainCompleted() []CompletedTask` | TUI `dispatch()` 前 | 返回已完成未注入结果，标记为 injected（幂等） |
| `List() []TaskSnapshot` | TUI 任务面板 | 全量快照，按创建顺序 |
| `Get(id) (TaskDetail, bool)` | TUI 任务详情 | 返回含全过程日志深拷贝的 `TaskDetail` |
| `RunningCount() int` | TUI 状态栏 | 运行中任务数 |
| `DoneCount() int` | TUI 状态栏 | 已结束（完成 + 失败）任务数 |
| `SetNotify(fn func())` | TUI 初始化时 | 注册完成通知回调 |

### 两条独立路径

**注入路径**：`Finish` 将最终文本写入内存，父代理**下次 dispatch** 时 `DrainCompleted` 排空并前置拼入 LLM 上下文（`pendingSubAgentInject` 缓冲）。`DrainCompleted` 是幂等的，已注入的结果不会被再次取走。

**提示路径**：`Finish` 同时触发 `SetNotify` 回调——TUI 在启动时将其注册为 `tea.Program.Send(subAgentNotifyMsg{})`，后台任务完成瞬间即向 scrollback 追加一条「✓ 后台子代理完成」提示（仅展示，不消费注入缓冲，二者互不干扰）。

**全过程日志**：`AppendLog` 直接写入内存缓冲（加锁），完全不经 channel，从根本上杜绝 send-on-closed-channel 风险。日志通过 `Get(id).Log` 暴露给 `/tasks` 面板详情页。

---

## 后台任务查看器

### 状态栏指示

状态栏在存在后台任务时自动显示任务计数段：

```
⚙ 2 运行/3 完成
```

由 `renderStatusBar()` 调用 `TaskTracker.RunningCount()` 和 `DoneCount()` 实时读取，仅在至少有一个任务（运行中或已完成）时展示，零任务时不占用状态栏空间。

### 打开面板

两种等价方式：

| 方式 | 说明 |
|------|------|
| `Ctrl+T` | 键盘快捷键切换（空闲态可用；运行中、审批、审查、恢复选择等模态冲突时忽略） |
| `/tasks` + Enter | 斜杠命令，效果与 `Ctrl+T` 完全相同 |

面板为**模态视图**：激活时 `taskPanelMode = true`，`View()` 将输入区替换为 `renderTaskPanel()` 渲染的面板内容，普通输入和其他快捷键全部由 `handleTaskPanelKey` 接管。

### 列表视图

面板打开时默认展示任务列表，每行格式：

```
{● 运行/✓ 完成/✗ 失败}  {agent}  {状态文字}  "{prompt 前 48 字节}"
```

当前选中行以 `▶` 高亮。按键说明：

| 按键 | 行为 |
|------|------|
| `↑` / `↓` | 移动光标 |
| `Enter` | 进入选中任务的详情视图 |
| `Esc` 或 `Ctrl+T` | 关闭面板，返回正常输入模式 |

### 详情视图

按 `Enter` 选中任务后进入详情视图，展示该后台子代理的全过程日志（通过 `TaskTracker.Get(id)` 取 `TaskDetail.Log` 深拷贝）：

```
code-reviewer — 完成  （↑↓ 滚动，Esc 返回）

启动…
▸ read_file(main.go)
▸ bash(go vet ./...)
  ✗ 工具执行失败
发现 2 处安全问题…

— 最终结果 —
建议修复以下两处…
```

日志渲染由 `formatTaskLog` 完成，覆盖 `SubAgentStart / SubAgentToolStart / SubAgentDelta / SubAgentToolResult（仅失败）/ SubAgentError` 五种事件，`SubAgentDone` 及 `FinalText` 合并为结尾「最终结果」块。

| 按键 | 行为 |
|------|------|
| `↑` / `↓` | 滚动日志（`taskDetailScroll` 偏移） |
| `Esc` | 返回列表视图（`taskDetailID = ""`） |
| `Ctrl+T` | 关闭整个面板 |

### 实时刷新

运行中的任务每次面板渲染时直接读取 `TaskTracker` 快照（`List()` / `Get()`），无需订阅通知，TUI 主循环驱动即可保持日志行数（`LogLines`）的实时更新。

---

## @ 提及调用

### 基本用法

在输入框中以 `@<agent> <task>` 格式发送，**绕过主 LLM 的工具决策**，直接前台调用指定子代理：

```
@code-reviewer 审查 internal/tools/bash.go 的安全性
```

发送后：
1. TUI 立即追加用户消息行（`▶ You: @code-reviewer …`）
2. 子代理名称行（`◆ code-reviewer:`）追加到 scrollback
3. `running = true`，输入框禁用
4. 子代理流式进度实时渲染到 `subAgentLines`（与 `task` 工具前台执行完全相同的渲染路径）
5. 完成后，最终文本直接追加到 scrollback（作为 assistant 消息落入对话），`running = false`，输入框恢复

### Tab 补全子代理名

在输入框键入 `@` 后按 `Tab`，自动补全已注册的子代理名：

```
@cod[Tab] → @code-reviewer 
```

补全逻辑在 `cycleCompletion()` 中处理，以 `@` 守卫与 `/` 斜杠命令补全并列，共享同一套 `typedPrefix / completions / completionIdx` 循环状态，多次 `Tab` 可在所有匹配名称中循环。

### Ctrl+C 取消

`@agent` 执行期间按 `Ctrl+C`：`cancelFn()` 取消派生的子 context，Runner 中 `execCtx.Done()` 触发，子引擎 `RunStream` 随之退出；`subAgentDirectMsg{done: true, err: ctx.Err()}` 经 channel 发回 TUI，`running = false`，输入框恢复。

### 前台 vs 后台

`@` 语法**仅支持前台执行**（`background=false`）。

需要后台执行时，通过自然语言向主代理表达意图（如「在后台用 code-reviewer 检查一下最新提交」），由主 LLM 决策调用 `task` 工具并附 `background=true`，结果出现在 `/tasks` 面板。

| 维度 | `@agent task`（前台直跑） | 主 LLM → `task(background=true)` |
|------|--------------------------|-----------------------------------|
| 触发方 | 用户直接输入 | 主 LLM 工具决策 |
| 主 LLM 是否介入 | 否，完全绕过 | 是，由 LLM 选择子代理和 prompt |
| 执行模式 | 前台阻塞，流式进度可见 | 后台异步，结果存入 TaskTracker |
| 结果落点 | 直接展示在 scrollback | `/tasks` 面板 + 下次 dispatch 注入 |
| 取消 | `Ctrl+C` 即时取消 | baseCtx 取消（进程关闭）才取消 |

---

## 数据流总结

```
主代理 LLM
    │  决定调用 task 工具
    ▼
TaskTool.Execute(ctx, args)
    │  解析 subagent_type / prompt / background
    ▼
Runner.Run(ctx, def, prompt, background)
    ├─ buildChildRegistry(def)
    │       ResolveTools → 白名单∩全集 - 黑名单 - task
    │       hookChain: permFileHook → denyTaskHook → dangerHook → offloadHook
    │
    ├─ providerFor(def.Model) → LLMProvider + ctxWindow
    │
    ├─ newPromptBuilder(def.SystemPrompt, workDir, def.Skills, skillsLoader)
    │       systemPrompt + workDir + skills 正文
    │
    ├─ memory.NewMemorySession(childID)   # 独立纯内存 Session
    │
    └─ engine.NewAgentEngine(provider, childReg, workDir, opts...)
           │
           sub.RunStream(execCtx, prompt)
           │
           ▼
       事件流消费循环
           ├─ EventActionDelta   → emit(SubAgentDelta)   → EventSubAgent → TUI 进度行
           ├─ EventThinkingDelta → emit(SubAgentThinking)  （TUI 不展示）
           ├─ EventToolStart     → emit(SubAgentToolStart) → TUI 进度行
           ├─ EventToolResult    → emit(SubAgentToolResult)→ TUI 进度行
           ├─ EventApprovalRequired → 前台:透传父 ApprovalFunc / 后台:自动拒绝
           ├─ EventError         → emit(SubAgentError) → 返回 error
           └─ EventDone          → channel 关闭，循环自然结束
                   │
    前台: return FinalText → task tool result → 父代理上下文
    后台: tracker.AppendLog(id, update)（流式，全过程日志入内存）
          tracker.Finish(id, finalText, isErr)
              → 下次 dispatch() → DrainCompleted() → prompt 前缀注入 → 主代理 LLM
```

---

## 接线示例（main.go）

```go
// 1. 子代理基础工具实例（沙箱根目录 = workDir）
subAgentBaseTools := []tools.BaseTool{
    tools.NewReadFileTool(workDir),
    tools.NewWriteFileTool(workDir),
    tools.NewBashTool(workDir),
    tools.NewEditFileTool(workDir),
    skills.NewUseSkillTool(skillsIndex),
}

// 2. 定义注册表：先注册内置，再加载文件式定义
subAgentReg := subagent.NewRegistry()
subAgentReg.Register(subagent.SubAgentDefinition{
    Name: "code-reviewer", Description: "...", SystemPrompt: "...",
    Tools: []string{"read_file", "bash"}, MaxTurns: 20, Source: "builtin",
})
subAgentReg.LoadFromDir(filepath.Join(workDir, ".harness9", "agents"))

// 3. Runner：全局持有一份，运行期只读
subAgentTracker := subagent.NewTaskTracker()
subAgentRunner := subagent.NewRunner(subagent.RunnerConfig{
    BaseTools:       subAgentBaseTools,
    SharedHooks:     []hooks.ToolHook{dangerHook, offloadHook},
    SettingsPath:    settingsPath,
    SkillsIndex:     skillsIndex,
    WorkDir:         workDir,
    DefaultMaxTurns: 20,
    ToolTimeout:     60 * time.Second,
    ProviderFor:     func(model string) (provider.LLMProvider, int, error) { ... },
    CompactorFor:    func(p provider.LLMProvider, ctxWin int) memory.Compactor { ... },
    BaseCtx:         ctx,
})

// 4. 注册 task 工具进父代理 registry
taskTool := subagent.NewTaskTool(subAgentReg, subAgentRunner, subAgentTracker)
registry.Register(taskTool)
```

---

## 文件索引

| 文件 | 职责 |
|------|------|
| `internal/subagent/definition.go` | `SubAgentDefinition` 结构体、`Validate`、`ResolveTools` |
| `internal/subagent/registry.go` | `Registry`：`Register` / `Get` / `List` |
| `internal/subagent/frontmatter.go` | `parseAgentFile`：YAML frontmatter 解析 |
| `internal/subagent/loader.go` | `Registry.LoadFromDir`：文件式定义加载 |
| `internal/subagent/prompt.go` | `promptBuilder`：system prompt + skills + workDir 组装 |
| `internal/subagent/tracker.go` | `TaskTracker`：后台任务单一事实源（全过程日志 + 结果注入） |
| `internal/subagent/runner.go` | `Runner`：构建隔离子引擎 + 执行 + 事件转发 |
| `internal/subagent/task_tool.go` | `TaskTool`：`task` 工具实现（前台 / 后台） |
| `internal/schema/subagent.go` | `SubAgentUpdate` / `SubAgentUpdateKind` 类型定义 |
| `internal/hooks/subagent_progress.go` | `SubAgentProgressFunc`：context 注入/提取 |
| `internal/engine/stream.go` | `EventSubAgent`、`EventApprovalRequired`、进度 sink 注入 |
| `cmd/harness9/main.go` | 完整接线：内置子代理注册、Runner 构建、task 工具注册 |
| `cmd/harness9/tui_update.go` | `EventSubAgent` 处理、`dispatch()` 中 `TaskTracker.DrainCompleted` 注入、`dispatchMention`（@ 前台直跑）、`handleTaskPanelKey`（任务面板按键） |
| `cmd/harness9/tui_view.go` | `renderSubAgentProgress()`（暗青色进度块）、`renderTaskPanel()`（面板列表/详情）、`renderStatusBar()` 中后台任务计数 |
