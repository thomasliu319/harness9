package evals_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harness9/internal/evals"
)

func TestBuildReport_PassRate(t *testing.T) {
	results := []evals.Result{
		{Case: &evals.Case{ID: "a", Category: "cat1"}, Passed: true},
		{Case: &evals.Case{ID: "b", Category: "cat1"}, Passed: false},
		{Case: &evals.Case{ID: "c", Category: "cat2"}, Passed: true},
	}
	report := evals.BuildReport(results)
	if report.TotalCases != 3 {
		t.Errorf("TotalCases: want 3, got %d", report.TotalCases)
	}
	if report.Passed != 2 {
		t.Errorf("Passed: want 2, got %d", report.Passed)
	}
	want := 2.0 / 3.0
	if abs := report.PassRate - want; abs > 0.001 || abs < -0.001 {
		t.Errorf("PassRate: want %.4f, got %.4f", want, report.PassRate)
	}
	if _, ok := report.Categories["cat1"]; !ok {
		t.Error("expected category cat1")
	}
}

func TestWriteJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	report := evals.BuildReport([]evals.Result{
		{Case: &evals.Case{ID: "x", Category: "test"}, Passed: true},
	})
	if err := evals.WriteJSON(report, path); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"total_cases"`) {
		t.Error("JSON missing total_cases field")
	}
}

func TestWriteMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	report := evals.BuildReport([]evals.Result{
		{Case: &evals.Case{ID: "y", Category: "test"}, Passed: false,
			Failures: []*evals.Failure{{AssertionName: "test", Message: "failed"}}},
	})
	if err := evals.WriteMarkdown(report, path); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "Eval Report") {
		t.Error("Markdown missing header")
	}
}
