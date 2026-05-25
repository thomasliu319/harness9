package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/harness9/internal/engine"
	"github.com/harness9/internal/schema"
)

// newTestModel 返回适合单元测试的 tuiModel（nil engine/skills，固定尺寸）。
func newTestModel() tuiModel {
	m := newTUIModel(nil, nil, nil, nil, nil, context.Background(), "/tmp/test", "test-model")
	m.width = 80
	m.height = 24
	return m
}

// applyUpdate 调用 m.Update(msg)，返回更新后的 tuiModel。
// 丢弃返回的 tea.Cmd（单元测试中不执行 Cmd）。
func applyUpdate(m tuiModel, msg tea.Msg) tuiModel {
	updated, _ := m.Update(msg)
	return updated.(tuiModel)
}

func TestEventActionDelta_AppendsToLastLine(t *testing.T) {
	m := newTestModel()
	m.lines = []string{""}
	m.running = true

	m = applyUpdate(m, eventMsg{Type: engine.EventActionDelta, Data: "hello"})
	if got := m.lines[len(m.lines)-1]; got != "hello" {
		t.Errorf("first delta: got %q, want %q", got, "hello")
	}

	m = applyUpdate(m, eventMsg{Type: engine.EventActionDelta, Data: " world"})
	if got := m.lines[len(m.lines)-1]; got != "hello world" {
		t.Errorf("second delta: got %q, want %q", got, "hello world")
	}
}

func TestEventActionDelta_InitializesEmptyLines(t *testing.T) {
	m := newTestModel()
	m.lines = nil
	m.running = true

	m = applyUpdate(m, eventMsg{Type: engine.EventActionDelta, Data: "hi"})
	if len(m.lines) == 0 {
		t.Fatal("lines should not be empty after delta on nil slice")
	}
	if got := m.lines[len(m.lines)-1]; got != "hi" {
		t.Errorf("got %q, want %q", got, "hi")
	}
}

func TestEventToolStart_SetsCurrentTool(t *testing.T) {
	m := newTestModel()
	m.running = true

	tc := schema.ToolCall{Name: "bash", ID: "1", Arguments: json.RawMessage(`{"command":"ls"}`)}
	m = applyUpdate(m, eventMsg{Type: engine.EventToolStart, Data: tc})

	if m.currentTool != "bash" {
		t.Errorf("currentTool = %q, want %q", m.currentTool, "bash")
	}
	if m.toolStart.IsZero() {
		t.Error("toolStart should be set when tool starts")
	}
	if m.toolArgs == nil {
		t.Error("toolArgs should be set on EventToolStart")
	}
}

func TestEventToolResult_ClearsCurrentToolAndAppendsLine(t *testing.T) {
	m := newTestModel()
	m.running = true
	m.currentTool = "bash"
	m.lines = []string{}

	data := engine.ToolResultData{Result: schema.ToolResult{Output: "ok", IsError: false}, Duration: 100 * time.Millisecond}
	m = applyUpdate(m, eventMsg{Type: engine.EventToolResult, Data: data})

	if m.currentTool != "" {
		t.Errorf("currentTool should be cleared, got %q", m.currentTool)
	}
	if len(m.lines) == 0 {
		t.Error("completion line should be appended to scrollback")
	}
	if !strings.Contains(m.lines[len(m.lines)-1], "bash") {
		t.Errorf("completion line should mention tool name, got %q", m.lines[len(m.lines)-1])
	}
}

func TestEventToolResult_ErrorMark(t *testing.T) {
	m := newTestModel()
	m.running = true
	m.currentTool = "bash"
	m.lines = []string{}

	data := engine.ToolResultData{Result: schema.ToolResult{Output: "failed", IsError: true}, Duration: 0}
	m = applyUpdate(m, eventMsg{Type: engine.EventToolResult, Data: data})

	if len(m.lines) == 0 {
		t.Fatal("completion line should be appended")
	}
	if !strings.Contains(m.lines[len(m.lines)-1], "✗") {
		t.Errorf("error result should use ✗, got %q", m.lines[len(m.lines)-1])
	}
}

func TestEventDone_ResetsRunningState(t *testing.T) {
	m := newTestModel()
	m.running = true
	m.currentTool = "bash"
	m.lines = []string{""}
	var cancelled bool
	m.cancelFn = func() { cancelled = true }

	m = applyUpdate(m, eventMsg{Type: engine.EventDone})

	if m.running {
		t.Error("running should be false after EventDone")
	}
	if m.currentTool != "" {
		t.Errorf("currentTool should be cleared, got %q", m.currentTool)
	}
	if !cancelled {
		t.Error("EventDone should call cancelFn to release context")
	}
}

