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
