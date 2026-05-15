# CLI 分发 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让外部技术用户通过 `curl | bash` 一行命令安装 harness9 二进制，并在任意目录使用 harness9 agent。

**Architecture:** GoReleaser 驱动交叉编译（darwin/linux × amd64/arm64）并自动上传到 GitHub Releases；GitHub Actions 在 `git tag v*` 时触发发布；`scripts/install.sh` 负责下载、校验、安装二进制到 `/usr/local/bin`。

**Tech Stack:** Go 1.25.3、GoReleaser v2、GitHub Actions、Bash

---

## 文件清单

| 操作 | 路径 | 职责 |
|------|------|------|
| Modify | `cmd/harness9/main.go` | 新增 `version` 包级变量 + `--version` flag |
| Create | `.goreleaser.yaml` | 声明编译目标、压缩格式、checksum、Homebrew formula |
| Create | `.github/workflows/ci.yml` | PR/push → go test + go build |
| Create | `.github/workflows/release.yml` | tag v* → goreleaser release |
| Create | `scripts/install.sh` | curl \| bash 安装脚本，含 SHA256 校验 |

---

## Task 1: 给 main.go 添加 `--version` flag

**Files:**
- Modify: `cmd/harness9/main.go`

- [ ] **Step 1: 在 `main.go` 顶部（`package main` 声明之后、`import` 之前）插入 version 变量**

打开 `cmd/harness9/main.go`，在第 16 行（`package main`）之后、`import (` 之前插入：

```go
// version 由 goreleaser ldflags 在发布构建时注入；本地开发构建显示 "dev"。
var version = "dev"
```

- [ ] **Step 2: 在 `main()` 函数内、`flag.Parse()` 之前添加 `--version` flag**

找到：
```go
func main() {
	feishuMode := flag.Bool("feishu", false, "启动飞书 Bot 模式（需配置 FEISHU_APP_ID / FEISHU_APP_SECRET）")
	flag.Parse()
```

替换为：
```go
func main() {
	versionMode := flag.Bool("version", false, "打印版本号并退出")
	feishuMode  := flag.Bool("feishu", false, "启动飞书 Bot 模式（需配置 FEISHU_APP_ID / FEISHU_APP_SECRET）")
	flag.Parse()

	if *versionMode {
		fmt.Println("harness9 " + version)
		return
	}
```

- [ ] **Step 3: 验证编译和版本输出**

```bash
go build ./cmd/harness9 && ./harness9 --version
```

期望输出：
```
harness9 dev
```

- [ ] **Step 4: 验证原有流程不受影响**

```bash
go test ./...
```

期望输出：所有测试 PASS，无 FAIL。

- [ ] **Step 5: 清理构建产物并提交**

```bash
rm -f harness9
git add cmd/harness9/main.go
git commit -m "feat: 新增 --version flag，goreleaser 发布时注入版本号"
```

---

## Task 2: 创建 `.goreleaser.yaml`

**Files:**
- Create: `.goreleaser.yaml`

- [ ] **Step 1: 在项目根目录创建 `.goreleaser.yaml`**

内容如下（注意将 `owner` 字段替换为实际 GitHub 用户名/org）：

```yaml
version: 2
project_name: harness9

builds:
  - id: harness9
    main: ./cmd/harness9
    binary: harness9
    env:
      - CGO_ENABLED=0
    goos:
      - darwin
      - linux
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w
      - -X main.version={{.Version}}

archives:
  - id: harness9
    format: tar.gz
    name_template: "harness9_{{.Version}}_{{.Os}}_{{.Arch}}"
    files:
      - LICENSE
      - README.md

checksum:
  name_template: "harness9_{{.Version}}_SHA256SUMS"
  algorithm: sha256

release:
  github:
    owner: harness9        # 替换为实际 GitHub org/user
    name: harness9
  draft: false
  prerelease: auto         # v0.x.x-beta 等自动标记为 prerelease

brews:
  - name: harness9
    repository:
      owner: harness9      # 替换为实际 GitHub org/user
      name: homebrew-tap   # 需预先创建该仓库
    homepage: "https://github.com/harness9/harness9"
    description: "轻量级、生产可用的 Agent Harness CLI"
    install: |
      bin.install "harness9"
    test: |
      system "#{bin}/harness9", "--version"
```

- [ ] **Step 2: 安装 goreleaser（如果本地尚未安装）**

macOS：
```bash
brew install goreleaser
```

Linux：
```bash
curl -fsSL https://goreleaser.com/static/run | bash
```

验证安装：
```bash
goreleaser --version
```

