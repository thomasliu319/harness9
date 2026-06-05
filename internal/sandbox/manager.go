// Package sandbox 提供基于 Docker 的沙箱容器生命周期管理。
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
	ID       string // Sandbox UUID
	DockerID string // Docker container ID（前 12 位短 hash）
	State    ContainerState
	WorkDir  string
	Image    string
}

// Manager 管理所有活跃 Sandbox 的生命周期，是 Sandbox 系统的单一事实源。
// 线程安全：所有公开方法均可并发调用。
type Manager struct {
	containers map[string]*Container
	mu         sync.RWMutex
	cfg        SandboxConfig
	onUpdate   func([]SandboxInfo)
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

// WithUpdateNotify 设置状态变更通知回调，供 TUI 通过 channel 接收更新。
func (m *Manager) WithUpdateNotify(fn func([]SandboxInfo)) {
	m.onUpdate = fn
}

// Create 为一个 Agent 创建独立 Sandbox，返回可用的 Environment。
func (m *Manager) Create(ctx context.Context, workDir string) (Environment, error) {
	id := generateID()

	run := cmdRunner(realCmdRunner)
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

// ReapOrphans 清理上次进程崩溃遗留的孤儿容器（label=harness9=1，status=exited）。
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

// ListAll 返回所有活跃 Sandbox 的只读快照（线程安全）。
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

// generateID 生成 16 位 hex 格式的随机 Sandbox UUID（crypto/rand，无冲突）。
func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// shortDockerID 截取 Docker container ID 前 12 位供展示使用。
func shortDockerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
