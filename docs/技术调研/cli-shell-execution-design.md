# CLI Agent 框架 Shell 命令执行设计调研报告

## 调研概述

harness9 目前的 `bash` 工具让 **LLM 侧**可执行 Shell 命令（LLM ToolCall → engine → `exec.Command`）。本次调研聚焦**用户侧**在 TUI 对话框直接执行 Shell 命令的设计模式。

两条数据流对比：

| 维度 | LLM 侧（现有 bash 工具） | 用户侧（待实现） |
|------|------------------------|----------------|
| 触发者 | LLM ToolCall | 用户键盘输入 |
| 执行路径 | engine → tools.BashTool | TUI 直接 → os/exec |
| 上下文注入 | 自动（Observation 消息） | 可选（按需注入） |
| 交互性 | 非交互式 | 可支持 PTY（但主流框架不开启） |

---

## 各框架分析

### Claude Code

**信息来源**：官方文档 `code.claude.com/docs/en/interactive-mode`

#### 触发方式

输入框中 `!` 前缀触发 Shell 模式，与 `/`（斜杠命令）、`@`（文件路径补全）并列为三类快捷前缀：

```
Quick commands:
  /   → 斜杠命令或 Skill
  !   → Shell 模式：直接执行并将输出追加到对话上下文
  @   → 文件路径 mention
```

激活约束：**仅当光标位于行首（offset == 0）且为 normal 模式时生效**。防止在句中意外触发。

#### 执行机制

- 命令不经过 Claude 解析或审批，直接传入 Shell 执行
- stdout 和 stderr 同时捕获
- 命令及其输出**自动追加到对话上下文**，LLM 下一轮可直接引用
- 模式在提交后自动复位为 normal
- 按 Escape、Backspace（空输入时）或 Ctrl+U 可退出 Shell 模式而不提交

#### 输出展示

- TUI 内 **inline 实时展示**，不退出 AltScreen
- 渲染格式：`# Shell / $ <command>` 块，与工具调用结果块视觉一致
- 长输出不自动截断（文档明确警告：`! cat production.log` 可能 dump 50,000 行进上下文）

#### 交互式命令支持

- **不支持交互式 PTY**（vim、ssh 等无法通过 `!` 运行）
- 长时间运行的命令可按 `Ctrl+B` 推入**后台异步执行**
- 后台任务输出写入临时文件，LLM 可通过 Read 工具检索；任务退出自动清理
- 单任务输出超 5 GB 自动终止

#### 历史与补全

- `!` 命令有独立历史记录（按 workDir 隔离，与对话命令分开）
- 输入 `!` + 前缀后按 Tab 触发基于历史的自动补全
- 注意：通用历史扩展（`!` history expansion）在 `/config` 中**默认禁用**

#### 安全策略

- 无确认提示，无沙箱，直接执行（信任用户）
- 官方文档明确的风险：1）大输出撑爆上下文；2）`! env` 将 API Key 注入对话上下文

---

### OpenCode（anomalyco/opencode）

**来源**：GitHub（Stars: 164,945，TypeScript，MIT，dev 分支）

#### 触发方式与激活条件

与 Claude Code 完全相同。源码（`packages/opencode/src/cli/cmd/tui/component/prompt/index.tsx`）：

```typescript
// Shell mode binding - only triggers at cursor offset 0 in normal mode
bindings: [
  {
    key: "!",
    desc: "Shell mode",
    group: "Prompt",
    cmd: () => {
      setStore("placeholder", randomIndex(shell().length))
      setStore("mode", "shell")        // visual indicator switches
    },
  },
]
// Activation conditions:
// - input target is defined
// - user input is not disabled
// - current mode is "normal"
// - autocomplete NOT visible
// - cursor offset === 0
```

激活后 placeholder 切换为 Shell 示例提示（如 `Run a command... "git status"`）。

#### 执行机制

