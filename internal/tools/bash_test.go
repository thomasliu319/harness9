package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBashTool_Name(t *testing.T) {
	tool := NewBashTool("/tmp")
	if tool.Name() != "bash" {
		t.Errorf("expected 'bash', got %q", tool.Name())
	}
}

func TestBashTool_Definition(t *testing.T) {
	tool := NewBashTool("/tmp")
	def := tool.Definition()
	if def.Name != "bash" {
		t.Errorf("definition name mismatch: %q", def.Name)
	}
	if def.Description == "" {
		t.Error("definition should have a description")
	}
	if def.InputSchema == nil {
		t.Error("definition should have an input schema")
	}
}

func TestBashTool_Execute_BasicCommand(t *testing.T) {
	tool := NewBashTool("/tmp")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected output containing 'hello', got %q", out)
	}
}

func TestBashTool_Execute_EmptyCommand(t *testing.T) {
	tool := NewBashTool("/tmp")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":""}`))
	if err != nil {
		t.Fatalf("empty command should not return Go error, got: %v", err)
	}
	if !strings.Contains(out, "Error") {
		t.Errorf("empty command should return error string, got: %q", out)
	}
}

func TestBashTool_Execute_NonZeroExitCode(t *testing.T) {
	tool := NewBashTool("/tmp")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"exit 1"}`))
	if err != nil {
		t.Fatalf("non-zero exit should not return Go error (Self-Correction Loopback), got: %v", err)
	}
	if !strings.Contains(out, "执行报错") {
		t.Errorf("non-zero exit should contain error info, got: %q", out)
	}
}

func TestBashTool_Execute_EmptyOutput(t *testing.T) {
	tool := NewBashTool("/tmp")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"true"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "成功") {
		t.Errorf("silent command should say success, got: %q", out)
	}
}

func TestBashTool_Execute_LargeOutput(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("x", maxOutputLen+100)
	if err := os.WriteFile(dir+"/large.txt", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewBashTool(dir)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"cat large.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "截断") {
		t.Errorf("large output should include truncation notice, got length=%d", len(out))
	}
}

func TestBashTool_Execute_PipeCommand(t *testing.T) {
	tool := NewBashTool("/tmp")
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"printf 'hello' | tr 'a-z' 'A-Z'"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "HELLO") {
		t.Errorf("pipe command should work, got: %q", out)
	}
}

func TestBashTool_Execute_BadJSON(t *testing.T) {
	tool := NewBashTool("/tmp")
	_, err := tool.Execute(context.Background(), json.RawMessage(`not_json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON args")
	}
}

// TestBashTool_Execute_ParentContextCancelled verifies that a long-running command is
// killed when the parent context times out, and the result is returned as a string (not
// a Go error), preserving the Self-Correction Loopback contract.
//
// When the parent context's DeadlineExceeded propagates to the child timeoutCtx, bash.go
// detects it as DeadlineExceeded and emits a timeout warning rather than an exec error
// string — both are acceptable informative outputs, so this test only checks for no
// panic and no Go error.
func TestBashTool_Execute_ParentContextCancelled(t *testing.T) {
	tool := NewBashTool("/tmp")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	out, err := tool.Execute(ctx, json.RawMessage(`{"command":"sleep 10"}`))
	if err != nil {
		t.Fatalf("bash should never return Go error, got: %v", err)
	}
	// The killed command should produce some informative output (timeout warning or error
	// string) — never an empty string or a spurious success message.
	if out == "" || strings.Contains(out, "成功") {
		t.Errorf("killed command should return informative output, got: %q", out)
	}
}

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
func (m *mockEnv) ReadFile(_ context.Context, _ string) ([]byte, error)  { return nil, nil }
func (m *mockEnv) WriteFile(_ context.Context, _ string, _ []byte) error { return nil }
func (m *mockEnv) ID() string                                            { return "mock-env" }
func (m *mockEnv) Close(_ context.Context) error                         { return nil }

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

func TestBashTool_WithEnvironment_EmptyOutput(t *testing.T) {
	env := &mockEnv{runOut: ""}
	tool := NewBashTool("/tmp", WithEnvironment(env))

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"true"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "成功") {
		t.Errorf("空输出应提示成功，got: %q", out)
	}
}

func TestBashTool_WithEnvironment_EnvError(t *testing.T) {
	// RunBash 返回非空 error（环境级错误，非命令失败）
	env := &mockEnv{runOut: "", runErr: fmt.Errorf("container not found")}
	tool := NewBashTool("/tmp", WithEnvironment(env))

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"echo hi"}`))
	if err != nil {
		t.Fatalf("Execute 不应返回 Go error: %v", err)
	}
	if !strings.Contains(out, "执行报错") {
		t.Errorf("env error 应转为错误字符串，got: %q", out)
	}
}
