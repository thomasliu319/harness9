# harness9 Sandbox 系统设计规格

> 日期：2026-06-05
> 状态：待实现
> 基于：[sandbox-design-research.md](../../技术调研/sandbox-design-research.md)

---

## 1. 需求与目标

为 harness9 引入 Docker 模式的 Sandbox 系统，提供操作系统级隔离，支持多 Sandbox 并发，并具备完整的生命周期管理能力。

### 核心决策汇总

| 维度 | 决策 |
|------|------|
| 集成方式 | 透明替换，Environment 接口注入，工具对外接口不变 |
| 工具范围 | 全工具入容器（bash via docker exec；文件工具 via bind mount） |
| 并发粒度 | Agent 级，主 Agent 和每个 Sub-Agent 各自拥有独立 Sandbox |
| 网络策略 | `--network none`，fail-closed，默认完全禁网 |
| 接口命名 | `Environment`（对齐 HermesAgent BaseEnvironment 设计） |
| 安全加固 | `--cap-drop all` + `--no-new-privileges` + `--pids-limit 256` + tmpfs |
| 生命周期 | 五状态机（Pending/Running/Stopping/Terminated/Failed） |
| Manager API | Create/Destroy/DestroyAll/ReapOrphans/ListAll |
| 孤儿回收 | 启动时通过 `label=harness9=1` 扫描清理 |
| TUI 集成 | StatusBar 下方新增 SandboxBar，实时展示 Sandbox 状态 |
| 测试策略 | 单元（MockEnvironment）+ 集成（`//go:build integration`）分层隔离 |

---

## 2. 架构概览

### 新增包结构

```
internal/sandbox/
├── environment.go         # Environment 接口定义
├── local_environment.go   # LocalEnvironment（进程级，Sandbox 关闭时默认）
├── docker_environment.go  # DockerEnvironment（Docker 容器级，OS 级隔离）
├── container.go           # Container 单容器生命周期状态机
├── manager.go             # Manager 多 Sandbox 并发管理 + 孤儿回收
├── config.go              # SandboxConfig 配置结构体
├── container_test.go
├── manager_test.go
└── docker_environment_test.go
```

### 组件关系

```
cmd/harness9/main.go
    └── sandbox.Manager（单例，管理所有容器）
          └── sandbox.Container（每个 Agent 一个）
                └── sandbox.DockerEnvironment（实现 Environment 接口）
                      └── docker exec → 容器内执行

internal/tools/
    bash.go       ←── WithEnvironment(env) 注入
    read_file.go  ←── WithEnvironment(env) 注入
    write_file.go ←── WithEnvironment(env) 注入
    edit_file.go  ←── WithEnvironment(env) 注入

cmd/harness9/tui.go
    └── sandboxes []sandbox.SandboxInfo  ← SandboxUpdateMsg 驱动更新
```

### 核心数据流

```
LLM 发起 ToolCall "bash"
    → engine 调用 Registry.Execute("bash", args)
    → BashTool.Execute(ctx, args)
        → env.RunBash(ctx, cmd, workDir)
            ├── LocalEnvironment → exec.Command("bash", "-c", cmd)   [Sandbox 关闭]
            └── DockerEnvironment → docker exec <id> bash -c <cmd>   [Sandbox 开启]
```

### 与现有架构的关系

- **不改动**：`engine`、`hooks`、`schema`、`provider`、`memory`、`ltm` 等包
- **最小改动**：现有 4 个工具文件各加一个 `WithEnvironment` 选项函数
- **新增**：`internal/sandbox/` 包，无循环依赖
- **扩展**：`internal/subagent/runner.go` 持有 Manager 引用，Sub-Agent 启动时自动 Create

---

## 3. Environment 接口

```go
// internal/sandbox/environment.go

// Environment 表示 Agent 工具运行其中的完整执行环境。
// LocalEnvironment 提供进程级隔离（当前默认），
// DockerEnvironment 提供 OS 级容器隔离。
type Environment interface {
    // RunBash 在环境中执行 bash 命令，返回合并的 stdout+stderr
    RunBash(ctx context.Context, cmd, workDir string) (string, error)
    // ReadFile 读取文件内容
    ReadFile(ctx context.Context, path string) ([]byte, error)
    // WriteFile 写入文件内容（自动创建父目录）
    WriteFile(ctx context.Context, path string, data []byte) error
    // ID 返回环境唯一标识（UUID）
    ID() string
    // Close 释放环境资源（停止容器、回收进程等）
    Close(ctx context.Context) error
}
```

