// Package sandbox 提供 Docker 容器级执行环境抽象，
// 为 Agent 工具调用提供 OS 级隔离（网络、进程空间、文件系统）。
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
