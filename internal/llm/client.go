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
	return &Client{cfg: cfg, http: &http.Client{Timeout: 5 * time.Minute}}
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

func FormatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}
