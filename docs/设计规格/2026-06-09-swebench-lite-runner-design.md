# SWE-bench Lite Runner — 设计文档

**日期**: 2026-06-09
**分支**: benchmark
**状态**: 已批准，待实现

---

## 背景与目标

harness9 是一款 Go 语言构建的轻量级 Agent Harness 框架，核心功能已开发完成。本文档描述如何通过 SWE-bench Lite（300 个 Python 仓库真实 Issue）对 harness9 的 Agent 能力进行客观基准评估。

**目标**：
- 运行方式：按 repo 类别抽样（每类 10 条），覆盖多样性同时控制成本
- 评估方式：官方 `swebench` Python 包 + 官方 Docker 镜像，得到标准 Resolved% 分数
- System Prompt：结构化流程约束 + 自由探索结合（C 方案）

---

## 架构

### 目录结构

```
cmd/swebench/
├── main.go       CLI 入口：flag 解析、数据集加载、采样、并发控制、汇总
├── runner.go     单 instance 执行：环境准备 → engine.Run → patch 收集
├── dataset.go    读取 SWE-bench Lite JSONL、按 repo 分类、随机采样
├── prompt.go     专用 system prompt 构造
└── report.go     写 predictions.jsonl、运行摘要 Markdown
```

### 关键数据结构

```go
// Instance 是 SWE-bench Lite 数据集的一条记录（JSONL 格式）
type Instance struct {
    InstanceID       string `json:"instance_id"`
    Repo             string `json:"repo"`
    BaseCommit       string `json:"base_commit"`
    ProblemStatement string `json:"problem_statement"`
    HintsText        string `json:"hints_text"`
}

// Prediction 是写入 predictions.jsonl 的一条记录（官方格式）
type Prediction struct {
    InstanceID string `json:"instance_id"`
    ModelPatch string `json:"model_patch"` // git diff HEAD 的输出，无改动时为空字符串
}

// RunResult 记录单个 instance 的运行结果（用于汇总）
type RunResult struct {
    Instance  Instance
    Patch     string
    Error     error
    Duration  time.Duration
    TokensUsed int
}
```

### CLI Flags

| Flag | 说明 | 默认值 |
|------|------|--------|
| `--dataset` | SWE-bench Lite JSONL 文件路径 | 必填 |
| `--sample` | 每个 repo 抽取的 instance 数量 | `10` |
| `--output` | 输出目录路径 | `./swebench-results/` |
| `--max-turns` | 每个 instance 最大 Turn 数 | `30` |
| `--parallel` | 并发执行的 instance 数 | `1` |
| `--resume` | 跳过 predictions.jsonl 中已有结果的 instance | `false` |
| `--timeout` | 单个 instance 超时（分钟） | `10` |

---

## 数据流：Instance 执行生命周期

```
dataset.go          runner.go                          harness9 engine
──────────          ─────────                          ─────────────────
Load JSONL
  │
Sample by repo
  │
  ▼
for each instance:
  │
  ├─► 1. git clone <repo> @ <base_commit> → tmpDir
  │         (宿主机执行：git clone + git checkout)
  │
  ├─► 2. preflight check
  │         验证 API Key / Docker daemon / git / 输出目录
  │
  ├─► 3. sandbox.Manager.Create(tmpDir)
  │         → DockerEnvironment（复用现有沙箱基础设施）
  │
  ├─► 4. 构造 tools registry
  │         bash / read_file / write_file / edit_file
  │         工作目录锁定在 tmpDir
  │
  ├─► 5. engine.Run(ctx, "请修复上述 Issue。")
  │         system prompt = buildSWEBenchPrompt(instance)
  │         （buildSWEBenchPrompt 将 ProblemStatement 注入 prompt 末尾）
  │         provider = 真实 LLM（LLM_MODEL 环境变量）
  │         max_turns = --max-turns flag
  │
  ├─► 6. bash: git diff HEAD 捕获 patch
  │         在 tmpDir 内执行
  │
  ├─► 7. 追加写入 predictions.jsonl（立即 flush）
  │
  └─► 8. defer: os.RemoveAll(tmpDir) + env.Close(ctx)
```

**关键设计决策：**

