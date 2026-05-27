// Package permission 提供基于 JSON 配置文件的工具执行权限规则系统。
//
// 规则以 allow/deny/ask 三种动作描述工具调用模式，评估时按声明顺序匹配，第一条匹配规则生效。
// 无匹配时默认返回 ask（要求人类审批）。
//
// 配置文件格式（JSON）：
//
//	{
//	  "permissions": {
//	    "allow": ["bash(git *)", "read_file"],
//	    "deny":  ["bash(rm -rf *)"],
//	    "ask":   ["bash(sudo *)"]
//	  }
//	}
//
// 规则语法：
//   - "toolName"           → 匹配任意参数的该工具
//   - "toolName(pattern)"  → 工具名匹配 AND 参数包含符合 glob 的子串
package permission

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	RuleAllow = "allow"
	RuleDeny  = "deny"
	RuleAsk   = "ask"
)

type rule struct {
	action   string
	patterns []string
}

// Rules 是有序规则列表，第一条匹配规则决定结果。
type Rules struct {
	rules []rule
}

// NewRules 返回空规则集。
func NewRules() *Rules { return &Rules{} }

// AddRule 追加一批规则（相同 action，多个 pattern）。
func (r *Rules) AddRule(action string, patterns []string) {
	r.rules = append(r.rules, rule{action: action, patterns: patterns})
}

// Evaluate 返回工具调用对应的第一条匹配规则动作，无匹配时返回 RuleAsk。
func (r *Rules) Evaluate(toolName, argStr string) string {
	for _, ru := range r.rules {
		for _, p := range ru.patterns {
			if matchPattern(toolName, argStr, p) {
				return ru.action
			}
		}
	}
	return RuleAsk
}

func matchPattern(toolName, argStr, pattern string) bool {
	parenIdx := strings.Index(pattern, "(")
	if parenIdx < 0 {
		return strings.EqualFold(toolName, pattern)
	}

	pTool := pattern[:parenIdx]
	if !strings.EqualFold(toolName, pTool) {
		return false
	}

	pGlob := strings.TrimSuffix(pattern[parenIdx+1:], ")")
	if pGlob == "" {
		return true
	}

	return globContains(strings.ToLower(argStr), strings.ToLower(pGlob))
}

func globContains(s, glob string) bool {
	if !strings.ContainsAny(glob, "*?[") {
		return strings.Contains(s, glob)
	}
	// 尾部星号快捷路径：形如 "prefix*" 的 glob 直接用前缀匹配，避免调用 filepath.Match
	if strings.HasSuffix(glob, "*") && !strings.ContainsAny(glob[:len(glob)-1], "*?[") {
		prefix := glob[:len(glob)-1]
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	// 头部星号快捷路径：形如 "*suffix" 的 glob 直接用后缀匹配
	if strings.HasPrefix(glob, "*") && !strings.ContainsAny(glob[1:], "*?[") {
		suffix := glob[1:]
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	// 全串 filepath.Match（处理 *word* 等含通配符模式）
	if matched, _ := filepath.Match(glob, s); matched {
		return true
	}
	// 逐词 filepath.Match（将 glob 与参数字符串的每个单词分别匹配）
	for _, word := range strings.Fields(s) {
		if matched, _ := filepath.Match(glob, word); matched {
			return true
		}
	}
	return false
}

type configFile struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
		Ask   []string `json:"ask"`
	} `json:"permissions"`
}

// LoadRules 从 JSON 配置文件加载规则。文件不存在时返回空规则集（非错误）。
// 评估顺序：deny 优先 → allow → ask。
func LoadRules(path string) (*Rules, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return NewRules(), nil
		}
		return nil, err
	}

	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	r := NewRules()
	if len(cfg.Permissions.Deny) > 0 {
		r.AddRule(RuleDeny, cfg.Permissions.Deny)
	}
	if len(cfg.Permissions.Allow) > 0 {
		r.AddRule(RuleAllow, cfg.Permissions.Allow)
	}
	if len(cfg.Permissions.Ask) > 0 {
		r.AddRule(RuleAsk, cfg.Permissions.Ask)
	}
	return r, nil
}

// SaveRules 将当前规则集写回 JSON 配置文件（覆写）。
// 用于"总是允许"动态更新白名单。
//
// 注意：序列化后重新加载时，规则顺序重置为 deny→allow→ask（LoadRules 固定顺序），
// 与原始插入顺序无关。若业务逻辑依赖精确的跨类型顺序，应避免使用 SaveRules 后再 LoadRules。
func SaveRules(path string, r *Rules) error {
	cfg := configFile{}
	for _, ru := range r.rules {
		switch ru.action {
		case RuleAllow:
			cfg.Permissions.Allow = append(cfg.Permissions.Allow, ru.patterns...)
		case RuleDeny:
			cfg.Permissions.Deny = append(cfg.Permissions.Deny, ru.patterns...)
		case RuleAsk:
			cfg.Permissions.Ask = append(cfg.Permissions.Ask, ru.patterns...)
		}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
