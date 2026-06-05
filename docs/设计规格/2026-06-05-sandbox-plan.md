# Sandbox 系统实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 harness9 实现 Docker 容器级 Sandbox 系统，通过 Environment 接口透明替换工具执行环境，支持多 Sandbox 并发与完整生命周期管理。

**Architecture:** 新增 `internal/sandbox/` 包，定义 `Environment` 接口及 `LocalEnvironment`（进程级）/ `DockerEnvironment`（容器级）两个实现；`Manager` 管理所有 Container 生命周期；现有 4 个工具通过 `WithEnvironment` 选项注入执行环境；Sub-Agent Runner 持有 Manager，为每个子代理自动创建独立 Sandbox；TUI 新增 SandboxBar 实时展示状态。

**Tech Stack:** Go 1.25.3、Docker CLI（`docker exec/run/stop/rm/ps`）、`crypto/rand`（UUID）、`lipgloss`（TUI 样式）

---

## 文件改动清单

### 新增文件

| 文件 | 职责 |
|------|------|
| `internal/sandbox/config.go` | SandboxConfig + DefaultConfig |
| `internal/sandbox/environment.go` | Environment 接口 |
| `internal/sandbox/local_environment.go` | 进程级实现（Sandbox 关闭时默认） |
| `internal/sandbox/local_environment_test.go` | LocalEnvironment 单元测试 |
| `internal/sandbox/container.go` | 单容器生命周期状态机（含 cmdRunner 可注入接口） |
| `internal/sandbox/container_test.go` | Container 状态机单元测试（mock docker） |
| `internal/sandbox/docker_environment.go` | Docker 容器级实现（docker exec 路由） |
| `internal/sandbox/docker_environment_test.go` | DockerEnvironment 集成测试（`//go:build integration`） |
| `internal/sandbox/manager.go` | Manager：Create/Destroy/DestroyAll/ReapOrphans/ListAll |
| `internal/sandbox/manager_test.go` | Manager 并发 + 孤儿回收测试（mock） |

### 修改文件

| 文件 | 改动 |
|------|------|
| `internal/tools/bash.go` | 加 `BashOption`/`WithEnvironment`；`NewBashTool` 改为可变参；`Execute` 加 env 分支 |
| `internal/tools/bash_test.go` | 加 MockEnvironment 注入测试 |
| `internal/tools/read_file.go` | 加 `ReadFileOption`/`WithEnvironment`；`NewReadFileTool` 改为可变参 |
| `internal/tools/write_file.go` | 加 `WriteFileOption`/`WithEnvironment`；`NewWriteFileTool` 改为可变参 |
| `internal/tools/edit_file.go` | 加 `EditFileOption`/`WithEnvironment`；`NewEditFileTool` 改为可变参 |
| `internal/subagent/runner.go` | 加 `sandboxMgr` 字段；`buildChildRegistry` 加 baseTools 参数；`Run` 加 sandbox 创建/销毁 |
| `internal/subagent/runner_test.go` | 加 sandbox 集成测试（MockManager） |
| `cmd/harness9/main.go` | 加 SandboxManager 初始化、ReapOrphans、工具 env 注入、Runner 传 mgr |
| `cmd/harness9/tui.go` | 加 `sandboxes []sandbox.SandboxInfo` 字段 + SandboxBar 样式变量 |
| `cmd/harness9/tui_update.go` | 加 `sandboxUpdateMsg` 类型 + Update 分支 |
| `cmd/harness9/tui_view.go` | 加 `renderSandboxBar()` + 在 View 中调用 |

---

## Task 1: SandboxConfig

**Files:**
- Create: `internal/sandbox/config.go`
- Create: `internal/sandbox/config_test.go`

- [ ] **Step 1: 写 failing 测试**

```go
// internal/sandbox/config_test.go
package sandbox

import (
	"os"
	"testing"
	"time"
)

func TestDefaultConfig_Defaults(t *testing.T) {
	os.Unsetenv("SANDBOX_ENABLED")
	os.Unsetenv("SANDBOX_IMAGE")
	os.Unsetenv("SANDBOX_CPUS")
	os.Unsetenv("SANDBOX_MEMORY")

	cfg := DefaultConfig()

	if cfg.Enabled {
		t.Error("Enabled 默认应为 false")
	}
	if cfg.Image != "ubuntu:22.04" {
		t.Errorf("Image = %q, 期望 ubuntu:22.04", cfg.Image)
	}
	if cfg.CPUs != "1.0" {
		t.Errorf("CPUs = %q, 期望 1.0", cfg.CPUs)
	}
	if cfg.Memory != "512m" {
		t.Errorf("Memory = %q, 期望 512m", cfg.Memory)
	}
	if cfg.PidsLimit != 256 {
		t.Errorf("PidsLimit = %d, 期望 256", cfg.PidsLimit)
	}
	if cfg.StartTimeout != 30*time.Second {
		t.Errorf("StartTimeout = %v, 期望 30s", cfg.StartTimeout)
	}
	if cfg.StopTimeout != 10*time.Second {
		t.Errorf("StopTimeout = %v, 期望 10s", cfg.StopTimeout)
	}
}

func TestDefaultConfig_FromEnv(t *testing.T) {
	t.Setenv("SANDBOX_ENABLED", "true")
	t.Setenv("SANDBOX_IMAGE", "alpine:3.18")
	t.Setenv("SANDBOX_CPUS", "2.0")
	t.Setenv("SANDBOX_MEMORY", "256m")

	cfg := DefaultConfig()

	if !cfg.Enabled {
		t.Error("SANDBOX_ENABLED=true 时 Enabled 应为 true")
	}
	if cfg.Image != "alpine:3.18" {
		t.Errorf("Image = %q, 期望 alpine:3.18", cfg.Image)
	}
	if cfg.CPUs != "2.0" {
		t.Errorf("CPUs = %q, 期望 2.0", cfg.CPUs)
	}
	if cfg.Memory != "256m" {
		t.Errorf("Memory = %q, 期望 256m", cfg.Memory)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd /Users/zsa/Desktop/harness/harness9
go test ./internal/sandbox/... 2>&1
```
预期：`no Go files in .../sandbox` 或 compile error。

- [ ] **Step 3: 实现 config.go**

```go
// internal/sandbox/config.go

// Package sandbox 提供 Docker 容器级执行环境抽象，
// 为 Agent 工具调用提供 OS 级隔离（网络、进程空间、文件系统）。
package sandbox

import (
	"os"
	"time"
)

// SandboxConfig 是 Sandbox 系统的全局配置。
// 通过 DefaultConfig 从环境变量读取，支持 .env 文件覆盖（internal/env 先行加载）。
type SandboxConfig struct {
	// Enabled 是否启用 Docker Sandbox。false 时工具使用本地进程执行，行为与原始版本完全一致。
	Enabled bool
	// Image Docker 镜像名称。
	Image string
	// CPUs 容器可用 CPU 核数（docker --cpus 参数）。
	CPUs string
	// Memory 容器内存上限（docker --memory 参数）。
	Memory string
	// PidsLimit 容器进程数上限，防 fork bomb（docker --pids-limit 参数）。
	PidsLimit int
	// StartTimeout 等待容器就绪的超时时间。
	StartTimeout time.Duration
	// StopTimeout docker stop 的宽限期，超时后 SIGKILL。
	StopTimeout time.Duration
}

// DefaultConfig 从环境变量读取配置，未设置时使用内置安全默认值。
func DefaultConfig() SandboxConfig {
	return SandboxConfig{
		Enabled:      os.Getenv("SANDBOX_ENABLED") == "true",
		Image:        getenvOr("SANDBOX_IMAGE", "ubuntu:22.04"),
		CPUs:         getenvOr("SANDBOX_CPUS", "1.0"),
		Memory:       getenvOr("SANDBOX_MEMORY", "512m"),
		PidsLimit:    256,
		StartTimeout: 30 * time.Second,
		StopTimeout:  10 * time.Second,
	}
}

func getenvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./internal/sandbox/... -v -run TestDefaultConfig
```
预期：`PASS`，2 个测试全部通过。

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/config.go internal/sandbox/config_test.go
git commit -m "feat(sandbox): SandboxConfig + DefaultConfig（从环境变量读取）"
```

---

## Task 2: Environment 接口 + LocalEnvironment

**Files:**
- Create: `internal/sandbox/environment.go`
- Create: `internal/sandbox/local_environment.go`
- Create: `internal/sandbox/local_environment_test.go`

- [ ] **Step 1: 写 failing 测试**

```go
// internal/sandbox/local_environment_test.go
package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalEnvironment_ID(t *testing.T) {
	env := NewLocalEnvironment()
	if env.ID() == "" {
		t.Error("ID() 不应为空")
	}
}

