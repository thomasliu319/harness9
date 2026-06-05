// internal/sandbox/manager_test.go
package sandbox

import (
	"context"
	"sync"
	"testing"
)

// newTestManager 创建使用 mock runner 的 Manager，不真实启动 Docker。
func newTestManager() *Manager {
	cfg := testCfg()
	mgr := NewManager(cfg)
	mgr.runnerFactory = func(id, workDir string, c SandboxConfig) cmdRunner {
		return newMock(
			id[:8], errNil(), // docker run → dockerID = id prefix（id 是 16 位 hex，前 8 位够用）
			"true", errNil(), // docker inspect → running
			"", errNil(), // docker stop
			"", errNil(), // docker rm
		).run
	}
	return mgr
}

func TestManager_CreateAndListAll(t *testing.T) {
	mgr := newTestManager()
	workDir := t.TempDir()

	env, err := mgr.Create(context.Background(), workDir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if env == nil {
		t.Fatal("Create 应返回非 nil Environment")
	}

	infos := mgr.ListAll()
	if len(infos) != 1 {
		t.Errorf("ListAll 应有 1 个 Sandbox，实际 %d", len(infos))
	}
	if infos[0].State != StateRunning {
		t.Errorf("Sandbox 状态应为 Running，实际 %v", infos[0].State)
	}
	if infos[0].WorkDir != workDir {
		t.Errorf("WorkDir = %q, 期望 %q", infos[0].WorkDir, workDir)
	}
}

func TestManager_Destroy(t *testing.T) {
	mgr := newTestManager()
	env, _ := mgr.Create(context.Background(), t.TempDir())

	if err := mgr.Destroy(context.Background(), env.ID()); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(mgr.ListAll()) != 0 {
		t.Error("Destroy 后 ListAll 应为空")
	}
}

func TestManager_DestroyAll(t *testing.T) {
	mgr := newTestManager()
	for i := 0; i < 3; i++ {
		if _, err := mgr.Create(context.Background(), t.TempDir()); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	if len(mgr.ListAll()) != 3 {
		t.Fatalf("期望 3 个 Sandbox")
	}

	mgr.DestroyAll(context.Background())
	if len(mgr.ListAll()) != 0 {
		t.Error("DestroyAll 后 ListAll 应为空")
	}
}

func TestManager_ConcurrentCreate(t *testing.T) {
	mgr := newTestManager()
	var wg sync.WaitGroup
	const n = 10

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := mgr.Create(context.Background(), t.TempDir()); err != nil {
				t.Errorf("并发 Create 失败: %v", err)
			}
		}()
	}
	wg.Wait()

	infos := mgr.ListAll()
	if len(infos) != n {
		t.Errorf("并发 Create 后应有 %d 个 Sandbox，实际 %d", n, len(infos))
	}

	// 验证所有 ID 唯一
	ids := make(map[string]bool)
	for _, info := range infos {
		if ids[info.ID] {
			t.Errorf("Sandbox ID 重复: %s", info.ID)
		}
		ids[info.ID] = true
	}
}

func TestManager_WithUpdateNotify(t *testing.T) {
	mgr := newTestManager()
	var mu sync.Mutex
	var received [][]SandboxInfo
	mgr.WithUpdateNotify(func(infos []SandboxInfo) {
		mu.Lock()
		received = append(received, infos)
		mu.Unlock()
	})

	mgr.Create(context.Background(), t.TempDir())

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Error("Create 后应触发 onUpdate 通知")
	}
}
