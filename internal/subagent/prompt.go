// Package subagent — promptBuilder：子代理 system prompt 组装器。
// 本文件实现 promptBuilder，组合子代理 system prompt、预加载 skill 正文和工作目录信息。
// 通过结构类型（Structural Typing）隐式满足 engine.PromptBuilder 接口，无需显式 import engine 包，
// 避免循环依赖（subagent 依赖 engine，engine 不反向依赖 subagent）。
package subagent

import (
	"errors"
	"fmt"
	"strings"
)

// errSkillMissing 仅用于测试桩，表示 skill 不存在。
var errSkillMissing = errors.New("skill missing")

// skillLoader 按名称加载 skill 正文。生产中由 *skills.Index.GetFullContent 适配。
type skillLoader func(name string) (string, error)

// promptBuilder 是子代理的静态 PromptBuilder，实现 engine.PromptBuilder 接口（Build() string）。
// 输出 = 子代理 system prompt + 预加载 skills 正文 + 工作目录信息。
type promptBuilder struct {
	systemPrompt string
	workDir      string
	skills       []string
	loader       skillLoader
}

// newPromptBuilder 创建子代理 PromptBuilder。loader 为 nil 时不加载 skills。
func newPromptBuilder(systemPrompt, workDir string, skills []string, loader skillLoader) *promptBuilder {
	return &promptBuilder{systemPrompt: systemPrompt, workDir: workDir, skills: skills, loader: loader}
}

// Build 组装子代理的完整 system prompt。
func (b *promptBuilder) Build() string {
	var sb strings.Builder
	sb.WriteString(b.systemPrompt)
	fmt.Fprintf(&sb, "\n\n工作目录：%s", b.workDir)

	if b.loader != nil {
		for _, name := range b.skills {
			body, err := b.loader(name)
			if err != nil || strings.TrimSpace(body) == "" {
				continue // skill 加载失败静默忽略，不阻断子代理启动
			}
			fmt.Fprintf(&sb, "\n\n## 预加载技能：%s\n\n%s", name, body)
		}
	}
	return sb.String()
}