func TestEventError_AppendsToScrollbackAndResetsRunning(t *testing.T) {
	m := newTestModel()
	m.running = true
	m.currentTool = "bash"
	m.lines = []string{"partial text"}
	m.pendingReplyStart = 0

	m = applyUpdate(m, eventMsg{Type: engine.EventError, Data: "context cancelled"})

	if m.running {
		t.Error("running should be false after EventError")
	}
	if m.currentTool != "" {
		t.Errorf("currentTool should be cleared, got %q", m.currentTool)
	}
	if len(m.lines) == 0 {
		t.Fatal("error line should be appended to scrollback")
	}
	if !strings.Contains(m.lines[len(m.lines)-1], "context cancelled") {
		t.Errorf("last line should contain error message, got %q", m.lines[len(m.lines)-1])
	}
}

func TestWindowSizeMsg_UpdatesDimensions(t *testing.T) {
	m := newTestModel()

	m = applyUpdate(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	if m.width != 120 || m.height != 40 {
		t.Errorf("got %dx%d, want 120x40", m.width, m.height)
	}
}

func TestKeyCtrlC_WhenIdle_ReturnsQuitCmd(t *testing.T) {
	m := newTestModel()
	m.running = false

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected a non-nil quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg, got %T", msg)
	}
}

func TestKeyCtrlC_WhenRunning_CallsCancelFn(t *testing.T) {
	m := newTestModel()
	m.running = true
	var cancelled bool
	m.cancelFn = func() { cancelled = true }

	m = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlC})

	if !cancelled {
		t.Error("cancelFn should be called when Ctrl-C during agent run")
	}
	if !m.running {
		// running stays true until EventDone/EventError arrives from engine
		t.Error("running should remain true until engine confirms cancellation")
	}
}

