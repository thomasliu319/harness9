package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/harness9/internal/engine"
	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/provider"
	"github.com/harness9/internal/sandbox"
	"github.com/harness9/internal/schema"
	"github.com/harness9/internal/tools"
)

// Config 存储从 CLI flags 解析的运行配置。
type Config struct {
	DatasetPath string
	OutputDir   string
	SampleN     int
	// MaxTurns 为 0 时沿用引擎默认值（500），大于 0 时显式限制。
	MaxTurns    int
	Parallel    int
	Resume      bool
	TimeoutMins int
	Model       string
}

// resolveModelName 解析最终使用的模型名：cfg.Model > LLM_MODEL 环境变量 > 默认值。
// 与 newProvider 保持相同的优先级逻辑，用于填写 predictions.jsonl 的 model_name_or_path。
func resolveModelName(model string) string {
	if model == "" {
		model = os.Getenv("LLM_MODEL")
	}
	if model == "" {
		model = "openai/gpt-4o-mini"
	}
	return model
}

// newProvider 根据模型名创建 LLM provider。
// 优先级：cfg.Model > LLM_MODEL 环境变量 > 默认值 openai/gpt-4o-mini。
func newProvider(model string) (provider.LLMProvider, error) {
	return provider.NewOpenAIProvider(resolveModelName(model))
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
	// 使用 --filter=blob:none（blobless clone）：只拉取 commits 和 tree 元数据，
	// 不下载文件内容；checkout 时按需拉取，对大仓库（Django/Sympy 等）速度快 10x+。
	repoURL := "https://github.com/" + inst.Repo
	cloneCtx, cloneCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cloneCancel()

	cloneOut, err := exec.CommandContext(cloneCtx, "git", "clone", "--filter=blob:none", repoURL, tmpDir).CombinedOutput()
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("git clone 失败: %w\n%s", err, cloneOut), Duration: time.Since(start)}
	}
	checkoutCtx, checkoutCancel := context.WithTimeout(ctx, 3*time.Minute)
	defer checkoutCancel()
	checkoutOut, err := exec.CommandContext(checkoutCtx, "git", "-C", tmpDir, "checkout", inst.BaseCommit).CombinedOutput()
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("git checkout 失败: %w\n%s", err, checkoutOut), Duration: time.Since(start)}
	}

	// 3. 创建 Docker Sandbox 环境
	// SWE-bench 仓库需要 Python 环境；若用户未通过 SANDBOX_IMAGE 显式覆盖，
	// 强制使用 python:3.11-slim 替代默认的 ubuntu:22.04，
	// 避免 Agent 在无 Python 的容器中陷入无效的解释器搜索死循环。
	sandboxCfg := sandbox.DefaultConfig()
	if os.Getenv("SANDBOX_IMAGE") == "" {
		sandboxCfg.Image = "python:3.11-slim"
	}
	// macOS Docker Desktop 用 VirtioFS 处理 bind mount，大型 git repo 的 volume
	// 注册比 Linux 慢，30s（默认值）容易触发超时；扩大到 90s 留足缓冲。
	sandboxCfg.StartTimeout = 90 * time.Second
	mgr := sandbox.NewManager(sandboxCfg)
	// sandboxCtx 必须 > StartTimeout（90s），否则外层超时先触发，
	// 使内部 StartTimeout 的 90s 缓冲完全无效。设为 120s 留有余量。
	sandboxCtx, sandboxCancel := context.WithTimeout(ctx, 120*time.Second)
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
	// MaxTurns=0 时不传 WithMaxTurns，沿用引擎默认值（500）。
	llm, err := newProvider(cfg.Model)
	if err != nil {
		return RunResult{Instance: inst, Error: fmt.Errorf("创建 LLM provider 失败: %w", err), Duration: time.Since(start)}
	}
	engOpts := []engine.Option{
		engine.WithPromptBuilder(&swebenchPromptBuilder{instance: inst}),
	}
	if cfg.MaxTurns > 0 {
		engOpts = append(engOpts, engine.WithMaxTurns(cfg.MaxTurns))
	}
	eng := engine.NewAgentEngine(llm, hookReg, tmpDir, engOpts...)

	// 6. 执行 agent loop（带 per-instance 超时），同时将完整 trajectory 写入日志
	instanceCtx, instanceCancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMins)*time.Minute)
	defer instanceCancel()

	// logs/ 目录由 main.go 在进入并发循环前统一创建（os.MkdirAll），此处无需再建。
	logPath := filepath.Join(cfg.OutputDir, "logs", inst.InstanceID+".log")
	runErr := runWithTrajectory(instanceCtx, eng, "请修复上述 Issue。", logPath, inst)

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

