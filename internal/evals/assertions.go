package evals

import (
	"fmt"
	"strings"
)

// Failure 描述单次断言失败的详情。
type Failure struct {
	AssertionName string
	Message       string
	// IsSoft=true 表示仅警告，不导致 Case 失败（效率断言）。
	IsSoft bool
}

func (f *Failure) Error() string {
	return fmt.Sprintf("[%s] %s", f.AssertionName, f.Message)
}

// Assertion 是评估断言的基接口。
// Check 返回 nil 表示通过，返回 *Failure 表示失败。
type Assertion interface {
	Check(result *Result) *Failure
	Name() string
}

// Result 保存单个 Case 的运行结果。完整定义在 harness.go 中；
// 此处定义使 assertions.go 可以独立编译。Task 9 实现 harness.go 时不再重复定义。
type Result struct {
	Case              *Case
	Passed            bool
	TurnCount         int
	ToolCallsExecuted []string
	FinalOutput       string
	RunError          error
	Failures          []*Failure
	Warnings          []*Failure
}

// Case 是评估用例。完整定义在 harness.go 中；
// 此处定义使 assertions.go 可以独立编译。Task 9 实现 harness.go 时不再重复定义。
type Case struct {
	ID         string
	Category   string
	Prompt     string
	Provider   *ScriptedProvider
	Assertions []Assertion
	MaxTurns   int
	WorkDir    string
}

// ToolCalledAssertion 断言指定工具被调用了至少 MinTimes 次（Hard）。
type ToolCalledAssertion struct {
	ToolName string
	MinTimes int // 0 或 1 均表示"至少一次"
}

func (a *ToolCalledAssertion) Name() string { return fmt.Sprintf("tool_called(%s)", a.ToolName) }

func (a *ToolCalledAssertion) Check(r *Result) *Failure {
	count := 0
	for _, call := range r.ToolCallsExecuted {
		if call == a.ToolName {
			count++
		}
	}
	min := a.MinTimes
	if min <= 0 {
		min = 1
	}
	if count < min {
		return &Failure{
			AssertionName: a.Name(),
			Message:       fmt.Sprintf("工具 %q 调用了 %d 次，期望 >= %d 次", a.ToolName, count, min),
		}
	}
	return nil
}

// ToolNotCalledAssertion 断言指定工具一次都没有被调用（Hard）。
type ToolNotCalledAssertion struct {
	ToolName string
}

func (a *ToolNotCalledAssertion) Name() string {
	return fmt.Sprintf("tool_not_called(%s)", a.ToolName)
}

func (a *ToolNotCalledAssertion) Check(r *Result) *Failure {
	for _, call := range r.ToolCallsExecuted {
		if call == a.ToolName {
			return &Failure{
				AssertionName: a.Name(),
				Message:       fmt.Sprintf("工具 %q 不应被调用，但实际调用了", a.ToolName),
			}
		}
	}
	return nil
}

// OutputContainsAssertion 断言最终文本输出包含期望字符串（Hard）。
type OutputContainsAssertion struct {
	Expected string
}

func (a *OutputContainsAssertion) Name() string {
	return fmt.Sprintf("output_contains(%q)", a.Expected)
}

func (a *OutputContainsAssertion) Check(r *Result) *Failure {
	if !strings.Contains(r.FinalOutput, a.Expected) {
		return &Failure{
			AssertionName: a.Name(),
			Message:       fmt.Sprintf("输出不包含 %q，实际输出: %q", a.Expected, truncate(r.FinalOutput, 200)),
		}
	}
	return nil
}

// OutputExcludesAssertion 断言最终文本输出不包含某字符串（Hard）。
type OutputExcludesAssertion struct {
	Forbidden string
}

func (a *OutputExcludesAssertion) Name() string {
	return fmt.Sprintf("output_excludes(%q)", a.Forbidden)
}

func (a *OutputExcludesAssertion) Check(r *Result) *Failure {
	if strings.Contains(r.FinalOutput, a.Forbidden) {
		return &Failure{
			AssertionName: a.Name(),
			Message:       fmt.Sprintf("输出不应包含 %q，但实际包含了", a.Forbidden),
		}
	}
	return nil
}

// NoErrorAssertion 断言 Case 执行时没有 RunError（Hard）。
type NoErrorAssertion struct{}

func (a *NoErrorAssertion) Name() string { return "no_run_error" }

func (a *NoErrorAssertion) Check(r *Result) *Failure {
	if r.RunError != nil {
		return &Failure{
			AssertionName: a.Name(),
			Message:       fmt.Sprintf("期望执行成功，但出错: %v", r.RunError),
		}
	}
	return nil
}

// ErrorAssertion 断言 Case 执行时发生了 RunError（Hard，用于测试错误路径）。
type ErrorAssertion struct{}

func (a *ErrorAssertion) Name() string { return "run_error" }

func (a *ErrorAssertion) Check(r *Result) *Failure {
	if r.RunError == nil {
		return &Failure{AssertionName: a.Name(), Message: "期望执行出错，但实际成功"}
	}
	return nil
}

// MaxTurnsAssertion 警告 Turn 数超过期望值（Soft，仅记警告，不影响 Passed）。
type MaxTurnsAssertion struct {
	Max int
}

func (a *MaxTurnsAssertion) Name() string { return fmt.Sprintf("max_turns(%d)", a.Max) }

func (a *MaxTurnsAssertion) Check(r *Result) *Failure {
	if r.TurnCount > a.Max {
		return &Failure{
			AssertionName: a.Name(),
			Message:       fmt.Sprintf("执行了 %d 轮，期望 <= %d 轮（效率警告）", r.TurnCount, a.Max),
			IsSoft:        true,
		}
	}
	return nil
}

// MaxToolCallsAssertion 警告工具调用次数超过期望值（Soft）。
type MaxToolCallsAssertion struct {
	Max int
}

func (a *MaxToolCallsAssertion) Name() string {
	return fmt.Sprintf("max_tool_calls(%d)", a.Max)
}

func (a *MaxToolCallsAssertion) Check(r *Result) *Failure {
	if len(r.ToolCallsExecuted) > a.Max {
		return &Failure{
			AssertionName: a.Name(),
			Message:       fmt.Sprintf("工具调用 %d 次，期望 <= %d 次（效率警告）", len(r.ToolCallsExecuted), a.Max),
			IsSoft:        true,
		}
	}
	return nil
}

// truncate 截断字符串，超出 maxLen 时追加 "..."。
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