命令通过 SDK 客户端发送到后端（Client-Server 架构）：

```typescript
if (store.mode === "shell") {
  void sdk.client.session.shell({
    sessionID,
    agent: agent.name,
    model: { providerID, modelID },
    command: inputText,
  })
  setStore("mode", "normal")    // reset after submission
}
```

后端使用 `$SHELL` 解析 Shell 路径，通过 `Process.text([cmd], { shell: sh })` 执行（非交互单次执行）。

**已知 Windows bug（Issue #5310）**：`process.env["SHELL"]` 在 Windows 为 undefined，fallback 为 `"bash"`，而 Windows 通常无 bash，导致 Shell 模式挂起。

#### Shell 解析模块

独立模块 `packages/opencode/src/shell/shell.ts`，提供：
- 跨平台 Shell 检测（Unix 读 `/etc/shells`，Windows 查注册表）
- bash、zsh、fish、ksh、PowerShell 等 Shell 的元数据（login shell 支持、POSIX 兼容性）
- fish 特殊处理：偏好 bash/sh（避免 fish 不兼容脚本）

#### PTY 模块（有但 `!` 未用）

`packages/opencode/src/pty/` 是独立的 PTY 工程实现，但 `!` 模式使用的是非交互的 `Process.text()`，PTY 模块不被 Shell 模式直接调用。

#### 沙箱（实验性，PR #21538，2026 年 4 月）

macOS 实验性 bash 沙箱覆盖所有执行面，包括 `!` Shell 模式：
- 内置预设：`default`、`strict`、`network`
- 模式：`workspace-write`、`read-only`
- 可配置额外读/写/拒绝路径

#### 已知问题

- Issue #15987：v1.2.16 上 Shell 模式输出空白（显示 `# Shell / $ ls` 块但无内容）
- Issue #22667：`tool_details_visibility: false` 时，用户 `!` 命令输出被 `shouldHide` 逻辑一并隐藏（未区分用户主动 Shell 与 LLM 工具调用）
- Issue #7750：功能请求"持久 Shell 会话"——当前每次 `!` 后 Shell 退出，用户需反复键入 `!`

---

### OpenClaw（openclaw/openclaw）

**来源**：GitHub（Stars: 374,482，TypeScript，MIT）

#### 设计定位差异

OpenClaw 定位为**面向聊天频道（WhatsApp、Telegram、Discord、iMessage、Slack）的 AI 助手 Gateway**，而非纯编码 Agent。TUI 主要是本地连接测试界面，不是日常开发工作流的主战场。

#### Shell 执行：`exec` 工具（非 `!` 前缀）

没有用户侧 `!` 前缀机制。Shell 执行通过 **`exec` 工具**实现，是 Agent 工具体系的一部分：

- `exec` 工具是变更性 Shell 面（mutating shell surface），命令可创建、编辑、删除文件
- **默认禁用**，需在 `openclaw.json` 中显式 opt-in（2026 年 1 月安全更新后强制要求）
- Shell 解析：非 Windows 使用 `$SHELL`，fish 时 fallback bash；Windows 优先 PowerShell 7，回退 PowerShell 5.1
- 安全：拒绝注入 `env.PATH` 和 `LD_*/DYLD_*` loader 覆盖，防止二进制劫持

#### TUI 命令（非 Shell 执行）

TUI 斜杠命令是**会话控制命令**，不是 Shell 执行：

```
/status, /new, /reset, /compact
/think <level>, /verbose on|off, /trace on|off
/usage off|tokens|full, /restart
/activation mention|always
```

#### 对 harness9 的参考价值

OpenClaw 的 `exec` 工具**默认禁用 + 显式配置启用**策略（安全敏感场景下的权限分级）值得借鉴，但 `!` 前缀交互模式不适用于 OpenClaw 的架构定位。

---

### DeepAgents（langchain-ai/deepagents）

**来源**：GitHub（Stars: 23,290，Python，MIT）

