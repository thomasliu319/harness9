# harness9 官网设计文档

**日期**：2026-05-25
**状态**：已批准

---

## 1. 目标与受众

**目标**：基于 GitHub Pages 为 harness9 创建官方网站，同时服务于技术决策者（判断框架是否值得引入）和开发者（快速上手、查阅文档）。

**受众**：技术决策者 + 开发者混合。网站需同时传递两件事：框架够专业、生产可用（给决策者）；代码直白、上手快（给开发者）。

---

## 2. 技术选型

**静态站点生成器**：VitePress

选择理由：
- 开箱即用的深色主题，原生支持 `appearance: 'dark'`
- Markdown 直接渲染，现有 10 篇文档几乎零改动迁入
- 内置全文搜索（local provider），零额外配置
- 自动侧边栏导航，文档系统完善
- 构建产物轻量，GitHub Actions 部署链路成熟

**视觉风格**：深色极简（Dark Minimal）——黑/深灰底色，与 TUI 终端气质一致，不提供明暗切换。

---

## 3. 仓库与部署架构

### 域名

初期使用 GitHub Project Pages：`https://zhangshennao.github.io/harness9/`

后续可创建 GitHub Organization `harness9` 并绑定自定义域名（如 `harness9.dev`）。

### 代码存放

官网源码位于 `ZhangShenao/harness9` 仓库的 `official-site` 分支，放在 `website/` 子目录。

```
harness9/
├── website/                        # VitePress 站点源码
│   ├── .vitepress/
│   │   ├── config.ts               # 站点配置、导航、侧边栏
│   │   └── theme/
│   │       ├── index.ts            # 自定义主题入口
│   │       └── components/
│   │           └── TerminalDemo.vue  # Hero 区终端动画组件
│   ├── index.md                    # Landing Page（home layout）
│   ├── docs/                       # 迁移自 docs/核心功能/ 的文档
│   │   ├── quick-start.md
│   │   ├── tui.md
│   │   ├── shell-execution.md
│   │   ├── agent-loop.md
│   │   ├── tool-calling.md
│   │   ├── context-engineering.md
│   │   ├── planning.md
│   │   ├── agent-skills.md
│   │   ├── file-system.md
│   │   └── cli.md
│   └── package.json
└── .github/workflows/
    └── deploy-website.yml          # 自动构建并推 gh-pages
```

### GitHub Actions 部署流程

触发条件：`official-site` 分支下 `website/**` 路径有提交。

```yaml
on:
  push:
    branches: [official-site]
    paths: ['website/**']

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: '20' }
      - run: cd website && npm ci && npm run build
      - uses: peaceiris/actions-gh-pages@v4
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
          publish_dir: website/.vitepress/dist
```

首次部署后，在仓库 Settings → Pages 中将 Source 设为 `gh-pages` 分支根目录。

---

## 4. 页面结构

### 顶部导航栏

```
HARNESS9    首页    文档    GitHub ↗
```

- `首页` → `/`（Landing Page）
- `文档` → `/docs/quick-start`（文档站入口）
- `GitHub` → `https://github.com/ZhangShenao/harness9`（外链）

### Landing Page 分区（自上而下）

| 区块 | 内容 |
|------|------|
| **Hero** | 标题 `harness9`、定位语"轻量级、功能完备、生产可用的 Go Agent Harness 框架"、安装命令代码块、「快速开始」+「查看文档」两个按钮、右侧 `TerminalDemo.vue` 终端动画 |
| **Why harness9** | 三列卡片：简洁 / 完备 / 生产可用，对应 README 的核心设计理念 |
| **核心特性** | 6 个特性交替左右布局：TUI、Context Engineering、Planning、Shell 执行、并发工具执行、推理内容展示 |
| **架构图** | 引用仓库中已有的 `harness9_architecture.png` |
| **快速开始** | 三步代码块：安装 → 配置 API Key → 启动 |
| **Footer** | MIT License · GitHub 链接 · 文档链接 |

### 文档站侧边栏

```
快速开始
  └── 快速启动指南
核心功能
  ├── TUI 交互界面
  ├── Shell 执行
  ├── Agent Loop
  ├── Tool Calling
  ├── Context Engineering
  ├── Planning 模块
  ├── Agent Skills
  ├── 文件系统能力
  └── CLI 使用指南
```

---

## 5. VitePress 配置要点

```typescript
// website/.vitepress/config.ts
export default defineConfig({
  title: 'harness9',
  description: '轻量级、功能完备、生产可用的 Go Agent Harness 框架',
  base: '/harness9/',       // Project Pages 必须设置 base path
  appearance: 'dark',       // 强制深色，不显示明暗切换按钮
  themeConfig: {
    nav: [
      { text: '首页', link: '/' },
      { text: '文档', link: '/docs/quick-start' },
      { text: 'GitHub', link: 'https://github.com/ZhangShenao/harness9' }
    ],
    sidebar: {
      '/docs/': [
        { text: '快速开始', items: [{ text: '快速启动指南', link: '/docs/quick-start' }] },
        {
          text: '核心功能',
          items: [
            { text: 'TUI 交互界面', link: '/docs/tui' },
            { text: 'Shell 执行', link: '/docs/shell-execution' },
            { text: 'Agent Loop', link: '/docs/agent-loop' },
            { text: 'Tool Calling', link: '/docs/tool-calling' },
            { text: 'Context Engineering', link: '/docs/context-engineering' },
            { text: 'Planning 模块', link: '/docs/planning' },
            { text: 'Agent Skills', link: '/docs/agent-skills' },
            { text: '文件系统能力', link: '/docs/file-system' },
            { text: 'CLI 使用指南', link: '/docs/cli' },
          ]
        }
      ]
    },
    socialLinks: [
      { icon: 'github', link: 'https://github.com/ZhangShenao/harness9' }
    ],
    search: { provider: 'local' }
  }
})
```

---

## 6. TerminalDemo 组件

`website/.vitepress/theme/components/TerminalDemo.vue`：模拟 harness9 启动和首次对话的伪终端动画，字符逐帧输出。内容取自 README 中的对话示例，无需联网，纯 CSS/JS 实现。嵌入 Hero 区右侧，强化"终端工具"视觉印象。

---

## 7. 文档迁移规则

- 源文件：`docs/核心功能/*.md` → 目标：`website/docs/*.md`
- 正文内容无需改动
- 每篇文件头部添加 frontmatter：
  ```yaml
  ---
  title: 文档标题
  description: 一句话描述
  ---
  ```
- 文件间的相对链接更新为新路径

---

## 8. 不在范围内

- 国际化（i18n）
- 博客 / 更新日志页面
- 评论系统
- 自定义域名（初期）
- 多版本文档
