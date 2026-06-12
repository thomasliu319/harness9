// 内置工具：WebSearch（网页搜索工具）。
//
// 使用 DuckDuckGo HTML 端点（html.duckduckgo.com/html/）执行搜索，
// 无需 API Key，零外部平台依赖。
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/harness9/internal/schema"
)

const (
	searchTimeout = 20 * time.Second
	ddgSearchURL  = "https://html.duckduckgo.com/html/"
)

// WebSearchTool 实现 BaseTool 接口，通过 DuckDuckGo 搜索互联网内容。
type WebSearchTool struct {
	// backendURL 默认为 ddgSearchURL，测试中可替换为 httptest 服务器地址。
	backendURL string
}

// NewWebSearchTool 创建默认使用 DuckDuckGo 的搜索工具实例。
func NewWebSearchTool() *WebSearchTool {
	return &WebSearchTool{backendURL: ddgSearchURL}
}

func (t *WebSearchTool) Name() string { return "web_search" }

// Definition 返回 web_search 工具的 schema 定义。
func (t *WebSearchTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name: t.Name(),
		Description: "在互联网上搜索信息，返回标题、URL 和摘要列表。" +
			"使用 DuckDuckGo，无需 API Key。" +
			"搜索后可使用 web_fetch 抓取具体页面内容。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "搜索词，建议使用英文以获得更好效果",
				},
				"max_results": map[string]interface{}{
					"type":        "integer",
					"description": "返回结果数量（默认 5，最大 10）",
				},
			},
			"required": []string{"query"},
		},
	}
}

type webSearchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

// Execute 解析参数、执行搜索并格式化输出。
func (t *WebSearchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input webSearchArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("parse args failed: %w", err)
	}
	if input.Query == "" {
		return "Error: query parameter is required", nil
	}

	maxResults := input.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}

	results, err := t.duckDuckGoSearch(ctx, input.Query, maxResults)
	if err != nil {
		return fmt.Sprintf("Error: search failed — %v", err), nil
	}
	if len(results) == 0 {
		return "未找到相关结果。", nil
	}

	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("[%d] %s\n", i+1, r.Title))
		sb.WriteString(fmt.Sprintf("URL: %s\n", r.URL))
		if r.Snippet != "" {
			sb.WriteString(fmt.Sprintf("摘要: %s\n", r.Snippet))
		}
		if i < len(results)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String(), nil
}

// duckDuckGoSearch 向 DDG HTML 端点发送 POST 请求并解析结果。
func (t *WebSearchTool) duckDuckGoSearch(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
	formData := url.Values{"q": {query}}

	reqCtx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.backendURL,
		strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", webUserAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	return parseDDGResults(string(body), maxResults), nil
}

// parseDDGResults 解析 DuckDuckGo HTML 响应，提取搜索结果列表。
func parseDDGResults(htmlBody string, maxResults int) []searchResult {
	doc, err := html.Parse(strings.NewReader(htmlBody))
	if err != nil {
		return nil
	}

	var results []searchResult
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(results) >= maxResults {
			return
		}
		if n.Type == html.ElementNode && n.Data == "div" &&
			hasHTMLClass(n, "result") && !hasHTMLClass(n, "result--more") {
			r := extractDDGResult(n)
			if r.Title != "" && r.URL != "" {
				results = append(results, r)
				return
			}
		}
		for c := n.FirstChild; c != nil && len(results) < maxResults; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return results
}

// extractDDGResult 从单个 result div 节点中提取标题、URL 和摘要。
func extractDDGResult(resultNode *html.Node) searchResult {
	var r searchResult
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "a" && hasHTMLClass(n, "result__a") {
				r.Title = strings.TrimSpace(textContent(n))
				for _, a := range n.Attr {
					if a.Key == "href" {
						r.URL = decodeUDDG(a.Val)
					}
				}
			}
			if n.Data == "a" && hasHTMLClass(n, "result__snippet") {
				r.Snippet = strings.TrimSpace(textContent(n))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(resultNode)
	return r
}

// decodeUDDG 从 DuckDuckGo 重定向 href 中提取真实目标 URL。
// DDG 的链接格式：/l/?uddg=ENCODED_URL&rut=...
func decodeUDDG(href string) string {
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if uddg := u.Query().Get("uddg"); uddg != "" {
		decoded, err := url.QueryUnescape(uddg)
		if err == nil {
			return decoded
		}
	}
	return href
}

// hasHTMLClass 检查节点是否包含指定 CSS 类名。
func hasHTMLClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, c := range strings.Fields(a.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// textContent 递归提取节点下所有文本内容。
func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}
