package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/harness9/internal/engine"
	"github.com/harness9/internal/memory"
	"github.com/harness9/internal/planning"
)

// accentStyle 返回当前执行模式对应的强调色样式（accent style）。
//
// 优先级（高→低）：Shell 模式 > Plan Mode > Default。
// Shell 模式优先级高于 Plan Mode 的原因：用户激活 Shell 模式时需要即时视觉反馈，
// 即使当前是 Plan Mode 也应当清晰显示 Shell 模式的绿色主题。
//
//   - shellMode=true              → 亮绿色（shellModeAccentStyle, #83）
//   - PlanModePlan / PlanModeAutoEdit → 琥珀黄（planAccentStyle, #220）
//   - PlanModeDefault              → 青色（cyanStyle, #81）
//
// 将颜色切换逻辑集中于此一处，renderStatusBar / renderFooter / renderTodoLines
// 统一调用，View 层无散落的 if 判断，模式颜色映射易于维护和扩展。
func (m tuiModel) accentStyle() lipgloss.Style {
	if m.shellMode {
		return shellModeAccentStyle
	}
	if m.planMode != planning.PlanModeDefault {
		return planAccentStyle
	}
	return cyanStyle
}

// activeStatusBarStyle 返回当前模式下的状态栏容器背景样式。
//
// 三种背景色通过不同色调给用户强烈的视觉区分信号：
//   - shellMode=true      → 深绿底（shellStatusBarStyle, bg #22 / fg #120）
//   - Plan/AutoEdit 模式  → 深橙底（planStatusBarStyle,  bg #94 / fg #220）
//   - Default 模式        → 深灰底（statusBarStyle,       bg #235 / fg #11）
//
// 优先级规则与 accentStyle() 保持一致。
func (m tuiModel) activeStatusBarStyle() lipgloss.Style {
	if m.shellMode {
		return shellStatusBarStyle
	}
	if m.planMode != planning.PlanModeDefault {
		return planStatusBarStyle
	}
	return statusBarStyle
}

// shortPath 将绝对路径中的 $HOME 替换为 "~"。
func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return strings.Replace(p, home, "~", 1)
}

// renderTodoLines 将 TodoItem 列表渲染为结构化多行文本，追加到 Scrollback（m.lines）。
//
// 输出格式：标题行（图标 + "Tasks" + 进度统计 + 活跃任务数）+ 分隔线 + 各任务行。
// 每个任务行包含：序号、状态图标（✔/▶/○/⊘）和内容文本。
//
// 状态图标映射：
//   - in_progress → ▶（黄色，工具运行色）
//   - completed   → ✔（绿色）
//   - cancelled   → ⊘（灰色）
//   - pending     → ○（灰色）
//
// 颜色跟随当前 planMode 的 accentStyle（Plan Mode 下为琥珀色，其他为青色）。
func (m tuiModel) renderTodoLines(items []planning.TodoItem) []string {
	if len(items) == 0 {
		return nil
	}

	accent := m.accentStyle()

	// 统计完成数与进行中数
	var done, active int
	for _, item := range items {
		switch item.Status {
		case planning.TodoCompleted:
			done++
		case planning.TodoInProgress:
			active++
		}
	}

	lines := make([]string, 0, len(items)+3)

	// 标题行：图标 + "Tasks" + 进度 + 活跃数
	progress := accent.Render(fmt.Sprintf("%d/%d", done, len(items)))
	title := "  " + accent.Render("☰") + "  " +
		lipgloss.NewStyle().Bold(true).Render("Tasks") +
		dimStyle.Render("  ·  ") + progress
	if active > 0 {
		title += dimStyle.Render("  ·  ") + toolRunStyle.Render(fmt.Sprintf("%d active", active))
	}
	lines = append(lines, title)
	lines = append(lines, dimStyle.Render("  ──────────────────────────────────────"))

	// 各任务项
	for i, item := range items {
		num := dimStyle.Render(fmt.Sprintf("%2d.", i+1))
		var icon, content string
		switch item.Status {
		case planning.TodoInProgress:
			icon = toolRunStyle.Render("▶")
			content = toolRunStyle.Render(item.Content)
		case planning.TodoCompleted:
			icon = toolOKStyle.Render("✔")
			content = dimStyle.Render(item.Content)
		case planning.TodoCancelled:
			icon = dimStyle.Render("⊘")
			content = dimStyle.Render(item.Content)
		default: // pending
			icon = dimStyle.Render("○")
			content = item.Content
		}
		lines = append(lines, "  "+num+"  "+icon+"  "+content)
	}
	return lines
}