### LocalEnvironment

封装当前 bash/read_file/write_file 的本地行为，零新增依赖，零行为变化。Sandbox 未启用时使用。

### DockerEnvironment

- `RunBash`：通过 `docker exec -w <workDir> <dockerID> bash -c <cmd>` 在容器内执行
- `ReadFile` / `WriteFile`：通过 bind mount 共享 workDir，宿主机 Go 侧直接读写（与容器视图一致）
- `Close`：停止并移除容器

---

## 4. Docker 安全配置

参考 HermesAgent `DockerEnvironment` 最高安全标准：

```go
docker run -d \
  --name harness9-<uuid> \
  --label harness9=1 \
  --cap-drop all \
  --cap-add DAC_OVERRIDE \
  --no-new-privileges \
  --pids-limit 256 \
  --cpus <cfg.CPUs> \
  --memory <cfg.Memory> \
  --network none \
  --tmpfs /tmp:size=256m,nosuid,noexec \
  --mount type=bind,src=<workDir>,dst=<workDir> \
  <cfg.Image> \
  sleep infinity
```

| 参数 | 值 | 说明 |
|------|-----|------|
| `--cap-drop all` | — | 丢弃所有 Linux Capabilities |
| `--cap-add DAC_OVERRIDE` | — | 仅恢复包管理器所需的最小能力 |
| `--no-new-privileges` | — | 禁止 setuid 特权提升 |
| `--pids-limit` | 256 | 防 fork bomb |
| `--network` | none | fail-closed，完全禁网 |
| `--tmpfs /tmp` | 256m,nosuid,noexec | 临时目录隔离 |
| bind mount | workDir | 宿主机与容器共享工作目录 |

---

## 5. Container 生命周期状态机

### 状态定义

```
         创建请求
            ↓
        [Pending]      ← docker run 已发出，等待容器就绪
            ↓ docker inspect Running=true
        [Running]      ← 正常执行状态，接受工具调用
            ↓ Close() 调用 或 Agent 退出
       [Stopping]      ← docker stop 已发出
            ↓
      [Terminated]     ← 容器已停止并移除

   任何状态 → [Failed] ← docker 命令报错 / 健康检查超时
```

### Container 结构

```go
// internal/sandbox/container.go

type ContainerState int

const (
    StatePending    ContainerState = iota
    StateRunning
    StateStopping
    StateTerminated
    StateFailed
)

type Container struct {
    id       string
    dockerID string
    state    ContainerState
    mu       sync.RWMutex
    env      *DockerEnvironment
    cfg      SandboxConfig
    err      error
}

func (c *Container) Start(ctx context.Context) error
func (c *Container) Stop(ctx context.Context) error
func (c *Container) State() ContainerState
func (c *Container) Environment() Environment
```

### 启动流程

```
Start(ctx)
  1. docker run -d ... sleep infinity  → 获取 dockerID
  2. 轮询 docker inspect 等待 Running=true
     ├── 就绪 → StateRunning
     └── 超时（默认 30s） → StateFailed
```

### 停止流程

```
Stop(ctx)
  1. → StateStopping
  2. docker stop -t 5 <dockerID>   （SIGTERM → 5s → SIGKILL）
  3. docker rm <dockerID>
  4. → StateTerminated
  └── 任意步骤失败 → StateFailed（继续尝试 rm）
```

### 异常自愈

```
RunBash 返回 "No such container"
  → 尝试重新 Start()
  → 重试命令一次
  → 仍失败 → 返回 error 给 LLM
```

---

## 6. Manager：多 Sandbox 并发管理

