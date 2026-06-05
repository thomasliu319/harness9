// internal/sandbox/docker_environment_test.go
package sandbox

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestDockerEnvironment_ReadWriteFile(t *testing.T) {
	env := newDockerEnvironment("c123", "uuid", t.TempDir(), nil)
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

func TestDockerEnvironment_WriteFile_AutoMkdir(t *testing.T) {
	env := newDockerEnvironment("c123", "uuid", t.TempDir(), nil)
	path := filepath.Join(t.TempDir(), "nested", "dir", "file.txt")

	if err := env.WriteFile(context.Background(), path, []byte("data")); err != nil {
		t.Fatalf("WriteFile 自动创建目录: %v", err)
	}
}
