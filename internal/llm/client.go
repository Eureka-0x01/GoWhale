package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"gowhale/internal/config"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string             `json:"type"`
	Function ToolFunctionSchema `json:"function"`
}

type ToolFunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Client struct {
	cfg  config.Config
	http *http.Client
}

func NewClient(cfg config.Config) *Client {
	// 2 分钟超时：正常响应几秒内完成，超时快速失败并报错，避免长时间"卡住"。
	return &Client{cfg: cfg, http: &http.Client{Timeout: 2 * time.Minute}}
}

func (c *Client) Model() string    { return c.cfg.Model }
func (c *Client) BaseURL() string  { return c.cfg.BaseURL }
func (c *Client) SetModel(m string)  { c.cfg.Model = m }
func (c *Client) SetBaseURL(u string) { c.cfg.BaseURL = u }
func (c *Client) SetAPIKey(k string)  { c.cfg.APIKey = k }

// SwitchTo 一键切换提供商
func (c *Client) SwitchTo(baseURL, apiKey, model, proModel string) {
	c.cfg.BaseURL = baseURL
	c.cfg.APIKey = apiKey
	c.cfg.Model = model
	c.cfg.ProModel = proModel
}

type chatRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	Tools     []Tool    `json:"tools,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Message
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
}

func (c *Client) Chat(messages []Message, tools []Tool) (Message, Usage, error) {
	body, err := json.Marshal(chatRequest{
		Model:     c.cfg.Model,
		Messages:  messages,
		Tools:     tools,
		MaxTokens: 8192,
	})
	if err != nil {
		return Message{}, Usage{}, err
	}

	url := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Message{}, Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return Message{}, Usage{}, err
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		detail := strings.TrimSpace(string(raw))
		if readErr != nil {
			detail = fmt.Sprintf("(读取错误体失败: %v)", readErr)
		}
		return Message{}, Usage{}, fmt.Errorf("大模型返回错误 %d: %s", resp.StatusCode, detail)
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Message{}, Usage{}, fmt.Errorf("解析响应失败: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return Message{}, Usage{}, fmt.Errorf("模型未返回任何内容")
	}
	var usage Usage
	if parsed.Usage != nil {
		usage = *parsed.Usage
	}
	rawMsg := parsed.Choices[0].Message
	msg := rawMsg.Message

	// DeepSeek 思考模式下，调用工具时的说明文字在 reasoning_content 字段，
	// content 为空。把 reasoning 填入 content，让调用方（TUI/终端）能显示思考。
	if strings.TrimSpace(msg.Content) == "" && strings.TrimSpace(rawMsg.ReasoningContent) != "" {
		msg.Content = rawMsg.ReasoningContent
	}

	// 某些 Ollama 模型（如 qwen3-coder）返回 XML 格式的 tool_call，
	// 而不是标准 JSON tool_calls。在此兼容解析。
	if len(msg.ToolCalls) == 0 && strings.Contains(msg.Content, "</tool_call>") {
		msg.ToolCalls = ParseXMLToolCalls(msg.Content)
	}

	return msg, usage, nil
}

// parseXMLToolCalls 从 XML 格式的 tool_call 块中提取 ToolCall 列表。
// Ollama 的 qwen3-coder 等模型可能返回如下格式：
//
//	<tool_call>
//	{"name": "read_file", "arguments": {"path": "main.go"}}
//	</tool_call>
func ParseXMLToolCalls(content string) []ToolCall {
	var calls []ToolCall

	// 格式 1: <tool_call>{"name": "xxx", "arguments": {...}}</tool_call>
	re1 := regexp.MustCompile(`<tool_call>\s*\n?(.*?)\n?\s*</tool_call>`)
	for i, m := range re1.FindAllStringSubmatch(content, -1) {
		if len(m) < 2 {
			continue
		}
		if c := parseToolCallJSON(strings.TrimSpace(m[1]), i); c != nil {
			calls = append(calls, *c)
		}
	}

	// 格式 2: <function=NAME> <parameter=KEY> VAL </parameter> </function>
	// qwen3-coder 常见非标准输出
	if len(calls) > 0 {
		return calls // 格式 1 成功就不再尝试格式 2
	}
	re2 := regexp.MustCompile(`<function=(\w+)>(.*?)</function>`)
	for i, m := range re2.FindAllStringSubmatch(content, -1) {
		if len(m) < 3 {
			continue
		}
		name := m[1]
		params := m[2]
		args := parseFunctionParams(params)
		calls = append(calls, ToolCall{
			ID:   fmt.Sprintf("xml_%d", i),
			Type: "function",
			Function: FunctionCall{
				Name:      name,
				Arguments: args,
			},
		})
	}
	return calls
}

// parseToolCallJSON 解析 tool_call 内的 JSON，返回 ToolCall。
func parseToolCallJSON(jsonStr string, idx int) *ToolCall {
	var tc struct {
		Name      string         `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &tc); err != nil {
		return nil
	}
	return &ToolCall{
		ID:   fmt.Sprintf("xml_%d", idx),
		Type: "function",
		Function: FunctionCall{
			Name:      tc.Name,
			Arguments: string(tc.Arguments),
		},
	}
}

// parseFunctionParams 解析 qwen 风格 <parameter=KEY> VAL </parameter>，返回 JSON 参数字符串。
func parseFunctionParams(params string) string {
	re := regexp.MustCompile(`<parameter=(\w+)>\s*(.*?)\s*</parameter>`)
	matches := re.FindAllStringSubmatch(params, -1)
	if len(matches) == 0 {
		return "{}"
	}
	// 构建 JSON 对象
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		key := m[1]
		val := strings.TrimSpace(m[2])
		// 如果值不是以 [ 或 { 开头，包裹为字符串；否则保持 JSON 原样
		if val == "" {
			val = "\"\""
		} else if val[0] != '{' && val[0] != '[' && val[0] != '"' && val != "true" && val != "false" && val != "null" {
			// 尝试作为数字，否则作为字符串
			if _, err := fmt.Sscanf(val, "%f", new(float64)); err != nil {
				val = fmt.Sprintf("%q", val)
			}
		}
		parts = append(parts, fmt.Sprintf("%q:%s", key, val))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
func FormatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}
