package hooks

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/harness9/internal/schema"
)

// dangerPattern 定义一个危险命令模式。
type dangerPattern struct {
	substr    string
	riskLevel string
	reason    string
}

var defaultDangerPatterns = []dangerPattern{
	// 高风险：不可逆的破坏性操作
	{substr: "rm -rf", riskLevel: "high", reason: "强制递归删除文件/目录"},
	{substr: "rm -r /", riskLevel: "high", reason: "强制递归删除根目录"},
	{substr: "| bash", riskLevel: "high", reason: "管道执行远程脚本（curl|bash 攻击）"},
	{substr: "|bash", riskLevel: "high", reason: "管道执行远程脚本（无空格变体）"},
	{substr: "| sh", riskLevel: "high", reason: "管道执行远程脚本"},
	{substr: "|sh", riskLevel: "high", reason: "管道执行远程脚本（无空格变体）"},
	{substr: ":(){ :|:", riskLevel: "high", reason: "Fork Bomb"},
	{substr: "dd if=", riskLevel: "high", reason: "直接写入块设备（可能覆盖磁盘）"},
	{substr: "> /dev/", riskLevel: "high", reason: "写入设备文件"},
	{substr: "chmod -r 777", riskLevel: "high", reason: "递归赋予所有人全部权限"},
	{substr: "chown -r", riskLevel: "high", reason: "递归修改文件所有者"},
	// 中风险
	{substr: "sudo ", riskLevel: "medium", reason: "以 root 权限执行命令"},
	{substr: "chmod 777 ", riskLevel: "medium", reason: "赋予所有人全部权限"},
	{substr: "chmod +x ", riskLevel: "medium", reason: "添加可执行权限"},
	{substr: "pkill ", riskLevel: "medium", reason: "按名称杀死进程"},
	{substr: "kill -9 ", riskLevel: "medium", reason: "强制杀死进程"},
	{substr: "killall ", riskLevel: "medium", reason: "杀死所有同名进程"},
	{substr: "iptables ", riskLevel: "medium", reason: "修改防火墙规则"},
	{substr: "systemctl ", riskLevel: "medium", reason: "管理系统服务"},
}

// DangerHook 在工具执行前检查 bash 命令是否包含已知高危模式。
type DangerHook struct {
	patterns []dangerPattern
}

// NewDangerHook 创建使用默认高危模式列表的 DangerHook。
func NewDangerHook() *DangerHook {
	return &DangerHook{patterns: defaultDangerPatterns}
}

// BeforeExecute 检查 bash 工具的命令参数，匹配到高危模式时返回 Ask。
func (h *DangerHook) BeforeExecute(ctx context.Context, tc schema.ToolCall) (context.Context, HookDecision, error) {
	if tc.Name != "bash" {
		return ctx, Allow(), nil
	}

	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(tc.Arguments, &args); err != nil || args.Command == "" {
		return ctx, Allow(), nil
	}

	cmd := strings.ToLower(args.Command)
	for _, p := range h.patterns {
		if strings.Contains(cmd, strings.ToLower(p.substr)) {
			return ctx, Ask(p.reason, p.riskLevel), nil
		}
	}
	return ctx, Allow(), nil
}

// AfterExecute 对 DangerHook 是空操作，原样返回结果。
func (h *DangerHook) AfterExecute(_ context.Context, _ schema.ToolCall, result schema.ToolResult) schema.ToolResult {
	return result
}
