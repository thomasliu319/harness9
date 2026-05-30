// Command harness9 是 harness9 框架的主入口。
//
// 在交互式终端（TTY）中运行时自动进入全屏 TUI 模式；
// 通过管道或 CI 调用时退回交互式 CLI REPL 模式。
// Agent 的工具沙箱根目录固定为启动时的进程工作目录（cwd）。
//
// 环境变量（可通过 .env 文件或系统环境变量提供）：
//
//	OPENAI_API_KEY     LLM Provider API Key（必填）
//	OPENAI_BASE_URL    自定义 OpenAI 兼容 API 地址（可选）
//	LLM_MODEL          模型名称（默认：openai/gpt-4o-mini）
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	harctx "github.com/harness9/internal/context"
	"github.com/harness9/internal/engine"
	"github.com/harness9/internal/env"
	"github.com/harness9/internal/hooks"
	"github.com/harness9/internal/logfmt"
	"github.com/harness9/internal/memory"
	"github.com/harness9/internal/permission"
	"github.com/harness9/internal/planning"
	"github.com/harness9/internal/provider"
	"github.com/harness9/internal/skills"
	"github.com/harness9/internal/subagent"
	"github.com/harness9/internal/tools"
)

// version 由 goreleaser ldflags 在发布构建时注入；本地开发构建显示 "dev"。
var version = "dev"

