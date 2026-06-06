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
	"github.com/harness9/internal/ltm"
	"github.com/harness9/internal/memory"
	"github.com/harness9/internal/permission"
	"github.com/harness9/internal/planning"
	"github.com/harness9/internal/provider"
	"github.com/harness9/internal/sandbox"
	"github.com/harness9/internal/skills"
	"github.com/harness9/internal/subagent"
	"github.com/harness9/internal/tools"
)

// version 由 goreleaser ldflags 在发布构建时注入；本地开发构建显示 "dev"。
var version = "dev"

// generalPurposeSystemPrompt 是内置 general-purpose 子代理的 system prompt。
//
// 设计对标 Claude Code / DeepAgents 的通用子代理：强调「上下文隔离 + 自包含结论」——
// 子代理在独立 context 中自主探索与执行，最终仅把结构化结论回传给主代理，避免冗长的
// 中间过程污染主代理上下文。
const generalPurposeSystemPrompt = `你是一个通用型子代理（general-purpose sub-agent），在与主代理隔离的独立上下文中完成被委派的任务。

工作原则：
1. 你看不到主代理的对话历史，完成任务所需的全部信息都在传入的 prompt 中。若信息不足，基于合理假设继续推进，并在最终结论中显式说明你做出的假设。
2. 你拥有与主代理相同的工具集（读写文件、执行命令、调用 skill 等），可自主决定如何分解并完成任务。优先用工具去探索与验证，不要把未经核实的猜测当作结论。
3. 你的最终回复是返回给主代理的唯一结果，因此必须自包含、结构清晰、聚焦结论：说明你做了什么、得到的关键结果、以及主代理需要据此采取的后续动作。省略无价值的中间过程，但保留关键的文件路径、命令输出与证据引用。
4. 完成任务后立即停止，不要做与任务无关的额外操作，也不要反复确认。`

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

	// ---- Sandbox 系统接线 ----
	sandboxCfg := sandbox.DefaultConfig()
	var sandboxMgr *sandbox.Manager
	var sandboxEnv sandbox.Environment // nil = 工具走本地执行路径

	// SandboxBar 通知 channel 必须在 Create 之前创建并注册，
	// 否则 Create 内部触发的 notify() 因 onUpdate==nil 而丢失，TUI 永远不会收到初始状态。
	sandboxNotifyCh := make(chan []sandbox.SandboxInfo, 8)

	if sandboxCfg.Enabled {
		sandboxMgr = sandbox.NewManager(sandboxCfg)
		// WithUpdateNotify 必须在 Create 之前调用，确保初始创建通知能送达 TUI
		sandboxMgr.WithUpdateNotify(func(infos []sandbox.SandboxInfo) {
			select {
			case sandboxNotifyCh <- infos:
			default: // 丢弃：buffer 满时 TUI 仍持有旧快照，下次更新会覆盖
			}
		})
		if err := sandboxMgr.ReapOrphans(ctx); err != nil {
			log.Print(logfmt.FormatMsg("main", fmt.Sprintf("清理孤儿 Sandbox 失败（忽略）: %v", err)))
		}
		var sandboxErr error
		sandboxEnv, sandboxErr = sandboxMgr.Create(ctx, workDir)
		if sandboxErr != nil {
			log.Print(logfmt.FormatMsg("main", fmt.Sprintf("Sandbox 启动失败，已降级为本地进程模式: %v", sandboxErr)))
			sandboxMgr = nil
			sandboxEnv = nil
		} else {
			defer sandboxMgr.DestroyAll(ctx)
		}
	}
	// ---- Sandbox 系统接线（续：工具注入见下）----

	// 构建 System Prompt（基础 prompt + AGENTS.md + skills 索引），现已可访问 sandboxEnv
	promptBuilder := harctx.NewPromptBuilder(workDir, skillsIndex).
		WithTodoEnabled(true).
		WithOffloadEnabled(true).
		WithSandboxContext(sandboxEnv != nil)

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

	// ---- Long-Term Memory 接线 ----
	// 复用 Manager 的 SQLite 连接，初始化长期记忆 Store 与 MEMORY.md 物化视图。
	ltmStore, err := ltm.NewStore(mgr.DB())
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("初始化长期记忆 Store 失败: %v", err)))
	}
	memoryFilePath := filepath.Join(homeDir, ".harness9", "memories", "MEMORY.md")
	ltmPrecis := ltm.NewPrecis(ltmStore, memoryFilePath, 5120)
	// 启动时回收过期记忆并重建一次精华（fail-soft）。
	if _, err := ltmStore.PurgeExpired(ctx); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("回收过期记忆失败: %v", err)))
	}
	if err := ltmPrecis.Regenerate(ctx); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("重建记忆精华失败: %v", err)))
	}
	promptBuilder = promptBuilder.WithLongTermMemory(func() string {
		content, _ := ltmPrecis.Read()
		return content
	})
	// ---- Long-Term Memory 接线（续：工具注册见下）----

	registry := tools.NewRegistry()
	todoStore := planning.NewTodoStore()
	planWriter, err := hooks.NewFilePlanWriter(workDir, homeDir, sess.SessionID())
	if err != nil {
		log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("初始化 FilePlanWriter 失败: %v", err)))
	}
	for _, tool := range []tools.BaseTool{
		tools.NewReadFileTool(workDir, tools.ReadFileWithEnvironment(sandboxEnv)),
		tools.NewWriteFileTool(workDir, tools.WriteFileWithEnvironment(sandboxEnv)),
		tools.NewBashTool(workDir, tools.WithEnvironment(sandboxEnv)),
		tools.NewEditFileTool(workDir, tools.EditFileWithEnvironment(sandboxEnv)),
		skills.NewUseSkillTool(skillsIndex),
		tools.NewTodoWriteTool(todoStore, tools.WithPlanWriter(planWriter)),
		tools.NewMemoryWriteTool(ltmStore, ltmPrecis),
		tools.NewMemorySearchTool(ltmStore),
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

	// agentMaxTurns 是主代理与子代理共用的最大 Turn 数：子代理与主代理保持一致，
	// 避免子代理过早因 Turn 上限失败（同时显式化主代理的轮数，不再依赖引擎默认值）。
	const agentMaxTurns = 50

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
	//
	// 内置 general-purpose 子代理：对标 Claude Code 与 DeepAgents 的「通用子代理」设计。
	// 不限定工具白名单（Tools 为空 = 继承父全部可用工具），不覆盖模型（Model 为空 = 继承父模型），
	// 用于需要兼顾探索与修改、复杂推理或多步依赖、且希望隔离上下文（只回传结论、不污染主上下文）
	// 的通用任务。它是「没有更专门子代理时」的兜底选择。
	subAgentReg := subagent.NewRegistry()
	if err := subAgentReg.Register(subagent.SubAgentDefinition{
		Name:         "general-purpose",
		Description:  "通用子代理，处理需要兼顾探索与修改、复杂推理或多步依赖的任务。当任务边界清晰、可独立完成、且希望隔离上下文（仅回传最终结论而非冗长中间过程）时使用；在没有更专门的子代理可用时，它是默认兜底选择。继承父代理可用的全部工具与模型。",
		SystemPrompt: generalPurposeSystemPrompt,
		Source:       "builtin", // Tools/Model/MaxTurns 均留空：工具与模型继承父代理，轮数继承引擎默认
	}); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("注册内置子代理失败: %v", err)))
	}
	if err := subAgentReg.LoadFromDir(filepath.Join(workDir, ".harness9", "agents")); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("加载文件式子代理失败: %v", err)))
	}

	subAgentTracker := subagent.NewTaskTracker()
	subAgentRunner := subagent.NewRunner(subagent.RunnerConfig{
		BaseTools:          subAgentBaseTools,
		SharedHooks:        []hooks.ToolHook{dangerHook, offloadHook},
		SettingsPath:       settingsPath,
		SkillsIndex:        skillsIndex,
		WorkDir:            workDir,
		DefaultMaxTurns:    agentMaxTurns,
		ToolTimeout:        60 * time.Second,
		MaxConcurrentTools: 0,
		SandboxMgr:         sandboxMgr,
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

	taskTool := subagent.NewTaskTool(subAgentReg, subAgentRunner, subAgentTracker)
	if err := registry.Register(taskTool); err != nil {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("注册 task 工具失败: %v", err)))
	}
	// ---- Sub-Agent 接线结束 ----

	// Hook 执行顺序：PermissionHook（配置规则）→ DangerHook（内置模式）→ OffloadHook（大输出）
	hookReg := hooks.NewHookRegistry(registry, permHook, dangerHook, offloadHook)

	// SummarizationCompactor 使用同一 LLM 生成摘要，内置 TokenBudgetCompactor 作为错误回退。
	// 注入长期记忆 Extractor：压缩前从 head 消息提取持久事实（fail-open）。
	compactor := memory.NewSummarizationCompactor(llm, modelLimits.ContextTokens,
		memory.WithTodoInjector(todoStore),
		memory.WithMemoryExtractor(ltm.NewExtractor(llm, ltmStore)),
	)

	eng := engine.NewAgentEngine(llm, hookReg, workDir,
		engine.WithPromptBuilder(promptBuilder),
		engine.WithSession(sess),
		engine.WithCompactor(compactor),
		engine.WithContextWindow(modelLimits.ContextTokens),
		engine.WithTodoStore(todoStore),
		engine.WithMaxTurns(agentMaxTurns),
		engine.WithMemoryNudge(10, "如果本轮对话中出现了值得跨会话长期保留的信息（用户偏好、稳定的项目知识、关键决策、可复用技能），请调用 memory_write 工具记录；否则忽略此提示。"),
	)

	if term.IsTerminal(os.Stdin.Fd()) {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("harness9 TUI 启动 │ workDir=%s", workDir)))
		if err := RunTUI(ctx, eng, mgr, sess, skillsIndex, todoStore, subAgentTracker, subAgentReg, subAgentRunner, workDir, modelName, sandboxNotifyCh); err != nil {
			log.Fatal(logfmt.FormatMsg("main", fmt.Sprintf("TUI 退出: %v", err)))
		}
	} else {
		log.Print(logfmt.FormatMsg("main", fmt.Sprintf("harness9 CLI 启动 │ workDir=%s", workDir)))
		RunCLI(ctx, eng, skillsIndex)
	}
	log.Print(logfmt.FormatMsg("main", "harness9 正常退出"))
}