// renderConversation 渲染对话历史区（Scrollback）。
// scrollH 为可显示行数（由 scrollHeight() 计算）。
func (m tuiModel) renderConversation(scrollH int) string {
	var scrollLines []string
	if m.viewTop < 0 || len(m.lines) <= scrollH {
		if len(m.lines) >= scrollH {
			scrollLines = m.lines[len(m.lines)-scrollH:]
		} else {
			pad := make([]string, scrollH-len(m.lines))
			scrollLines = append(pad, m.lines...)
		}
	} else {
		start := m.viewTop
		end := start + scrollH
		if end > len(m.lines) {
			end = len(m.lines)
		}
		scrollLines = m.lines[start:end]
		if len(scrollLines) < scrollH {
			pad := make([]string, scrollH-len(scrollLines))
			scrollLines = append(pad, scrollLines...)
		}
	}
	return strings.Join(scrollLines, "\n")
}

// renderToolProgress 渲染工具执行进度行。
// 仅在 phaseChat && running && currentTool != "" 时调用。
func (m tuiModel) renderToolProgress() string {
	verb := spinnerVerbs[m.verbIdx]
	elapsed := time.Since(m.toolStart).Round(time.Millisecond)
	summary := summarizeTool(m.currentTool, m.toolArgs)

	var toolDisplay string
	if summary != "" {
		toolDisplay = fmt.Sprintf("%s(%s)", m.currentTool, summary)
	} else {
		toolDisplay = m.currentTool
	}

	return "  " +
		verbRunStyle.Render(m.spinner.View()+" "+verb+"...") +
		toolRunStyle.Render("  "+toolDisplay) +
		dimStyle.Render(fmt.Sprintf("  [%s]", elapsed))
}

// renderStatusBar 渲染常驻状态栏（model 名 + mode + workdir + session 信息）。
// 宽度充足时展示完整 session ID；窄终端（< 120 列）时截断为前 8 位加 "…"。
func (m tuiModel) renderStatusBar() string {
	accent := m.accentStyle()

	sessionInfo := ""
	if m.sessionID != "" {
		sid := m.sessionID
		if m.width < 120 && len(sid) > 8 {
			sid = sid[:8] + "…"
		}
		sessionInfo = dimStyle.Render("  │  session: ") + accent.Render(sid)

		if m.contextTokens > 0 {
			var tokenStr string
			if m.contextWindow > 0 {
				pct := m.contextTokens * 100 / m.contextWindow
				var tokenStyle lipgloss.Style
				switch {
				case pct >= 80:
					tokenStyle = tokenHighStyle
				case pct >= 50:
					tokenStyle = tokenWarnStyle
				default:
					tokenStyle = tokenOKStyle
				}
				tokenStr = tokenStyle.Render(
					memory.FormatTokenCount(m.contextTokens)+"/"+memory.FormatTokenCount(m.contextWindow),
				) + dimStyle.Render(fmt.Sprintf(" (%d%%)", pct))
			} else {
				tokenStr = accent.Render(memory.FormatTokenCount(m.contextTokens))
			}
			sessionInfo += dimStyle.Render("  ctx: ") + tokenStr
		}
	}
	// modePart 在状态栏中显示当前模式标签：
	//   Shell 模式  → "│ SHELL"（亮绿加粗），优先于 Plan 模式标签展示
	//   Plan/Auto   → "│ [PLAN]"（琥珀 Color "208"）
	//   Default     → 空字符串，不占用状态栏空间
	modeLabel := m.planMode.Label()
	var modePart string
	if m.shellMode {
		modePart = dimStyle.Render("  │  ") + shellModeLabelInBarStyle.Render("SHELL")
	} else if modeLabel != "" {
		modePart = dimStyle.Render("  │  ") + planModeLabelStyle.Render(modeLabel)
	}
	if m.permMode != engine.PermissionModeDefault {
		modePart += dimStyle.Render("  │  ") + approvalTitleMedStyle.Render(m.permMode.String())
	}

	var tasksPart string
	if m.todoStore != nil {
		items := m.todoStore.Read()
		if len(items) > 0 {
			var completed int
			for _, item := range items {
				if item.Status == planning.TodoCompleted {
					completed++
				}
			}
			tasksPart = dimStyle.Render("  │  ") + accent.Render(fmt.Sprintf("%d/%d tasks", completed, len(items)))
		}
	}

	content := dimStyle.Render("  model: ") +
		accent.Render(m.modelName) +
		modePart +
		tasksPart +
		dimStyle.Render("  │  ") +
		accent.Render(shortPath(m.workDir)) +
		sessionInfo
	return m.activeStatusBarStyle().Width(m.width).Render(content)
}

