package ltm

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestStore 创建一个内存 SQLite Store，now 固定为可控时间。
func newTestStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	s, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	s.now = func() time.Time { return now }
	return s, &now
}

func TestStoreAddAndGet(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	got, err := s.Add(ctx, &Entry{Title: "Go 版本", Content: "项目使用 Go 1.25.3", Category: CategoryKnowledge, Importance: 7})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got.ID == "" {
		t.Fatal("Add 应生成非空 ID")
	}
	if got.Signature == "" {
		t.Fatal("Add 应写入签名")
	}
	fetched, err := s.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched.Content != "项目使用 Go 1.25.3" || fetched.Importance != 7 {
		t.Errorf("Get 内容不符: %+v", fetched)
	}
}

func TestStoreAddDedup(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	a, err := s.Add(ctx, &Entry{Title: "x", Content: "重复内容"})
	if err != nil {
		t.Fatalf("第一次 Add: %v", err)
	}
	b, err := s.Add(ctx, &Entry{Title: "y", Content: "重复内容"})
	if err != nil {
		t.Fatalf("第二次 Add: %v", err)
	}
	if a.ID != b.ID {
		t.Errorf("相同内容应去重为同一条目: %s != %s", a.ID, b.ID)
	}
	if b.UseCount != 1 {
		t.Errorf("去重命中应自增 use_count，got %d", b.UseCount)
	}
}

func TestStoreSearchAndReinforce(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Add(ctx, &Entry{Title: "Go 版本", Content: "项目使用 Go 1.25.3 构建"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ctx, &Entry{Title: "数据库", Content: "使用 SQLite 持久化会话"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(ctx, "SQLite", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Title != "数据库" {
		t.Fatalf("期望命中「数据库」，got %+v", res)
	}
	// 强化：命中后 use_count 自增、last_used_at 写入。
	again, _ := s.Get(ctx, res[0].ID)
	if again.UseCount != 1 {
		t.Errorf("命中应自增 use_count，got %d", again.UseCount)
	}
	if again.LastUsedAt.IsZero() {
		t.Error("命中应写入 last_used_at")
	}
}

func TestStoreSearchEmptyQuery(t *testing.T) {
	s, _ := newTestStore(t)
	res, err := s.Search(context.Background(), "   ", 5)
	if err != nil {
		t.Fatalf("空查询不应报错: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("空查询应返回空结果，got %d", len(res))
	}
}

func TestStoreUpdate(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	e, _ := s.Add(ctx, &Entry{Title: "旧标题", Content: "旧内容"})
	e.Title = "新标题"
	e.Content = "新内容"
	e.Importance = 9
	if err := s.Update(ctx, e); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.Get(ctx, e.ID)
	if got.Title != "新标题" || got.Content != "新内容" || got.Importance != 9 {
		t.Errorf("更新未生效: %+v", got)
	}
	if got.Signature != Signature("新内容") {
		t.Error("更新内容应重算签名")
	}
	// FTS 应可检索到新内容、检索不到旧内容。
	if res, _ := s.Search(ctx, "新内容", 5); len(res) != 1 {
		t.Errorf("更新后 FTS 应命中新内容，got %d", len(res))
	}
	if res, _ := s.Search(ctx, "旧内容", 5); len(res) != 0 {
		t.Errorf("更新后 FTS 不应命中旧内容，got %d", len(res))
	}
}

func TestStoreSoftDelete(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	e, _ := s.Add(ctx, &Entry{Title: "t", Content: "待删除内容"})
	if err := s.SoftDelete(ctx, e.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	got, _ := s.Get(ctx, e.ID) // 仍可取到（审计）
	if !got.Disabled {
		t.Error("软删除后 disabled 应为 true")
	}
	if res, _ := s.Search(ctx, "待删除内容", 5); len(res) != 0 {
		t.Errorf("软删除后不应被检索到，got %d", len(res))
	}
}

func TestStoreReAddAfterSoftDelete(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	first, err := s.Add(ctx, &Entry{Title: "t", Content: "可复活内容"})
	if err != nil {
		t.Fatalf("首次 Add: %v", err)
	}
	if err := s.SoftDelete(ctx, first.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	// 软删除后重新添加相同内容：不应触发 UNIQUE 冲突，应得到一条新的活跃条目。
	second, err := s.Add(ctx, &Entry{Title: "t", Content: "可复活内容"})
	if err != nil {
		t.Fatalf("软删除后重新 Add 不应报错: %v", err)
	}
	if second.ID == first.ID {
		t.Error("软删除后重新 Add 应创建新条目，而非命中已删除条目")
	}
	if res, _ := s.Search(ctx, "可复活内容", 5); len(res) != 1 {
		t.Errorf("重新添加后应可检索到 1 条活跃记忆，got %d", len(res))
	}
}
