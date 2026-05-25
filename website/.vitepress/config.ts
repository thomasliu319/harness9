import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'harness9',
  description: '轻量级、功能完备、生产可用的 Go Agent Harness 框架',
  base: '/harness9/',
  appearance: 'dark',
  head: [
    ['link', { rel: 'icon', href: '/harness9/favicon.ico' }],
  ],
  themeConfig: {
    nav: [
      { text: '首页', link: '/' },
      { text: '文档', link: '/docs/quick-start' },
      {
        text: 'GitHub',
        link: 'https://github.com/ZhangShenao/harness9',
        target: '_blank',
      },
    ],
    sidebar: {
      '/docs/': [
        {
          text: '快速开始',
          items: [
            { text: '快速启动指南', link: '/docs/quick-start' },
          ],
        },
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
          ],
        },
      ],
    },
    socialLinks: [
      { icon: 'github', link: 'https://github.com/ZhangShenao/harness9' },
    ],
    search: {
      provider: 'local',
    },
    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright © 2025-present ZhangShenao',
    },
  },
})
