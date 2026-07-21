package ui

import (
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"gowhale/internal/agent"
)

// Model 是 Bubble Tea 的应用状态。
type Model struct {
	agent   *agent.Agent
	events  <-chan agent.Event

	// 对话区
	viewport viewport.Model
	messages []agent.Event

	// 子组件
	StatusBar StatusBar
	Footer    Footer
	Sidebar   Sidebar

	// 审批状态
	pendingApproval *agent.ApprovalRequest
	approvalWarning string

	// 输入
	input textarea.Model

	// 运行时统计
	lastCallCount int

	// 尺寸
	width  int
	height int
}

func NewModel(ag *agent.Agent) *Model {
	ta := textarea.New()
	ta.Placeholder = "输入任务... / 开头查看命令"
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	ta.CharLimit = 0
	ta.Focus()

	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

	return &Model{
		agent:     ag,
		viewport:  vp,
		StatusBar: NewStatusBar(ag),
		Footer:    NewFooter(),
		Sidebar:   NewSidebar(),
		input:     ta,
		width:     80,
		height:    24,
	}
}
