package ltm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrecisRegenerateAndRead(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	s.Add(ctx, &Entry{Title: "高优先", Content: "重要事实", Importance: 9, Category: CategoryKnowledge})
	s.Add(ctx, &Entry{Title: "低优先", Content: "次要事实", Importance: 1})

	path := filepath.Join(t.TempDir(), "memories", "MEMORY.md")
	p := NewPrecis(s, path, 4096)
	if err := p.Regenerate(ctx); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	content, err := p.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(content, "高优先") || !strings.Contains(content, "重要事实") {
		t.Errorf("精华应包含高优先条目: %q", content)
	}
	// 文件确实落盘。
	if _, err := os.Stat(path); err != nil {
		t.Errorf("MEMORY.md 应已写入磁盘: %v", err)
	}
}

func TestPrecisByteCap(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 50; i++ {
		s.Add(ctx, &Entry{Title: "条目", Content: strings.Repeat("内容", 50), Importance: 5})
	}
	p := NewPrecis(s, filepath.Join(t.TempDir(), "MEMORY.md"), 500)
	if err := p.Regenerate(ctx); err != nil {
		t.Fatal(err)
	}
	content, _ := p.Read()
	if len(content) > 600 { // 500 上限 + 截断标记裕量
		t.Errorf("精华应被截断到约 500 字节，got %d", len(content))
	}
}

func TestPrecisReadMissing(t *testing.T) {
	p := NewPrecis(nil, filepath.Join(t.TempDir(), "nope.md"), 4096)
	content, err := p.Read()
	if err != nil {
		t.Errorf("缺失文件应返回空串而非错误: %v", err)
	}
	if content != "" {
		t.Errorf("缺失文件应返回空串，got %q", content)
	}
}
