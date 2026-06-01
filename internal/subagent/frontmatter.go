// Package subagent — frontmatter：agent Markdown 文件解析器。
// 本文件实现 parseAgentFile，解析 .harness9/agents/*.md 文件中的 YAML frontmatter，
// 提取子代理定义字段（name、description、tools 等），正文作为 SystemPrompt。
// 解析逻辑与 skills.parseFrontmatter 相似，但字段集合不同，故各自独立实现。
package subagent

import (
	"fmt"
	"strconv"
	"strings"
)

// parseAgentFile 解析一个 agent Markdown 文件内容（YAML frontmatter + 正文）。
// frontmatter 之后的正文作为 SystemPrompt。未包含合法 frontmatter 时返回错误。
//
// 支持字段：name、description、tools、disallowed_tools、model、max_turns、skills。
// 列表字段（tools/disallowed_tools/skills）以逗号分隔。值支持单/双引号包裹。
func parseAgentFile(content string) (SubAgentDefinition, error) {
	const delim = "---\n"
	if !strings.HasPrefix(content, delim) {
		return SubAgentDefinition{}, fmt.Errorf("缺少 frontmatter 起始分隔符")
	}
	rest := content[len(delim):]
	idx := strings.Index(rest, "\n---\n")
	if idx == -1 {
		return SubAgentDefinition{}, fmt.Errorf("缺少 frontmatter 闭合分隔符")
	}
	fm := rest[:idx]
	body := strings.TrimPrefix(rest[idx+len("\n---\n"):], "\n")

	var def SubAgentDefinition
	def.SystemPrompt = strings.TrimSpace(body)

	for _, line := range strings.Split(fm, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = unquote(v)
		switch k {
		case "name":
			def.Name = v
		case "description":
			def.Description = v
		case "tools":
			def.Tools = splitList(v)
		case "disallowed_tools":
			def.DisallowedTools = splitList(v)
		case "model":
			def.Model = v
		case "max_turns":
			if n, err := strconv.Atoi(v); err == nil {
				def.MaxTurns = n
			}
		case "skills":
			def.Skills = splitList(v)
		}
	}
	return def, nil
}

// unquote 去除值两端成对的单引号或双引号。
func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// splitList 把逗号分隔的字符串拆为去空白的非空项切片。
func splitList(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
