package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gowhale/internal/config"
)

// Message 是一条对话消息。
// role 为 system / user / assistant / tool。
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // assistant 发起的工具调用
	ToolCallID string     `json:"tool_call_id,omitempty"` // tool 角色：对应的调用 ID
}

// ToolCall 是模型要求执行的一次工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall 里的 Arguments 是一段 JSON 字符串。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool 是发给模型的工具定义（OpenAI function calling 格式）。
type Tool struct {
	Type     string             `json:"type"`
	Function ToolFunctionSchema `json:"function"`
}

type ToolFunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Usage 记录单次 API 调用的 token 消耗。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Client 封装对大模型的调用。
type Client struct {
	cfg  config.Config
	http *http.Client
}

func NewClient(cfg config.Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 5 * time.Minute}}
}

// Model 返回当前使用的模型名。
func (c *Client) Model() string { return c.cfg.Model }

// SetModel 动态切换模型（用于复杂度路由）。
func (c *Client) SetModel(m string) { c.cfg.Model = m }

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
}

// Chat 发送一轮对话（非流式）。带上 tools 后模型可能返回 tool_calls。
// 返回消息和 token 用量信息。
func (c *Client) Chat(messages []Message, tools []Tool) (Message, Usage, error) {
	body, err := json.Marshal(chatRequest{
		Model:    c.cfg.Model,
		Messages: messages,
		Tools:    tools,
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
	return parsed.Choices[0].Message, usage, nil
}

// formatTokens 将 token 数转为可读字符串。
func FormatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}
