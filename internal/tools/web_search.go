// 内置工具：WebSearch（网页搜索工具）。
//
// 支持 DuckDuckGo（默认，无需 API Key）和 Tavily Search API（需配置 TAVILY_API_KEY）。
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/harness9/internal/schema"
)

const (
	searchTimeout     = 20 * time.Second
	searchDialTimeout = 10 * time.Second
	ddgSearchURL      = "https://html.duckduckgo.com/html/"
	tavilySearchURL   = "https://api.tavily.com/search"
)

// searchClient 是专用于 web_search 的 HTTP 客户端，配置了 dial 超时以避免
// 底层 TCP 握手阶段因 context cancel 响应不及时而长时间阻塞。
var searchClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: searchDialTimeout,
		}).DialContext,
	},
}

// WebSearchTool 实现 BaseTool 接口，通过 DuckDuckGo（默认）或 Tavily（配置 API Key 时）搜索互联网内容。
type WebSearchTool struct {
	// backendURL 默认为 ddgSearchURL 或 tavilySearchURL，测试中可替换为 httptest 服务器地址。
	backendURL string
	// tavilyAPIKey 为空时使用 DuckDuckGo，非空时使用 Tavily Search API。
	tavilyAPIKey string
}

// NewWebSearchTool 创建搜索工具实例。
// 若环境变量 TAVILY_API_KEY 已设置，优先使用 Tavily Search API，否则使用 DuckDuckGo。
func NewWebSearchTool() *WebSearchTool {
	apiKey := os.Getenv("TAVILY_API_KEY")
	t := &WebSearchTool{backendURL: ddgSearchURL}
	if apiKey != "" {
		t.backendURL = tavilySearchURL
		t.tavilyAPIKey = apiKey
	}
	return t
}

func (t *WebSearchTool) Name() string { return "web_search" }

// Definition 返回 web_search 工具的 schema 定义。
func (t *WebSearchTool) Definition() schema.ToolDefinition {
	desc := "在互联网上搜索信息，返回标题、URL 和摘要列表。" +
		"搜索后可使用 web_fetch 抓取具体页面内容。"

	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: desc,
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

	var results []searchResult
	var err error
	if t.tavilyAPIKey != "" {
		results, err = t.tavilySearch(ctx, input.Query, maxResults)
	} else {
		results, err = t.duckDuckGoSearch(ctx, input.Query, maxResults)
	}
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
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", webUserAgent)

	resp, err := searchClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from search backend", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	return parseDDGResults(string(body), maxResults), nil
}

// ---- Tavily Search API ----

type tavilyRequest struct {
	APIKey      string `json:"api_key"`
	Query       string `json:"query"`
	SearchDepth string `json:"search_depth"`
	MaxResults  int    `json:"max_results"`
}

type tavilyResponse struct {
	Results []tavilyResult `json:"results"`
}

type tavilyResult struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// tavilySearch 调用 Tavily Search API 执行搜索。
func (t *WebSearchTool) tavilySearch(ctx context.Context, query string, maxResults int) ([]searchResult, error) {
	body := tavilyRequest{
		APIKey:      t.tavilyAPIKey,
		Query:       query,
		SearchDepth: "basic",
		MaxResults:  maxResults,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, t.backendURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", webUserAgent)

	resp, err := searchClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyPreview, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("unexpected status %d from Tavily API: %s", resp.StatusCode, strings.TrimSpace(string(bodyPreview)))
	}

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var tavilyResp tavilyResponse
	if err := json.Unmarshal(respBytes, &tavilyResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	results := make([]searchResult, 0, len(tavilyResp.Results))
	for _, r := range tavilyResp.Results {
		results = append(results, searchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}
	return results, nil
}

// ---- DuckDuckGo HTML 解析 ----

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
