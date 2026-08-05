package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Sidebar 右侧边栏（tview 实现）。
type Sidebar struct {
	Visible bool
	Active  string // Work / Tasks / Context

	flex     *tview.Flex
	pages    *tview.Pages
	tabViews map[string]*tview.TextView // 标签 TextView，用于样式切换

	workView    *tview.TextView
	tasksView   *tview.TextView
	contextView *tview.TextView

	// 运行时数据
	modelName   string
	totalTokens int
	callCount   int
	maxCalls    int
	msgCount    int // 消息条数
	recentSteps []SidebarStep
	recentTasks []string
	chatRole    string
	chatRound   int
}

type SidebarStep struct {
	Step    int
	Tool    string
	Status  string // "pending", "ok", "err"
	Summary string
}

func NewSidebar() *Sidebar {
	s := &Sidebar{
		Visible:  true,
		Active:   "Work",
		tabViews: map[string]*tview.TextView{},
	}

	s.workView = tview.NewTextView().SetDynamicColors(true)
	s.workView.SetBorder(true).SetTitle(" 任务步骤 ").SetBorderColor(Theme.SidebarBorder)

	s.tasksView = tview.NewTextView().SetDynamicColors(true)
	s.tasksView.SetBorder(true).SetTitle(" 最近任务 ").SetBorderColor(Theme.SidebarBorder)

	s.contextView = tview.NewTextView().SetDynamicColors(true)
	s.contextView.SetBorder(true).SetTitle(" 上下文 ").SetBorderColor(Theme.SidebarBorder)

	s.pages = tview.NewPages().
		AddPage("Work", s.workView, true, true).
		AddPage("Tasks", s.tasksView, true, false).
		AddPage("Context", s.contextView, true, false)

	tabBar := s.buildTabBar()

	s.flex = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(tabBar, 1, 0, false).
		AddItem(s.pages, 0, 1, true)

	s.renderContext()
	return s
}

func (s *Sidebar) Flex() *tview.Flex {
	return s.flex
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
	s.switchTab()
}

// buildTabBar 创建标签栏并缓存 tabViews。
func (s *Sidebar) buildTabBar() *tview.Flex {
	flex := tview.NewFlex().SetDirection(tview.FlexColumn)
	for _, name := range []string{"Work", "Tasks", "Context"} {
		tv := s.makeTab(name)
		s.tabViews[name] = tv
		flex.AddItem(tv, 0, 1, false)
	}
	return flex
}

// makeTab 创建单个标签 TextView。
func (s *Sidebar) makeTab(name string) *tview.TextView {
	tv := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	fmt.Fprint(tv, " "+name+" ")

	tv.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftClick {
			s.Active = name
			s.switchTab()
		}
		return action, event
	})
	return tv
}

func (s *Sidebar) applyTabStyle(tv *tview.TextView, name string) {
	if name == s.Active {
		tv.SetBackgroundColor(Theme.TabActiveBg)
		tv.SetTextColor(Theme.TabActiveFg)
	} else {
		tv.SetBackgroundColor(Theme.TabInactiveBg)
		tv.SetTextColor(Theme.TabInactiveFg)
	}
}

// switchTab 切换面板并原地更新标签样式（不再重建 widget）。
func (s *Sidebar) switchTab() {
	s.pages.SwitchToPage(s.Active)
	for name, tv := range s.tabViews {
		s.applyTabStyle(tv, name)
	}
}

// ── 数据更新 ──

func (s *Sidebar) SetModel(name string, tokens int) {
	s.modelName = name
	s.totalTokens = tokens
	s.renderContext()
}

func (s *Sidebar) SetMsgCount(count int) {
	s.msgCount = count
	s.renderContext()
}

func (s *Sidebar) SetBudget(calls, maxCalls int) {
	s.callCount = calls
	s.maxCalls = maxCalls
	s.renderContext()
}

func (s *Sidebar) SetTasks(tasks []string) {
	s.recentTasks = tasks
	s.renderTasks()
}

func (s *Sidebar) SetChatRole(role string, round int) {
	s.chatRole = role
	s.chatRound = round
}