期望输出：包含 `goreleaser version ...`。

- [ ] **Step 3: 校验配置文件语法**

```bash
goreleaser check
```

期望输出：
```
• checking  ...
• config is valid
```

如有报错，根据提示修正 `.goreleaser.yaml`（常见：`owner` 字段不能为 `harness9` 占位符，需改为真实值；`brews` 需要 `homebrew-tap` 仓库存在）。

> **注意**：若 `homebrew-tap` 仓库暂未创建，可临时注释掉整个 `brews:` 块，待后续创建 tap 仓库后再启用。

- [ ] **Step 4: 本地快照构建验证（不发布到 GitHub）**

```bash
goreleaser build --snapshot --clean
```

期望输出：在 `dist/` 目录生成 4 个平台的二进制：
```
dist/harness9_darwin_amd64_v1/harness9
dist/harness9_darwin_arm64/harness9
dist/harness9_linux_amd64_v1/harness9
dist/harness9_linux_arm64/harness9
```

- [ ] **Step 5: 验证注入的版本号**

```bash
dist/harness9_darwin_arm64/harness9 --version   # Apple Silicon
# 或
dist/harness9_darwin_amd64_v1/harness9 --version  # Intel
```

期望输出（snapshot 构建版本号含 commit hash）：
```
harness9 0.0.0-SNAPSHOT-xxxxxxx
```

- [ ] **Step 6: 清理 dist 并提交**

```bash
echo "dist/" >> .gitignore   # 确保 dist/ 不被追踪（若已存在则跳过）
git add .goreleaser.yaml .gitignore
git commit -m "build: 新增 GoReleaser 配置，支持 darwin/linux × amd64/arm64 交叉编译"
```

---

## Task 3: 创建 CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: 创建目录并写入 `ci.yml`**

```bash
mkdir -p .github/workflows
```

创建 `.github/workflows/ci.yml`，内容：

```yaml
name: CI

on:
  push:
    branches: [master]
  pull_request:
    branches: [master]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Run tests
        run: go test ./...

      - name: Build binary
        run: go build ./cmd/harness9
```

- [ ] **Step 2: 用 Python 或 yamllint 快速校验 YAML 语法**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo "YAML OK"
```

期望输出：`YAML OK`

- [ ] **Step 3: 提交**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: 新增 CI workflow，PR/push 自动运行 test + build"
```

---

## Task 4: 创建 Release workflow

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: 创建 `.github/workflows/release.yml`**

```yaml
name: Release

on:
  push:
    tags:
      - "v*"

jobs:
  release:
    runs-on: ubuntu-latest
    permissions:
      contents: write        # 允许创建 GitHub Release 和上传产物

    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0     # goreleaser 需要完整 git 历史生成 changelog

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          version: latest
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          # 启用 Homebrew tap 时取消注释并在 GitHub 仓库设置中添加 secret：
          # HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}
```

- [ ] **Step 2: 校验 YAML 语法**

```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))" && echo "YAML OK"
```

期望输出：`YAML OK`

- [ ] **Step 3: 提交**

```bash
git add .github/workflows/release.yml
git commit -m "ci: 新增 Release workflow，git tag v* 触发 goreleaser 自动发布"
```

---

## Task 5: 创建安装脚本 `scripts/install.sh`

**Files:**
- Create: `scripts/install.sh`

- [ ] **Step 1: 创建 `scripts/` 目录并写入 `install.sh`**

```bash
mkdir -p scripts
```

创建 `scripts/install.sh`，内容：

