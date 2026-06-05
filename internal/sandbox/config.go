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

// getenvOr 从环境变量读取 key，未设置或为空时返回 fallback。
func getenvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
