package permission_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/harness9/internal/permission"
)

func TestRules_Evaluate_Allow(t *testing.T) {
	r := permission.NewRules()
	r.AddRule(permission.RuleAllow, []string{"bash(git *)", "read_file"})

	if got := r.Evaluate("bash", "git status"); got != permission.RuleAllow {
		t.Errorf("git status should match allow rule, got %s", got)
	}
	if got := r.Evaluate("read_file", ""); got != permission.RuleAllow {
		t.Errorf("read_file should match allow rule, got %s", got)
	}
}

func TestRules_Evaluate_Deny(t *testing.T) {
	r := permission.NewRules()
	r.AddRule(permission.RuleDeny, []string{"bash(rm -rf *)"})

	if got := r.Evaluate("bash", "rm -rf /tmp"); got != permission.RuleDeny {
		t.Errorf("rm -rf should match deny rule, got %s", got)
	}
}

func TestRules_Evaluate_Ask(t *testing.T) {
	r := permission.NewRules()
	r.AddRule(permission.RuleAsk, []string{"bash(sudo *)"})

	if got := r.Evaluate("bash", "sudo apt-get update"); got != permission.RuleAsk {
		t.Errorf("sudo should match ask rule, got %s", got)
	}
}

func TestRules_Evaluate_NoMatch_ReturnsAsk(t *testing.T) {
	r := permission.NewRules()
	if got := r.Evaluate("bash", "echo hello"); got != permission.RuleAsk {
		t.Errorf("no match should return ask (default), got %s", got)
	}
}

func TestRules_FirstMatchWins(t *testing.T) {
	r := permission.NewRules()
	r.AddRule(permission.RuleAllow, []string{"bash(git *)"})
	r.AddRule(permission.RuleDeny, []string{"bash(git *)"})

	if got := r.Evaluate("bash", "git commit"); got != permission.RuleAllow {
		t.Errorf("first match (allow) should win, got %s", got)
	}
}

func TestLoadRules_ValidJSON(t *testing.T) {
	content := `{
		"permissions": {
			"allow": ["bash(git *)", "read_file"],
			"deny": ["bash(rm -rf *)"],
			"ask": ["bash(sudo *)"]
		}
	}`
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	r, err := permission.LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Evaluate("bash", "git log"); got != permission.RuleAllow {
		t.Errorf("expected allow for git log, got %s", got)
	}
	if got := r.Evaluate("bash", "rm -rf /tmp"); got != permission.RuleDeny {
		t.Errorf("expected deny for rm -rf, got %s", got)
	}
}

func TestLoadRules_MissingFile_ReturnsEmpty(t *testing.T) {
	r, err := permission.LoadRules("/nonexistent/settings.json")
	if err != nil {
		t.Fatal("missing file should return empty rules, not error")
	}
	if r == nil {
		t.Fatal("LoadRules should return non-nil Rules even for missing file")
	}
}

func TestLoadRules_InvalidJSON_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{invalid json}"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := permission.LoadRules(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSaveRules_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	r := permission.NewRules()
	r.AddRule(permission.RuleAllow, []string{"bash(git *)", "read_file"})
	r.AddRule(permission.RuleDeny, []string{"bash(rm -rf *)"})

	if err := permission.SaveRules(path, r); err != nil {
		t.Fatal(err)
	}

	loaded, err := permission.LoadRules(path)
	if err != nil {
		t.Fatalf("LoadRules after SaveRules: %v", err)
	}
	// deny 规则在 LoadRules 中优先
	if got := loaded.Evaluate("bash", "rm -rf /tmp"); got != permission.RuleDeny {
		t.Errorf("expected deny after round-trip, got %s", got)
	}
	if got := loaded.Evaluate("bash", "git log"); got != permission.RuleAllow {
		t.Errorf("expected allow after round-trip, got %s", got)
	}
}

// TestGlobContains_Patterns 覆盖 matchPattern / globContains 的各类匹配路径：
// 尾部星号快捷路径、精确工具名、大小写不敏感、空括号。
func TestGlobContains_Patterns(t *testing.T) {
	cases := []struct {
		pattern string
		tool    string
		arg     string
		want    string
	}{
		// 尾部星号快捷路径
		{"bash(git *)", "bash", "git commit -m 'test'", permission.RuleAllow},
		// 精确工具名匹配（无括号）
		{"read_file", "read_file", "anything", permission.RuleAllow},
		// 工具名大小写不敏感
		{"READ_FILE", "read_file", "", permission.RuleAllow},
		// 空括号等价于仅匹配工具名
		{"bash()", "bash", "", permission.RuleAllow},
	}

	for _, tc := range cases {
		r := permission.NewRules()
		r.AddRule(permission.RuleAllow, []string{tc.pattern})
		got := r.Evaluate(tc.tool, tc.arg)
		if got != tc.want {
			t.Errorf("pattern=%q tool=%q arg=%q: got %s, want %s", tc.pattern, tc.tool, tc.arg, got, tc.want)
		}
	}
}