// renderPlanReviewDialog 渲染 Plan Mode 完成后的审查选择对话框（带圆角边框）。
// 对话框在 planReviewing == true 时由 View() 插入到 StatusBar 之前，
// ↑↓ 移动光标，Enter 确认，Esc 取消。
func (m tuiModel) renderPlanReviewDialog() string {
	options := []string{
		"批准并自动执行",
		"批准并逐步确认编辑",
		"继续修改计划（保持 Plan Mode）",
		"取消",
	}

	var sb strings.Builder
	sb.WriteString(planModeLabelStyle.Render("Plan Mode 完成 — 选择下一步操作"))
	sb.WriteString("\n\n")
	for i, opt := range options {
		if i == m.planReviewCursor {
			sb.WriteString(planAccentStyle.Render("▶ ") + planReviewSelectedStyle.Render(opt))
		} else {
			sb.WriteString("  " + dimStyle.Render(opt))
		}
		if i < len(options)-1 {
			sb.WriteByte('\n')
		}
	}
	sb.WriteString("\n\n")
	sb.WriteString(dimStyle.Render("↑↓ 移动  Enter 确认  Esc 取消"))

	return planReviewBoxStyle.Render(sb.String())
}

// renderInput 渲染底部输入行，根据当前模式切换提示符样式。
//
//   - Shell 模式：" [SHELL]  $ <textinput>"（深橄榄徽章 + 绿色 $ 提示符）
//   - Plan Mode：  "  › <textinput>"（琥珀黄 › 提示符）
//   - Default 模式："  › <textinput>"（普通灰色 › 提示符）
//
// Shell 模式下的徽章和 $ 提示符使用预计算的包级样式变量，避免每帧创建新样式对象。
func (m tuiModel) renderInput() string {
	if m.shellMode {
		badge := shellModeTagStyle.Render("SHELL")
		prompt := shellModePromptStyle.Render(" $ ")
		return " " + badge + prompt + m.input.View()
	}
	if m.planMode != planning.PlanModeDefault {
		return "  " + planAccentStyle.Render("›") + " " + m.input.View()
	}
	return "  › " + m.input.View()
}

// renderFooter 渲染底部快捷键提示行，提示内容按以下优先级选取：
//
//  1. Shell 模式：固定展示 enter/esc/ctrl+c 及"输出自动注入"说明
//  2. 补全提示（completionHint != ""）：Tab 补全候选列表
//  3. 手动滚动（viewTop ≥ 0）：含百分比的滚动位置提示
//  4. 默认：常规快捷键提示（enter / / / ↑↓ / ctrl+c）
//
// Shell 模式 footer 特意提示"输出自动注入 LLM 上下文"，
// 使用户意识到命令结果会被纳入下次 LLM 请求的上下文中。
func (m tuiModel) renderFooter() string {
	if m.shellMode {
		a := shellModeAccentStyle
		return "  " +
			a.Render("enter") + dimStyle.Render(" 执行  ") +
			a.Render("esc") + dimStyle.Render(" 取消  ") +
			dimStyle.Render("输出自动注入 LLM 上下文  ") +
			a.Render("ctrl+c") + dimStyle.Render(" 退出")
	}

	if m.completionHint != "" {
		return m.completionHint
	}

	accent := m.accentStyle()

	if m.viewTop >= 0 {
		scrollH := m.scrollHeight()
		maxTop := len(m.lines) - scrollH
		if maxTop < 1 {
			maxTop = 1
		}
		pct := m.viewTop * 100 / maxTop
		return "  " +
			accent.Render("enter") + dimStyle.Render(" 发送  ") +
			accent.Render("/") + dimStyle.Render(" 技能命令  ") +
			accent.Render("↑↓") + dimStyle.Render(" 滚动  ") +
			accent.Render("end") + dimStyle.Render(fmt.Sprintf(" 回底部 (%d%%)  ", pct)) +
			accent.Render("ctrl+c") + dimStyle.Render(" 退出")
	}

	return "  " +
		accent.Render("enter") + dimStyle.Render(" 发送  ") +
		accent.Render("/") + dimStyle.Render(" 技能命令  ") +
		accent.Render("↑↓") + dimStyle.Render(" 滚动  ") +
		accent.Render("ctrl+c") + dimStyle.Render(" 退出")
}

