package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"gowhale/internal/config"

	"gowhale/internal/agent"
)

type executeTaskMsg string

func (m *Model) Init() tea.Cmd {
	tasks := m.agent.LastTasks(10)
	for _, t := range tasks {
		m.messages = append(m.messages, agent.Event{
			Type:    agent.EventType(999),
			Message: fmt.Sprintf("[%s] %s", t.Time, t.Task),
		})
	}
	if len(m.messages) > 0 {
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
	}
	if m.initialTask != "" {
		return tea.Batch(textarea.Blink, func() tea.Msg { return executeTaskMsg(m.initialTask) })
	}
	return textarea.Blink
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 8
		m.input.SetWidth(msg.Width - 4)
		if m.Sidebar.Visible {
			m.viewport.Width = msg.Width - 24 - 2
		}
		return m, nil

	case executeTaskMsg:
		task := string(msg)
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: task})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		m.Footer.SetWorking("执行中")
		m.events = m.agent.RunAsync(task)
		return m, m.waitForEvent()

	case tea.KeyMsg:
		if m.pendingApproval != nil {
			switch msg.String() {
			case "y":
				m.pendingApproval.Callback <- agent.ApprovalReply{Allowed: true}
				m.pendingApproval = nil
				m.approvalWarning = ""
				return m, m.waitForEvent()
			case "a":
				m.pendingApproval.Callback <- agent.ApprovalReply{Allowed: true, Permanent: true}
				m.pendingApproval = nil
				m.approvalWarning = ""
				return m, m.waitForEvent()
			case "n", "enter", "esc":
				m.pendingApproval.Callback <- agent.ApprovalReply{Allowed: false}
				m.pendingApproval = nil
				m.approvalWarning = ""
				return m, m.waitForEvent()
			default:
				return m, nil
			}
		}

		if m.showCommands {
			switch msg.String() {
			case "up", "ctrl+p":
				matched := filterCommands(m.input.Value())
				if len(matched) > 0 {
					m.commandIdx--
					if m.commandIdx < 0 {
						m.commandIdx = len(matched) - 1
					}
				}
				return m, nil
			case "down", "ctrl+n":
				matched := filterCommands(m.input.Value())
				if len(matched) > 0 {
					m.commandIdx++
					if m.commandIdx >= len(matched) {
						m.commandIdx = 0
					}
				}
				return m, nil
			case "esc":
				m.showCommands = false
				m.commandIdx = 0
				return m, nil
			}
		}

		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.Sidebar.Toggle()
			m.viewport.Width = m.width
			if m.Sidebar.Visible {
				m.viewport.Width = m.width - 24 - 2
			}
			m.viewport.SetContent(m.renderMessages())
			return m, nil
		case "ctrl+w":
			m.Sidebar.CycleMode()
			return m, nil

		case "enter":
			if m.showCommands {
				matched := filterCommands(m.input.Value())
				if m.commandIdx >= 0 && m.commandIdx < len(matched) {
					m.input.Reset()
					m.input.SetValue(matched[m.commandIdx] + " ")
					m.showCommands = false
					m.commandIdx = 0
					return m, textarea.Blink
				}
				m.showCommands = false
				m.commandIdx = 0
				return m, nil
			}

			input := strings.TrimSpace(m.input.Value())
			if input == "" {
				return m, nil
			}
			m.input.Reset()

			if strings.HasPrefix(input, "/") {
				m.handleCommand(input)
				m.viewport.SetContent(m.renderMessages())
				m.viewport.GotoBottom()
				return m, nil
			}

			m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: input})
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			m.Footer.SetWorking("执行中")
			m.events = m.agent.RunAsync(input)
			return m, m.waitForEvent()

		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
			m.showCommands = strings.HasPrefix(strings.TrimSpace(m.input.Value()), "/")
			if m.showCommands {
				m.commandIdx = 0
			}
		}

	case agent.Event:
		switch msg.Type {
		case agent.EventDone:
			m.events = nil
			m.messages = append(m.messages, msg)
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			m.Footer.SetWorking("  ✅ 完成")
			m.Footer.SetTokens(msg.TokenCount)
			return m, nil
		case agent.EventError:
			m.events = nil
			m.messages = append(m.messages, msg)
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			m.Footer.SetWorking("  ✗ 错误")
			m.Footer.SetTokens(msg.TokenCount)
			return m, nil
		case agent.EventApprovalRequest:
			m.pendingApproval = msg.ApprovalRequest
			m.approvalWarning = msg.ApprovalRequest.Warning
			m.viewport.SetContent(m.renderMessages())
			return m, nil
		case agent.EventThinking:
			m.Footer.SetWorking("⏳ 思考中...")
		case agent.EventToolCall:
			m.lastCallCount = msg.CallCount
			m.Sidebar.AddStep(msg.Step, msg.ToolName, "pending", "")
			m.messages = append(m.messages, msg)
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
		case agent.EventToolResult:
			status := "ok"
			if msg.IsError {
				status = "err"
			}
			m.Sidebar.AddStep(msg.Step, msg.ToolName, status, firstLine(msg.ToolResult))
			m.messages = append(m.messages, msg)
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			m.Sidebar.SetModel(m.agent.ModelName(), m.agent.TokenCount())
			m.Sidebar.SetBudget(m.lastCallCount, 40)
		default:
			m.messages = append(m.messages, msg)
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
		}
		if m.events != nil {
			return m, m.waitForEvent()
		}
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.events
		if !ok {
			return agent.Event{Type: agent.EventDone}
		}
		return ev
	}
}

