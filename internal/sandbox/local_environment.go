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
