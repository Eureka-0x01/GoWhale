package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"gowhale/internal/agent"
)

func (m *Model) Init() tea.Cmd {
	return textarea.Blink
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6
		if m.Sidebar.Visible {
			m.viewport.Width = msg.Width - 24 - 2
		}
		return m, nil

	case tea.KeyMsg:
		// 审批模式
		if m.pendingApproval != nil {
			switch msg.String() {
			case "y":
				m.pendingApproval.Callback <- agent.ApprovalReply{Allowed: true}
				m.pendingApproval = nil
				m.approvalWarning = ""
				return m, nil
			case "a":
				m.pendingApproval.Callback <- agent.ApprovalReply{Allowed: true, Permanent: true}
				m.pendingApproval = nil
				m.approvalWarning = ""
				return m, nil
			case "n", "enter", "esc":
				m.pendingApproval.Callback <- agent.ApprovalReply{Allowed: false}
				m.pendingApproval = nil
				m.approvalWarning = ""
				return m, nil
			default:
				return m, nil
			}
		}

		// 正常模式
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

			// 用户消息
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
		help := "命令列表：\n" +
			"  /help     帮助信息\n" +
			"  /model    查看当前模型\n" +
			"  /clear    清空对话\n" +
			"  /compact  压缩上下文\n" +
			"  /exit     退出\n" +
			"  Tab       切换边栏  Ctrl+W 切换边栏面板\n" +
			"  y/a/n     审批：允许/始终允许/拒绝"
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: help})
	case "/model":
		info := fmt.Sprintf("当前模型: %s", m.agent.ModelName())
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: info})
	case "/clear":
		m.messages = nil
		m.Footer.SetWorking("")
		m.Footer.SetTokens(0)
	case "/compact":
		m.agent.Compact()
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: "✓ 上下文已压缩"})
	case "/exit", "/quit":
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: "再见！"})
	}
}
