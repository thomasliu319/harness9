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
	run         cmdRunner
}

func newDockerEnvironment(containerID, id, _ string, run cmdRunner) *DockerEnvironment {
	// workDir 参数不存储：文件读写通过 bind mount 在宿主机侧执行，无需 DockerEnvironment 持有路径。
	return &DockerEnvironment{
		containerID: containerID,
		id:          id,
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
