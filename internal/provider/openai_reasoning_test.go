package provider

import (
	"testing"
)

func TestWithIncludeReasoning_SetsField(t *testing.T) {
	p := &OpenAIProvider{}
	WithIncludeReasoning()(p)
	if !p.includeReasoning {
		t.Error("WithIncludeReasoning should set includeReasoning=true")
	}
}

func TestWithIncludeReasoning_AutoDetectOpenRouter(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", "https://openrouter.ai/api/v1")
	p, err := NewOpenAIProvider("gpt-4o")
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	if !p.includeReasoning {
		t.Error("OpenRouter base URL should auto-enable includeReasoning")
	}
}

func TestWithIncludeReasoning_NonOpenRouterDisabled(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	p, err := NewOpenAIProvider("gpt-4o")
	if err != nil {
		t.Fatalf("NewOpenAIProvider: %v", err)
	}
	if p.includeReasoning {
		t.Error("non-OpenRouter base URL should not auto-enable includeReasoning")
	}
}

func TestExtractReasoningContent_PresentField(t *testing.T) {
	raw := `{"choices":[{"delta":{"reasoning_content":"step one"}}]}`
	got := extractReasoningContent(raw)
	if got != "step one" {
		t.Errorf("got %q, want %q", got, "step one")
	}
}

func TestExtractReasoningContent_ReasoningField(t *testing.T) {
	// OpenRouter 为 OpenAI/gpt-5.x 等模型使用 delta.reasoning（无 _content 后缀）
	raw := `{"choices":[{"delta":{"reasoning":"reasoning step"}}]}`
	got := extractReasoningContent(raw)
	if got != "reasoning step" {
		t.Errorf("got %q, want %q", got, "reasoning step")
	}
}

func TestExtractReasoningContent_AbsentField(t *testing.T) {
	raw := `{"choices":[{"delta":{"content":"hello"}}]}`
	got := extractReasoningContent(raw)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExtractReasoningContent_EmptyChoices(t *testing.T) {
	raw := `{"choices":[]}`
	got := extractReasoningContent(raw)
	if got != "" {
		t.Errorf("expected empty string for empty choices, got %q", got)
	}
}

func TestExtractReasoningContent_InvalidJSON(t *testing.T) {
	raw := `not-json`
	got := extractReasoningContent(raw)
	if got != "" {
		t.Errorf("expected empty string for invalid JSON, got %q", got)
	}
}

// TestExtractReasoningContent_PriorityReasoningContent 验证当两个字段同时存在时，
// reasoning_content 优先于 reasoning 返回（DeepSeek-R1 原生格式优先）。
func TestExtractReasoningContent_PriorityReasoningContent(t *testing.T) {
	raw := `{"choices":[{"delta":{"reasoning_content":"primary","reasoning":"fallback"}}]}`
	got := extractReasoningContent(raw)
	if got != "primary" {
		t.Errorf("reasoning_content should take priority over reasoning, got %q", got)
	}
}
