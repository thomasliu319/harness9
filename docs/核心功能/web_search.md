# 网页搜索与抓取技术方案

harness9 通过 `web_search` 和 `web_fetch` 两个内置工具赋予 Agent 实时访问互联网的能力，无需任何 API Key，内置生产级 SSRF 防护。

---

## 1. 设计决策

### 为什么不做成 MCP 工具

DeepAgents 和 OpenCode 将网页能力完全外包给 MCP 服务器，零配置即零能力——用户必须自行安装配置 Tavily 或 Brave MCP。harness9 的哲学是**开箱即用**：DuckDuckGo 无需任何 Key，`go-readability` 是纯 Go 库，整个能力链条不依赖任何外部平台账号。

### 工具原子化，深度搜索在 LLM 层

工具只提供两个原子能力：搜索（返回结果链接）和抓取（返回页面内容）。是否继续跟踪链接、是否扩展搜索词、何时停止——这些决策完全交给 LLM 在 ReAct 循环中完成。这是六个主流框架的共同设计选择，工具层无需也不应内置搜索树遍历逻辑。

### 为什么要注入当前日期

LLM 的知识截止日期（training cutoff）与当前时间存在差距。不注入日期时，Agent 在搜索时倾向于使用训练截止年份，导致搜索"最新版本"时拿到过期结果。harness9 在 System Prompt 基础段注入 `time.Now().Format("2006-01-02")` 实时日期，Agent 每次会话自动感知当天日期，无需额外工具调用。

---

## 2. 工具接口

### `web_search`

搜索互联网，返回标题、URL 和摘要列表。

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|:----:|--------|------|
| `query` | string | 是 | — | 搜索词，英文效果更好 |
| `max_results` | int | 否 | 5 | 返回条数，范围 1–10 |

**输出格式：**

```
[1] Go 1.25 Release Notes
URL: https://go.dev/doc/go1.25
摘要: Go 1.25 introduces range-over-func, improved type inference...

[2] What's new in Go 1.25
URL: https://blog.golang.org/go1.25
摘要: The Go team is pleased to announce Go 1.25...
```

### `web_fetch`

抓取指定 URL，返回 Markdown 格式的主要内容。

| 参数 | 类型 | 必填 | 默认值 | 说明 |
|------|------|:----:|--------|------|
| `url` | string | 是 | — | 目标 URL（http/https） |
| `max_chars` | int | 否 | 8000 | 返回字符上限，最大 32000 |

**输出格式：**

```markdown
# Go 1.25 Release Notes

> 来源：https://go.dev/doc/go1.25

## New Features

...主内容（Markdown 格式）...

[内容已截断，已显示前 8000 字符]   ← 仅超出限制时出现
```

---

## 3. SSRF 防护（`web_safety.go`）

所有 HTTP 请求在发出前经过 `isSafeURL` 校验，类比 `safe_path.go` 对文件系统路径的沙箱保护。

### 检查链

```
URL 输入
  │
  ▼
① scheme 校验：必须是 http 或 https，拒绝 ftp / file 等
  │
  ▼
② userinfo 拒绝：URL 中含 user:pass@... 格式一律拒绝
  │
  ▼
③ DNS 解析：net.LookupHost(hostname)
  │
  ├── 解析失败 → fail-closed（拒绝请求）
  │              防止 DNS rebinding 攻击的第一道屏障
  │
  ▼
④ IP 段检查：对每个解析出的 IP 地址检查
```

### 永久禁止的 IP 范围

| 网段 | 说明 | 可配置 |
|------|------|:------:|
| `169.254.0.0/16` | 链路本地 + AWS/Azure/GCP metadata 端点 | 永久禁止 |
| `127.0.0.0/8` | IPv4 loopback | 默认禁止 |
| `::1` | IPv6 loopback（`net.IPv6loopback` 独立检查） | 默认禁止 |
| `10.0.0.0/8` | RFC1918 私网 | 默认禁止 |
| `172.16.0.0/12` | RFC1918 私网 | 默认禁止 |
| `192.168.0.0/16` | RFC1918 私网 | 默认禁止 |
| `100.64.0.0/10` | CGNAT | 默认禁止 |
| `fe80::/10` | IPv6 链路本地（等价于 IPv4 169.254.0.0/16） | 默认禁止 |
| `fc00::/7` | IPv6 唯一本地地址 ULA（等价于 IPv4 RFC1918 私网） | 默认禁止 |

### 为什么 DNS 解析后二次检查 IP