```go
// internal/sandbox/manager.go

type Manager struct {
    containers map[string]*Container
    mu         sync.RWMutex
    cfg        SandboxConfig
}

func NewManager(cfg SandboxConfig) *Manager

// Create 为一个 Agent 创建独立 Sandbox，返回可用的 Environment
func (m *Manager) Create(ctx context.Context, workDir string) (Environment, error)

// Destroy 销毁指定 Sandbox
func (m *Manager) Destroy(ctx context.Context, id string) error

// DestroyAll 销毁所有活跃 Sandbox（程序退出时调用）
func (m *Manager) DestroyAll(ctx context.Context)

// ReapOrphans 清理上次进程崩溃遗留的孤儿容器
func (m *Manager) ReapOrphans(ctx context.Context) error

// ListAll 返回所有活跃 Sandbox 的只读快照（线程安全）
func (m *Manager) ListAll() []SandboxInfo
```

### SandboxInfo

```go
type SandboxInfo struct {
    ID       string
    DockerID string         // 短 hash（前 12 位）
    State    ContainerState
    WorkDir  string
    Image    string
}
```

### 孤儿容器回收

```bash
# Manager 初始化时执行一次
docker ps -a \
  --filter label=harness9=1 \
  --filter status=exited \
  --format {{.ID}}
→ docker rm <id>
```

### 并发安全

- `mu.Lock()` 保护 containers map 写入
- `Container.Start()` 不持 Manager 锁，并发创建无阻塞
- `DockerEnvironment.RunBash` 无共享状态，天然并发安全

---

## 7. SandboxConfig

```go
// internal/sandbox/config.go

type SandboxConfig struct {
    Enabled      bool          // 是否启用 Sandbox，默认 false
    Image        string        // 默认 "ubuntu:22.04"
    CPUs         string        // 默认 "1.0"
    Memory       string        // 默认 "512m"
    PidsLimit    int           // 默认 256
    StartTimeout time.Duration // 默认 30s
    StopTimeout  time.Duration // 默认 10s
}

func DefaultConfig() SandboxConfig
```

通过环境变量覆盖（与现有 `internal/env/` 加载机制一致）：

| 环境变量 | 对应字段 |
|---------|---------|
| `SANDBOX_ENABLED` | `Enabled` |
| `SANDBOX_IMAGE` | `Image` |
| `SANDBOX_MEMORY` | `Memory` |
| `SANDBOX_CPUS` | `CPUs` |

---

## 8. 工具层集成

### WithEnvironment 选项函数（以 BashTool 为例）

```go
type BashTool struct {
    workDir string
    env     sandbox.Environment  // nil = 本地执行
}

func NewBashTool(workDir string, opts ...BashOption) *BashTool

type BashOption func(*BashTool)

func WithEnvironment(env sandbox.Environment) BashOption {
    return func(t *BashTool) { t.env = env }
}

func (t *BashTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
    if t.env != nil {
        return t.env.RunBash(ctx, cmd, t.workDir)  // Sandbox 路径
    }
    return t.runLocal(ctx, cmd)                     // 原有本地路径
}
```

`read_file`、`write_file`、`edit_file` 同样增加 `WithEnvironment`；当前文件读写通过 bind mount 在宿主机侧执行，行为与容器内一致。

### Sub-Agent 衔接

```go
// internal/subagent/runner.go

func (r *Runner) Run(ctx context.Context, def SubAgentDefinition) error {
    var env sandbox.Environment
    if r.sandboxMgr != nil {
        env, _ = r.sandboxMgr.Create(ctx, r.workDir)
        defer r.sandboxMgr.Destroy(ctx, env.ID())
    }
    // 注入 env 到子引擎工具集
}
```

**安全原则**：子 Agent Sandbox 配置继承父 Manager 的 `SandboxConfig`，不可扩权。

### main.go 完整初始化链路

```go
cfg := sandbox.DefaultConfig()   // 读取 SANDBOX_* 环境变量

var mgr *sandbox.Manager
var env sandbox.Environment      // nil = 本地执行（Sandbox 关闭时）

if cfg.Enabled {
    mgr = sandbox.NewManager(cfg)
    mgr.ReapOrphans(ctx)         // 清理孤儿容器
    env, _ = mgr.Create(ctx, workDir)
    defer mgr.DestroyAll(ctx)
}

// env 为 nil 时，工具走原有本地执行路径，行为与未引入 Sandbox 前完全一致
registry.Register(tools.NewBashTool(workDir, tools.WithEnvironment(env)))
registry.Register(tools.NewReadFileTool(workDir, tools.WithEnvironment(env)))
registry.Register(tools.NewWriteFileTool(workDir, tools.WithEnvironment(env)))
registry.Register(tools.NewEditFileTool(workDir, tools.WithEnvironment(env)))
// Runner 持有 mgr 引用（可为 nil），Sub-Agent 启动时若 mgr 非 nil 则自动 Create
```

