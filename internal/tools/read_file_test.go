package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestReadFileTool_Name(t *testing.T) {
	tool := NewReadFileTool("/tmp")
	if tool.Name() != "read_file" {
		t.Errorf("expected 'read_file', got %q", tool.Name())
	}
}

func TestReadFileTool_Definition(t *testing.T) {
	tool := NewReadFileTool("/tmp")
	def := tool.Definition()
	if def.Name != "read_file" {
		t.Errorf("definition name mismatch: %q", def.Name)
	}
	if def.Description == "" {
		t.Error("definition should have a description")
	}
	if def.InputSchema == nil {
		t.Error("definition should have an input schema")
	}
}

func TestReadFileTool_Execute_Success(t *testing.T) {
	dir := t.TempDir()
	content := "hello, world"
	if err := os.WriteFile(dir+"/test.txt", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"test.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != content {
		t.Errorf("expected %q, got %q", content, out)
	}
}

func TestReadFileTool_Execute_Truncation(t *testing.T) {
	dir := t.TempDir()
	large := strings.Repeat("x", maxReadLen+100)
	if err := os.WriteFile(dir+"/big.txt", []byte(large), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"big.txt"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "截断") {
		t.Errorf("output should mention truncation, got length=%d", len(out))
	}
	if len([]byte(out)) < maxReadLen {
		t.Errorf("truncated output should still contain %d bytes of content", maxReadLen)
	}
}

func TestReadFileTool_Execute_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFileTool(dir)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"nonexistent.txt"}`))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestReadFileTool_Execute_BadJSON(t *testing.T) {
	tool := NewReadFileTool("/tmp")
	_, err := tool.Execute(context.Background(), json.RawMessage(`not_json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON args")
	}
}

func TestReadFileTool_Execute_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFileTool(dir)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"../../etc/passwd"}`))
	if err == nil {
		t.Fatal("expected sandbox error for path traversal")
	}
	if !strings.Contains(err.Error(), "超出工作区范围") {
		t.Errorf("expected sandbox error, got: %v", err)
	}
}

func TestReadFileTool_Execute_WithOffset(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nline3\nline4\n"
	if err := os.WriteFile(dir+"/f.txt", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileTool(dir)

	// offset=6 skips "line1\n", should start reading from "line2"
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"f.txt","offset":6,"limit":5}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(out, "line2") {
		t.Errorf("expected output starting with 'line2', got %q", out)
	}
}

func TestReadFileTool_Execute_OffsetBeyondEOF(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/small.txt", []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileTool(dir)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"small.txt","offset":1000}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "offset=1000") || !strings.Contains(out, "超出") {
		t.Errorf("expected offset-beyond-EOF message, got %q", out)
	}
}

func TestReadFileTool_Execute_LimitTruncation(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("abcde", 100) // 500 bytes
	if err := os.WriteFile(dir+"/big.txt", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileTool(dir)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"big.txt","offset":0,"limit":20}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// First 20 bytes + truncation message
	if !strings.HasPrefix(out, content[:20]) {
		t.Errorf("expected first 20 bytes of content")
	}
	if !strings.Contains(out, "offset=20") {
		t.Errorf("truncation message should hint next offset=20, got %q", out)
	}
}

// start_line/end_line 行号模式测试
func TestReadFileTool_Execute_StartEndLine(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(dir+"/lines.txt", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileTool(dir)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"lines.txt","start_line":2,"end_line":4}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "line2") || !strings.Contains(out, "line4") {
		t.Errorf("should contain line2-line4, got: %q", out)
	}
	if strings.Contains(out, "line1") || strings.Contains(out, "line5") {
		t.Errorf("should not contain line1 or line5, got: %q", out)
	}
}

func TestReadFileTool_Execute_StartLineOnly(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "line"+string(rune('0'+i)))
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(dir+"/lines.txt", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileTool(dir)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"lines.txt","start_line":3}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "line3") {
		t.Errorf("should contain line3, got: %q", out)
	}
}

func TestReadFileTool_Execute_StartLineOutOfRange(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/short.txt", []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFileTool(dir)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"short.txt","start_line":999}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "超出") {
		t.Errorf("should report out-of-range, got: %q", out)
	}
}
