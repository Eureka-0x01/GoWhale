package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// SearchTool 联网搜索。优先用 DuckDuckGo JSON API，不可达时退到 HTML lite。
type SearchTool struct{}

func (SearchTool) Name() string                   { return "web_search" }
func (SearchTool) Review(json.RawMessage) Decision { return Decision{} }

func (SearchTool) Description() string {
	return "联网搜索，返回摘要和链接。用于查最新文档、错误解决方案等。"
}

func (SearchTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "搜索关键词，空格分隔。如 'golang json unmarshal example'",
			},
		},
		"required": []string{"query"},
	}
}

func (SearchTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	q := strings.TrimSpace(p.Query)
	if q == "" {
		return "", fmt.Errorf("query 不能为空")
	}

	// 方案 A: DuckDuckGo JSON API（国外快，国内可能慢/不通）
	result, err := searchDDGJSON(q)
	if err == nil {
		return result, nil
	}

	// 方案 B: 百度（国内可用）
	return searchBaidu(q)
}

func searchDDGJSON(query string) (string, error) {
	apiURL := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) +
		"&format=json&no_html=1&skip_disambig=1&t=aicode"
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var ddg struct {
		Abstract    string `json:"Abstract"`
		AbstractURL string `json:"AbstractURL"`
		Answer      string `json:"Answer"`
		RelatedTopics []struct {
			Text     string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
	}
	if err := json.Unmarshal(body, &ddg); err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("搜索: %s\n\n", query))
	if ddg.Answer != "" {
		out.WriteString(fmt.Sprintf("📌 答案: %s\n\n", ddg.Answer))
	}
	if ddg.Abstract != "" {
		out.WriteString(fmt.Sprintf("📄 %s\n   链接: %s\n\n", ddg.Abstract, ddg.AbstractURL))
	}
	for i, t := range ddg.RelatedTopics {
		if t.Text == "" || i >= 6 {
			break
		}
		out.WriteString(fmt.Sprintf("🔗 %s\n   链接: %s\n", cleanHTML(t.Text), t.FirstURL))
	}
	if out.Len() == 0 {
		return "", fmt.Errorf("无结果")
	}
	return out.String(), nil
}

func searchDDGLite(query string) (string, error) {
	searchURL := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(query)
	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", searchURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; aicode/1.0)")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// 用正则从 lite HTML 提取结果
	linkRe := regexp.MustCompile(`<a[^>]*href="([^"]*)"[^>]*class="result-link"[^>]*>([^<]*)</a>`)
	snippetRe := regexp.MustCompile(`<span class="result-snippet">([^<]*)</span>`)

	links := linkRe.FindAllStringSubmatch(html, -1)
	snippets := snippetRe.FindAllStringSubmatch(html, -1)

	if len(links) == 0 {
		return "", fmt.Errorf("无结果")
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("搜索: %s\n\n", query))
	for i, m := range links {
		if i >= 6 {
			break
		}
		title := strings.TrimSpace(m[2])
		href := m[1]
		if !strings.HasPrefix(href, "http") {
			href = "https:" + href
		}
		snip := ""
		if i < len(snippets) {
			snip = strings.TrimSpace(snippets[i][1])
		}
		out.WriteString(fmt.Sprintf("🔗 %s\n   链接: %s\n   %s\n\n", title, href, snip))
	}
	return out.String(), nil
}

func searchBaidu(query string) (string, error) {
	searchURL := "https://www.baidu.com/s?wd=" + url.QueryEscape(query)
	client := &http.Client{Timeout: 12 * time.Second}
	req, _ := http.NewRequest("GET", searchURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	// 百度 HTML 结果: <h3 class="t"><a href="URL">Title</a></h3> ... <span class="content-right_...">snippet</span>
	titleRe := regexp.MustCompile(`<h3[^>]*class="[^"]*t[^"]*"[^>]*>\s*<a[^>]*href="([^"]*)"[^>]*>([^<]*)</a>`)
	// snippet 在 <div class="c-abstract"> 或 <span class="content-right_...">
	snipRe := regexp.MustCompile(`<span[^>]*class="content-right_[^"]*"[^>]*>([^<]*)</span>`)

	titles := titleRe.FindAllStringSubmatch(html, -1)
	snippets := snipRe.FindAllStringSubmatch(html, -1)

	var out strings.Builder
	out.WriteString(fmt.Sprintf("搜索: %s\n\n", query))
	count := 0
	for i, m := range titles {
		if count >= 6 {
			break
		}
		title := cleanHTML(strings.TrimSpace(m[2]))
		href := m[1]
		snip := ""
		if i < len(snippets) {
			snip = cleanHTML(strings.TrimSpace(snippets[i][1]))
		}
		if title == "" {
			continue
		}
		out.WriteString(fmt.Sprintf("🔗 %s\n   链接: %s\n   %s\n\n", title, href, snip))
		count++
	}
	if count == 0 {
		return "", fmt.Errorf("未找到结果（百度返回的 HTML 格式可能已变更）")
	}
	return out.String(), nil
}

// cleanHTML 移除 HTML 标签。
func cleanHTML(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