// runWithTrajectory 通过 RunStream 执行 agent loop，将所有事件以可读格式写入 logPath。
// 日志文件创建失败时 fail-open：agent 仍正常运行，只是不写日志。
// Benchmark 场景下自动批准所有工具审批请求（无人值守）。
func runWithTrajectory(ctx context.Context, eng *engine.AgentEngine, userPrompt, logPath string, inst Instance) error {
	// 创建日志文件（fail-open：失败时写入 Discard，agent 继续运行）
	var w io.Writer = io.Discard
	if lf, err := os.Create(logPath); err == nil {
		bw := bufio.NewWriter(lf)
		defer func() { bw.Flush(); lf.Close() }()
		w = bw
	}

	// 写文件头
	fmt.Fprintf(w, "=== SWE-bench Instance: %s ===\n", inst.InstanceID)
	fmt.Fprintf(w, "Repo:        %s\n", inst.Repo)
	fmt.Fprintf(w, "BaseCommit:  %s\n", inst.BaseCommit)
	fmt.Fprintf(w, "StartTime:   %s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	stream, err := eng.RunStream(ctx, userPrompt)
	if err != nil {
		return err
	}

	currentTurn := 0
	var runErr error

	for evt := range stream {
		// 新 Turn 时打印分隔符
		if evt.Turn > 0 && evt.Turn != currentTurn {
			currentTurn = evt.Turn
			fmt.Fprintf(w, "\n\n--- Turn %d ---\n", currentTurn)
		}

		switch evt.Type {
		case engine.EventActionDelta:
			fmt.Fprint(w, evt.Data.(string))

		case engine.EventThinkingDelta:
			// thinking 内容用 <thinking> 标记，便于后处理过滤
			fmt.Fprint(w, evt.Data.(string))

		case engine.EventToolStart:
			tc := evt.Data.(schema.ToolCall)
			fmt.Fprintf(w, "\n\n[Tool Call: %s]\n%s\n", tc.Name, string(tc.Arguments))

		case engine.EventToolResult:
			trd := evt.Data.(engine.ToolResultData)
			status := "ok"
			if trd.Result.IsError {
				status = "error"
			}
			fmt.Fprintf(w, "\n[Tool Result: %s | %s | %s]\n%s\n",
				trd.Result.ToolCallID, trd.Duration.Round(time.Millisecond), status,
				trd.Result.Output)

		case engine.EventTokenUpdate:
			tud := evt.Data.(engine.TokenUpdateData)
			fmt.Fprintf(w, "\n[Tokens: %d]\n", tud.EstimatedTokens)

		case engine.EventCompaction:
			cd := evt.Data.(engine.CompactionData)
			fmt.Fprintf(w, "\n[Compaction: %d→%d tokens, %d→%d msgs]\n",
				cd.TokensBefore, cd.TokensAfter, cd.MsgsBefore, cd.MsgsAfter)

		case engine.EventError:
			// errors.Join 累积多次 EventError，保留完整错误链而非只保留最后一条。
			runErr = errors.Join(runErr, fmt.Errorf("%s", evt.Data.(string)))

		case engine.EventApprovalRequired:
			// Benchmark 无人值守模式：自动批准所有工具调用
			req := evt.Data.(engine.ApprovalRequest)
			fmt.Fprintf(w, "\n[Auto-Approved: %s]\n", req.ToolCall.Name)
			req.ResponseCh <- hooks.ApprovalResponse{Approved: true}
		}
	}

	return runErr
}
