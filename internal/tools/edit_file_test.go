package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestEditFileTool_Name(t *testing.T) {
	tool := NewEditFileTool("/tmp")
	if tool.Name() != "edit_file" {
		t.Errorf("expected 'edit_file', got %q", tool.Name())
	}
}

func TestEditFileTool_Definition(t *testing.T) {
	tool := NewEditFileTool("/tmp")
	def := tool.Definition()
	if def.Name != "edit_file" {
		t.Errorf("definition name mismatch: %q", def.Name)
	}
	if def.Description == "" {
		t.Error("definition should have a description")
	}
	if def.InputSchema == nil {
		t.Error("definition should have an input schema")
	}
}

func TestEditFileTool_Execute_BadJSON(t *testing.T) {
	tool := NewEditFileTool("/tmp")
	_, err := tool.Execute(context.Background(), json.RawMessage(`not_json`))
	if err == nil {
		t.Fatal("expected error for malformed JSON args")
	}
}

func TestEditFileTool_Execute_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditFileTool(dir)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"../../etc/passwd","source_text":"x","target_text":"y"}`))
	if err == nil {
		t.Fatal("expected sandbox error for path traversal")
	}
	if !strings.Contains(err.Error(), "超出工作区范围") {
		t.Errorf("expected sandbox error, got: %v", err)
	}
}

func TestEditFileTool_Execute_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditFileTool(dir)

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"nonexistent.txt","source_text":"x","target_text":"y"}`))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// L1: 精确匹配替换
func TestFuzzyReplace_L1_ExactMatch(t *testing.T) {
	content := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	source := `	fmt.Println("hello")`
	target := `	fmt.Println("world")`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `fmt.Println("world")`) {
		t.Errorf("target text should appear in result, got: %s", result)
	}
	if strings.Contains(result, `fmt.Println("hello")`) {
		t.Errorf("source text should not remain, got: %s", result)
	}
}

// L1: 精确匹配多处 → 返回错误
func TestFuzzyReplace_L1_MultipleMatch_Error(t *testing.T) {
	content := `foo
bar
foo`
	source := `foo`
	target := `baz`

	_, err := fuzzyReplace(content, source, target)
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
	if !strings.Contains(err.Error(), "匹配到了") {
		t.Errorf("error should mention multiple matches, got: %v", err)
	}
}

