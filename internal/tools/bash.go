// 内置工具：Bash（Shell 命令执行工具）。
//
// 让 Agent 具备完整的命令行操作能力，是 harness9 "YOLO 哲学"（Trust-the-LLM）的核心：
// 不限制可执行命令的种类，把所有判断与决策权完全交给大模型。
//
// 注入 sandbox.Environment 后，命令通过 docker exec 在容器内执行（OS 级隔离）；
// 未注入时（env=nil）走原有本地进程路径，行为与引入 Sandbox 前完全一致。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/harness9/internal/sandbox"
	"github.com/harness9/internal/schema"
)

const maxOutputLen = 8000
const bashHardTimeout = 30 * time.Second

// BashTool 实现 BaseTool 接口，在 workDir 下执行任意 bash 命令。
type BashTool struct {
	workDir string
	env     sandbox.Environment // nil = 本地执行；非 nil = 路由进 Sandbox 容器
}

// BashOption 是 BashTool 的功能选项函数。
type BashOption func(*BashTool)

// WithEnvironment 注入 sandbox.Environment，命令将路由到容器内执行。
// env 为 nil 时无效（等同于不注入）。
func WithEnvironment(env sandbox.Environment) BashOption {
	return func(t *BashTool) { t.env = env }
}

// NewBashTool 创建绑定到指定工作目录的 Bash 工具实例。
func NewBashTool(workDir string, opts ...BashOption) *BashTool {
	t := &BashTool{workDir: workDir}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "在当前工作区执行任意的 bash 命令。支持链式命令(如 &&)。返回标准输出(stdout)和标准错误(stderr)的合并内容。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "要执行的 bash 命令，例如: ls -la 或 go test ./... 等等",
				},
			},
			"required": []string{"command"},
		},
	}
}

type bashArgs struct {
	Command string `json:"command"`
}

func (t *BashTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input bashArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if input.Command == "" {
		return "Error: 命令为空字符串", nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, bashHardTimeout)
	defer cancel()

	if t.env != nil {
		return t.runInSandbox(timeoutCtx, input.Command)
	}
	return t.runLocal(timeoutCtx, input.Command)
}

// runInSandbox 通过注入的 Environment 在容器内执行命令。
//
// 超时处理说明：DockerEnvironment.RunBash 内部将 docker exec 的失败（含 ctx 取消/超时）
// 转换为错误字符串返回（err==nil），因此不在此处检查 ctx.Err()，
// 避免"命令刚好在超时边界成功完成"时误加超时警告。
func (t *BashTool) runInSandbox(ctx context.Context, cmd string) (string, error) {
	out, err := t.env.RunBash(ctx, cmd, t.workDir)
	if err != nil {
		return fmt.Sprintf("执行报错: %v", err), nil
	}
	if out == "" {
		return "命令执行成功，无终端输出。", nil
	}
	if len(out) > maxOutputLen {
		return fmt.Sprintf("%s\n\n...[终端输出过长，已截断至前 %d 字节]...", out[:maxOutputLen], maxOutputLen), nil
	}
	return out, nil
}

// runLocal 在本地进程中执行命令（Sandbox 关闭时的原有路径）。
func (t *BashTool) runLocal(ctx context.Context, cmd string) (string, error) {
	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	c.Dir = t.workDir
	out, err := c.CombinedOutput()
	outputStr := string(out)

	if ctx.Err() == context.DeadlineExceeded {
		// 先截断，再追加警告，避免警告被截断掉
		if len(outputStr) > maxOutputLen {
			outputStr = outputStr[:maxOutputLen]
		}
		return outputStr + fmt.Sprintf("\n[警告: 命令执行超时(%s)，已被系统强制终止。]", bashHardTimeout), nil
	}
	if err != nil {
		return fmt.Sprintf("执行报错: %v\n输出:\n%s", err, outputStr), nil
	}
	if outputStr == "" {
		return "命令执行成功，无终端输出。", nil
	}
	if len(outputStr) > maxOutputLen {
		return fmt.Sprintf("%s\n\n...[终端输出过长，已截断至前 %d 字节]...", outputStr[:maxOutputLen], maxOutputLen), nil
	}
	return outputStr, nil
}
