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
