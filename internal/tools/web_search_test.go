package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockDDGHTML 模拟 DuckDuckGo html.duckduckgo.com/html/ 响应格式。
const mockDDGHTML = `<!DOCTYPE html>
<html><body>
<div class="results">
<div class="result results_links results_links_deep web-result">
<div class="result__body">
<h2 class="result__title">
<a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fpage1&rut=abc">First Result Title</a>
</h2>
<a class="result__snippet" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fpage1&rut=abc">Snippet for result one.</a>
</div>
</div>
<div class="result results_links results_links_deep web-result">
<div class="result__body">
<h2 class="result__title">
<a class="result__a" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fpage2&rut=def">Second Result Title</a>
</h2>
<a class="result__snippet" href="/l/?uddg=https%3A%2F%2Fexample.com%2Fpage2&rut=def">Snippet for result two.</a>
</div>
</div>
</div>
</body></html>`

func TestParseDDGResults(t *testing.T) {
	results := parseDDGResults(mockDDGHTML, 10)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "First Result Title" {
		t.Errorf("first title = %q, want %q", results[0].Title, "First Result Title")
	}
	if results[0].URL != "https://example.com/page1" {
		t.Errorf("first URL = %q, want %q", results[0].URL, "https://example.com/page1")
	}
	if results[0].Snippet != "Snippet for result one." {
		t.Errorf("first snippet = %q, want %q", results[0].Snippet, "Snippet for result one.")
	}
}

func TestParseDDGResultsMaxResults(t *testing.T) {
	results := parseDDGResults(mockDDGHTML, 1)
	if len(results) != 1 {
		t.Errorf("expected 1 result with max_results=1, got %d", len(results))
	}
}

func TestDecodeUDDG(t *testing.T) {
	tests := []struct {
		href string
		want string
	}{
		{"/l/?uddg=https%3A%2F%2Fexample.com%2Fpath&rut=123", "https://example.com/path"},
		{"https://example.com/direct", "https://example.com/direct"},
		{"/l/?uddg=invalid%2Furl", "invalid/url"},
	}
	for _, tt := range tests {
		got := decodeUDDG(tt.href)
		if got != tt.want {
			t.Errorf("decodeUDDG(%q) = %q, want %q", tt.href, got, tt.want)
		}
	}
}

func TestWebSearchToolExecute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockDDGHTML))
	}))
	defer server.Close()

	tool := &WebSearchTool{backendURL: server.URL}
	args, _ := json.Marshal(map[string]interface{}{"query": "golang tutorial", "max_results": 5})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(result, "[1]") {
		t.Errorf("result should contain numbered results\ngot:\n%s", result)
	}
	if !strings.Contains(result, "First Result Title") {
		t.Errorf("result should contain first title\ngot:\n%s", result)
	}
	if !strings.Contains(result, "https://example.com/page1") {
		t.Errorf("result should contain first URL\ngot:\n%s", result)
	}
}

func TestWebSearchToolEmptyQuery(t *testing.T) {
	tool := NewWebSearchTool()
	args, _ := json.Marshal(map[string]interface{}{"query": ""})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Error") {
		t.Errorf("expected error for empty query, got: %s", result)
	}
}

func TestWebSearchToolEmptyResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body><div class='results'></div></body></html>"))
	}))
	defer server.Close()

	tool := &WebSearchTool{backendURL: server.URL}
	args, _ := json.Marshal(map[string]interface{}{"query": "xyzzy12345"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(result, "未找到") {
		t.Errorf("expected empty results message\ngot:\n%s", result)
	}
}
