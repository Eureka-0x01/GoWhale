package ui

import (
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"gowhale/internal/agent"
)

// Model 是 Bubble Tea 的应用状态。
type Model struct {
	agent   agent.AgentInterface
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
	input        textarea.Model
	showCommands bool // 是否显示 / 命令提示
	commandIdx   int  // 当前选中的命令索引

	// 运行时统计
	lastCallCount int

	// 尺寸
	width  int
	height int

	// 初始化后自动执行的任务（非空时触发）
	initialTask string
}

func NewModel(ag agent.AgentInterface, initialTask string) *Model {
	ta := textarea.New()
	ta.Placeholder = "输入任务...  / 命令  Tab 侧栏  Shift+Enter 换行"
	ta.ShowLineNumbers = false
	ta.SetHeight(5)
	ta.SetWidth(120)
	ta.CharLimit = 0
	ta.Focus()

	vp := viewport.New(120, 20)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

	return &Model{
		agent:       ag,
		viewport:    vp,
		StatusBar:   NewStatusBar(ag),
		Footer:      NewFooter(),
		Sidebar:     NewSidebar(),
		input:       ta,
		width:       120,
		height:      24,
		initialTask: initialTask,
	}
}
