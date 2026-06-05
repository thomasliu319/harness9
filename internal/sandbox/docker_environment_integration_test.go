//go:build integration

// internal/sandbox/docker_environment_integration_test.go
package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDockerEnvironmentIntegration_RunBash(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Image = "ubuntu:22.04"
	cfg.StartTimeout = 60 * time.Second

	c := newContainer("integ-test", t.TempDir(), cfg, realCmdRunner)
	if err := c.Start(context.Background()); err != nil {
		t.Skipf("Docker 不可用，跳过集成测试: %v", err)
	}
	defer c.Stop(context.Background())

	env := newDockerEnvironment(c.dockerID, c.id, t.TempDir(), realCmdRunner)
	out, err := env.RunBash(context.Background(), "echo hello_from_container", "/tmp")
	if err != nil {
		t.Fatalf("RunBash: %v", err)
	}
	if !strings.Contains(out, "hello_from_container") {
		t.Errorf("RunBash = %q, 期望包含 hello_from_container", out)
	}
}