func (s *Sidebar) AddStep(step int, tool, status, summary string) {
	s.recentSteps = append(s.recentSteps, SidebarStep{step, tool, status, summary})
	if len(s.recentSteps) > 20 {
		s.recentSteps = s.recentSteps[len(s.recentSteps)-20:]
	}
	s.renderWork()
}

// ── 渲染 ──

func (s *Sidebar) renderWork() {
	s.workView.Clear()

	if s.chatRole != "" {
		roleIcons := map[string]string{
			"pm": "🧑‍💼", "dev": "👨‍💻", "qa": "🔬", "user_proxy": "👤",
		}
		roleLabels := map[string]string{
			"pm": "产品经理", "dev": "程序员", "qa": "测试", "user_proxy": "用户代理",
		}
		icon := roleIcons[s.chatRole]
		if icon == "" {
			icon = "🔀"
		}
		label := roleLabels[s.chatRole]
		if label == "" {
			label = s.chatRole
		}
		roundInfo := ""
		if s.chatRound > 0 {
			roundInfo = fmt.Sprintf(" (第%d轮)", s.chatRound)
		}
		fmt.Fprintf(s.workView, "[%s]%s %s%s\n\n[white]", Theme.HighLight, icon, label, roundInfo)
	}

	if len(s.recentSteps) == 0 {
		fmt.Fprintf(s.workView, "[%s](等待任务开始)[white]\n", Theme.Dim)
		return
	}

	start := 0
	if len(s.recentSteps) > 12 {
		start = len(s.recentSteps) - 12
	}
	for _, st := range s.recentSteps[start:] {
		icon := "○"
		color := Theme.Dim
		switch st.Status {
		case "ok":
			icon = "✓"
			color = Theme.TaskDone
		case "err":
			icon = "✗"
			color = Theme.TaskErr
		}
		fmt.Fprintf(s.workView, "[%s] %s [%d] %s[white]\n", color, icon, st.Step, st.Tool)
		if st.Summary != "" && len(st.Summary) < 30 {
			fmt.Fprintf(s.workView, "[%s]     %s[white]\n", Theme.Dim, escapeTags(st.Summary))
		}
	}

	done := 0
	for _, st := range s.recentSteps {
		if st.Status == "ok" {
			done++
		}
	}
	total := len(s.recentSteps)
	if total > 0 {
		bar := strings.Repeat("█", done) + strings.Repeat("░", total-done)
		fmt.Fprintf(s.workView, "\n[%s]%s %d/%d[white]", Theme.Dim, bar, done, total)
	}
}

func (s *Sidebar) renderTasks() {
	s.tasksView.Clear()
	if len(s.recentTasks) == 0 {
		fmt.Fprintf(s.tasksView, "[%s](无最近任务)[white]\n", Theme.Dim)
		return
	}
	for _, t := range s.recentTasks {
		line := t
		if len(line) > 35 {
			line = line[:32] + "..."
		}
		fmt.Fprintf(s.tasksView, "[%s]•[white] %s\n", Theme.Dim, escapeTags(line))
	}
}

func (s *Sidebar) renderContext() {
	s.contextView.Clear()
	fmt.Fprintf(s.contextView, "[%s]模型:[white]  %s\n", Theme.HighLight, s.modelName)
	fmt.Fprintf(s.contextView, "[%s]Token:[white] %s\n", Theme.HighLight, formatTokens(s.totalTokens))

	usage := 0
	if s.maxCalls > 0 {
		usage = s.callCount * 100 / s.maxCalls
		if usage > 100 {
			usage = 100
		}
	}
	barW := 16
	filled := usage * barW / 100
	empty := barW - filled
	barColor := Theme.TaskDone
	if usage > 80 {
		barColor = Theme.TaskErr
	} else if usage > 50 {
		barColor = Theme.RoleChg
	}
	fmt.Fprintf(s.contextView, "[%s]调用:[white]  [%s]%s%s[white] %d/%d (%d%%)",
		Theme.HighLight,
		barColor, strings.Repeat("█", filled), strings.Repeat("░", empty),
		s.callCount, s.maxCalls, usage)
}