func (m *Model) handleCommand(cmd string) {
	switch strings.ToLower(cmd) {
	case "/help":
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999),
			Message: "/help  /model  /history  /clear  /compact  /ollama  /deepseek  /exit"})
	case "/model":
		_, _, model, proModel := m.agent.ProviderInfo()
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999),
			Message: fmt.Sprintf("当前: %s / %s", model, proModel)})
	case "/history":
		tasks := m.agent.LastTasks(10)
		var sb strings.Builder
		for _, t := range tasks {
			sb.WriteString(fmt.Sprintf("[%s] %s\n", t.Time, t.Task))
		}
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: sb.String()})
	case "/clear":
		m.messages = nil
		m.Footer.SetWorking("  ✅ 已清空")
	case "/compact":
		m.agent.Compact()
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: "✓ 上下文已压缩"})
	case "/ollama":
		cfg := config.Load()
		m.agent.SwitchProvider(cfg.OllamaURL, "ollama", cfg.OllamaModel, cfg.OllamaModel)
		config.SaveProvider("ollama")
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999),
			Message: fmt.Sprintf("✓ Ollama: %s", cfg.OllamaModel)})
	case "/deepseek":
		config.SaveProvider("deepseek")
		cfg := config.Load()
		m.agent.SwitchProvider(cfg.BaseURL, cfg.APIKey, cfg.Model, cfg.ProModel)
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999),
			Message: fmt.Sprintf("✓ DeepSeek: %s", cfg.Model)})
	case "/exit":
		os.Exit(0)
	}
}

var cmdDesc = map[string]string{
	"/help":    "帮助信息",
	"/model":   "查看当前模型",
	"/history": "查看最近对话记录",
	"/clear":   "清空对话历史",
	"/compact": "压缩上下文节省 token",
	"/ollama":  "切换使用 Ollama 本地模型",
	"/deepseek": "切换使用 DeepSeek 云端模型",
	"/exit":    "退出程序",
}
var cmdList []string

func init() {
	for k := range cmdDesc {
		cmdList = append(cmdList, k)
	}
}

func filterCommands(prefix string) []string {
	prefix = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(prefix), "/"))
	var result []string
	for _, c := range cmdList {
		if strings.HasPrefix(strings.TrimPrefix(c, "/"), prefix) {
			result = append(result, c)
		}
	}
	return result
}

func renderCommandHints(prefix string, width int, idx int) string {
	matched := filterCommands(prefix)
	if len(matched) == 0 {
		return ""
	}
	var lines []string
	for i, name := range matched {
		line := fmt.Sprintf("  %-12s %s", name, dim(cmdDesc[name]))
		if i == idx {
			line = "\033[7m" + line + "\033[0m"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\n"
}
