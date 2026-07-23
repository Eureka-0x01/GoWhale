package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"gowhale/internal/agent"
	"gowhale/internal/config"
)

// executeTaskMsg 是 TUI 启动时自动执行初始任务的内部消息。
type executeTaskMsg string

func (m *Model) Init() tea.Cmd {
	// 加载历史任务记录
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
		m.viewport.Height = msg.Height - 6
		if m.Sidebar.Visible {
			m.viewport.Width = msg.Width - 24 - 2
		}
		return m, nil

	// 启动时自动执行初始任务
	case executeTaskMsg:
		task := string(msg)
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: task})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		m.Footer.SetWorking("执行中")
		m.events = m.agent.RunAsync(task)
		return m, m.waitForEvent()

	case tea.KeyMsg:
		// 审批模式
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

		// 命令提示模式：拦截上下键/esc
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
			// 命令提示模式下选中命令
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
			// 检测 / 命令输入，重置选中索引
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
		help := "命令列表：\n" +
			"  /help        帮助信息\n" +
			"  /model       查看当前模型\n" +
			"  /history     查看最近对话记录\n" +
			"  /clear       清空对话\n" +
			"  /clear-key   清除已保存的 API Key\n" +
			"  /compact     压缩上下文\n" +
			"  /ollama      切换 Ollama 本地模型\n" +
			"  /deepseek    切换 DeepSeek 云端模型\n" +
			"  /exit        退出\n" +
			"  Tab          切换边栏  Ctrl+W 切换边栏面板\n" +
			"  y/a/n        审批：允许/始终允许/拒绝"
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: help})
	case "/model":
		_, baseURL, model, proModel := m.agent.ProviderInfo()
		info := fmt.Sprintf("提供商: %s\n地址: %s\n简单模型: %s\n复杂模型: %s",
			m.agent.ProviderName(), baseURL, model, proModel)
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: info})
	case "/history":
		m.showHistory()
	case "/clear":
		m.messages = nil
		m.Footer.SetWorking("")
		m.Footer.SetTokens(0)
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: "✓ 对话历史已清空"})
	case "/clear-key":
		cleared := clearAPIKeyFile()
		if cleared {
			m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: "✓ 已清除 API Key。重启后需重新输入。"})
		} else {
			m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: "没有已保存的 API Key。"})
		}
	case "/compact":
		before := m.agent.TokenCount()
		didCompact := m.agent.Compact()
		after := m.agent.TokenCount()
		m.Footer.SetTokens(after)

		if !didCompact {
			m.messages = append(m.messages, agent.Event{
				Type:    agent.EventType(999),
				Message: fmt.Sprintf("消息不足无需压缩（当前 %s token，不足 %d 条）", formatTokens(after), compactKeepHint()),
			})
			break
		}

		// 同步压缩 TUI 消息：保留最近 15 条，插入分割标记
		const tuiKeep = 15
		if len(m.messages) > tuiKeep {
			cut := len(m.messages) - tuiKeep
			messages := make([]agent.Event, 0, tuiKeep+2)
			messages = append(messages, agent.Event{
				Type:    agent.EventType(999),
				Message: fmt.Sprintf("── 上下文已压缩（%s → ~%s token）──", formatTokens(before), formatTokens(after)),
			})
			messages = append(messages, m.messages[cut:]...)
			m.messages = messages
		} else {
			m.messages = append(m.messages, agent.Event{
				Type:    agent.EventType(999),
				Message: fmt.Sprintf("✓ 上下文已压缩（%s → ~%s token）", formatTokens(before), formatTokens(after)),
			})
		}
	case "/ollama":
		switchOllama(m)
	case "/deepseek":
		switchDeepSeek(m)
	case "/exit", "/quit":
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: "再见！"})
	}
}

// ── 命令辅助函数 ──

// showHistory 从 journal 加载最近任务并显示。
func (m *Model) showHistory() {
	tasks := m.agent.LastTasks(10)
	if len(tasks) == 0 {
		m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: "(暂无历史记录)"})
		return
	}
	var sb strings.Builder
	sb.WriteString("📝 最近对话：\n")
	for _, t := range tasks {
		task := t.Task
		if len(task) > 70 {
			task = task[:70] + "…"
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s\n", t.Time, task))
		if len(t.Replies) > 0 {
			reply := strings.TrimSpace(t.Replies[len(t.Replies)-1])
			if len(reply) > 70 {
				reply = reply[:70] + "…"
			}
			sb.WriteString(fmt.Sprintf("    ↳ %s\n", reply))
		}
	}
	m.messages = append(m.messages, agent.Event{Type: agent.EventType(999), Message: sb.String()})
}

// clearAPIKeyFile 删除 ~/.gowhale/.env 文件。
func clearAPIKeyFile() bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	path := filepath.Join(homeDir, ".gowhale", ".env")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}
	return os.Remove(path) == nil
}

// switchOllama 切换到 Ollama 提供商。
func switchOllama(m *Model) {
	ollamaURL := os.Getenv("AICODE_OLLAMA_URL")
	ollamaModel := os.Getenv("AICODE_OLLAMA_MODEL")
	if ollamaURL == "" || ollamaModel == "" {
		m.messages = append(m.messages, agent.Event{
			Type:    agent.EventType(999),
			Message: "⚠️ 未配置 Ollama。请在终端模式首次运行 /ollama 配置，或设置环境变量 AICODE_OLLAMA_URL 和 AICODE_OLLAMA_MODEL。",
		})
		return
	}
	m.agent.SwitchProvider(ollamaURL, "ollama", ollamaModel, ollamaModel)
	config.SaveProvider("ollama")
	m.messages = append(m.messages, agent.Event{
		Type:    agent.EventType(999),
		Message: fmt.Sprintf("✓ 已切换到 Ollama (%s)", ollamaModel),
	})
}

// switchDeepSeek 切换到 DeepSeek 提供商。
func switchDeepSeek(m *Model) {
	cfg := config.Load()
	m.agent.SwitchProvider(cfg.BaseURL, cfg.APIKey, cfg.Model, cfg.ProModel)
	config.SaveProvider("deepseek")
	m.messages = append(m.messages, agent.Event{
		Type:    agent.EventType(999),
		Message: fmt.Sprintf("✓ 已切换到 DeepSeek (%s / %s)", cfg.Model, cfg.ProModel),
	})
}

// compactKeepHint 返回压缩所需的阈值，与 agent.compactKeep 保持同步。
func compactKeepHint() int { return 22 }