func TestKeyEnter_EmptyInput_Ignored(t *testing.T) {
	m := newTestModel()
	m.running = false
	initialLines := len(m.lines)

	m = applyUpdate(m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(m.lines) != initialLines {
		t.Error("empty Enter should not append to scrollback")
	}
}

func TestKeyEnter_WhenRunning_Ignored(t *testing.T) {
	m := newTestModel()
	m.running = true
	m.input.SetValue("do something")
	initialLines := len(m.lines)

	m = applyUpdate(m, tea.KeyMsg{Type: tea.KeyEnter})

	if len(m.lines) != initialLines {
		t.Error("Enter while agent is running should be ignored")
	}
}

func TestKeyPgUp_EntersManualScroll(t *testing.T) {
	m := newTestModel()
	// 填充足够多的行触发滚动
	for i := 0; i < 30; i++ {
		m.lines = append(m.lines, "line")
	}

	m = applyUpdate(m, tea.KeyMsg{Type: tea.KeyPgUp})

	if m.viewTop < 0 {
		t.Error("PgUp should enter manual scroll mode (viewTop >= 0)")
	}
}

func TestMouseWheelUp_ScrollsUp(t *testing.T) {
	m := newTestModel()
	for i := 0; i < 30; i++ {
		m.lines = append(m.lines, "line")
	}

	m = applyUpdate(m, tea.MouseMsg{
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})

	if m.viewTop < 0 {
		t.Error("MouseWheelUp should enter manual scroll mode (viewTop >= 0)")
	}
}

func TestMouseWheelDown_AtBottom_NoChange(t *testing.T) {
	m := newTestModel()
	// viewTop=-1（底部），向下滚动不应改变状态
	m = applyUpdate(m, tea.MouseMsg{
		Button: tea.MouseButtonWheelDown,
		Action: tea.MouseActionPress,
	})

	if m.viewTop != -1 {
		t.Errorf("WheelDown at bottom should keep viewTop=-1, got %d", m.viewTop)
	}
}

func TestKeyEnd_ReturnsToAutoScroll(t *testing.T) {
	m := newTestModel()
	m.viewTop = 5 // 已在手动滚动

	m = applyUpdate(m, tea.KeyMsg{Type: tea.KeyEnd})

	if m.viewTop != -1 {
		t.Errorf("End should reset to auto-scroll (viewTop=-1), got %d", m.viewTop)
	}
}

func TestKeyPgDown_AtBottom_StaysAutoScroll(t *testing.T) {
	m := newTestModel()
	// viewTop=-1 时按 PgDn 不应改变状态
	m = applyUpdate(m, tea.KeyMsg{Type: tea.KeyPgDown})

	if m.viewTop != -1 {
		t.Errorf("PgDn at auto-scroll bottom should keep viewTop=-1, got %d", m.viewTop)
	}
}

func TestSummarizeTool_Bash(t *testing.T) {
	args := json.RawMessage(`{"command":"go test ./... 2>&1 | head -20"}`)
	got := summarizeTool("bash", args)
	if got != "go test ./... 2>&1 | head -20" {
		t.Errorf("got %q", got)
	}
}

func TestSummarizeTool_Bash_Truncates(t *testing.T) {
	long := strings.Repeat("x", 130)
	args := json.RawMessage(`{"command":"` + long + `"}`)
	got := summarizeTool("bash", args)
	if len([]rune(got)) != 121 { // 120 chars + "…"
		t.Errorf("expected 121 runes (120 + ellipsis), got %d: %q", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
}

func TestSummarizeTool_ReadFile(t *testing.T) {
	args := json.RawMessage(`{"path":"/home/user/project/main.go"}`)
	got := summarizeTool("read_file", args)
	if got != "main.go" {
		t.Errorf("got %q, want %q", got, "main.go")
	}
}

func TestSummarizeTool_Other(t *testing.T) {
	args := json.RawMessage(`{"key":"value"}`)
	got := summarizeTool("custom_tool", args)
	if got != `{"key":"value"}` {
		t.Errorf("got %q", got)
	}
}

func TestSummarizeTool_WriteFile(t *testing.T) {
	args := json.RawMessage(`{"path":"/home/user/project/utils.go","content":"package main"}`)
	got := summarizeTool("write_file", args)
	if got != "utils.go" {
		t.Errorf("got %q, want %q", got, "utils.go")
	}
}

func TestSummarizeTool_InvalidArgs(t *testing.T) {
	args := json.RawMessage(`not-json`)
	got := summarizeTool("bash", args)
	if got != "" {
		t.Errorf("invalid args should return empty string, got %q", got)
	}
}

func TestPhaseTransition_WelcomeToChat(t *testing.T) {
	m := newTestModel()
	if m.phase != phaseWelcome {
		t.Fatal("new model should start in phaseWelcome")
	}
	m.input.SetValue("hello world")

	m = applyUpdate(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.phase != phaseChat {
		t.Errorf("phase should be phaseChat after first Enter, got %v", m.phase)
	}
}

func TestPhaseStaysChat_AfterFirstEnter(t *testing.T) {
	m := newTestModel()
	m.phase = phaseChat
	m.input.SetValue("second message")

	m = applyUpdate(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.phase != phaseChat {
		t.Errorf("phase should remain phaseChat, got %v", m.phase)
	}
}

func TestSpinnerVerbRotation(t *testing.T) {
	m := newTestModel()
	m.running = true
	m.currentTool = "bash"
	m.verbIdx = 0
	m.tickCount = 29

	m = applyUpdate(m, spinner.TickMsg{})

	if m.tickCount != 30 {
		t.Errorf("tickCount should be 30, got %d", m.tickCount)
	}
	if m.verbIdx != 1 {
		t.Errorf("verbIdx should advance to 1 after 30 ticks, got %d", m.verbIdx)
	}

	// verify wraparound
	m.verbIdx = 5
	m.tickCount = 59
	m = applyUpdate(m, spinner.TickMsg{})
	if m.verbIdx != 0 {
		t.Errorf("verbIdx should wrap to 0, got %d", m.verbIdx)
	}
}

func TestScrollHeight_DynamicReservedLines(t *testing.T) {
	m := newTestModel() // height = 24
	if got := m.scrollHeight(); got != 21 {
		t.Errorf("idle: want 21 (24-3), got %d", got)
	}
	m.running = true
	m.currentTool = "bash"
	if got := m.scrollHeight(); got != 20 {
		t.Errorf("running with tool: want 20 (24-4), got %d", got)
	}
}

func TestEventThinkingDelta_CreatesThinkingBlock(t *testing.T) {
	m := newTestModel()
	m.running = true

	m = applyUpdate(m, eventMsg{Type: engine.EventThinkingDelta, Data: "step one"})

	// 应该有 « thinking » 标题行
	var hasHeader bool
	for _, line := range m.lines {
		if strings.Contains(line, "thinking") {
			hasHeader = true
			break
		}
	}
	if !hasHeader {
		t.Error("lines should contain thinking header after first EventThinkingDelta")
	}
	if m.thinkingLineStart == -1 {
		t.Error("thinkingLineStart should be set after first EventThinkingDelta")
	}
	if m.pendingThinking != "step one" {
		t.Errorf("pendingThinking = %q, want %q", m.pendingThinking, "step one")
	}
}

func TestEventThinkingDelta_AccumulatesContent(t *testing.T) {
	m := newTestModel()
	m.running = true

	m = applyUpdate(m, eventMsg{Type: engine.EventThinkingDelta, Data: "part one"})
	m = applyUpdate(m, eventMsg{Type: engine.EventThinkingDelta, Data: " part two"})

	if m.pendingThinking != "part one part two" {
		t.Errorf("pendingThinking = %q, want %q", m.pendingThinking, "part one part two")
	}
}

func TestEventActionDelta_FlushesThinkingBlock(t *testing.T) {
	m := newTestModel()
	m.running = true

	// 先发 thinking delta
	m = applyUpdate(m, eventMsg{Type: engine.EventThinkingDelta, Data: "reason"})
	if m.thinkingLineStart == -1 {
		t.Fatal("setup: thinkingLineStart should be set")
	}

	// 再发 action delta，应 flush thinking 块
	m = applyUpdate(m, eventMsg{Type: engine.EventActionDelta, Data: "answer"})

	if m.thinkingLineStart != -1 {
		t.Error("thinkingLineStart should be reset to -1 after action delta flushes thinking")
	}
	if m.pendingThinking != "" {
		t.Errorf("pendingThinking should be empty after flush, got %q", m.pendingThinking)
	}
	// action 文本应该在 lines 中
	if m.pendingReply != "answer" {
		t.Errorf("pendingReply = %q, want %q", m.pendingReply, "answer")
	}
}

func TestEventToolStart_FlushesThinkingBlock(t *testing.T) {
	m := newTestModel()
	m.running = true

	m = applyUpdate(m, eventMsg{Type: engine.EventThinkingDelta, Data: "reason"})
	if m.thinkingLineStart == -1 {
		t.Fatal("setup: thinkingLineStart should be set")
	}

	tc := schema.ToolCall{Name: "bash", ID: "1", Arguments: json.RawMessage(`{}`)}
	m = applyUpdate(m, eventMsg{Type: engine.EventToolStart, Data: tc})

	if m.thinkingLineStart != -1 {
		t.Error("thinkingLineStart should be reset after EventToolStart flushes thinking")
	}
	if m.pendingThinking != "" {
		t.Errorf("pendingThinking should be empty after flush, got %q", m.pendingThinking)
	}
}

func TestEventDone_FlushesActiveThinkingBlock(t *testing.T) {
	m := newTestModel()
	m.running = true
	m.cancelFn = func() {}

	m = applyUpdate(m, eventMsg{Type: engine.EventThinkingDelta, Data: "reason"})
	if m.thinkingLineStart == -1 {
		t.Fatal("setup: thinkingLineStart should be set")
	}

	m = applyUpdate(m, eventMsg{Type: engine.EventDone})

	if m.thinkingLineStart != -1 {
		t.Error("EventDone should flush thinking block (thinkingLineStart reset to -1)")
	}
	if m.pendingThinking != "" {
		t.Errorf("pendingThinking should be empty after EventDone flush, got %q", m.pendingThinking)
	}
}

func TestThinkingWordWrap_ShortText(t *testing.T) {
	lines := thinkingWordWrap("hello world", 80)
	if len(lines) != 1 || lines[0] != "hello world" {
		t.Errorf("short text should stay on one line, got %v", lines)
	}
}

func TestThinkingWordWrap_LongText(t *testing.T) {
	// 40 chars wide: "one two three four" each word ~5 chars
	text := "alpha beta gamma delta epsilon zeta eta theta"
	lines := thinkingWordWrap(text, 20)
	if len(lines) < 2 {
		t.Errorf("long text should wrap into multiple lines, got %v", lines)
	}
	for _, line := range lines {
		if len([]rune(line)) > 20 {
			t.Errorf("line exceeds width: %q (%d runes)", line, len([]rune(line)))
		}
	}
}

func TestThinkingWordWrap_ZeroWidth(t *testing.T) {
	text := "no wrapping when width is zero"
	lines := thinkingWordWrap(text, 0)
	if len(lines) != 1 || lines[0] != text {
		t.Errorf("zero width should not wrap, got %v", lines)
	}
}

func TestRenderThinkingLines_WrapsAtWidth(t *testing.T) {
	// width=40: prefix "  │ " = 4 cols, so wrap at 35 runes
	longText := "I need to create a temporary directory for a new Go web project with no external dependencies"
	lines := renderThinkingLines(longText, 40)
	if len(lines) < 2 {
		t.Errorf("expected wrapping into multiple lines at width=40, got %d line(s): %v", len(lines), lines)
	}
}

// TestEventError_ResetsThinkingBlock 验证 EventError 在 thinking 块活跃时，
// 正确截断 lines（移除从 thinkingLineStart 起的所有行）并重置 thinkingLineStart。
func TestEventError_ResetsThinkingBlock(t *testing.T) {
	m := newTestModel()
	m.running = true
	m.cancelFn = func() {}

	// 先触发 thinking 块
	m = applyUpdate(m, eventMsg{Type: engine.EventThinkingDelta, Data: "deep reason"})
	if m.thinkingLineStart == -1 {
		t.Fatal("setup: thinkingLineStart should be set after thinking delta")
	}
	linesWithThinking := len(m.lines)

	// 再触发错误
	m = applyUpdate(m, eventMsg{Type: engine.EventError, Data: "network error"})

	if m.thinkingLineStart != -1 {
		t.Error("EventError should reset thinkingLineStart to -1")
	}
	if m.pendingThinking != "" {
		t.Errorf("pendingThinking should be empty after EventError, got %q", m.pendingThinking)
	}
	// lines 应该已截断（移除了 thinking 块的所有行，并追加了错误行）
	if len(m.lines) >= linesWithThinking {
		t.Errorf("EventError should truncate thinking lines; linesWithThinking=%d, after=%d",
			linesWithThinking, len(m.lines))
	}
	if !strings.Contains(m.lines[len(m.lines)-1], "network error") {
		t.Errorf("last line should contain error message, got %q", m.lines[len(m.lines)-1])
	}
}

// TestEventThinkingDelta_NoBlankLineBeforeHeader 验证 thinking 块头部（« thinking »）
// 直接追加在 "◆ harness9:" 之后，不出现孤立的空行占位符。
func TestEventThinkingDelta_NoBlankLineBeforeHeader(t *testing.T) {
	m := newTestModel()
	// 模拟 dispatch 的初始状态：尾部有一个空行占位符，pendingReplyStart 指向它
	m.lines = []string{"◆ harness9:", ""}
	m.pendingReplyStart = 1 // 指向空行占位符
	m.thinkingLineStart = -1
	m.running = true

	m = applyUpdate(m, eventMsg{Type: engine.EventThinkingDelta, Data: "first thought"})

	// 检查 lines 中不存在连续的空行（""）紧跟 thinking 内容
	for i, line := range m.lines {
		if line == "" && i+1 < len(m.lines) && strings.Contains(m.lines[i+1], "thinking") {
			t.Errorf("blank line at index %d precedes thinking header at index %d", i, i+1)
		}
	}
	// thinking 标题行必须存在
	var hasHeader bool
	for _, line := range m.lines {
		if strings.Contains(line, "thinking") {
			hasHeader = true
			break
		}
	}
	if !hasHeader {
		t.Error("thinking header should be present after first EventThinkingDelta")
	}
}

// TestThinkingWordWrap_FirstWordExceedsWidth 验证首词超长（如 URL）时被正确 hard-break。
func TestThinkingWordWrap_FirstWordExceedsWidth(t *testing.T) {
	url := "https://pkg.go.dev/github.com/charmbracelet/bubbletea#Model"
	lines := thinkingWordWrap(url, 20)
	for _, line := range lines {
		if len([]rune(line)) > 20 {
			t.Errorf("line exceeds width 20: %q (%d runes)", line, len([]rune(line)))
		}
	}
	if len(lines) < 2 {
		t.Errorf("long URL should be split into multiple lines, got %v", lines)
	}
}

// TestFlushPendingThinking_UpdatesPendingReplyStart 验证 flushPendingThinking 后
// pendingReplyStart 被更新到 thinking 结束行之后，后续 action 文本从正确位置写入。
func TestFlushPendingThinking_UpdatesPendingReplyStart(t *testing.T) {
	m := newTestModel()
	m.running = true

	// 模拟有 thinking 块的状态
	m = applyUpdate(m, eventMsg{Type: engine.EventThinkingDelta, Data: "reason"})
	linesBeforeFlush := len(m.lines)
	if linesBeforeFlush == 0 {
		t.Fatal("setup: lines should not be empty after thinking delta")
	}

	// 发 action delta，触发 flushPendingThinking + 追加文本
	m = applyUpdate(m, eventMsg{Type: engine.EventActionDelta, Data: "answer"})

	// pendingReplyStart 应在结束行之后（至少 linesBeforeFlush+1，含结束分隔线）
	if m.pendingReplyStart <= linesBeforeFlush {
		t.Errorf("pendingReplyStart=%d should be > %d (lines before flush) after thinking flush",
			m.pendingReplyStart, linesBeforeFlush)
	}
	if m.pendingReply != "answer" {
		t.Errorf("pendingReply = %q, want %q", m.pendingReply, "answer")
	}
}
