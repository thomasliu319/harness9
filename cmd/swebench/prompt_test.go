package main

import (
	"strings"
	"testing"
)

func TestSwebenchPromptBuilder(t *testing.T) {
	inst := Instance{
		InstanceID:       "django__django-99",
		ProblemStatement: "There is a bug in QuerySet.filter() when using complex Q objects.",
	}
	b := &swebenchPromptBuilder{instance: inst, workDir: "/tmp/swebench-test"}
	prompt := b.Build()

	if !strings.Contains(prompt, inst.ProblemStatement) {
		t.Error("prompt should contain the problem statement")
	}
	if strings.Contains(prompt, "{{PROBLEM_STATEMENT}}") {
		t.Error("prompt should not contain unreplaced placeholder")
	}
	if !strings.Contains(prompt, "Step 1") {
		t.Error("prompt should contain structured workflow steps")
	}
	if !strings.Contains(prompt, "不修改测试文件") {
		t.Error("prompt should contain constraint about not modifying test files")
	}
	// 语言锁定（P2：防止语言漂移）
	if !strings.Contains(prompt, "语言要求") {
		t.Error("prompt should contain language requirement")
	}
	// Python 快速放弃策略（P0：杜绝死循环搜索）
	if !strings.Contains(prompt, "NO_PYTHON") {
		t.Error("prompt should contain NO_PYTHON fast-detection pattern")
	}
	// 新版：立即跳过 Step 3 全部内容
	if !strings.Contains(prompt, "立即跳过 Step 3 全部内容") {
		t.Error("prompt should contain instruction to skip step 3 immediately")
	}
	// workDir 注入（P0：消除 /repo 路径假设）
	if !strings.Contains(prompt, "/tmp/swebench-test") {
		t.Error("prompt should contain the injected workDir")
	}
	if strings.Contains(prompt, "{{WORK_DIR}}") {
		t.Error("prompt should not contain unreplaced WORK_DIR placeholder")
	}
	// bash 超时约束说明
	if !strings.Contains(prompt, "30 秒") {
		t.Error("prompt should mention 30s bash timeout")
	}
	// 先规划后编辑（P1：减少 edit_file 反复撤回）
	if !strings.Contains(prompt, "在调用任何编辑工具之前") {
		t.Error("prompt should contain plan-before-edit instruction")
	}
	// 验证上限（P2：杜绝过度验证）
	if !strings.Contains(prompt, "验证至多 1-2 步") {
		t.Error("prompt should contain max verification steps constraint")
	}
	// edit_file diff 可信度声明
	if !strings.Contains(prompt, "diff 即为权威确认") {
		t.Error("prompt should declare diff as authoritative")
	}
	// 禁止 sed 读文件，引导使用 read_file
	if !strings.Contains(prompt, "start_line") {
		t.Error("prompt should mention read_file start_line parameter")
	}
	// 并发工具调用引导（P3）
	if !strings.Contains(prompt, "尽量同时发起多个工具调用") {
		t.Error("prompt should encourage concurrent tool calls")
	}
}
