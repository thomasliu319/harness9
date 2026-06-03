// internal/tools/memory_search_test.go
package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/harness9/internal/ltm"
	_ "modernc.org/sqlite"
)

func TestMemorySearchExecute(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	t.Cleanup(func() { db.Close() })
	store, err := ltm.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	store.Add(context.Background(), &ltm.Entry{Title: "数据库", Content: "项目使用 SQLite 持久化"})

	tool := NewMemorySearchTool(store)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"SQLite"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "SQLite") {
		t.Errorf("应检索到含 SQLite 的记忆: %s", out)
	}
}

func TestMemorySearchNoHit(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	t.Cleanup(func() { db.Close() })
	store, _ := ltm.NewStore(db)
	tool := NewMemorySearchTool(store)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"不存在"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "[]" {
		t.Errorf("无命中应返回 \"[]\"，got %q", out)
	}
}
