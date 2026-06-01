// sync-docs 单元测试（零依赖，仅用 Node 内置断言模块）。
// 运行方式：node website/scripts/sync-docs.test.mjs
// 验收标准：所有断言通过，打印 "All tests passed." 并以 0 退出；
//           任何断言失败则立即抛出并以非零退出码结束，使 CI 可检测失败。
//
// 注意：本文件仅测试纯函数逻辑（toSlug / extractMeta / rewriteLinks / yamlQuote）。
// 端到端验证（文件读写 + VitePress 构建）以 `npm run build` 跑通为准（见 spec）。

import assert from 'node:assert/strict'

// ─────────────────────────────────────────────────────────────────────────────
// 从脚本中复制的纯函数（与 sync-docs.mjs 保持同步；如修改原函数需同步更新此处）
// ─────────────────────────────────────────────────────────────────────────────

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
  return content.replace(/\]\(([^)#]+\.md)(#[^)]*)?\)/g, (full, target, frag = '') => {
    if (/^[a-z]+:\/\//i.test(target)) return full
    return `](${target.replace(/_/g, '-')}${frag})`
  })
}

function yamlQuote(value) {
  return `"${String(value)
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    .replace(/\n/g, '\\n')
    .replace(/\r/g, '\\r')}"`
}

// ─────────────────────────────────────────────────────────────────────────────
// toSlug
// ─────────────────────────────────────────────────────────────────────────────

assert.equal(toSlug('quick_start.md'), 'quick-start', 'toSlug: 下划线文件名转连字符')
assert.equal(toSlug('agent-loop.md'), 'agent-loop', 'toSlug: 已是连字符保持不变')
assert.equal(toSlug('tool_calling.md'), 'tool-calling', 'toSlug: 多下划线均替换')
assert.equal(toSlug('cli.md'), 'cli', 'toSlug: 无下划线简单文件名')

// ─────────────────────────────────────────────────────────────────────────────
// extractMeta — title 提取
// ─────────────────────────────────────────────────────────────────────────────

// 正常：首行即 # 标题
assert.equal(
  extractMeta('# My Title\n\nSome paragraph.').title,
  'My Title',
  'extractMeta.title: 正常提取',
)

// 标题在正文段落之后
assert.equal(
  extractMeta('Some intro.\n\n# Late Title\n').title,
  'Late Title',
  'extractMeta.title: 非首行标题',
)

// 代码块内的 # 不算标题
assert.equal(
  extractMeta('```\n# not a title\n```\n# Real Title\n').title,
  'Real Title',
  'extractMeta.title: 代码块内 # 不提取',
)

// 无标题时回退空字符串
assert.equal(extractMeta('no heading here\n').title, '', 'extractMeta.title: 无标题返回空串')

// 标题前后有空格应被 trim
assert.equal(
  extractMeta('#  Padded Title  \n').title,
  'Padded Title',
  'extractMeta.title: 标题两端空白 trim',
)

// ─────────────────────────────────────────────────────────────────────────────
// extractMeta — description 提取
// ─────────────────────────────────────────────────────────────────────────────

// 正常：首个非空非标题段落
assert.equal(
  extractMeta('# Title\n\nFirst paragraph.').description,
  'First paragraph.',
  'extractMeta.description: 正常提取',
)

// 跳过标题行、取正文
assert.equal(
  extractMeta('# Title\n## Sub\nActual desc.').description,
  'Actual desc.',
  'extractMeta.description: 跳过次级标题',
)

// 跳过列表行（带空格的列表标记）
assert.equal(
  extractMeta('# Title\n- item\nReal desc.').description,
  'Real desc.',
  'extractMeta.description: 跳过列表行',
)

// 加粗/代码/链接标记应被剥离
assert.equal(
  extractMeta('# T\n\n**bold** `code` [link](url)').description,
  'bold code link',
  'extractMeta.description: 剥离 markdown 标记',
)

// 超过 120 字截断
const longText = 'a'.repeat(130)
const { description: truncated } = extractMeta(`# T\n\n${longText}`)
assert.equal(truncated.length, 120, 'extractMeta.description: 超 120 字截断到 120')
assert.ok(truncated.endsWith('...'), 'extractMeta.description: 截断末尾有 ...')

// 代码块内的段落不作为 description
assert.equal(
  extractMeta('# T\n```\ncode paragraph\n```\nOutside.').description,
  'Outside.',
  'extractMeta.description: 跳过代码块内内容',
)

// ** 开头的加粗段落（行内标记）不应被 skip（不同于 - 列表标记）
assert.equal(
  extractMeta('# T\n\n**Bold paragraph** text.').description,
  'Bold paragraph text.',
  'extractMeta.description: ** 开头行不被列表规则跳过',
)

// ─────────────────────────────────────────────────────────────────────────────
// rewriteLinks
// ─────────────────────────────────────────────────────────────────────────────

// 下划线文件名转连字符
assert.equal(
  rewriteLinks('[link](file_name.md)'),
  '[link](file-name.md)',
  'rewriteLinks: 下划线转连字符',
)

// 带 fragment
assert.equal(
  rewriteLinks('[link](tool_calling.md#section)'),
  '[link](tool-calling.md#section)',
  'rewriteLinks: 保留 fragment',
)

// 外部 URL 不改写
assert.equal(
  rewriteLinks('[ext](https://example.com/file_name.md)'),
  '[ext](https://example.com/file_name.md)',
  'rewriteLinks: 外链不改写',
)

// 已是连字符不变
assert.equal(
  rewriteLinks('[link](agent-loop.md)'),
  '[link](agent-loop.md)',
  'rewriteLinks: 已是连字符保持不变',
)

// http 外链也不改写
assert.equal(
  rewriteLinks('[ext](http://example.com/file_a.md)'),
  '[ext](http://example.com/file_a.md)',
  'rewriteLinks: http 外链不改写',
)

// ─────────────────────────────────────────────────────────────────────────────
// yamlQuote
// ─────────────────────────────────────────────────────────────────────────────

// 普通字符串
assert.equal(yamlQuote('hello world'), '"hello world"', 'yamlQuote: 普通字符串')

// 含双引号
assert.equal(yamlQuote('say "hi"'), '"say \\"hi\\""', 'yamlQuote: 双引号转义')

// 含反斜杠
assert.equal(yamlQuote('path\\file'), '"path\\\\file"', 'yamlQuote: 反斜杠转义')

// 含换行符（防御性转义）
assert.equal(yamlQuote('line1\nline2'), '"line1\\nline2"', 'yamlQuote: 换行符转义')

// 含回车符（CRLF 防御）
assert.equal(yamlQuote('line1\rline2'), '"line1\\rline2"', 'yamlQuote: 回车符转义')

// 空字符串
assert.equal(yamlQuote(''), '""', 'yamlQuote: 空字符串')

// 含冒号（YAML 裸标量中需引号，已由 quote 包裹）
assert.equal(yamlQuote('harness9: A Framework'), '"harness9: A Framework"', 'yamlQuote: 含冒号')

// 中文不受影响
assert.equal(yamlQuote('Agent Loop 核心实现原理'), '"Agent Loop 核心实现原理"', 'yamlQuote: 中文')

// ─────────────────────────────────────────────────────────────────────────────

console.log('All tests passed.')