- **推断与评估完全解耦**：runner 只生成 patch，不跑测试套件；官方 `swebench evaluate` 事后单独执行
- **git clone 在宿主机执行**：tmpDir 通过 bind mount 共享给 Docker 容器，agent 在容器内执行命令，文件落在宿主机
- **predictions.jsonl 追加写**：每条完成后立即 flush，配合 `--resume` 支持断点续跑
- **per-instance 完全隔离**：独立 tmpDir + 独立 DockerEnvironment + 独立 engine 实例

---

## 采样策略

SWE-bench Lite 覆盖的主要 repo（共约 11 个）：

```
astropy, django, flask, matplotlib, mpl-finance,
pydicom, pylint, pytest, requests, scikit-learn,
sphinx, sympy, xarray
```

`dataset.go` 实现：
1. 读取全量 JSONL，按 `repo` 字段分组
2. 每组随机采样 `--sample` 条（不足时取全部）
3. 打乱顺序后返回，确保并发时 repo 分布均匀

---

## 专用 System Prompt

```
你是一名资深软件工程师，正在处理一个真实的 GitHub Issue。
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
- `find . -type f -name "*.py" | grep -v __pycache__ | head -60` 了解项目结构
- `grep -r "<关键词>" --include="*.py" -l` 定位相关文件
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

{{.ProblemStatement}}
```

`prompt.go` 的 `buildSWEBenchPrompt(instance Instance) string` 将模板中的 `{{.ProblemStatement}}` 替换为实际内容。

---

## 输出格式

### 目录结构

```
swebench-results/
├── predictions.jsonl          # 官方格式，每行一条 Prediction
├── run_summary.md             # 运行摘要
└── logs/
    ├── django__django-1234.log   # 每个 instance 的完整对话日志
    └── ...
```

### predictions.jsonl（官方兼容格式）

```jsonl
{"instance_id": "django__django-11179", "model_patch": "diff --git a/django/..."}
{"instance_id": "astropy__astropy-12345", "model_patch": ""}
```

空 `model_patch` 表示 agent 未做任何改动，评估器会标记为 Unresolved。

### run_summary.md

```markdown
# SWE-bench Lite Run Summary

- 运行时间: 2026-06-09 14:30 — 16:45
- 总实例数: 87（9 个 repo × ~10 条）
- 成功生成 patch: 82 / 87
- 空 patch（agent 无改动）: 3
- 运行出错: 2
- 估算 API 费用: ~$43

## 按 Repo 分布

| Repo | 实例数 | 有 patch | 空 patch | 出错 |
|------|--------|---------|---------|------|
| django | 10 | 9 | 1 | 0 |
```

---

## 评估步骤（runner 跑完后执行）

```bash
# 1. 安装官方评估器
pip install swebench

# 2. 运行评估
python -m swebench.harness.run_evaluation \
    --dataset_name princeton-nlp/SWE-bench_Lite \
    --predictions_path ./swebench-results/predictions.jsonl \
    --max_workers 4 \
    --run_id harness9-lite-v1

# 3. 查看 Resolved% 分数
```

---

## 错误处理与韧性

### 错误分类

| 错误类型 | 场景 | 处理方式 |
|----------|------|----------|
| **环境错误** | git clone 失败、Docker 启动失败 | 跳过该 instance，记录错误，写空 patch，继续 |
| **引擎错误** | LLM API 超时/限流、MaxTurns 触发 | 同上；MaxTurns 时仍收集当前 git diff |
| **致命错误** | API Key 未配置、输出目录不可写 | preflight check 阶段立即终止 |

### 韧性机制

- **追加写 + `--resume`**：每条完成后立即 flush；重启后跳过已有 `instance_id`
- **per-instance timeout**：`context.WithTimeout`（默认 10 分钟），防止单条卡住整个运行
- **tmpDir 清理保证**：`defer os.RemoveAll(tmpDir)` + `defer env.Close(ctx)`，无论成功还是失败都回收
- **并发控制**：`golang.org/x/sync/semaphore` 限制并发数，防止过多 Docker 容器和 API 并发

### Preflight Check

```
✓ OPENAI_API_KEY 已配置
✓ dataset 文件存在且可读
✓ output 目录可写（不存在时自动创建）
✓ Docker daemon 可达
✓ git 命令可用
```

---

## 不在范围内

- 自动下载 SWE-bench 数据集（用户手动从 HuggingFace 下载 JSONL）
- 内嵌评估（不在容器内直接跑测试套件）
- TUI 界面（纯 CLI，日志输出到 stderr）
- 与现有 `internal/evals/` hermetic 框架集成