攻击者可以让域名先解析到公网 IP 通过校验，然后在 DNS TTL 窗口内将解析切换到内网地址（DNS rebinding）。先解析再检查 IP 可以斩断这条路——`isSafeURL` 拿到的是 DNS 查询时刻的真实 IP，不是 hostname。

### IPv4-mapped IPv6 规范化

`::ffff:169.254.169.254` 是 `169.254.169.254` 的 IPv6 表示形式。代码通过 `ip.To4()` 将 IPv4-mapped IPv6 规范化为 IPv4，确保 IPv6 表示的内网地址不会绕过 CIDR 检查。

### 重定向链 SSRF 防护

`web_fetch` 的 `CheckRedirect` 回调在每次重定向后对新 URL 重新调用 `isSafeURL`，防止 open redirect → 内网的攻击路径：

```
外网 URL → SSRF check ✓ → 服务端 301 → 内网 URL → SSRF check ✗（拒绝）
```

---

## 4. HTML 内容提取管线（`web_content.go`）

`web_fetch` 抓取到 HTML 后，经过三级管线转换为 LLM 友好的 Markdown：

```
HTML body (io.Reader)
    │
    ▼
① 大小限制（1MB）
    │ 超出 → 返回 error
    ▼
② go-readability.FromReader()
    │ 成功 → Article{Title, Content(HTML)}
    │ 失败 → 直接走 fallback
    ▼
③ html-to-markdown.Convert(Article.Content)
    │ 成功 → Markdown 字符串
    │ 失败 → 使用 Article.TextContent（readability 提取的纯文本）
    ▼
④ assemblePage(rawURL, title, content, maxChars)
    │ 组装输出：# 标题 + > 来源 + 内容
    │ 超出 maxChars → 截断 + 追加标记
    ▼
Markdown 输出
```

**Fallback 路径**：当 `go-readability` 完全失败（如极简 HTML 或内容为空），使用 `golang.org/x/net/html` tokenizer 做简单文本提取——跳过 `<script>` 和 `<style>` 标签内容，拼接可见文本节点。这个 tokenizer 是 Go 标准库范畴的间接依赖，零额外引入。

### 大小常量

| 常量 | 值 | 说明 |
|------|-----|------|
| `defaultMaxChars` | 8,000 | `max_chars` 参数默认值（约 2000 token） |
| `hardMaxChars` | 32,000 | `max_chars` 参数上限（约 8000 token） |
| `maxHTMLBodySize` | 1,048,576 (1MB) | HTTP 响应体读取上限 |

---

## 5. DuckDuckGo 搜索后端（`web_search.go`）

### 为什么选择 DuckDuckGo

调研六个主流框架的搜索后端选择：DuckDuckGo 是唯一一个**所有框架都支持、无 API Key 要求**的后端，且其 HTML 端点（`html.duckduckgo.com/html/`）返回纯 HTML——不需要 JavaScript 渲染，标准 HTTP 客户端即可处理。

### 请求流程

```
POST https://html.duckduckgo.com/html/
Content-Type: application/x-www-form-urlencoded
User-Agent: harness9/1.0
Body: q=<url-encoded-query>
```

超时控制：
- **Dial 超时**：10s（TCP 握手阶段，`net.Dialer.Timeout`）
- **请求超时**：20s（Context deadline，含 DNS + dial + 读取响应）

双重超时解决了 `http.DefaultClient` 的一个已知问题：仅用 context timeout 时，极慢的 TCP 握手可能无法被及时中断；独立的 dial timeout 保证连接建立阶段的响应性。

### HTML 解析

`golang.org/x/net/html`（已有间接依赖，零新增）的 `html.Parse()` 构建完整 DOM 树，递归遍历提取：

| 选择器 | 目标内容 |
|--------|---------|
| `div.result`（排除 `div.result--more`）| 单条搜索结果容器 |
| `a.result__a` | 标题文本 + href |
| `a.result__snippet` | 摘要文本 |

### DDG 重定向 URL 解码

DuckDuckGo 的链接使用重定向格式：

```
href="/l/?uddg=https%3A%2F%2Fexample.com%2Fpath&rut=..."
```

`decodeUDDG()` 从 `uddg` 参数提取真实目标 URL，LLM 拿到的是可直接传入 `web_fetch` 的最终 URL。

### 可测试性设计

`WebSearchTool` 结构体的 `backendURL` 字段默认指向 DDG 端点，测试中可替换为 `httptest.NewServer` 的本地地址，无需真实网络请求：

