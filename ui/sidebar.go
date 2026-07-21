package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Sidebar 右侧边栏（可折叠、可切换面板）。
type Sidebar struct {
	Visible bool
	Active  string // Work / Tasks / Context
	width   int

	// 运行时数据（由 Model 在接收到事件时更新）
	modelName    string
	totalTokens  int
	callCount    int
	maxCalls     int
	recentSteps  []SidebarStep
	recentTasks  []string // 最近任务摘要（来自 journal）
}

// SidebarStep 侧边栏任务步骤条目。
type SidebarStep struct {
	Step    int
	Tool    string
	Status  string // "pending", "ok", "err"
	Summary string
}

// ── 样式 ──

var (
	sidebarStyle = lipgloss.NewStyle().
			Width(24).
			Padding(0, 1)

	panelHeader = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Bold(true)

	panelTitle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true)

	tabActive = lipgloss.NewStyle().
			Background(lipgloss.Color("4")).
			Foreground(lipgloss.Color("15")).
			Padding(0, 1)

	tabInactive = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8")).
			Padding(0, 1)

	stepPending = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stepOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	stepErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// ── 构造 ──

func NewSidebar() Sidebar {
	return Sidebar{
		Active: "Work",
		width:  24,
	}
}

func (s *Sidebar) Toggle()  { s.Visible = !s.Visible }
func (s *Sidebar) CycleMode() {
	switch s.Active {
	case "Work":
		s.Active = "Tasks"
	case "Tasks":
		s.Active = "Context"
	default:
		s.Active = "Work"
	}
}

// ── 渲染 ──

func (s *Sidebar) View() string {
	if !s.Visible {
		return ""
	}

	// 顶部标签栏
	tabs := s.renderTabs()
	divider := strings.Repeat("─", s.width)

	var content string
	switch s.Active {
	case "Work":
		content = s.renderWork()
	case "Tasks":
		content = s.renderTasks()
	case "Context":
		content = s.renderContext()
	}

	return sidebarStyle.Render(tabs + "\n" + divider + "\n" + content)
}

func (s *Sidebar) renderTabs() string {
	tabs := []string{"Work", "Tasks", "Context"}
	var parts []string
	for _, t := range tabs {
		if t == s.Active {
			parts = append(parts, tabActive.Render(t))
		} else {
			parts = append(parts, tabInactive.Render(t))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// Work 面板：当前任务的步骤进度。
func (s *Sidebar) renderWork() string {
	var sb strings.Builder

	// 标题
	sb.WriteString(panelTitle.Render("📋 任务步骤"))
	sb.WriteString("\n")

	if len(s.recentSteps) == 0 {
		sb.WriteString(dim("(等待任务开始)\n"))
		return sb.String()
	}

	// 显示最近 12 个步骤
	start := 0
	if len(s.recentSteps) > 12 {
		start = len(s.recentSteps) - 12
	}
	for _, st := range s.recentSteps[start:] {
		icon := "○"
		style := stepPending
		switch st.Status {
		case "ok":
			icon = "✓"
			style = stepOK
		case "err":
			icon = "✗"
			style = stepErr
		}
		label := fmt.Sprintf("[%d]", st.Step)
		if len(label) < 4 {
			label += strings.Repeat(" ", 4-len(label))
		}
		sb.WriteString(style.Render(fmt.Sprintf(" %s %s %s", icon, label, st.Tool)))
		sb.WriteString("\n")
		if st.Summary != "" && len(st.Summary) < 30 {
			sb.WriteString(dim(fmt.Sprintf("     %s\n", st.Summary)))
		}
	}

	// 进度条
	done := 0
	for _, st := range s.recentSteps {
		if st.Status == "ok" {
			done++
		}
	}
	sb.WriteString(fmt.Sprintf("\n  进度: %d/%d 步", done, s.callCount))
	return sb.String()
}

// Tasks 面板：最近的对话任务。
func (s *Sidebar) renderTasks() string {
	var sb strings.Builder
	sb.WriteString(panelTitle.Render("📝 最近任务"))
	sb.WriteString("\n")

	if len(s.recentTasks) == 0 {
		sb.WriteString(dim("(暂无记录)\n"))
		return sb.String()
	}

	for _, t := range s.recentTasks {
		if len(t) > 22 {
			t = t[:22] + "…"
		}
		sb.WriteString(dim(fmt.Sprintf("  • %s\n", t)))
	}
	return sb.String()
}

// Context 面板：会话上下文信息。
func (s *Sidebar) renderContext() string {
	var sb strings.Builder
	sb.WriteString(panelTitle.Render("💡 上下文"))
	sb.WriteString("\n\n")

	info := []struct {
		label string
		value string
	}{
		{"模型", s.modelName},
		{"Token", formatTokens(s.totalTokens)},
		{"调用", fmt.Sprintf("%d/%d", s.callCount, s.maxCalls)},
	}

	for _, item := range info {
		sb.WriteString(dim(item.label + ":"))
		sb.WriteString("\n  ")
		sb.WriteString(item.value)
		sb.WriteString("\n\n")
	}

	// 用量条
	usage := 0.0
	if s.maxCalls > 0 {
		usage = float64(s.callCount) / float64(s.maxCalls) * 100
	}
	bar := makeProgressBar(int(usage), 18)
	sb.WriteString(dim("工具预算:\n"))
	sb.WriteString(fmt.Sprintf("  %s %.0f%%\n", bar, usage))

	return sb.String()
}

func makeProgressBar(pct, width int) string {
	if pct > 100 {
		pct = 100
	}
	filled := pct * width / 100
	empty := width - filled
	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
	if pct > 80 {
		bar = "\033[31m" + bar + "\033[0m" // 红色警告
	} else if pct > 50 {
		bar = "\033[33m" + bar + "\033[0m" // 黄色
	}
	return bar
}

// ── 数据更新方法（由 Model.updateSidebar 调用）──

func (s *Sidebar) SetModel(name string, tokens int) {
	s.modelName = name
	s.totalTokens = tokens
}

func (s *Sidebar) SetBudget(used, max int) {
	s.callCount = used
	s.maxCalls = max
}

func (s *Sidebar) AddStep(step int, tool, status, summary string) {
	s.recentSteps = append(s.recentSteps, SidebarStep{
		Step:    step,
		Tool:    tool,
		Status:  status,
		Summary: summary,
	})
	if len(s.recentSteps) > 50 {
		s.recentSteps = s.recentSteps[len(s.recentSteps)-50:]
	}
}

func (s *Sidebar) SetTasks(tasks []string) {
	s.recentTasks = tasks
}
