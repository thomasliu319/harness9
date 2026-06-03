package memory_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/harness9/internal/memory"
)

func TestManager_NewAndListSessions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mgr, err := memory.NewManager(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	sess1, err := mgr.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sess2, err := mgr.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if sess1.SessionID() == sess2.SessionID() {
		t.Error("sessions must have unique IDs")
	}

	list, err := mgr.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(list))
	}
}

func TestManager_OpenExistingSession(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mgr, err := memory.NewManager(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	sess, err := mgr.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := sess.SessionID()

	reopened, err := mgr.OpenSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.SessionID() != id {
		t.Errorf("want %q, got %q", id, reopened.SessionID())
	}
}

func TestManager_OpenNonExistentSession(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mgr, err := memory.NewManager(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	_, err = mgr.OpenSession(ctx, "nonexistent-id")
	if err == nil {
		t.Error("want error for non-existent session, got nil")
	}
}

func TestManager_DeleteSession(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mgr, err := memory.NewManager(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	sess, err := mgr.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.DeleteSession(ctx, sess.SessionID()); err != nil {
		t.Fatal(err)
	}

	list, err := mgr.ListSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("want 0 sessions after delete, got %d", len(list))
	}
}

func TestManager_CreatesDirectory(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "dir", "test.db")

	mgr, err := memory.NewManager(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}

	_, err = mgr.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
}

func TestManager_DeleteSession_CleansOffloadDir(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	offloadBase := t.TempDir()

	mgr, err := memory.NewManager(
		filepath.Join(dbDir, "test.db"),
		memory.WithToolResultsDir(offloadBase),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	sess, err := mgr.NewSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessID := sess.SessionID()

	// 模拟 offload 文件写入
	offloadDir := filepath.Join(offloadBase, sessID)
	if err := os.MkdirAll(offloadDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(offloadDir, "tool1.txt"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}

	// 删除 session
	if err := mgr.DeleteSession(ctx, sessID); err != nil {
		t.Fatalf("DeleteSession error: %v", err)
	}

	// offload 目录应已被清理
	if _, err := os.Stat(offloadDir); !os.IsNotExist(err) {
		t.Error("offload directory should be removed after session deletion")
	}
}

func TestManager_DeleteSession_NoToolResultsDir_NoError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// 不设置 WithToolResultsDir，DeleteSession 不应出错
	mgr, err := memory.NewManager(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	sess, _ := mgr.NewSession(ctx)
	if err := mgr.DeleteSession(ctx, sess.SessionID()); err != nil {
		t.Fatalf("DeleteSession without toolResultsDir should not error: %v", err)
	}
}

func TestManagerDBAccessor(t *testing.T) {
	dir := t.TempDir()
	mgr, err := memory.NewManager(filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()
	if mgr.DB() == nil {
		t.Fatal("DB() 应返回非 nil 连接")
	}
	if err := mgr.DB().Ping(); err != nil {
		t.Errorf("DB() 连接应可用: %v", err)
	}
}