```go
// 生产
tool := NewWebSearchTool()  // backendURL = "https://html.duckduckgo.com/html/"

// 测试
tool := &WebSearchTool{backendURL: server.URL}
```

同理，`WebFetchTool` 的 `safetyCheck` 字段在测试中替换为 no-op，允许访问 127.0.0.1 的 httptest 服务器而不触发 SSRF 拦截。

---

## 6. 当前日期注入（`internal/context/builder.go`）

`DefaultPromptBuilder.Build()` 在基础 System Prompt 中注入当天日期：

```
工作目录：/path/to/project
当前日期：2026-06-12
```

每次 `Build()` 调用时实时执行 `time.Now().Format("2006-01-02")`，保证 Agent 在每个会话中感知正确的日期，搜索时自然带入当前年份，避免因训练截止日期偏差导致的陈旧搜索结果。

---

## 7. 模块结构

```
internal/tools/
├── web_safety.go        # SSRF 防护：isSafeURL（检查链 + blockedCIDRs）
├── web_safety_test.go   # 19 个测试用例：IP 段 / scheme / DNS fail-closed / IPv6 / IPv6 ULA / IPv6 链路本地
├── web_content.go       # HTML→Markdown 管线：extractContent + extractPlainText + assemblePage（UTF-8 安全截断）
├── web_content_test.go  # 5 个测试用例：基础转换 / 截断 / fallback / 超大 body / UTF-8 安全截断
├── web_fetch.go         # WebFetchTool：HTTP GET + Content-Type 分支
├── web_fetch_test.go    # 6 个测试用例：SSRF / HTML / text / unsupported / 空URL / 4xx
├── web_search.go        # WebSearchTool：DuckDuckGo POST + DOM 解析 + decodeUDDG
└── web_search_test.go   # 6 个测试用例：DDG 解析 / max_results / URL 解码 / Execute / 空结果
```

**依赖关系：**

```
web_fetch.go ──────┐
                   ├──→ web_safety.go (isSafeURL)
web_search.go ─────┘
     │
     └──→ web_content.go (extractContent)  ──→ go-readability
                                           ──→ html-to-markdown
                                           ──→ golang.org/x/net/html (已有依赖)
```

---

## 8. 配置与使用

两个工具随 harness9 启动自动注册，主 Agent 和所有 Sub-Agent 均可使用，无需任何配置。

**典型搜索流程：**

```
用户：搜索一下 Go 1.25 的新特性

Agent:
  1. web_search("Go 1.25 release notes") → 获取结果列表
  2. web_fetch("https://go.dev/doc/go1.25") → 获取详细内容
  3. 基于抓取内容回答用户
```

**Sub-Agent 委派（研究场景）：**

```
@general-purpose 搜索 LangGraph 最新版本的 checkpoint 机制并总结关键 API
```

`general-purpose` 子代理继承父代理的全部工具，包括 `web_search` 和 `web_fetch`，可独立完成多轮搜索 + 抓取 + 总结，最终只将结论回传给主上下文。

---

## 9. 限制与注意事项

- **DuckDuckGo 限速**：无官方 SLA，高频调用可能触发临时封锁。如需稳定高频搜索，建议通过环境变量配置 Brave Search 或 Tavily（待 P2 后端接口实现后支持）
- **JavaScript 渲染页面**：`web_fetch` 使用纯 HTTP 请求，不执行 JavaScript，动态渲染的 SPA 内容无法抓取
- **`go-readability` 废弃声明**：当前使用 `github.com/go-shiori/go-readability`，作者已标注 deprecated，建议未来迁移至 `codeberg.org/readeck/go-readability/v2`
- **1MB 大小限制**：超过 1MB 的页面会被拒绝处理并返回 error

---

## 10. 参考实现

| 框架 | 策略 | 关键特点 |
|------|------|---------|
| OpenHarness | 内置极简 | DuckDuckGo HTML 解析 + 自实现 HTML→纯文本，12000 字符截断 |
| OpenClaw | 插件化混合 | Mozilla Readability + htmlToMarkdown，5+ 搜索后端级联 Fallback |
| HermesAgent | 全功能 | 8 个后端，LLM 内容压缩管线（Gemini Flash，支持 2M 字符分块） |
| Claude Agent SDK | 官方内置 | `WebSearch`/`WebFetch` 原生工具，research-agent 示例展示 Sub-Agent 并行研究模式 |

详细调研见 `docs/技术调研/web-search-capability.md`（本地调研文档，不随仓库发布）。