func TestLocalEnvironment_RunBash_Success(t *testing.T) {
	env := NewLocalEnvironment()
	out, err := env.RunBash(context.Background(), "echo hello", t.TempDir())
	if err != nil {
		t.Fatalf("RunBash 不应返回 Go error: %v", err)
	}
	if out != "hello\n" {
		t.Errorf("RunBash = %q, 期望 %q", out, "hello\n")
	}
}

func TestLocalEnvironment_RunBash_Failure(t *testing.T) {
	env := NewLocalEnvironment()
	out, err := env.RunBash(context.Background(), "exit 1", t.TempDir())
	if err != nil {
		t.Fatalf("RunBash 失败时不应返回 Go error（Self-Correction Loopback）: %v", err)
	}
	if out == "" {
		t.Error("RunBash 失败时应返回非空错误字符串")
	}
}

func TestLocalEnvironment_ReadWriteFile(t *testing.T) {
	env := NewLocalEnvironment()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := env.WriteFile(context.Background(), path, []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := env.ReadFile(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("ReadFile = %q, 期望 hello", data)
	}
}

func TestLocalEnvironment_WriteFile_AutoMkdir(t *testing.T) {
	env := NewLocalEnvironment()
	path := filepath.Join(t.TempDir(), "nested", "dir", "file.txt")

	if err := env.WriteFile(context.Background(), path, []byte("data")); err != nil {
		t.Fatalf("WriteFile（自动创建目录）: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("WriteFile 后文件应存在: %v", err)
	}
}

func TestLocalEnvironment_Close(t *testing.T) {
	env := NewLocalEnvironment()
	if err := env.Close(context.Background()); err != nil {
		t.Errorf("LocalEnvironment.Close() 不应返回 error: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/sandbox/... -run TestLocalEnvironment
```
预期：compile error（类型未定义）。

- [ ] **Step 3: 实现 environment.go**

```go
// internal/sandbox/environment.go
package sandbox

import "context"

// Environment 表示 Agent 工具运行其中的完整执行环境。
// LocalEnvironment 提供进程级隔离（Sandbox 关闭时默认），
// DockerEnvironment 提供 OS 级容器隔离（Sandbox 开启时）。
type Environment interface {
	// RunBash 执行 bash 命令，返回合并的 stdout+stderr。
	// 命令执行失败（非零退出）时返回 (errorString, nil)，保持 Self-Correction Loopback 语义。
	RunBash(ctx context.Context, cmd, workDir string) (string, error)
	// ReadFile 读取文件内容。
	ReadFile(ctx context.Context, path string) ([]byte, error)
	// WriteFile 写入文件，自动创建父目录。
	WriteFile(ctx context.Context, path string, data []byte) error
	// ID 返回环境的唯一标识符（UUID 或 "local"）。
	ID() string
	// Close 释放环境持有的所有资源。
	Close(ctx context.Context) error
}
```

- [ ] **Step 4: 实现 local_environment.go**

```go
// internal/sandbox/local_environment.go
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// LocalEnvironment 在本地进程中执行工具调用，不提供容器级隔离。
// 当 SandboxConfig.Enabled = false 时使用，行为与引入 Sandbox 前完全一致。
type LocalEnvironment struct{}

// NewLocalEnvironment 创建本地进程执行环境。
func NewLocalEnvironment() *LocalEnvironment { return &LocalEnvironment{} }

func (e *LocalEnvironment) ID() string { return "local" }

func (e *LocalEnvironment) Close(_ context.Context) error { return nil }

// RunBash 在本地以子进程方式执行 bash 命令。
func (e *LocalEnvironment) RunBash(_ context.Context, cmd, workDir string) (string, error) {
	c := exec.Command("bash", "-c", cmd)
	c.Dir = workDir
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("执行报错: %v\n输出:\n%s", err, out), nil
	}
	return string(out), nil
}

// ReadFile 从本地文件系统读取文件内容。
func (e *LocalEnvironment) ReadFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile 向本地文件系统写入文件，自动创建父目录。
func (e *LocalEnvironment) WriteFile(_ context.Context, path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
```

- [ ] **Step 5: 运行测试，确认通过**

```bash
go test ./internal/sandbox/... -v -run TestLocalEnvironment
```
预期：`PASS`，5 个测试全部通过。

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/environment.go internal/sandbox/local_environment.go internal/sandbox/local_environment_test.go
git commit -m "feat(sandbox): Environment 接口 + LocalEnvironment（进程级默认实现）"
```

---

## Task 3: Container 生命周期状态机

**Files:**
- Create: `internal/sandbox/container.go`
- Create: `internal/sandbox/container_test.go`

- [ ] **Step 1: 写 failing 测试**

```go
// internal/sandbox/container_test.go
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// mockCmdRunner 捕获 docker 命令调用，按顺序返回预设响应。
type mockCmdRunner struct {
	mu        sync.Mutex
	responses []mockResponse
	idx       int
	Calls     [][]string
}

type mockResponse struct {
	out string
	err error
}

func newMock(pairs ...interface{}) *mockCmdRunner {
	m := &mockCmdRunner{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.responses = append(m.responses, mockResponse{
			out: pairs[i].(string),
			err: pairs[i+1].(error),
		})
	}
	return m
}

func errNil() error { return nil }

func (m *mockCmdRunner) run(_ context.Context, args ...string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, append([]string{}, args...))
	if m.idx >= len(m.responses) {
		return "", nil
	}
	r := m.responses[m.idx]
	m.idx++
	return r.out, r.err
}

func testCfg() SandboxConfig {
	return SandboxConfig{
		Image:        "ubuntu:22.04",
		CPUs:         "1.0",
		Memory:       "512m",
		PidsLimit:    256,
		StartTimeout: 5 * time.Second,
		StopTimeout:  5 * time.Second,
	}
}

func TestContainerStart_Success(t *testing.T) {
	mock := newMock(
		"abc123def456", errNil(), // docker run → containerID
		"true", errNil(),         // docker inspect → running
	)
	c := newContainer("test-uuid", t.TempDir(), testCfg(), mock.run)

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	if c.State() != StateRunning {
		t.Errorf("State = %v, 期望 Running", c.State())
	}
	if c.dockerID != "abc123def456" {
		t.Errorf("dockerID = %q, 期望 abc123def456", c.dockerID)
	}
}

func TestContainerStart_DockerRunFails(t *testing.T) {
	mock := newMock(
		"", errors.New("daemon not found"),
	)
	c := newContainer("test-uuid", t.TempDir(), testCfg(), mock.run)

	err := c.Start(context.Background())
	if err == nil {
		t.Fatal("docker run 失败时 Start() 应返回 error")
	}
	if c.State() != StateFailed {
		t.Errorf("State = %v, 期望 Failed", c.State())
	}
}

func TestContainerStop(t *testing.T) {
	mock := newMock(
		"abc123", errNil(), // docker run
		"true", errNil(),   // docker inspect
		"", errNil(),       // docker stop
		"", errNil(),       // docker rm
	)
	c := newContainer("test-uuid", t.TempDir(), testCfg(), mock.run)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error: %v", err)
	}
	if c.State() != StateTerminated {
		t.Errorf("State = %v, 期望 Terminated", c.State())
	}
}

func TestContainerStateString(t *testing.T) {
	cases := []struct {
		state ContainerState
		want  string
	}{
		{StatePending, "Pending"},
		{StateRunning, "Running"},
		{StateStopping, "Stopping"},
		{StateTerminated, "Terminated"},
		{StateFailed, "Failed"},
	}
	for _, tc := range cases {
		if got := tc.state.String(); got != tc.want {
			t.Errorf("ContainerState(%d).String() = %q, 期望 %q", tc.state, got, tc.want)
		}
	}
}

func TestContainerDockerRunArgs(t *testing.T) {
	mock := newMock(
		"abc123", errNil(),
		"true", errNil(),
	)
	cfg := testCfg()
	workDir := t.TempDir()
	c := newContainer("my-uuid", workDir, cfg, mock.run)
	_ = c.Start(context.Background())

	// 验证 docker run 调用参数包含安全加固选项
	runArgs := mock.Calls[0]
	mustContain := []string{
		"--cap-drop", "all",
		"--no-new-privileges",
		"--network", "none",
		"--label", "harness9=1",
		"--name", "harness9-my-uuid",
	}
	argStr := fmt.Sprintf("%v", runArgs)
	for _, want := range mustContain {
		found := false
		for _, arg := range runArgs {
			if arg == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("docker run 参数缺少 %q，完整参数: %s", want, argStr)
		}
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/sandbox/... -run TestContainer
```
预期：compile error（container 类型未定义）。

- [ ] **Step 3: 实现 container.go**

```go
// internal/sandbox/container.go
package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ContainerState 表示 Sandbox 容器的当前生命周期状态。
type ContainerState int

const (
	StatePending    ContainerState = iota // 容器创建中，等待就绪
	StateRunning                          // 容器正常运行，接受工具调用
	StateStopping                         // 容器停止中（docker stop 已发出）
	StateTerminated                       // 容器已停止并移除
	StateFailed                           // 发生不可恢复错误
)

// String 返回状态的可读名称，供 TUI 展示。
func (s ContainerState) String() string {
	switch s {
	case StatePending:
		return "Pending"
	case StateRunning:
		return "Running"
	case StateStopping:
		return "Stopping"
	case StateTerminated:
		return "Terminated"
	case StateFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// cmdRunner 是可注入的 docker 命令执行函数，便于单元测试 mock 替换真实 docker CLI。
type cmdRunner func(ctx context.Context, args ...string) (string, error)

// realCmdRunner 调用真实的 docker CLI，返回 stdout+stderr 合并输出（去首尾空白）。
func realCmdRunner(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Container 管理单个 Docker 容器的完整生命周期：创建→运行→停止→回收。
type Container struct {
	id       string         // Sandbox UUID（由 Manager 生成）
	dockerID string         // docker container ID（docker run 返回的完整 ID）
	state    ContainerState
	mu       sync.RWMutex
	cfg      SandboxConfig
	workDir  string
	run      cmdRunner
	err      error // StateFailed 时记录原因
}

func newContainer(id, workDir string, cfg SandboxConfig, run cmdRunner) *Container {
	return &Container{
		id:      id,
		state:   StatePending,
		workDir: workDir,
		cfg:     cfg,
		run:     run,
	}
}

// ID 返回 Sandbox UUID。
func (c *Container) ID() string { return c.id }

// DockerID 返回 Docker container ID（线程安全）。
func (c *Container) DockerID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dockerID
}

// State 返回当前容器状态（线程安全）。
func (c *Container) State() ContainerState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// Err 返回 Failed 状态的错误原因（其他状态返回 nil）。
func (c *Container) Err() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.err
}

func (c *Container) setState(s ContainerState, err error) {
	c.mu.Lock()
	c.state = s
	c.err = err
	c.mu.Unlock()
}

// Start 启动容器并等待就绪：docker run → 轮询 docker inspect → StateRunning。
func (c *Container) Start(ctx context.Context) error {
	startCtx, cancel := context.WithTimeout(ctx, c.cfg.StartTimeout)
	defer cancel()

	dockerID, err := c.run(startCtx,
		"run", "-d",
		"--name", "harness9-"+c.id,
		"--label", "harness9=1",
		"--cap-drop", "all",
		"--cap-add", "DAC_OVERRIDE",
		"--no-new-privileges",
		"--pids-limit", fmt.Sprintf("%d", c.cfg.PidsLimit),
		"--cpus", c.cfg.CPUs,
		"--memory", c.cfg.Memory,
		"--network", "none",
		"--tmpfs", "/tmp:size=256m,nosuid,noexec",
		"--mount", fmt.Sprintf("type=bind,src=%s,dst=%s", c.workDir, c.workDir),
		c.cfg.Image,
		"sleep", "infinity",
	)
	if err != nil {
		c.setState(StateFailed, fmt.Errorf("docker run 失败: %w", err))
		return c.err
	}
	c.mu.Lock()
	c.dockerID = dockerID
	c.mu.Unlock()

	// 轮询等待容器 Running=true，超时则转 Failed
	for {
		if startCtx.Err() != nil {
			c.setState(StateFailed, fmt.Errorf("等待容器就绪超时（%v）", c.cfg.StartTimeout))
			return c.err
		}
		out, inspectErr := c.run(startCtx, "inspect", "--format={{.State.Running}}", dockerID)
		if inspectErr == nil && out == "true" {
			break
		}
		select {
		case <-startCtx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}

	c.setState(StateRunning, nil)
	return nil
}

// Stop 停止并移除容器：docker stop（带宽限期）→ docker rm → StateTerminated。
func (c *Container) Stop(ctx context.Context) error {
	c.setState(StateStopping, nil)

	stopCtx, cancel := context.WithTimeout(ctx, c.cfg.StopTimeout)
	defer cancel()

	dockerID := c.DockerID()
	// docker stop 宽限期内发 SIGTERM，超时后 SIGKILL
	_, _ = c.run(stopCtx, "stop", "-t", "5", dockerID)
	// docker rm 无论 stop 是否成功都尝试
	_, _ = c.run(ctx, "rm", dockerID)

	c.setState(StateTerminated, nil)
	return nil
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./internal/sandbox/... -v -run TestContainer
```
预期：`PASS`，4 个测试全部通过。

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/container.go internal/sandbox/container_test.go
git commit -m "feat(sandbox): Container 五状态生命周期状态机（cmdRunner 可 mock）"
```

---

## Task 4: DockerEnvironment

**Files:**
- Create: `internal/sandbox/docker_environment.go`
- Create: `internal/sandbox/docker_environment_test.go`

- [ ] **Step 1: 写 failing 测试（包含集成测试 + 单元测试）**

```go
// internal/sandbox/docker_environment_test.go
package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ---- 单元测试（无需 Docker）----

func TestDockerEnvironment_ID(t *testing.T) {
	env := newDockerEnvironment("container123", "sandbox-uuid", t.TempDir(), nil)
	if env.ID() != "sandbox-uuid" {
		t.Errorf("ID() = %q, 期望 sandbox-uuid", env.ID())
	}
}

func TestDockerEnvironment_RunBash_Success(t *testing.T) {
	mock := newMock("hello\n", errNil())
	env := newDockerEnvironment("container123", "uuid", t.TempDir(), mock.run)

	out, err := env.RunBash(context.Background(), "echo hello", "/workspace")
	if err != nil {
		t.Fatalf("RunBash 不应返回 Go error: %v", err)
	}
	if out != "hello\n" {
		t.Errorf("RunBash = %q, 期望 hello\\n", out)
	}
	// 验证调用了 docker exec -w <workDir> <containerID> bash -c <cmd>
	if len(mock.Calls) == 0 || mock.Calls[0][0] != "exec" {
		t.Errorf("应调用 docker exec，实际调用: %v", mock.Calls)
	}
}

func TestDockerEnvironment_RunBash_CommandFails(t *testing.T) {
	mock := newMock("exit 1 output", errors.New("exit status 1"))
	env := newDockerEnvironment("container123", "uuid", t.TempDir(), mock.run)

	out, err := env.RunBash(context.Background(), "exit 1", "/workspace")
	if err != nil {
		t.Fatalf("命令失败时不应返回 Go error（Self-Correction Loopback）: %v", err)
	}
	if !strings.Contains(out, "执行报错") {
		t.Errorf("命令失败应包含错误信息, got: %q", out)
	}
}

func TestDockerEnvironment_Close(t *testing.T) {
	env := newDockerEnvironment("c123", "uuid", t.TempDir(), nil)
	if err := env.Close(context.Background()); err != nil {
		t.Errorf("Close() 不应返回 error: %v", err)
	}
}

// ---- 集成测试（需要 Docker Daemon，通过 -tags integration 触发）----
// 运行：go test -tags integration ./internal/sandbox/... -v -run TestDockerEnvironmentIntegration
```

```go
// internal/sandbox/docker_environment_integration_test.go
//go:build integration

package sandbox

import (
	"context"
	"strings"
	"testing"
)

func TestDockerEnvironmentIntegration_RunBash(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image = "ubuntu:22.04"
	cfg.StartTimeout = 60 * 1000000000 // 60s

	c := newContainer("integ-test", t.TempDir(), cfg, realCmdRunner)
	if err := c.Start(context.Background()); err != nil {
		t.Skipf("Docker 不可用，跳过集成测试: %v", err)
	}
	defer c.Stop(context.Background())

	env := newDockerEnvironment(c.dockerID, c.id, t.TempDir(), realCmdRunner)
	out, err := env.RunBash(context.Background(), "echo hello_from_container", "/tmp")
	if err != nil {
		t.Fatalf("RunBash: %v", err)
	}
	if !strings.Contains(out, "hello_from_container") {
		t.Errorf("RunBash = %q, 期望包含 hello_from_container", out)
	}
}
```

- [ ] **Step 2: 运行单元测试，确认失败**

```bash
go test ./internal/sandbox/... -run TestDockerEnvironment
```
预期：compile error（newDockerEnvironment 未定义）。

- [ ] **Step 3: 实现 docker_environment.go**

```go
// internal/sandbox/docker_environment.go
package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// DockerEnvironment 通过 docker exec 在 Docker 容器内执行 bash 命令，
// 提供 OS 级进程隔离（独立 namespace、capability 限制、网络禁用）。
// 文件读写通过 bind mount 共享 workDir 与宿主机，保持文件系统视图一致。
type DockerEnvironment struct {
	containerID string // Docker container ID（完整 ID）
	id          string // Sandbox UUID
	workDir     string
	run         cmdRunner
}

func newDockerEnvironment(containerID, id, workDir string, run cmdRunner) *DockerEnvironment {
	return &DockerEnvironment{
		containerID: containerID,
		id:          id,
		workDir:     workDir,
		run:         run,
	}
}

func (e *DockerEnvironment) ID() string { return e.id }

// Close 是空操作：DockerEnvironment 不负责容器生命周期，由 Manager/Container 管理。
func (e *DockerEnvironment) Close(_ context.Context) error { return nil }

// RunBash 通过 docker exec 在容器内执行 bash 命令。
// 命令失败（非零退出）时返回 (errorString, nil)，保持 Self-Correction Loopback 语义。
func (e *DockerEnvironment) RunBash(ctx context.Context, cmd, workDir string) (string, error) {
	out, err := e.run(ctx,
		"exec", "-w", workDir, e.containerID,
		"bash", "-c", cmd,
	)
	if err != nil {
		return fmt.Sprintf("执行报错: %v\n输出:\n%s", err, out), nil
	}
	return out, nil
}

// ReadFile 从 bind mount 的宿主机文件系统读取文件，与容器内视图一致。
func (e *DockerEnvironment) ReadFile(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteFile 向 bind mount 的宿主机文件系统写入文件，自动创建父目录。
func (e *DockerEnvironment) WriteFile(_ context.Context, path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
```

- [ ] **Step 4: 运行单元测试，确认通过**

```bash
go test ./internal/sandbox/... -v -run TestDockerEnvironment
```
预期：`PASS`，4 个单元测试通过（集成测试被跳过）。

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/docker_environment.go internal/sandbox/docker_environment_test.go internal/sandbox/docker_environment_integration_test.go
git commit -m "feat(sandbox): DockerEnvironment（docker exec 路由 + bind mount 文件系统）"
```

---

## Task 5: Manager（多 Sandbox 并发管理）

**Files:**
- Create: `internal/sandbox/manager.go`
- Create: `internal/sandbox/manager_test.go`

- [ ] **Step 1: 写 failing 测试**

```go
// internal/sandbox/manager_test.go
package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockContainerFactory 替代真实 docker 调用，用于 Manager 单元测试。
// 注入到 Manager，newContainer 时自动使用 mock runner。
func newTestManager() *Manager {
	cfg := testCfg()
	mgr := NewManager(cfg)
	// 注入 mock runner 工厂，使 Create 不真实启动 Docker
	mgr.runnerFactory = func(id, workDir string, c SandboxConfig) cmdRunner {
		return newMock(
			id[:8], errNil(), // docker run → dockerID = id prefix
			"true", errNil(), // docker inspect → running
			"", errNil(),     // docker stop
			"", errNil(),     // docker rm
		).run
	}
	return mgr
}

func TestManager_CreateAndListAll(t *testing.T) {
	mgr := newTestManager()
	workDir := t.TempDir()

	env, err := mgr.Create(context.Background(), workDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if env == nil {
		t.Fatal("Create 应返回非 nil Environment")
	}

	infos := mgr.ListAll()
	if len(infos) != 1 {
		t.Errorf("ListAll 应有 1 个 Sandbox，实际 %d", len(infos))
	}
	if infos[0].State != StateRunning {
		t.Errorf("Sandbox 状态应为 Running，实际 %v", infos[0].State)
	}
	if infos[0].WorkDir != workDir {
		t.Errorf("WorkDir = %q, 期望 %q", infos[0].WorkDir, workDir)
	}
}

func TestManager_Destroy(t *testing.T) {
	mgr := newTestManager()
	env, _ := mgr.Create(context.Background(), t.TempDir())

	if err := mgr.Destroy(context.Background(), env.ID()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(mgr.ListAll()) != 0 {
		t.Error("Destroy 后 ListAll 应为空")
	}
}

func TestManager_DestroyAll(t *testing.T) {
	mgr := newTestManager()
	for i := 0; i < 3; i++ {
		if _, err := mgr.Create(context.Background(), t.TempDir()); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	if len(mgr.ListAll()) != 3 {
		t.Fatalf("期望 3 个 Sandbox")
	}

	mgr.DestroyAll(context.Background())
	if len(mgr.ListAll()) != 0 {
		t.Error("DestroyAll 后 ListAll 应为空")
	}
}

func TestManager_ConcurrentCreate(t *testing.T) {
	mgr := newTestManager()
	var wg sync.WaitGroup
	const n = 10

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := mgr.Create(context.Background(), t.TempDir()); err != nil {
				t.Errorf("并发 Create 失败: %v", err)
			}
		}()
	}
	wg.Wait()

	infos := mgr.ListAll()
	if len(infos) != n {
		t.Errorf("并发 Create 后应有 %d 个 Sandbox，实际 %d", n, len(infos))
	}

	// 验证所有 ID 唯一
	ids := make(map[string]bool)
	for _, info := range infos {
		if ids[info.ID] {
			t.Errorf("Sandbox ID 重复: %s", info.ID)
		}
		ids[info.ID] = true
	}
}

func TestManager_WithUpdateNotify(t *testing.T) {
	mgr := newTestManager()
	var mu sync.Mutex
	var received [][]SandboxInfo
	mgr.WithUpdateNotify(func(infos []SandboxInfo) {
		mu.Lock()
		received = append(received, infos)
		mu.Unlock()
	})

	mgr.Create(context.Background(), t.TempDir())

	time.Sleep(10 * time.Millisecond) // 等待通知
	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Error("Create 后应触发 onUpdate 通知")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/sandbox/... -run TestManager
```
预期：compile error（Manager 未定义）。

- [ ] **Step 3: 实现 manager.go**

```go
// internal/sandbox/manager.go
package sandbox

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
)

// SandboxInfo 是 Sandbox 状态的只读快照，用于 TUI 展示（ListAll 返回）。
type SandboxInfo struct {
	ID       string         // Sandbox UUID
	DockerID string         // Docker container ID（前 12 位短 hash）
	State    ContainerState
	WorkDir  string
	Image    string
}

// Manager 管理所有活跃 Sandbox 的生命周期，是 Sandbox 系统的单一事实源。
// 线程安全：所有公开方法均可并发调用。
type Manager struct {
	containers    map[string]*Container
	mu            sync.RWMutex
	cfg           SandboxConfig
	onUpdate      func([]SandboxInfo)
	// runnerFactory 用于测试注入 mock runner；nil 时使用 realCmdRunner。
	runnerFactory func(id, workDir string, cfg SandboxConfig) cmdRunner
}

// NewManager 创建 Sandbox 管理器。
func NewManager(cfg SandboxConfig) *Manager {
	return &Manager{
		containers: make(map[string]*Container),
		cfg:        cfg,
	}
}

// WithUpdateNotify 设置状态变更通知回调，供 TUI 通过 tea.Program.Send 接收更新。
// 每次 Create/Destroy/DestroyAll 后调用。
func (m *Manager) WithUpdateNotify(fn func([]SandboxInfo)) {
	m.onUpdate = fn
}

// Create 为一个 Agent 创建独立 Sandbox，返回可用的 Environment。
// 成功后 Sandbox 处于 StateRunning 状态，工具调用可立即路由。
func (m *Manager) Create(ctx context.Context, workDir string) (Environment, error) {
	id := generateID()

	run := realCmdRunner
	if m.runnerFactory != nil {
		run = m.runnerFactory(id, workDir, m.cfg)
	}

	c := newContainer(id, workDir, m.cfg, run)
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("sandbox: 启动容器失败: %w", err)
	}

	m.mu.Lock()
	m.containers[id] = c
	m.mu.Unlock()

	m.notify()
	return newDockerEnvironment(c.DockerID(), id, workDir, run), nil
}

// Destroy 停止并移除指定 Sandbox。ID 不存在时静默返回 nil。
func (m *Manager) Destroy(ctx context.Context, id string) error {
	m.mu.Lock()
	c, ok := m.containers[id]
	if ok {
		delete(m.containers, id)
	}
	m.mu.Unlock()

	if !ok {
		return nil
	}
	err := c.Stop(ctx)
	m.notify()
	return err
}

// DestroyAll 并发停止所有活跃 Sandbox，程序退出时调用（defer）。
func (m *Manager) DestroyAll(ctx context.Context) {
	m.mu.Lock()
	cs := make([]*Container, 0, len(m.containers))
	for _, c := range m.containers {
		cs = append(cs, c)
	}
	m.containers = make(map[string]*Container)
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, c := range cs {
		wg.Add(1)
		c := c
		go func() {
			defer wg.Done()
			_ = c.Stop(ctx)
		}()
	}
	wg.Wait()
	m.notify()
}

// ReapOrphans 清理上次进程崩溃遗留的孤儿容器（label=harness9=1，status=exited 或 dead）。
// 在 Manager 初始化时调用一次；错误时 fail-open（打印日志但不阻断启动）。
func (m *Manager) ReapOrphans(ctx context.Context) error {
	out, err := realCmdRunner(ctx,
		"ps", "-a",
		"--filter", "label=harness9=1",
		"--filter", "status=exited",
		"--format", "{{.ID}}",
	)
	if err != nil {
		return fmt.Errorf("sandbox: 列出孤儿容器失败: %w", err)
	}
	for _, id := range strings.Fields(out) {
		_, _ = realCmdRunner(ctx, "rm", id)
	}
	return nil
}

// ListAll 返回所有活跃 Sandbox 的只读快照（线程安全，不持锁做 I/O）。
func (m *Manager) ListAll() []SandboxInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]SandboxInfo, 0, len(m.containers))
	for _, c := range m.containers {
		c.mu.RLock()
		infos = append(infos, SandboxInfo{
			ID:       c.id,
			DockerID: shortDockerID(c.dockerID),
			State:    c.state,
			WorkDir:  c.workDir,
			Image:    c.cfg.Image,
		})
		c.mu.RUnlock()
	}
	return infos
}

func (m *Manager) notify() {
	if m.onUpdate != nil {
		m.onUpdate(m.ListAll())
	}
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func shortDockerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
go test ./internal/sandbox/... -v -run TestManager -race
```
预期：`PASS`，5 个测试通过，无 race condition。

- [ ] **Step 5: 运行全包测试**

```bash
go test ./internal/sandbox/... -race
```
预期：所有测试通过。

- [ ] **Step 6: Commit**

```bash
git add internal/sandbox/manager.go internal/sandbox/manager_test.go
git commit -m "feat(sandbox): Manager（Create/Destroy/DestroyAll/ReapOrphans/ListAll，并发安全）"
```

---

## Task 6: BashTool WithEnvironment 集成

**Files:**
- Modify: `internal/tools/bash.go`
- Modify: `internal/tools/bash_test.go`

- [ ] **Step 1: 写 failing 测试（追加到现有 bash_test.go）**

在 `internal/tools/bash_test.go` 末尾追加：

```go
// mockEnv 是 sandbox.Environment 的测试 mock，记录所有 RunBash 调用。
type mockEnv struct {
	runOut string
	runErr error
	Calls  []string
}

func (m *mockEnv) RunBash(_ context.Context, cmd, _ string) (string, error) {
	m.Calls = append(m.Calls, cmd)
	return m.runOut, m.runErr
}
func (m *mockEnv) ReadFile(_ context.Context, _ string) ([]byte, error)         { return nil, nil }
func (m *mockEnv) WriteFile(_ context.Context, _ string, _ []byte) error        { return nil }
func (m *mockEnv) ID() string                                                    { return "mock-env" }
func (m *mockEnv) Close(_ context.Context) error                                 { return nil }

func TestBashTool_WithEnvironment_RoutesToEnv(t *testing.T) {
	env := &mockEnv{runOut: "container output\n"}
	tool := NewBashTool("/tmp", WithEnvironment(env))

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "container output\n" {
		t.Errorf("应路由到 env.RunBash，output = %q", out)
	}
	if len(env.Calls) != 1 || env.Calls[0] != "echo hello" {
		t.Errorf("env.RunBash 未被调用，Calls = %v", env.Calls)
	}
}

func TestBashTool_NilEnvironment_UsesLocal(t *testing.T) {
	// 不注入 env（nil），应走原有本地执行路径
	tool := NewBashTool("/tmp")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo local"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "local") {
		t.Errorf("nil env 应走本地执行，output = %q", out)
	}
}

func TestBashTool_WithEnvironment_LargeOutputTruncated(t *testing.T) {
	largeOut := strings.Repeat("x", maxOutputLen+100)
	env := &mockEnv{runOut: largeOut}
	tool := NewBashTool("/tmp", WithEnvironment(env))

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"big"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "截断") {
		t.Errorf("大输出应被截断，output length = %d", len(out))
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test ./internal/tools/... -run "TestBashTool_WithEnvironment|TestBashTool_NilEnvironment"
```
预期：compile error（WithEnvironment 未定义）。

- [ ] **Step 3: 修改 bash.go**

将 `internal/tools/bash.go` 改为以下内容：

```go
// 内置工具：Bash（Shell 命令执行工具）。
//
// 让 Agent 具备完整的命令行操作能力，是 harness9 "YOLO 哲学"（Trust-the-LLM）的核心：
// 不限制可执行命令的种类，把所有判断与决策权完全交给大模型。
//
// 注入 sandbox.Environment 后，命令通过 docker exec 在容器内执行（OS 级隔离）；
// 未注入时（env=nil）走原有本地进程路径，行为与引入 Sandbox 前完全一致。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/harness9/internal/sandbox"
	"github.com/harness9/internal/schema"
)

const maxOutputLen = 8000
const bashHardTimeout = 30 * time.Second

// BashTool 实现 BaseTool 接口，在 workDir 下执行任意 bash 命令。
type BashTool struct {
	workDir string
	env     sandbox.Environment // nil = 本地执行；非 nil = 路由进 Sandbox 容器
}

// BashOption 是 BashTool 的功能选项函数。
type BashOption func(*BashTool)

// WithEnvironment 注入 sandbox.Environment，命令将路由到容器内执行。
// env 为 nil 时无效（等同于不注入）。
func WithEnvironment(env sandbox.Environment) BashOption {
	return func(t *BashTool) { t.env = env }
}

// NewBashTool 创建绑定到指定工作目录的 Bash 工具实例。
func NewBashTool(workDir string, opts ...BashOption) *BashTool {
	t := &BashTool{workDir: workDir}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "在当前工作区执行任意的 bash 命令。支持链式命令(如 &&)。返回标准输出(stdout)和标准错误(stderr)的合并内容。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "要执行的 bash 命令，例如: ls -la 或 go test ./... 等等",
				},
			},
			"required": []string{"command"},
		},
	}
}

type bashArgs struct {
	Command string `json:"command"`
}

func (t *BashTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input bashArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if input.Command == "" {
		return "Error: 命令为空字符串", nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, bashHardTimeout)
	defer cancel()

	if t.env != nil {
		return t.runInSandbox(timeoutCtx, input.Command)
	}
	return t.runLocal(timeoutCtx, input.Command)
}

// runInSandbox 通过注入的 Environment 在容器内执行命令。
func (t *BashTool) runInSandbox(ctx context.Context, cmd string) (string, error) {
	out, err := t.env.RunBash(ctx, cmd, t.workDir)
	if err != nil {
		return fmt.Sprintf("执行报错: %v", err), nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return out + fmt.Sprintf("\n[警告: 命令执行超时(%s)，已被系统强制终止。]", bashHardTimeout), nil
	}
	if len(out) > maxOutputLen {
		return fmt.Sprintf("%s\n\n...[终端输出过长，已截断至前 %d 字节]...", out[:maxOutputLen], maxOutputLen), nil
	}
	return out, nil
}

// runLocal 在本地进程中执行命令（Sandbox 关闭时的原有路径）。
func (t *BashTool) runLocal(ctx context.Context, cmd string) (string, error) {
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	c.Dir = t.workDir
	out, err := c.CombinedOutput()
	outputStr := string(out)

	if ctx.Err() == context.DeadlineExceeded {
		return outputStr + fmt.Sprintf("\n[警告: 命令执行超时(%s)，已被系统强制终止。]", bashHardTimeout), nil
	}
	if err != nil {
		return fmt.Sprintf("执行报错: %v\n输出:\n%s", err, outputStr), nil
	}
	if outputStr == "" {
		return "命令执行成功，无终端输出。", nil
	}
	if len(outputStr) > maxOutputLen {
		return fmt.Sprintf("%s\n\n...[终端输出过长，已截断至前 %d 字节]...", outputStr[:maxOutputLen], maxOutputLen), nil
	}
	return outputStr, nil
}
```

- [ ] **Step 4: 运行全部 bash 测试，确认通过**

```bash
go test ./internal/tools/... -v -run TestBashTool -race
```
预期：所有 BashTool 测试通过（含原有 9 个 + 新增 3 个）。

- [ ] **Step 5: Commit**

```bash
git add internal/tools/bash.go internal/tools/bash_test.go
git commit -m "feat(tools): BashTool 支持 WithEnvironment 注入 Sandbox 执行环境"
```

---

## Task 7: 文件工具 WithEnvironment 集成

**Files:**
- Modify: `internal/tools/read_file.go`
- Modify: `internal/tools/write_file.go`
- Modify: `internal/tools/edit_file.go`

> **注意：** 文件工具通过 bind mount 与容器共享 workDir，当前 Execute 实现不变（宿主机侧读写）。`WithEnvironment` 为 API 一致性预留，未来可扩展为容器内路由。

- [ ] **Step 1: 修改 read_file.go**

在 `ReadFileTool` 结构体加 `env` 字段，`NewReadFileTool` 改为可变参，加 `WithEnvironment`：

```go
// 在 ReadFileTool 结构体中加字段：
type ReadFileTool struct {
	workDir string
	env     sandbox.Environment // 预留：当前文件操作通过 bind mount 在宿主机侧执行
}

// ReadFileOption 是 ReadFileTool 的功能选项函数。
type ReadFileOption func(*ReadFileTool)

// WithEnvironment 注入执行环境（当前文件工具通过 bind mount 无需路由，预留扩展）。
func WithEnvironment(env sandbox.Environment) ReadFileOption {
	return func(t *ReadFileTool) { t.env = env }
}

// NewReadFileTool 创建绑定到指定工作区的文件读取工具。
func NewReadFileTool(workDir string, opts ...ReadFileOption) *ReadFileTool {
	t := &ReadFileTool{workDir: filepath.Clean(workDir)}
	for _, opt := range opts {
		opt(t)
	}
	return t
}
```

同时在 import 中加 `"github.com/harness9/internal/sandbox"`。
`Execute` 方法**不变**（保持原有本地文件读取逻辑）。

- [ ] **Step 2: 修改 write_file.go**

同样模式：加 `env sandbox.Environment` 字段，`WriteFileOption` 类型，`WithEnvironment` 函数，`NewWriteFileTool` 改为可变参。`Execute` 不变。

```go
type WriteFileTool struct {
	workDir string
	env     sandbox.Environment
}

type WriteFileOption func(*WriteFileTool)

func WithEnvironment(env sandbox.Environment) WriteFileOption {
	return func(t *WriteFileTool) { t.env = env }
}

func NewWriteFileTool(workDir string, opts ...WriteFileOption) *WriteFileTool {
	t := &WriteFileTool{workDir: filepath.Clean(workDir)}
	for _, opt := range opts {
		opt(t)
	}
	return t
}
```

- [ ] **Step 3: 修改 edit_file.go**

同样模式：加 `env sandbox.Environment` 字段，`EditFileOption` 类型，`WithEnvironment` 函数，`NewEditFileTool` 改为可变参。`Execute` 不变。

```go
type EditFileTool struct {
	workDir string
	env     sandbox.Environment
}

type EditFileOption func(*EditFileTool)

func WithEnvironment(env sandbox.Environment) EditFileOption {
	return func(t *EditFileTool) { t.env = env }
}

func NewEditFileTool(workDir string, opts ...EditFileOption) *EditFileTool {
	t := &EditFileTool{workDir: filepath.Clean(workDir)}
	for _, opt := range opts {
		opt(t)
	}
	return t
}
```

- [ ] **Step 4: 运行全部工具测试，确认通过**

```bash
go test ./internal/tools/... -race
```
预期：所有测试通过。

- [ ] **Step 5: 确认全量编译无误**

```bash
go build ./...
```
预期：0 错误。

- [ ] **Step 6: Commit**

```bash
git add internal/tools/read_file.go internal/tools/write_file.go internal/tools/edit_file.go
git commit -m "feat(tools): ReadFileTool/WriteFileTool/EditFileTool 加 WithEnvironment 选项（bind mount 语义，预留扩展）"
```

---

## Task 8: Sub-Agent Runner 集成

**Files:**
- Modify: `internal/subagent/runner.go`

- [ ] **Step 1: 修改 RunnerConfig 和 Runner 结构体**

在 `internal/subagent/runner.go` 中：

1. 加 import `"github.com/harness9/internal/sandbox"`
2. 在 `RunnerConfig` 加字段：
```go
SandboxMgr *sandbox.Manager // optional；nil = 子代理不使用 Sandbox
```
3. 在 `Runner` 加字段：
```go
sandboxMgr *sandbox.Manager
```
4. 在 `NewRunner` 中赋值：
```go
sandboxMgr: cfg.SandboxMgr,
```

- [ ] **Step 2: 修改 buildChildRegistry 接受 baseTools 参数**

将 `buildChildRegistry(def SubAgentDefinition)` 改为 `buildChildRegistry(def SubAgentDefinition, baseTools []tools.BaseTool)`，内部将所有 `r.baseTools` 改为参数 `baseTools`：

```go
func (r *Runner) buildChildRegistry(def SubAgentDefinition, baseTools []tools.BaseTool) (tools.Registry, error) {
	allNames := make([]string, 0, len(baseTools))
	byName := make(map[string]tools.BaseTool, len(baseTools))
	for _, t := range baseTools {
		allNames = append(allNames, t.Name())
		byName[t.Name()] = t
	}
	resolved := def.ResolveTools(allNames)

	base := tools.NewRegistry()
	for _, name := range resolved {
		if t, ok := byName[name]; ok {
			if err := base.Register(t); err != nil {
				return nil, fmt.Errorf("注册子代理工具 %q 失败: %w", name, err)
			}
		}
	}
	hookChain := []hooks.ToolHook{permission.NewFileHook(r.settingsPath), denyTaskHook{}}
	hookChain = append(hookChain, r.sharedHooks...)
	return hooks.NewHookRegistry(base, hookChain...), nil
}
```

- [ ] **Step 3: 修改 Run 方法加 Sandbox 创建/销毁**

在 `Run()` 方法开头（`buildChildRegistry` 调用之前）加：

```go
// 为子代理创建独立 Sandbox（若 Manager 已配置）
effectiveBaseTools := r.baseTools
if r.sandboxMgr != nil {
	sandboxEnv, err := r.sandboxMgr.Create(ctx, r.workDir)
	if err != nil {
		return SubAgentResult{}, fmt.Errorf("sandbox: 为子代理创建环境失败: %w", err)
	}
	defer r.sandboxMgr.Destroy(ctx, sandboxEnv.ID())
	effectiveBaseTools = wrapToolsWithSandbox(r.baseTools, sandboxEnv, r.workDir)
}

childReg, err := r.buildChildRegistry(def, effectiveBaseTools)
```

然后在同文件末尾加辅助函数：

```go
// wrapToolsWithSandbox 为标准工具注入 sandbox.Environment，非标准工具原样返回。
func wrapToolsWithSandbox(ts []tools.BaseTool, env sandbox.Environment, workDir string) []tools.BaseTool {
	result := make([]tools.BaseTool, len(ts))
	for i, t := range ts {
		switch t.Name() {
		case "bash":
			result[i] = tools.NewBashTool(workDir, tools.WithEnvironment(env))
		case "read_file":
			result[i] = tools.NewReadFileTool(workDir, tools.WithEnvironment(env))
		case "write_file":
			result[i] = tools.NewWriteFileTool(workDir, tools.WithEnvironment(env))
		case "edit_file":
			result[i] = tools.NewEditFileTool(workDir, tools.WithEnvironment(env))
		default:
			result[i] = t
		}
	}
	return result
}
```

- [ ] **Step 4: 运行 subagent 测试，确认通过**

```bash
go test ./internal/subagent/... -race
```
预期：所有测试通过（Runner 改动不破坏现有测试）。

- [ ] **Step 5: 确认全量编译**

```bash
go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add internal/subagent/runner.go
git commit -m "feat(subagent): Runner 集成 SandboxManager，子代理自动获得独立 Sandbox"
```

---

## Task 9: main.go 接线

**Files:**
- Modify: `cmd/harness9/main.go`

- [ ] **Step 1: 加 sandbox import**

在 `cmd/harness9/main.go` 的 import 块中加：
```go
"github.com/harness9/internal/sandbox"
```

- [ ] **Step 2: 在 main() 中初始化 SandboxManager**

在 `workDir := cwd` 之后、`registry := tools.NewRegistry()` 之前，加：

```go
// ---- Sandbox 系统接线 ----
sandboxCfg := sandbox.DefaultConfig()
var sandboxMgr *sandbox.Manager
var sandboxEnv sandbox.Environment // nil = 工具走本地执行路径

if sandboxCfg.Enabled {
	sandboxMgr = sandbox.NewManager(sandboxCfg)
	if err := sandboxMgr.ReapOrphans(ctx); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("清理孤儿 Sandbox 失败（忽略）: %v", err)))
	}
	sandboxEnv, err = sandboxMgr.Create(ctx, workDir)
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("创建主 Agent Sandbox 失败: %v", err)))
	}
	defer sandboxMgr.DestroyAll(ctx)
}
// ---- Sandbox 系统接线（续：工具注入见下）----
```

- [ ] **Step 3: 工具注册时注入 sandboxEnv**

将现有工具注册改为：

```go
for _, tool := range []tools.BaseTool{
	tools.NewReadFileTool(workDir, tools.WithEnvironment(sandboxEnv)),
	tools.NewWriteFileTool(workDir, tools.WithEnvironment(sandboxEnv)),
	tools.NewBashTool(workDir, tools.WithEnvironment(sandboxEnv)),
	tools.NewEditFileTool(workDir, tools.WithEnvironment(sandboxEnv)),
	skills.NewUseSkillTool(skillsIndex),
	tools.NewTodoWriteTool(todoStore, tools.WithPlanWriter(planWriter)),
	tools.NewMemoryWriteTool(ltmStore, ltmPrecis),
	tools.NewMemorySearchTool(ltmStore),
} {
```

同样更新 `subAgentBaseTools`：

```go
subAgentBaseTools := []tools.BaseTool{
	tools.NewReadFileTool(workDir),   // 子代理工具在 Runner.Run 时按需包装
	tools.NewWriteFileTool(workDir),
	tools.NewBashTool(workDir),
	tools.NewEditFileTool(workDir),
	skills.NewUseSkillTool(skillsIndex),
}
```

- [ ] **Step 4: 在 RunnerConfig 中传入 sandboxMgr**

```go
subAgentRunner := subagent.NewRunner(subagent.RunnerConfig{
	// ... 现有字段 ...
	SandboxMgr: sandboxMgr, // 新增：nil = 子代理无 Sandbox；非 nil = 子代理各自获得独立 Sandbox
})
```

- [ ] **Step 5: 编译验证**

```bash
go build ./cmd/harness9/
```
预期：0 错误。

- [ ] **Step 6: 运行全量测试**

```bash
go test ./... -race
```
预期：所有测试通过。

- [ ] **Step 7: Commit**

```bash
git add cmd/harness9/main.go
git commit -m "feat(main): 接线 SandboxManager，工具注入 sandboxEnv，子代理自动 Create/Destroy"
```

---

## Task 10: TUI SandboxBar

**Files:**
- Modify: `cmd/harness9/tui.go`
- Modify: `cmd/harness9/tui_update.go`
- Modify: `cmd/harness9/tui_view.go`
- Modify: `cmd/harness9/main.go`（TUI 通知回调）

- [ ] **Step 1: 修改 tui.go —— 加字段和样式**

1. 在 `tui.go` import 块加 `"github.com/harness9/internal/sandbox"`

2. 在样式变量区（`approvalSelectedStyle` 之后）追加：

```go
// SandboxBar 样式 — 仅在有活跃 Sandbox 时显示，位于 StatusBar 下方
sandboxBarBgStyle     = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("242")).Padding(0, 1)
sandboxRunningStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))  // 绿色：Running
sandboxPendingStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))  // 黄色：Pending
sandboxStoppingStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // 灰色：Stopping/Terminated
sandboxFailedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))   // 红色：Failed
```

3. 在 `tuiModel` 结构体末尾加字段（在现有最后一个字段之后）：

```go
// Sandbox 状态展示（SandboxBar）
sandboxes []sandbox.SandboxInfo
```

- [ ] **Step 2: 修改 tui_update.go —— 加消息类型和 Update 分支**

1. 在 import 块加 `"github.com/harness9/internal/sandbox"`

2. 在文件中任意位置（其他消息类型旁）加：

```go
// sandboxUpdateMsg 在 Sandbox 状态变化时由 Manager.WithUpdateNotify 发送。
type sandboxUpdateMsg struct {
	infos []sandbox.SandboxInfo
}
```

3. 在 `Update()` 方法的 `switch msg := msg.(type)` 中加分支（紧跟其他 case 之后）：

```go
case sandboxUpdateMsg:
	m.sandboxes = msg.infos
	return m, nil
```

- [ ] **Step 3: 修改 tui_view.go —— 实现 renderSandboxBar**

1. 在 import 块加：
```go
"github.com/harness9/internal/sandbox"
```

2. 在文件末尾（其他 render 函数之后）加：

```go
// renderSandboxBar 渲染 Sandbox 状态栏，仅在有活跃 Sandbox 时显示。
// 格式: [Sandbox] 3a2f (main) Running │ 7b1c (sub-1) Running
func (m tuiModel) renderSandboxBar() string {
	if len(m.sandboxes) == 0 {
		return ""
	}

	parts := make([]string, 0, len(m.sandboxes)+1)
	parts = append(parts, sandboxBarBgStyle.Render("[Sandbox]"))

	for i, info := range m.sandboxes {
		label := "main"
		if i > 0 {
			label = fmt.Sprintf("sub-%d", i)
		}

		shortID := info.DockerID
		if len(shortID) > 4 {
			shortID = shortID[:4]
		}

		stateStr := info.State.String()
		var stateStyled string
		switch info.State {
		case sandbox.StateRunning:
			stateStyled = sandboxRunningStyle.Render(stateStr)
		case sandbox.StatePending:
			stateStyled = sandboxPendingStyle.Render(stateStr)
		case sandbox.StateStopping, sandbox.StateTerminated:
			stateStyled = sandboxStoppingStyle.Render(stateStr)
		case sandbox.StateFailed:
			stateStyled = sandboxFailedStyle.Render(stateStr)
		default:
			stateStyled = stateStr
		}

		parts = append(parts, fmt.Sprintf("%s (%s) %s", shortID, label, stateStyled))
	}

	return strings.Join(parts, " │ ")
}
```

3. 在 `View()` 方法中，找到 `renderStatusBar()` 调用处，在其返回的字符串后面插入 SandboxBar。

找到 View() 中组装布局的部分（通常是 `lipgloss.JoinVertical` 或手动拼接），在 statusBar 行下方加入 sandboxBar：

```go
statusBar := m.renderStatusBar()
sandboxBar := m.renderSandboxBar()

// 组装布局（在原有代码基础上，在 statusBar 与内容区之间插入 sandboxBar）
// 若 sandboxBar 非空，则占一行；为空时不占空间
var extraBar string
if sandboxBar != "" {
	extraBar = "\n" + sandboxBar
}
// 在返回的字符串中，将 statusBar 替换为 statusBar+extraBar
```

> **注意：** View() 中的具体拼接方式取决于现有 tui_view.go 实现，需要阅读现有 View() 代码找到正确插入点。原则：SandboxBar 在 StatusBar 下方、对话内容上方，仅在非空时占行。

- [ ] **Step 4: 修改 main.go —— 连接 Manager 通知**

在 `sandboxMgr` 初始化之后（`if sandboxCfg.Enabled` 块内），`RunTUI` 调用之前，加：

```go
// TUI 通知回调：Sandbox 状态变化时发送 sandboxUpdateMsg 给 TUI
// program 变量在 RunTUI 内部创建，需要通过闭包或 channel 传递。
// 实现方式：在 RunTUI 中接受 sandboxNotify chan []sandbox.SandboxInfo 参数，
// 或使用 tea.Program.Send 通过全局 program 变量。
// 简洁实现：将通知 channel 注入 tuiModel，在 Init 中监听。
```

**简洁实现方案（推荐）：** 在 `tuiModel` 加 `sandboxCh <-chan []sandbox.SandboxInfo`，在 `Init()` 中返回 `waitForSandboxUpdate(m.sandboxCh)` Cmd，Update 收到后再发下一个 Cmd：

在 `tui.go` 的 `tuiModel` 字段中加：
```go
sandboxCh <-chan []sandbox.SandboxInfo // Manager 状态变更通知 channel（传递完整快照切片）
```

在 `newTUIModel()` 中接受并存储该 channel。

在 `tui_update.go` 加：
```go
func waitSandboxUpdate(ch <-chan []sandbox.SandboxInfo) tea.Cmd {
	return func() tea.Msg {
		infos, ok := <-ch
		if !ok {
			return nil
		}
		return sandboxUpdateMsg{infos: infos}
	}
}
```

在 `Init()` 中加 `waitSandboxUpdate(m.sandboxCh)`。
在 Update 的 `sandboxUpdateMsg` 分支末尾加 `waitSandboxUpdate(m.sandboxCh)` Cmd。

在 `main.go` 中：
```go
sandboxNotifyCh := make(chan []sandbox.SandboxInfo, 8)
if sandboxMgr != nil {
	sandboxMgr.WithUpdateNotify(func(infos []sandbox.SandboxInfo) {
		select {
		case sandboxNotifyCh <- infos:
		default: // 丢弃：TUI 下次 ListAll 会自动读到最新状态
		}
	})
}
// 将 sandboxNotifyCh 传入 RunTUI 或 newTUIModel
```

- [ ] **Step 5: 编译验证**

```bash
go build ./cmd/harness9/
```
预期：0 错误。

- [ ] **Step 6: 运行全量测试**

```bash
go test ./... -race
```
预期：所有测试通过。

- [ ] **Step 7: Commit**

```bash
git add cmd/harness9/tui.go cmd/harness9/tui_update.go cmd/harness9/tui_view.go cmd/harness9/main.go
git commit -m "feat(tui): SandboxBar 实时展示 Sandbox ID/状态（颜色编码），Manager 状态变更推送通知"
```

---

## Task 11: 全量验证 + 收尾

- [ ] **Step 1: 运行全量测试（含 race detector）**

```bash
go test ./... -race -count=1
```
预期：所有测试通过，无 race condition。

- [ ] **Step 2: 运行 go vet**

```bash
go vet ./...
```
预期：0 警告。

- [ ] **Step 3: 格式检查**

```bash
gofmt -l .
```
预期：无文件输出（所有文件已格式化）。

- [ ] **Step 4: 验证 Sandbox 关闭时行为不变**

```bash
# 不设置 SANDBOX_ENABLED，启动程序
SANDBOX_ENABLED= go run ./cmd/harness9/ --version
```
预期：正常输出版本号，无 sandbox 相关错误。

- [ ] **Step 5: 最终 Commit**

```bash
git add .
git commit -m "chore(sandbox): 全量验证通过，Sandbox 系统实现完毕"
```
