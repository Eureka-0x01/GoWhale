package ui

import (
	"fmt"
	"strings"

	"gowhale/internal/agent"
)

func (m *Model) View() string {
	if m.pendingApproval != nil {
		return m.renderApproval()
	}

	// ── 顶部状态栏 ──
	header := colorLine("4", "15", m.StatusBar.View())

	// ── 主体区域（对话区 + 侧边栏）──

	// 对话区
	messagesContent := m.renderMessages()
	m.viewport.SetContent(messagesContent)
	chatArea := m.viewport.View()

	// 侧边栏
	var sidebarContent string
	if m.Sidebar.Visible {
		m.updateSidebarInfo()
		sidebarContent = m.Sidebar.View()
	}

	// 分栏布局
	var body string
	if m.Sidebar.Visible {
		// 左侧对话区 + 右侧侧边栏
		chatW := m.columnWidth() - 26
		if chatW < 20 {
			chatW = 20
		}
		chatAreaStyled := clipWidth(chatArea, chatW)
		sidebarStyled := sidebarContent
		body = lipglossJoinH(chatAreaStyled, "  ", sidebarStyled)
	} else {
		body = chatArea
	}

	// ── 底部分隔线 ──
	divider := dim(strings.Repeat("─", m.columnWidth()))

	// ── 底部状态 ──
	var footer string
	if m.Footer.message != "" {
		footer = dim(fmt.Sprintf(" %s", m.Footer.message))
		if m.Footer.tokenCount > 0 {
			footer += dim(fmt.Sprintf(" [%s]", formatTokensF(m.Footer.tokenCount)))
		}
	}

	// ── 输入区 ──
	inputArea := m.input.View()

	return header + "\n" + body + "\n" + divider + "\n" + footer + "\n" + inputArea
}

func (m *Model) columnWidth() int {
	if m.width < 40 {
		return 40
	}
	return m.width
}

func (m *Model) renderMessages() string {
	var sb strings.Builder
	for _, ev := range m.messages {
		switch ev.Type {
		case agent.EventType(999):
			sb.WriteString(boldC(color("12", "▸ "+ev.Message)) + "\n")
		case agent.EventDone:
			sb.WriteString("\n" + color("10", ev.Message) + "\n")
		case agent.EventToolCall:
			icon := toolIcon(ev.ToolName)
			label := fmt.Sprintf("[%d]", ev.Step)
			sb.WriteString(color("11", fmt.Sprintf(" %s %s %s  %s",
				label, icon, ev.ToolName, ev.ToolArgs)) + "\n")
		case agent.EventToolResult:
			if ev.IsError {
				sb.WriteString(color("9", "    ✗ "+firstLine(ev.ToolResult)) + "\n")
			} else {
				sb.WriteString(dim("    ✓ " + firstLine(ev.ToolResult)) + "\n")
			}
		case agent.EventError:
			sb.WriteString(color("9", "✗ "+ev.Message) + "\n")
		}
	}
	return sb.String()
}

func (m *Model) renderApproval() string {
	warning := ""
	if m.approvalWarning != "" {
		warning = fmt.Sprintf("   ⚠️ %s\n", m.approvalWarning)
	}
	return fmt.Sprintf(
		"%s🔧 %s  %s\n%s   ▶ [y] 允许本次  [a] 始终允许  [n] 拒绝",
		color("3", "════ 审批 ════\n"),
		m.pendingApproval.ToolName,
		m.pendingApproval.Arguments,
		warning,
	)
}

// ── 侧边栏数据更新 ──

func (m *Model) updateSidebarInfo() {
	m.Sidebar.SetModel(m.agent.ModelName(), m.agent.TokenCount())
	m.Sidebar.SetBudget(m.lastCallCount, 40) // MaxToolCalls

	// 从 journal 加载最近任务
	taskEntries := m.agent.LastTasks(8)
	var tasks []string
	for _, t := range taskEntries {
		tasks = append(tasks, t.Task)
	}
	m.Sidebar.SetTasks(tasks)
}

// ── 辅助函数 ──

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	return s
}

func clipWidth(s string, w int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if len(l) > w {
			lines[i] = l[:w]
		}
	}
	return strings.Join(lines, "\n")
}

func lipglossJoinH(left, sep, right string) string {
	return left + sep + right
}

// ── 简易 ANSI 颜色 ──

func color(code, s string) string {
	return "\033[" + code + "m" + s + "\033[0m"
}

func colorLine(bg, fg, s string) string {
	return "\033[" + bg + ";" + fg + "m" + s + "\033[0m"
}

func dim(s string) string  { return "\033[2m" + s + "\033[0m" }
func boldC(s string) string { return "\033[1m" + s + "\033[0m" }

func toolIcon(name string) string {
	switch name {
	case "write_plan":
		return "📋"
	case "batch_write", "write_file":
		return "✏️"
	case "execute_shell":
		return "🔧"
	case "read_file":
		return "📄"
	case "list_dir":
		return "📁"
	case "grep_search":
		return "🔍"
	case "execute_python":
		return "🐍"
	default:
		return "🔹"
	}
}

func formatTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func formatTokensF(n int) string {
	return formatTokens(n) + " token"
}
