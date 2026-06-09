package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/harness9/internal/engine"
	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/provider"
	"github.com/harness9/internal/sandbox"
	"github.com/harness9/internal/tools"
)

// Config 存储从 CLI flags 解析的运行配置。
type Config struct {
	DatasetPath string
	OutputDir   string
	SampleN     int
	MaxTurns    int
	Parallel    int
	Resume      bool
	TimeoutMins int
	Model       string
}

// newProvider 根据模型名创建 LLM provider。
// 优先级：cfg.Model > LLM_MODEL 环境变量 > 默认值 openai/gpt-4o-mini。
func newProvider(model string) (provider.LLMProvider, error) {
	if model == "" {
		model = os.Getenv("LLM_MODEL")
	}
	if model == "" {
		model = "openai/gpt-4o-mini"
	}
	return provider.NewOpenAIProvider(model)
}

// runInstance 对单个 SWE-bench instance 执行完整的 clone → sandbox → engine → patch 流程。
// 任何环境错误都返回 RunResult.Error，不 panic。
func runInstance(ctx context.Context, inst Instance, cfg Config) RunResult {
	start := time.Now()

	// 1. 创建隔离临时目录
	tmpDir, err := os.MkdirTemp("", "swebench-"+inst.InstanceID+"-*")
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("创建临时目录失败: %w", err), Duration: time.Since(start)}
	}
	defer os.RemoveAll(tmpDir)

	// 2. git clone + checkout base_commit（宿主机执行）
	repoURL := "https://github.com/" + inst.Repo
	cloneCtx, cloneCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cloneCancel()

	cloneOut, err := exec.CommandContext(cloneCtx, "git", "clone", repoURL, tmpDir).CombinedOutput()
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("git clone 失败: %w\n%s", err, cloneOut), Duration: time.Since(start)}
	}
	checkoutCtx, checkoutCancel := context.WithTimeout(ctx, 30*time.Second)
	defer checkoutCancel()
	checkoutOut, err := exec.CommandContext(checkoutCtx, "git", "-C", tmpDir, "checkout", inst.BaseCommit).CombinedOutput()
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("git checkout 失败: %w\n%s", err, checkoutOut), Duration: time.Since(start)}
	}

	// 3. 创建 Docker Sandbox 环境
	sandboxCfg := sandbox.DefaultConfig()
	mgr := sandbox.NewManager(sandboxCfg)
	sandboxCtx, sandboxCancel := context.WithTimeout(ctx, 60*time.Second)
	defer sandboxCancel()
	env, err := mgr.Create(sandboxCtx, tmpDir)
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("sandbox 创建失败: %w", err), Duration: time.Since(start)}
	}
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		mgr.DestroyAll(cleanCtx)
	}()

	// 4. 注册工具
	registry := tools.NewRegistry()
	toolList := []tools.BaseTool{
		tools.NewBashTool(tmpDir, tools.WithEnvironment(env)),
		tools.NewReadFileTool(tmpDir, tools.ReadFileWithEnvironment(env)),
		tools.NewWriteFileTool(tmpDir, tools.WriteFileWithEnvironment(env)),
		tools.NewEditFileTool(tmpDir, tools.EditFileWithEnvironment(env)),
	}
	for _, t := range toolList {
		if err := registry.Register(t); err != nil {
			return RunResult{Instance: inst, Error: fmt.Errorf("注册工具失败: %w", err), Duration: time.Since(start)}
		}
	}
	hookReg := hooks.NewHookRegistry(registry)

	// 5. 构造 provider 和 engine
	llm, err := newProvider(cfg.Model)
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("创建 LLM provider 失败: %w", err), Duration: time.Since(start)}
	}
	eng := engine.NewAgentEngine(llm, hookReg, tmpDir,
		engine.WithMaxTurns(cfg.MaxTurns),
		engine.WithPromptBuilder(&swebenchPromptBuilder{instance: inst}),
	)

	// 6. 执行 agent loop（带 per-instance 超时）
	instanceCtx, instanceCancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMins)*time.Minute)
	defer instanceCancel()
	runErr := eng.Run(instanceCtx, "请修复上述 Issue。")

	// 7. 收集 patch（无论 runErr 如何，MaxTurns 触发时也可能有部分 patch）
	diffCtx, diffCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer diffCancel()
	patchOut, _ := exec.CommandContext(diffCtx, "git", "-C", tmpDir, "diff").CombinedOutput()
	patch := strings.TrimSpace(string(patchOut))

	if runErr != nil && patch == "" {
		return RunResult{Instance: inst, Error: runErr, Duration: time.Since(start)}
	}
	return RunResult{Instance: inst, Patch: patch, Duration: time.Since(start)}
}
