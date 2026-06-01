import { defineConfig } from 'vitepress'
import { docsSidebar } from './sidebar.generated.js'

export default defineConfig({
  title: 'harness9',
  description: '轻量级、功能完备、生产可用的 Go Agent Harness 框架',
  base: '/harness9/',
  appearance: 'dark',
  themeConfig: {
    nav: [
      { text: '首页', link: '/' },
      { text: '文档', link: '/docs/quick-start' },
      { text: '博客', link: '/blog/' },
      {
        text: 'GitHub',
        link: 'https://github.com/ZhangShenao/harness9',
        target: '_blank',
      },
    ],
    sidebar: {
      '/docs/': docsSidebar,
      '/blog/': [
        {
          text: '技术博客',
          items: [
            { text: '所有文章', link: '/blog/' },
            { text: 'Agent Loop — 500 行 Go 代码驱动的生产级 ReAct 主循环', link: '/blog/agent-loop/' },
            { text: '工具调用系统 — 从接口契约到并发沙箱的工程实践', link: '/blog/tool-calling/' },
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