// renderApprovalDialog 渲染工具执行审批对话框。
// 在 approvalPending == true 时由 View() 插入 StatusBar 之前。
func (m tuiModel) renderApprovalDialog() string {
	if m.approvalRequest == nil {
		return ""
	}
	req := m.approvalRequest

	var titleStyle lipgloss.Style
	switch req.RiskLevel {
	case "high":
		titleStyle = approvalTitleHighStyle
	case "medium":
		titleStyle = approvalTitleMedStyle
	default:
		titleStyle = approvalTitleLowStyle
	}

	riskLabel := map[string]string{
		"high":   "高风险",
		"medium": "中等风险",
		"low":    "低风险",
	}[req.RiskLevel]
	if riskLabel == "" {
		riskLabel = "需确认"
	}

	var sb strings.Builder
	sb.WriteString(titleStyle.Render(fmt.Sprintf("⚠  工具审批请求 [%s]", riskLabel)))
	sb.WriteString("\n\n")
	sb.WriteString(dimStyle.Render("工具: ") + lipgloss.NewStyle().Bold(true).Render(req.ToolCall.Name))
	if req.Reason != "" {
		sb.WriteString("\n")
		sb.WriteString(dimStyle.Render("原因: ") + req.Reason)
	}
	sb.WriteString("\n\n")

	if m.approvalInputting {
		sb.WriteString(dimStyle.Render("请输入拒绝原因（Enter 提交，Esc 取消）：\n"))
		sb.WriteString("> " + m.approvalFeedback + "█")
	} else {
		options := []string{
			"允许（仅本次）",
			"允许（本会话不再提示）",
			"总是允许（写入白名单）",
			"拒绝",
			"拒绝并提供反馈...",
		}
		for i, opt := range options {
			if i == m.approvalCursor {
				sb.WriteString(approvalSelectedStyle.Render("▶ ") +
					approvalSelectedStyle.Render(fmt.Sprintf("[%d] %s", i+1, opt)))
			} else {
				sb.WriteString(dimStyle.Render(fmt.Sprintf("  [%d] %s", i+1, opt)))
			}
			if i < len(options)-1 {
				sb.WriteByte('\n')
			}
		}
		sb.WriteString("\n\n")
		sb.WriteString(dimStyle.Render("↑↓ 移动  Enter/1-5 确认  Esc 拒绝"))
	}

	return approvalBoxStyle.Render(sb.String())
}

// View 实现 tea.Model——根据当前 phase 渲染完整 TUI 帧。
func (m tuiModel) View() string {
	if m.width == 0 {
		return ""
	}

	var sb strings.Builder

	if m.phase == phaseWelcome {
		sb.WriteString(bannerContent(m.width))
		sb.WriteByte('\n')
		sb.WriteString(m.renderStatusBar())
		sb.WriteByte('\n')
		sb.WriteString(m.renderInput())
		sb.WriteByte('\n')
		sb.WriteString(m.renderFooter())
	} else {
		scrollH := m.scrollHeight()
		sb.WriteString(m.renderConversation(scrollH))
		sb.WriteByte('\n')
		if m.running && m.currentTool != "" {
			sb.WriteString(m.renderToolProgress())
			sb.WriteByte('\n')
		}
		if m.approvalPending {
			sb.WriteString(m.renderApprovalDialog())
			sb.WriteByte('\n')
			sb.WriteString(m.renderStatusBar())
			return sb.String()
		}
		if m.planReviewing {
			sb.WriteString(m.renderPlanReviewDialog())
			sb.WriteByte('\n')
			sb.WriteString(m.renderStatusBar())
			return sb.String()
		}
		sb.WriteString(m.renderStatusBar())
		sb.WriteByte('\n')
		sb.WriteString(m.renderInput())
		sb.WriteByte('\n')
		sb.WriteString(m.renderFooter())
	}

	return sb.String()
}
