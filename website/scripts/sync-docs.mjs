// 此脚本把根 docs/核心功能/ 的 markdown 转换为 VitePress 页面与侧边栏。
// 根 docs 为唯一信息源；website/docs 与 sidebar.generated.js 均为生成产物（已 gitignore）。
//
// 转换规则：
//   - slug：文件名 _ -> -，去 .md（quick_start -> quick-start）
//   - title：正文首个 # 一级标题（缺失则回退为 slug 并 warning）
//   - description：首个非空正文段落，剥离常见 markdown 标记，截断到 ~120 字
//   - 正文中 .md 链接目标统一 _ -> -
//   - 侧边栏：quick-start 单列「快速开始」，其余进「核心功能」；
//     组内先按 CORE_ORDER 排序，未列出的新文档按字典序追加末尾（加文档零改动）

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(__dirname, '../..') // website/scripts -> website -> repo 根
const sourceDir = path.join(repoRoot, 'docs/核心功能')
const websiteDocsDir = path.resolve(__dirname, '../docs')
const sidebarOut = path.resolve(__dirname, '../.vitepress/sidebar.generated.js')

// 唯一手工配置：核心功能组的展示顺序（未列出的新文档自动追加末尾）
const CORE_ORDER = [
  'tui',
  'shell-execution',
  'agent-loop',
  'tool-calling',
  'context-engineering',
  'planning',
  'human-in-the-loop',
  'agent-skills',
  'sub-agent',
  'file-system',
  'cli',
]

function toSlug(filename) {
  return filename.replace(/\.md$/, '').replace(/_/g, '-')
}

function extractMeta(content) {
  const lines = content.split('\n')
  let title = ''
  let titleInFence = false
  for (const line of lines) {
    if (line.trim().startsWith('```')) {
      titleInFence = !titleInFence
      continue
    }
    if (titleInFence) continue
    const m = line.match(/^#\s+(.+?)\s*$/)
    if (m) {
      title = m[1].trim()
      break
    }
  }
  let description = ''
  let inFence = false
  for (const line of lines) {
    const t = line.trim()
    if (t.startsWith('```')) {
      inFence = !inFence
      continue
    }
    if (inFence || !t) continue
    // 跳过标题/引用/表格/有序与无序列表行；列表标记要求其后跟空格，
    // 避免误跳过以 **加粗** 等行内标记开头的正文段落
    if (/^(#{1,6}\s|>|\||[-*+]\s|\d+\.\s)/.test(t)) continue
    description = t
      .replace(/`([^`]*)`/g, '$1')
      .replace(/\*\*?([^*]*)\*\*?/g, '$1')
      .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
      .trim()
    break
  }
  if (description.length > 120) description = description.slice(0, 117) + '...'
  return { title, description }
}

function rewriteLinks(content) {
  // 仅改写指向本地 .md 的相对链接目标中的下划线（保留 #fragment，跳过外链）
  return content.replace(/\]\(([^)#]+\.md)(#[^)]*)?\)/g, (full, target, frag = '') => {
    if (/^[a-z]+:\/\//i.test(target)) return full // 外部 URL 不改写
    return `](${target.replace(/_/g, '-')}${frag})`
  })
}

function yamlQuote(value) {
  // 生成安全的 YAML 双引号标量。
  // YAML 规范要求双引号标量内必须转义：反斜杠、双引号、换行符（\n）、回车符（\r）。
  // 当前 extractMeta 逐行处理，title/description 理论上不含换行；
  // 此处仍做防御性转义，确保未来若源文件出现 CRLF 或异常内容时不会生成破损的 frontmatter。
  return `"${String(value)
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    .replace(/\n/g, '\\n')
    .replace(/\r/g, '\\r')}"`
}

function main() {
  if (!fs.existsSync(sourceDir)) {
    console.error(`[sync-docs] 源目录不存在: ${sourceDir}`)
    process.exit(1)
  }
  // 生成前清空旧的生成页，避免源文档重命名/删除后本地残留孤儿页面
  fs.rmSync(websiteDocsDir, { recursive: true, force: true })
  fs.mkdirSync(websiteDocsDir, { recursive: true })

  const files = fs.readdirSync(sourceDir).filter((f) => f.endsWith('.md'))
  const generated = []
  for (const file of files) {
    const raw = fs.readFileSync(path.join(sourceDir, file), 'utf8')
    const { title, description } = extractMeta(raw)
    const slug = toSlug(file)
    const finalTitle = title || slug
    if (!title) {
      console.warn(`[sync-docs] warning: ${file} 无 # 标题，title 回退为 ${slug}`)
    }
    const fm = `---\ntitle: ${yamlQuote(finalTitle)}\ndescription: ${yamlQuote(description)}\n---\n\n`
    fs.writeFileSync(path.join(websiteDocsDir, `${slug}.md`), fm + rewriteLinks(raw))
    generated.push({ slug, title: finalTitle })
  }

  const quick = generated.find((g) => g.slug === 'quick-start')
  const core = generated.filter((g) => g.slug !== 'quick-start')
  // 排序策略：在 CORE_ORDER 中出现的文档按其下标排列；
  // 未列出的新文档（indexOf 返回 -1）统一映射到 MAX_SAFE_INTEGER，
  // 追加到末尾并按 slug 字典序互相排列，实现「加文档零改动 CORE_ORDER」。
  core.sort((a, b) => {
    const ia = CORE_ORDER.indexOf(a.slug)
    const ib = CORE_ORDER.indexOf(b.slug)
    const ra = ia === -1 ? Number.MAX_SAFE_INTEGER : ia
    const rb = ib === -1 ? Number.MAX_SAFE_INTEGER : ib
    return ra !== rb ? ra - rb : a.slug.localeCompare(b.slug)
  })

  const sidebar = []
  if (quick) {
    sidebar.push({ text: '快速开始', items: [{ text: quick.title, link: '/docs/quick-start' }] })
  }
  if (core.length > 0) {
    sidebar.push({
      text: '核心功能',
      items: core.map((g) => ({ text: g.title, link: `/docs/${g.slug}` })),
    })
  }

  const out =
    '// 此文件由 website/scripts/sync-docs.mjs 自动生成，请勿手工编辑。\n' +
    `export const docsSidebar = ${JSON.stringify(sidebar, null, 2)}\n`
  fs.writeFileSync(sidebarOut, out)
  console.log(`[sync-docs] 已生成 ${generated.length} 个页面 + 侧边栏 (${sidebarOut})`)
}

main()