---

## 9. TUI Sandbox 信息展示

### 展示位置

在现有 StatusBar 下方新增 **SandboxBar**，仅在有活跃 Sandbox 时显示。

### 展示格式

```
[Sandbox] 3a2f (main) Running │ 7b1c (sub-1) Running │ 9d4e (sub-2) Pending
```

### 颜色编码

| 状态 | 颜色 |
|------|------|
| Pending | 黄色 |
| Running | 绿色 |
| Stopping | 灰色 |
| Terminated | 灰色（短暂展示后消失）|
| Failed | 红色 |

### 数据流

```
Manager.Create() / Destroy() / 状态变更
    → 发送 sandboxUpdateMsg 到 TUI tea.Program
    → tuiModel.Update() 更新 sandboxes []SandboxInfo
    → tuiModel.View() 渲染 SandboxBar
```

### tuiModel 新增字段

```go
type tuiModel struct {
    // 现有字段 ...
    sandboxes []sandbox.SandboxInfo
}

type sandboxUpdateMsg struct {
    infos []sandbox.SandboxInfo
}
```

---

## 10. 测试策略

### MockEnvironment

```go
// internal/sandbox/mock_environment_test.go

type MockEnvironment struct {
    id          string
    RunBashFn   func(ctx context.Context, cmd, workDir string) (string, error)
    ReadFileFn  func(ctx context.Context, path string) ([]byte, error)
    WriteFileFn func(ctx context.Context, path string, data []byte) error
    Closed      bool
}
```

用于工具层单元测试，无需 Docker，完全确定性。

### 测试分层

| 层次 | 覆盖范围 | 构建标签 |
|------|---------|---------|
| 单元测试 | Environment 接口、Manager 状态管理、Container 状态机、工具注入 | 无（默认） |
| 集成测试 | DockerEnvironment 真实容器行为、孤儿回收 | `//go:build integration` |

```bash
# 单元测试（无需 Docker）
go test ./internal/sandbox/...

# 集成测试（需要 Docker Daemon）
go test -tags integration ./internal/sandbox/...
```

### 关键测试用例

- Container 状态机：正常启动、docker run 失败、健康检查超时
- Manager 并发：10 个 goroutine 同时 Create，验证无 race condition，ID 唯一
- 孤儿回收：模拟崩溃残留容器，验证 ReapOrphans 正确清理
- BashTool 注入：MockEnvironment 注入验证路由正确，nil env 验证本地路径

---

## 11. 文件改动清单

### 新增文件

| 文件 | 说明 |
|------|------|
| `internal/sandbox/environment.go` | Environment 接口 |
| `internal/sandbox/local_environment.go` | 本地进程级实现 |
| `internal/sandbox/docker_environment.go` | Docker 容器级实现 |
| `internal/sandbox/container.go` | 单容器生命周期状态机 |
| `internal/sandbox/manager.go` | 多 Sandbox 并发管理 |
| `internal/sandbox/config.go` | SandboxConfig |
| `internal/sandbox/*_test.go` | 各组件测试 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `internal/tools/bash.go` | 加 `WithEnvironment` 选项 + env 分支 |
| `internal/tools/read_file.go` | 加 `WithEnvironment` 选项 |
| `internal/tools/write_file.go` | 加 `WithEnvironment` 选项 |
| `internal/tools/edit_file.go` | 加 `WithEnvironment` 选项 |
| `internal/subagent/runner.go` | 持有 Manager，Sub-Agent 启动时 Create |
| `cmd/harness9/main.go` | Manager 初始化、ReapOrphans、工具注入 |
| `cmd/harness9/tui.go` | 新增 `sandboxes` 字段 + SandboxBar 渲染 |
| `cmd/harness9/tui_update.go` | 处理 `sandboxUpdateMsg` |
| `cmd/harness9/tui_view.go` | 渲染 SandboxBar |
