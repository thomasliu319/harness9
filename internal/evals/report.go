package evals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SuiteReport 是一次 Suite 运行的聚合报告。
type SuiteReport struct {
	RunAt      time.Time                 `json:"run_at"`
	TotalCases int                       `json:"total_cases"`
	Passed     int                       `json:"passed"`
	Failed     int                       `json:"failed"`
	PassRate   float64                   `json:"pass_rate"`
	Categories map[string]*CategoryStats `json:"categories"`
	Results    []ResultSnapshot          `json:"results"`
}

// CategoryStats 是单个类别的统计信息。
type CategoryStats struct {
	Total    int     `json:"total"`
	Passed   int     `json:"passed"`
	PassRate float64 `json:"pass_rate"`
}

// ResultSnapshot 是单个 Case 结果的 JSON 序列化视图。
type ResultSnapshot struct {
	ID         string   `json:"id"`
	Category   string   `json:"category"`
	Passed     bool     `json:"passed"`
	TurnCount  int      `json:"turn_count"`
	ToolCalls  []string `json:"tool_calls"`
	Failures   []string `json:"failures,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	DurationMs int64    `json:"duration_ms"`
}

// BuildReport 从 Results 列表生成 SuiteReport。
func BuildReport(results []Result) SuiteReport {
	report := SuiteReport{
		RunAt:      time.Now(),
		TotalCases: len(results),
		Categories: make(map[string]*CategoryStats),
	}

	for _, r := range results {
		if r.Passed {
			report.Passed++
		} else {
			report.Failed++
		}

		cat := r.Case.Category
		if _, ok := report.Categories[cat]; !ok {
			report.Categories[cat] = &CategoryStats{}
		}
		report.Categories[cat].Total++
		if r.Passed {
			report.Categories[cat].Passed++
		}

		snap := ResultSnapshot{
			ID:         r.Case.ID,
			Category:   r.Case.Category,
			Passed:     r.Passed,
			TurnCount:  r.TurnCount,
			ToolCalls:  r.ToolCallsExecuted,
			DurationMs: r.Duration.Milliseconds(),
		}
		for _, f := range r.Failures {
			snap.Failures = append(snap.Failures, f.Error())
		}
		for _, w := range r.Warnings {
			snap.Warnings = append(snap.Warnings, w.Error())
		}
		report.Results = append(report.Results, snap)
	}

	if report.TotalCases > 0 {
		report.PassRate = float64(report.Passed) / float64(report.TotalCases)
	}
	for _, s := range report.Categories {
		if s.Total > 0 {
			s.PassRate = float64(s.Passed) / float64(s.Total)
		}
	}
	return report
}

// WriteJSON 将报告序列化为 JSON，写入 path（自动创建目录）。
func WriteJSON(report SuiteReport, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// WriteMarkdown 将报告渲染为 Markdown，写入 path（自动创建目录）。
func WriteMarkdown(report SuiteReport, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Eval Report — %s\n\n", report.RunAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "**总计**: %d cases | **通过**: %d | **失败**: %d | **通过率**: %.1f%%\n\n",
		report.TotalCases, report.Passed, report.Failed, report.PassRate*100)

	// 分类统计（按名称排序）
	cats := make([]string, 0, len(report.Categories))
	for k := range report.Categories {
		cats = append(cats, k)
	}
	sort.Strings(cats)

	fmt.Fprint(&b, "## 分类统计\n\n")
	fmt.Fprintln(&b, "| 类别 | 总数 | 通过 | 通过率 |")
	fmt.Fprintln(&b, "|------|------|------|--------|")
	for _, cat := range cats {
		s := report.Categories[cat]
		fmt.Fprintf(&b, "| %s | %d | %d | %.1f%% |\n", cat, s.Total, s.Passed, s.PassRate*100)
	}
	fmt.Fprintln(&b)

	// 详细结果
	fmt.Fprint(&b, "## 详细结果\n\n")
	for _, r := range report.Results {
		icon := "✅"
		if !r.Passed {
			icon = "❌"
		}
		fmt.Fprintf(&b, "### %s `%s`\n\n", icon, r.ID)
		fmt.Fprintf(&b, "- **轮次**: %d | **工具调用**: %v | **耗时**: %dms\n",
			r.TurnCount, r.ToolCalls, r.DurationMs)
		for _, f := range r.Failures {
			fmt.Fprintf(&b, "- ❌ %s\n", f)
		}
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "- ⚠️ %s\n", w)
		}
		fmt.Fprintln(&b)
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}