#### 架构背景

DeepAgents 拆分为两个包：
1. **`deepagents` CLI**（`libs/cli/`）：当前聚焦 `init`/`dev`/`deploy` 部署操作
2. **`deepagents-code`**：类 Claude Code 的交互式编码 Agent，含 TUI + `!` Shell 模式

#### `!` 前缀 + `!!` 隐身模式

`deepagents-code` 与 Claude Code 高度对齐，并有独特扩展：

- `!command`：命令直接执行，输出追加到对话上下文，LLM 可引用
- `!!command`：**隐身模式（incognito shell mode）**——命令在不被 LLM 观察的情况下运行，不注入上下文

`!!` 隐身模式是所有调研框架中独有的设计，适用场景：查看含密钥的环境变量（`!! env | grep KEY`）、不希望 LLM 看到的诊断命令。

Tab 补全基于历史 `!` 命令。

#### `execute` 工具（Agent 侧）

Agent 侧 Shell 执行通过 `execute` 工具实现，支持多种沙箱后端（Modal、Daytona、Runloop、E2B、Docker），通过 HITLMiddleware 提供人工审批（针对 LLM 工具调用，不是用户 `!` 命令）。

#### 非交互模式安全

管道输入（`echo "task" | dcode`）时 Shell 执行默认禁用，通过 `-S`/`--shell-allow-list` 白名单化（如 `-S "pytest,git,make"` 或 `-S all`）。

---

## 设计模式对比表

### 触发方式

| 框架 | 触发方式 | 激活条件 |
|------|---------|--------|
| Claude Code | `!` 前缀 | 光标行首（offset==0），normal 模式 |
| OpenCode | `!` 前缀 | 同上，另需 autocomplete 未展开 |
| OpenClaw | `exec` 工具（无用户前缀） | Agent 工具调用 |
| DeepAgents Code | `!` / `!!`（隐身） | 光标行首 |

**结论**：`!` 前缀是 CLI 编码 Agent 的 de facto standard。

### TUI 处理策略

| 框架 | 策略 | 实现 |
|------|------|------|
| Claude Code | Inline（不退出 TUI） | `# Shell / $ <cmd>` 块 |
| OpenCode | Inline（不退出 TUI） | 同格式，服务端执行后事件推送 |
| OpenClaw | 不适用 | TUI 是连接测试界面 |
| DeepAgents | Inline（不退出 TUI） | Textual 框架内 inline |

### 交互式命令处理

| 框架 | 支持交互式 PTY | 说明 |
|------|-------------|------|
| Claude Code | 否 | 交互式命令需另开终端 |
| OpenCode | 有 PTY 模块，但 `!` 未启用 | `!` 仅单次执行 |
| OpenClaw | exec 工具可配置 | — |
| DeepAgents | 未见支持 | 推测同 Claude Code |

### 上下文注入

| 框架 | 默认行为 | 隐身模式 |
|------|---------|--------|
| Claude Code | 自动注入 | 无 |
| OpenCode | 自动注入 | 无 |
| OpenClaw | 工具结果标准链路 | — |
| DeepAgents | `!` 注入，`!!` 不注入 | ✓ |

### 安全策略

| 框架 | 用户侧 `!` 沙箱 | 确认提示 | 工作目录限制 |
|------|---------------|---------|------------|
| Claude Code | 无 | 无 | 无 |
| OpenCode | 实验性（macOS） | 无 | 无 |
| OpenClaw | exec 工具默认禁用 | 可配置 | 可配置 |
| DeepAgents | 无（`!` 模式） | 无（`!` 模式） | 无 |

**规律**：用户主动发起的 `!` 命令普遍无沙箱无确认（信任用户）；LLM 工具调用才有权限控制。

---

## harness9 的设计建议

### 功能定位

实现**用户侧 Shell 命令执行**，核心能力：