```bash
#!/usr/bin/env bash
set -euo pipefail

REPO="harness9/harness9"   # 替换为实际 GitHub org/repo
BINARY="harness9"
INSTALL_DIR="/usr/local/bin"

# ── 检测操作系统 ──────────────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin|linux) ;;
  *) echo "错误：不支持的操作系统 $OS" >&2; exit 1 ;;
esac

# ── 检测 CPU 架构 ─────────────────────────────────────────
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "错误：不支持的架构 $ARCH" >&2; exit 1 ;;
esac

# ── 获取最新版本号 ─────────────────────────────────────────
echo "正在查询最新版本..."
VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | sed 's/.*"tag_name": *"\(.*\)".*/\1/')

if [ -z "$VERSION" ]; then
  echo "错误：无法获取版本号，请检查网络或仓库地址" >&2
  exit 1
fi

TARBALL="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
URL="${BASE_URL}/${TARBALL}"
CHECKSUM_URL="${BASE_URL}/${BINARY}_${VERSION}_SHA256SUMS"

# ── 下载到临时目录 ─────────────────────────────────────────
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "下载 ${BINARY} ${VERSION} (${OS}/${ARCH})..."
curl -fsSL "$URL" -o "$TMP/$TARBALL"
curl -fsSL "$CHECKSUM_URL" -o "$TMP/SHA256SUMS"

# ── 校验 SHA256 ────────────────────────────────────────────
cd "$TMP"
if command -v sha256sum &>/dev/null; then
  grep "$TARBALL" SHA256SUMS | sha256sum -c -
elif command -v shasum &>/dev/null; then
  grep "$TARBALL" SHA256SUMS | shasum -a 256 -c -
else
  echo "警告：未找到 sha256sum 或 shasum，跳过校验" >&2
fi

# ── 解压并安装 ─────────────────────────────────────────────
tar -xzf "$TARBALL"

if [ ! -w "$INSTALL_DIR" ]; then
  echo "需要 sudo 权限写入 ${INSTALL_DIR}..."
  sudo install -m755 "$BINARY" "$INSTALL_DIR/$BINARY"
else
  install -m755 "$BINARY" "$INSTALL_DIR/$BINARY"
fi

echo ""
echo "✅ ${BINARY} ${VERSION} 已安装到 ${INSTALL_DIR}/${BINARY}"
echo ""
echo "快速开始："
echo "  export OPENAI_API_KEY=\"your-key\""
echo "  cd /your/project && ${BINARY}"
```

- [ ] **Step 2: 赋予可执行权限并做本地语法检查**

```bash
chmod +x scripts/install.sh
bash -n scripts/install.sh && echo "语法检查通过"
```

期望输出：`语法检查通过`

- [ ] **Step 3: 本地功能冒烟测试（不实际安装，仅验证 OS/arch 检测逻辑）**

```bash
bash -c '
  OS=$(uname -s | tr "[:upper:]" "[:lower:]")
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64)        ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
  esac
  echo "检测到: OS=${OS} ARCH=${ARCH}"
'
```

期望输出（Apple Silicon Mac 为例）：
```
检测到: OS=darwin ARCH=arm64
```

- [ ] **Step 4: 提交**

```bash
git add scripts/install.sh
git commit -m "feat: 新增 curl|bash 安装脚本，含 OS/arch 检测和 SHA256 校验"
```

---

## Task 6: 端到端验证

**Files:** 无新增文件，验证前述全部产物。

- [ ] **Step 1: 运行完整测试套件确保回归**

```bash
go test ./...
```

期望：全部 PASS。

- [ ] **Step 2: 验证 goreleaser snapshot 构建（含版本注入）**

```bash
goreleaser build --snapshot --clean
```

期望：`dist/` 下出现 4 个平台二进制，无错误。

- [ ] **Step 3: 在当前 Mac 架构上运行快照二进制**

Apple Silicon（arm64）：
```bash
dist/harness9_darwin_arm64/harness9 --version
```

Intel Mac（amd64）：
```bash
dist/harness9_darwin_amd64_v1/harness9 --version
```

期望输出（含 snapshot 版本）：
```
harness9 0.0.0-SNAPSHOT-xxxxxxx
```

- [ ] **Step 4: 用本地 release 模拟测试（可选，需要 GITHUB_TOKEN）**

若要在本地完整模拟发布（不推送到 GitHub Releases），使用：

```bash
GITHUB_TOKEN=xxx goreleaser release --skip=publish --clean
```

- [ ] **Step 5: 清理 dist 目录**

```bash
rm -rf dist/
```

- [ ] **Step 6: 最终提交（如有遗漏文件）**

```bash
git status  # 确认没有未跟踪文件
```

---

## 发布手册（给开发者）

实施完成后，发布新版本的完整流程：

```bash
# 1. 确保测试全绿
go test ./...

# 2. 打 semver tag
git tag v0.1.0
git push origin v0.1.0

# 3. GitHub Actions 自动触发 release.yml
# 约 3-5 分钟后，在 GitHub Releases 页面确认：
#   harness9_v0.1.0_darwin_arm64.tar.gz  ✅
#   harness9_v0.1.0_darwin_amd64.tar.gz  ✅
#   harness9_v0.1.0_linux_arm64.tar.gz   ✅
#   harness9_v0.1.0_linux_amd64.tar.gz   ✅
#   harness9_v0.1.0_SHA256SUMS           ✅

# 4. 用户安装命令（在 README 中公示）
curl -fsSL https://raw.githubusercontent.com/harness9/harness9/master/scripts/install.sh | bash
```
