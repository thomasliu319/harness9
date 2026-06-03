package ltm

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/harness9/internal/logfmt"
	"github.com/harness9/internal/schema"
)

// Generator 抽象提取所需的 LLM 调用能力（与 memory.Summarizer 同形）。
type Generator interface {
	Generate(ctx context.Context, messages []schema.Message, tools []schema.ToolDefinition) (*schema.Message, *schema.Usage, error)
}

const extractTimeout = 60 * time.Second

const extractSystemPrompt = `你是长期记忆提取器。从对话中提取值得跨会话长期保留的事实` +
	`（用户偏好、稳定的项目知识、关键决策、可复用技能），忽略一次性的临时上下文。` +
	`仅输出 JSON 数组，每个元素形如 {"title","content","category","importance"}，` +
	`category ∈ {knowledge,preference,task,skill}，importance 为 0-10 整数。` +
	`没有值得保留的内容时输出 []。不要输出任何解释。`

// Extractor 在上下文压缩前用 LLM 从 head 消息提取持久事实并写入 Store。
// 实现 memory.MemoryExtractor 接口（Extract 方法）。所有错误 fail-open。
type Extractor struct {
	gen   Generator
	store *Store
}

// NewExtractor 创建绑定到指定 Generator 与 Store 的提取器。
func NewExtractor(gen Generator, store *Store) *Extractor {
	return &Extractor{gen: gen, store: store}
}

// extractedFact 是 LLM 返回的单条事实的解析结构。
type extractedFact struct {
	Title      string `json:"title"`
	Content    string `json:"content"`
	Category   string `json:"category"`
	Importance int    `json:"importance"`
}

// Extract 从 msgs 提取持久事实并 upsert 到 Store。任何环节出错仅记日志，不阻断调用方。
func (e *Extractor) Extract(msgs []schema.Message) {
	if e.gen == nil || e.store == nil || len(msgs) == 0 {
		return
	}
	convo := renderConversation(msgs)
	if strings.TrimSpace(convo) == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
	defer cancel()

	resp, _, err := e.gen.Generate(ctx, []schema.Message{
		{Role: schema.RoleSystem, Content: extractSystemPrompt},
		{Role: schema.RoleUser, Content: "对话：\n" + convo},
	}, nil)
	if err != nil || resp == nil {
		log.Print(logfmt.FormatMsg("ltm", "压缩前提取失败（fail-open）"))
		return
	}

	facts, err := parseFacts(resp.Content)
	if err != nil {
		log.Print(logfmt.FormatMsg("ltm", "提取结果解析失败（fail-open）"))
		return
	}
	for _, f := range facts {
		if strings.TrimSpace(f.Content) == "" {
			continue
		}
		if _, err := e.store.Add(ctx, &Entry{
			Title:      f.Title,
			Content:    f.Content,
			Category:   Category(f.Category),
			Importance: f.Importance,
		}); err != nil {
			log.Print(logfmt.FormatMsg("ltm", "提取条目写入失败（fail-open）"))
		}
	}
}

// renderConversation 将消息扁平化为文本，供提取 prompt 使用。
func renderConversation(msgs []schema.Message) string {
	var lines []string
	for _, m := range msgs {
		if m.Content == "" {
			continue
		}
		lines = append(lines, string(m.Role)+": "+m.Content)
	}
	return strings.Join(lines, "\n")
}

// parseFacts 解析 LLM 输出的 JSON 数组，容忍 ```json ``` 代码围栏包裹。
func parseFacts(out string) ([]extractedFact, error) {
	s := strings.TrimSpace(out)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	var facts []extractedFact
	if err := json.Unmarshal([]byte(s), &facts); err != nil {
		return nil, err
	}
	return facts, nil
}