1. `!` 前缀触发 Shell 模式（与行业标准对齐，用户零学习成本）
2. 命令输出 inline 展示在 TUI 对话流中
3. 输出默认注入 LLM 上下文（在下一轮 prompt 前附加，批量注入节省 token）

当前阶段**不实现**：交互式 PTY（与 Bubbletea AltScreen 冲突，工程复杂度高）、后台任务 Ctrl+B 模式。

---

### Go + Bubbletea 实现方案

#### 触发逻辑

在 `tui_update.go` 的 `case tea.KeyEnter:` 中，于斜杠命令判断前插入 `!` 前缀检测：

```go
// Detect "!" prefix (user-initiated shell command, bypasses LLM entirely)
if strings.HasPrefix(raw, "!") {
    shellCmd := strings.TrimSpace(strings.TrimPrefix(raw, "!"))
    return m.dispatchShellCommand(shellCmd)
}
```

#### 异步执行（避免阻塞 Bubbletea event loop）

```go
// shellResultMsg carries the result of a user-initiated shell command.
type shellResultMsg struct {
    cmd    string
    output string
    isErr  bool
    dur    time.Duration
}

// runShellCmd returns a tea.Cmd that executes a shell command asynchronously.
func runShellCmd(workDir, cmd string) tea.Cmd {
    return func() tea.Msg {
        start := time.Now()
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        out, err := exec.CommandContext(ctx, "bash", "-c", cmd).CombinedOutput()
        return shellResultMsg{
            cmd:    cmd,
            output: string(out),
            isErr:  err != nil,
            dur:    time.Since(start).Round(time.Millisecond),
        }
    }
}
```

#### `dispatchShellCommand`

```go
// dispatchShellCommand handles user-initiated "!<cmd>" shell execution inline in TUI.
func (m tuiModel) dispatchShellCommand(cmd string) (tuiModel, tea.Cmd) {
    if cmd == "" {
        m.input.Focus()
        return m, textinput.Blink
    }
    // Render command prompt line
    m.lines = append(m.lines, shellCmdStyle.Render("$ "+cmd))
    // Check for known interactive programs and refuse early
    if isInteractiveCmd(cmd) {
        m.lines = append(m.lines, shellErrStyle.Render(
            "  ✗ 该命令需要交互式终端，请在独立终端窗口中运行"))
        m.input.Focus()
        return m, textinput.Blink
    }
    return m, runShellCmd(m.workDir, cmd)
}
```

#### `shellResultMsg` 处理

在 `Update` 的 `switch msg.(type)` 中新增 case：

```go
case shellResultMsg:
    output := msg.output
    const maxShellDisplay = 4096
    if len(output) > maxShellDisplay {
        output = output[:maxShellDisplay] + "\n...[输出过长，已截断，建议使用 head -n N 重新执行]..."
    }
    for _, line := range splitLines(output) {
        m.lines = append(m.lines, shellOutputStyle.Render(line))
    }
    if msg.isErr {
        m.lines = append(m.lines, shellErrStyle.Render(
            fmt.Sprintf("  ✗ 非零退出 — %s", msg.dur)))
    } else {
        m.lines = append(m.lines, shellOKStyle.Render(
            fmt.Sprintf("  ✓ 完成 — %s", msg.dur)))
    }
    // Buffer output for context injection on next LLM turn
    m.pendingShellOutput = append(m.pendingShellOutput,
        fmt.Sprintf("$ %s\n%s", msg.cmd, msg.output))
    m.input.Focus()
    return m, textinput.Blink
```

#### 上下文注入时机

在 `dispatch()` 函数中，将 `pendingShellOutput` 缓冲前置到用户 prompt：

```go
func (m tuiModel) dispatch(prompt string) (tuiModel, tea.Cmd) {
    // Prepend buffered shell output to LLM context
    if len(m.pendingShellOutput) > 0 {
        shellContext := "[用户执行的 Shell 命令记录]\n" +
            strings.Join(m.pendingShellOutput, "\n---\n")
        prompt = shellContext + "\n\n" + prompt
        m.pendingShellOutput = nil
    }
    // ... existing dispatch logic
}
```

