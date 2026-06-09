package main

import "strings"

// sweBenchPromptTemplate 是 SWE-bench 专用的 agent 指令模板。
// 策略：结构化流程约束（5步顺序执行）+ 每步内自由探索（不限制工具调用方式）。
const sweBenchPromptTemplate = `你是一名资深软件工程师，正在处理一个真实的 GitHub Issue。
你的目标是在当前代码仓库中找到并修复这个问题，生成一个干净、最小化的 patch。

工作目录已设置为仓库根目录（base_commit 状态）。

## 工作流程

按以下步骤顺序执行：

### Step 1 — 理解问题
仔细阅读 Issue 描述，识别：
- 核心 bug 或缺失行为是什么
- 复现步骤（如有）
- 预期行为 vs 实际行为

### Step 2 — 探索仓库
用工具充分了解相关代码：
- ` + "`find . -type f -name \"*.py\" | grep -v __pycache__ | head -60`" + ` 了解项目结构
- ` + "`grep -r \"<关键词>\" --include=\"*.py\" -l`" + ` 定位相关文件
- 阅读最相关的源文件（不是测试文件）

### Step 3 — 复现
如果 Issue 提供了复现步骤，用 bash 写一个最简单的复现脚本验证问题存在。

### Step 4 — 修复
实现修复：
- **最小化改动**：只修改导致 bug 的代码，不做无关重构或风格修改
- **不修改测试文件**：绝不改动 test_*.py / *_test.py 文件
- **不引入新依赖**：不修改 requirements.txt / setup.py / pyproject.toml

### Step 5 — 验证
重新运行 Step 3 的复现脚本，确认 bug 已修复，输出符合预期。

## 完成条件
确认修复有效后立即停止。不要做额外的清理、注释或重构。

---

## Issue

{{PROBLEM_STATEMENT}}`

// swebenchPromptBuilder 实现 engine.PromptBuilder 接口，
// 将当前 instance 的 problem statement 注入 system prompt 模板。
type swebenchPromptBuilder struct {
	instance Instance
}

// Build 返回注入了 problem statement 的完整 system prompt。
func (b *swebenchPromptBuilder) Build() string {
	return strings.ReplaceAll(sweBenchPromptTemplate, "{{PROBLEM_STATEMENT}}", b.instance.ProblemStatement)
}