// L2: 换行符归一化匹配（CRLF → LF）
func TestFuzzyReplace_L2_CRLFNormalization(t *testing.T) {
	content := "line1\r\nline2\r\nline3"
	source := "line2"
	target := "modified"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CRLF 应被保留
	if !strings.Contains(result, "\r\n") {
		t.Error("CRLF line endings should be preserved")
	}
	if !strings.Contains(result, "modified") {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// L2: CRLF 文件中 source_text 也带 CRLF
func TestFuzzyReplace_L2_CRLFInSourceText(t *testing.T) {
	content := "line1\r\nline2\r\nline3"
	source := "line2\r\nline3"
	target := "modified"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "modified") {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// L2: CRLF 文件中 target_text 含 CRLF
func TestFuzzyReplace_L2_CRLFInTargetText(t *testing.T) {
	content := "line1\r\nline2\r\nline3"
	source := "line2"
	target := "modified\r\nnewline"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "modified") {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// L3: 整体首尾去空匹配
func TestFuzzyReplace_L3_TrimmedMatch(t *testing.T) {
	content := `package main

func main() {
    fmt.Println("hello")
}
`
	// source_text 带多余的缩进空白
	source := `        func main() {
    fmt.Println("hello")
}`
	target := `func main() {
    fmt.Println("world")
}`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `fmt.Println("world")`) {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// L4: 逐行去缩进匹配
func TestFuzzyReplace_L4_LineByLineMatch(t *testing.T) {
	content := `package main

func main() {
    fmt.Println("hello")
    fmt.Println("world")
}
`
	source := `func main() {
    fmt.Println("hello")
    fmt.Println("world")
}`
	target := `func main() {
    fmt.Println("hello universe")
}`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello universe") {
		t.Errorf("target text should appear, got: %s", result)
	}
	if strings.Contains(result, "fmt.Println(\"world\")") {
		t.Errorf("old code should be replaced, got: %s", result)
	}
}

// L4: 缩进不一致时的模糊匹配
func TestFuzzyReplace_L4_IndentAgnostic(t *testing.T) {
	content := `package main

func main() {
    fmt.Println("hello")
}
`
	// source_text 使用 tab 缩进而文件中是空格
	source := "func main() {\n\tfmt.Println(\"hello\")\n}"
	target := "func main() {\n    fmt.Println(\"world\")\n}"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `fmt.Println("world")`) {
		t.Errorf("target text should appear, got: %s", result)
	}
}

// L4: 逐行去缩进多处匹配 → 返回错误
func TestFuzzyReplace_L4_MultipleMatch_Error(t *testing.T) {
	content := `x := 1
y := 2
x := 3
`
	source := `x :=`
	target := `z :=`

	_, err := fuzzyReplace(content, source, target)
	// L1 就会匹配到两处
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
}

// L4: 无匹配 → 返回错误
func TestFuzzyReplace_NoMatch(t *testing.T) {
	content := `package main

func main() {
    fmt.Println("hello")
}
`
	source := `func foo() {
    return 42
}`
	target := `func bar() {
    return 0
}`

	_, err := fuzzyReplace(content, source, target)
	if err == nil {
		t.Fatal("expected error for no match")
	}
}

// 完整的 Execute 链路测试
func TestEditFileTool_Execute_Success(t *testing.T) {
	dir := t.TempDir()
	content := `package main

import "fmt"

func main() {
    fmt.Println("hello")
}
`
	if err := os.WriteFile(dir+"/main.go", []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"main.go","source_text":"fmt.Println(\"hello\")","target_text":"fmt.Println(\"world\")"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "成功修改文件") {
		t.Errorf("success message should contain success text, got: %q", out)
	}
	if !strings.Contains(out, "main.go") {
		t.Errorf("success message should mention file path, got: %q", out)
	}

	data, err := os.ReadFile(dir + "/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `fmt.Println("world")`) {
		t.Errorf("file should contain target text after edit, got: %s", string(data))
	}
}

// L4: 包含空行的代码块匹配
func TestFuzzyReplace_L4_WithBlankLines(t *testing.T) {
	content := `func process() {
    step1()

    step2()
}`
	source := `func process() {
    step1()

    step2()
}`
	target := `func process() {
    step1()
    step2()
}`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 应该只有 4 行（去掉空行）
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 4 {
		t.Errorf("expected 4 lines, got %d: %s", len(lines), result)
	}
}

// L4: 单行匹配
func TestFuzzyReplace_L4_SingleLine(t *testing.T) {
	content := `func main() {
    fmt.Println("hello")
}`
	source := `fmt.Println("hello")`
	target := `fmt.Println("world")`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `fmt.Println("world")`) {
		t.Errorf("target should appear, got: %s", result)
	}
}

// L3: 空 source_text 不应进入 L3
func TestFuzzyReplace_L3_EmptySourceTrimmed(t *testing.T) {
	content := `just some content`
	source := `   ` // 全空格
	target := `replacement`

	_, err := fuzzyReplace(content, source, target)
	if err == nil {
		t.Fatal("expected error for whitespace-only source")
	}
}

// L1: 原始文本精确匹配，保留原始格式（包括 \r\n）
func TestFuzzyReplace_L1_PreserveOriginalFormatting(t *testing.T) {
	content := "line1\nline2\nline3"
	source := "line2"
	target := "modified"

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "line1\nmodified\nline3" {
		t.Errorf("unexpected result: %q", result)
	}
}

// 验证写入文件后内容正确性
func TestEditFileTool_Execute_L1_ExactMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/test.txt", []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"test.txt","source_text":"world","target_text":"harness9"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(dir + "/test.txt")
	if string(data) != "hello harness9" {
		t.Errorf("file content mismatch: %q", string(data))
	}
}

// L4: 验证 lineByLineReplace 对 CRLF 文件的保留
func TestFuzzyReplace_L4_CRLFPreservation(t *testing.T) {
	content := "func main() {\r\n    fmt.Println(\"hello\")\r\n}"
	source := `func main() {
    fmt.Println("hello")
}`
	target := `func main() {
    fmt.Println("world")
}`

	result, err := fuzzyReplace(content, source, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "\r\n") {
		t.Error("CRLF should be preserved after L4 match")
	}
	if strings.Contains(result, "hello") {
		t.Error("old text should not remain")
	}
}