#### 视觉样式（新增到 `tui.go`）

```go
// Shell mode styles
shellCmdStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Bold(true)   // yellow bold: $ cmd
shellOutputStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))              // light gray: output lines
shellOKStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("34"))               // green: ✓ 完成
shellErrStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("160"))              // red: ✗ 非零退出
```

#### 交互式命令检测

```go
// interactiveCmds is a list of programs that require a PTY and cannot
// run inside the Bubbletea AltScreen TUI.
var interactiveCmds = map[string]bool{
    "vim": true, "vi": true, "nano": true, "emacs": true,
    "ssh": true, "top": true, "htop": true, "less": true,
    "man": true, "more": true, "watch": true, "tmux": true,
    "screen": true,
}

func isInteractiveCmd(cmd string) bool {
    fields := strings.Fields(cmd)
    if len(fields) == 0 {
        return false
    }
    return interactiveCmds[filepath.Base(fields[0])]
}
```

#### `running` 状态期间的 Shell 执行

当前 `m.running == true` 时 Enter 键被拦截（LLM 推理进行中）。建议：**LLM 运行期间允许执行 Shell 命令**（不影响 LLM 推理，用户可用来查看构建状态等）。需在 `case tea.KeyEnter:` 最顶层检测 `!` 前缀，在 `if m.running { return m, nil }` 之前处理。

#### 关于 `!!` 隐身模式

DeepAgents 的 `!!` 模式（不注入上下文）值得后续添加，适用于：
- `!! env | grep SECRET`（查看密钥，不暴露给 LLM）
- `!! cat /etc/hosts`（敏感文件查看）

初版先实现 `!`（默认注入），`!!` 作为后续迭代。

---

### 实现风险与注意事项

1. **长输出保护**：TUI 展示上限 4096 字节，注入上下文上限 2048 字节。引用现有 `maxOutputLen` 常量风格（`const maxShellDisplayLen = 4096`）
2. **workDir 复用**：直接使用 `m.workDir`，不需要新字段，与现有 bash 工具行为一致
3. **Bubbletea 线程安全**：`tea.Cmd` 异步执行 + `shellResultMsg` 回调，不在 goroutine 里直接修改 model，符合 Bubbletea Elm 架构要求
4. **历史记录**：初版不需要实现 Shell 历史（与对话历史复用 textinput 的 Up/Down 即可，无需独立历史存储）
5. **补全**：`!` 模式下 Tab 补全是 nice-to-have，初版跳过

---

## 参考来源

- [Claude Code Interactive Mode Documentation](https://code.claude.com/docs/en/interactive-mode)
- [Shell mode (! prefix) produces blank output — OpenCode Issue #15987](https://github.com/anomalyco/opencode/issues/15987)
- [Shell mode (! command) doesn't work on Windows — OpenCode Issue #5310](https://github.com/anomalyco/opencode/issues/5310)
- [Shell mode output hidden when "Hide tool details" enabled — OpenCode Issue #22667](https://github.com/anomalyco/opencode/issues/22667)
- [Persistent shell mode feature request — OpenCode Issue #7750](https://github.com/anomalyco/opencode/issues/7750)
- [TUI Commands & Keybindings — OpenCode DeepWiki](https://deepwiki.com/anomalyco/opencode/9.2-tui-commands-and-keybindings)
- [Add experimental macOS bash sandboxing — OpenCode PR #21538](https://github.com/anomalyco/opencode/pull/21538)
- [OpenClaw Exec Tool Documentation](https://docs.openclaw.ai/tools/exec)
- [Deep Agents Code Overview](https://docs.langchain.com/oss/python/deepagents/code/overview)