func main() {
	// upgrade 子命令在 flag 解析前处理，避免与 flag 系统冲突。
	if len(os.Args) > 1 && os.Args[1] == "upgrade" {
		if err := RunUpgrade(version); err != nil {
			fmt.Fprintf(os.Stderr, "升级失败: %v\n", err)
			os.Exit(1)
		}
		return
	}

	versionMode := flag.Bool("version", false, "打印版本号并退出")
	flag.Usage = func() {
		fmt.Print(`harness9 — 轻量级 AI Agent Harness 框架

用法:
  harness9 [flags]
  harness9 <command>

Flags:
  --version   打印版本号并退出
  --help      打印此帮助信息并退出

命令:
  upgrade     升级 harness9 到最新版本

环境变量:
  LLM_MODEL        模型名称（默认: openai/gpt-4o-mini）
  OPENAI_API_KEY   OpenAI 兼容 API Key（必填）
  OPENAI_BASE_URL  自定义 API 地址（可选，用于 OpenRouter / Azure 等）

示例:
  harness9                  启动（TTY 自动进入 TUI，管道模式退回 CLI REPL）
  harness9 --version        查看版本号
  harness9 upgrade          升级到最新版本
`)
	}
	flag.Parse()

	if *versionMode {
		fmt.Println("harness9 " + version)
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("获取工作目录失败: %v", err)))
	}

	if err := env.Load(filepath.Join(cwd, ".env")); err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("加载环境配置失败: %v", err)))
	}

	workDir := cwd

	// 加载 Skills（workdir/skills/，目录不存在时静默返回空 Index）
	skillsIndex, err := skills.LoadSkills(filepath.Join(workDir, "skills"))
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("加载 skills 失败: %v", err)))
	}

	// 构建 System Prompt（基础 prompt + AGENTS.md + skills 索引）
	promptBuilder := harctx.NewPromptBuilder(workDir, skillsIndex).WithTodoEnabled(true).WithOffloadEnabled(true)

	modelName := os.Getenv("LLM_MODEL")
	if modelName == "" {
		modelName = "openai/gpt-4o-mini"
	}
	llm, err := provider.NewOpenAIProvider(modelName)
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("创建 Provider 失败: %v", err)))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("获取 home 目录失败: %v", err)))
	}
	toolResultsDir := filepath.Join(workDir, ".harness9", "tool_results")
	mgr, err := memory.NewManager(
		filepath.Join(homeDir, ".harness9", "sessions.db"),
		memory.WithToolResultsDir(toolResultsDir),
	)
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("初始化 Memory Manager 失败: %v", err)))
	}
	defer mgr.Close()

	sess, err := mgr.NewSession(ctx)
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("创建会话失败: %v", err)))
	}

	registry := tools.NewRegistry()
	todoStore := planning.NewTodoStore()
	planWriter, err := hooks.NewFilePlanWriter(workDir, homeDir, sess.SessionID())
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("初始化 FilePlanWriter 失败: %v", err)))
	}
	for _, tool := range []tools.BaseTool{
		tools.NewReadFileTool(workDir),
		tools.NewWriteFileTool(workDir),
		tools.NewBashTool(workDir),
		tools.NewEditFileTool(workDir),
		skills.NewUseSkillTool(skillsIndex),
		tools.NewTodoWriteTool(todoStore, tools.WithPlanWriter(planWriter)),
	} {
		if err := registry.Register(tool); err != nil {
			log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("注册工具 %s 失败: %v", tool.Name(), err)))
		}
	}

	offloadHook := hooks.NewOffloadHook(workDir, sess.SessionID())
	dangerHook := hooks.NewDangerHook()

	settingsPath := filepath.Join(workDir, ".harness9", "settings.json")
	// NewFileHook 每次工具调用时从磁盘重新读取规则，确保 TUI 写入白名单后下次调用立即生效。
	permHook := permission.NewFileHook(settingsPath)

	modelLimits := provider.GetModelLimits(modelName)

	// ---- Sub-Agent 系统接线 ----
	// 子代理可用的基础工具实例（独立实例，沙箱根目录同为 workDir）。
	subAgentBaseTools := []tools.BaseTool{
		tools.NewReadFileTool(workDir),
		tools.NewWriteFileTool(workDir),
		tools.NewBashTool(workDir),
		tools.NewEditFileTool(workDir),
		skills.NewUseSkillTool(skillsIndex),
	}

	// 子代理定义注册表：先注册内置，再加载文件式定义（文件可覆盖同名内置）。
	subAgentReg := subagent.NewRegistry()
	if err := subAgentReg.Register(subagent.SubAgentDefinition{
		Name:         "code-reviewer",
		Description:  "代码审查专家。写完或修改代码后主动使用，检查安全、性能与最佳实践。",
		SystemPrompt: "你是一名资深代码审查专家。审查时聚焦：安全漏洞、性能问题、可维护性。给出具体、可操作的改进建议，引用文件与行号。",
		Tools:        []string{"read_file", "bash"},
		MaxTurns:     20,
		Source:       "builtin",
	}); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("注册内置子代理失败: %v", err)))
	}
	if err := subAgentReg.LoadFromDir(filepath.Join(workDir, ".harness9", "agents")); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("加载文件式子代理失败: %v", err)))
	}

	subAgentMailbox := subagent.NewMailbox()
	_ = subAgentMailbox // TODO(task13): 传入 RunTUI
	subAgentRunner := subagent.NewRunner(subagent.RunnerConfig{
		BaseTools:          subAgentBaseTools,
		SharedHooks:        []hooks.ToolHook{dangerHook, offloadHook},
		SettingsPath:       settingsPath,
		SkillsIndex:        skillsIndex,
		WorkDir:            workDir,
		DefaultMaxTurns:    20,
		ToolTimeout:        60 * time.Second,
		MaxConcurrentTools: 0,
		ProviderFor: func(model string) (provider.LLMProvider, int, error) {
			if model == "" {
				return llm, modelLimits.ContextTokens, nil
			}
			p, err := provider.NewOpenAIProvider(model)
			if err != nil {
				return nil, 0, err
			}
			return p, provider.GetModelLimits(model).ContextTokens, nil
		},
		CompactorFor: func(p provider.LLMProvider, ctxWin int) memory.Compactor {
			return memory.NewSummarizationCompactor(p, ctxWin)
		},
		BaseCtx: ctx,
	})

	taskTool := subagent.NewTaskTool(subAgentReg, subAgentRunner, subAgentMailbox)
	if err := registry.Register(taskTool); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("注册 task 工具失败: %v", err)))
	}
	// ---- Sub-Agent 接线结束 ----

	// Hook 执行顺序：PermissionHook（配置规则）→ DangerHook（内置模式）→ OffloadHook（大输出）
	hookReg := hooks.NewHookRegistry(registry, permHook, dangerHook, offloadHook)

	// SummarizationCompactor 使用同一 LLM 生成摘要，内置 TokenBudgetCompactor 作为错误回退。
	compactor := memory.NewSummarizationCompactor(llm, modelLimits.ContextTokens,
		memory.WithTodoInjector(todoStore),
	)

	eng := engine.NewAgentEngine(llm, hookReg, workDir,
		engine.WithPromptBuilder(promptBuilder),
		engine.WithSession(sess),
		engine.WithCompactor(compactor),
		engine.WithContextWindow(modelLimits.ContextTokens),
		engine.WithTodoStore(todoStore),
	)

	if term.IsTerminal(os.Stdin.Fd()) {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("harness9 TUI 启动 │ workDir=%s", workDir)))
		if err := RunTUI(ctx, eng, mgr, sess, skillsIndex, todoStore, workDir, modelName); err != nil {
			log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("TUI 退出: %v", err)))
		}
	} else {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("harness9 CLI 启动 │ workDir=%s", workDir)))
		RunCLI(ctx, eng, skillsIndex)
	}
	log.Print(logfmt.FormatMsg("main", "harness9 正常退出"))
}
