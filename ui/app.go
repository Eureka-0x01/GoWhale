package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"gowhale/internal/agent"
	"gowhale/internal/config"
)

// 消息缓冲区刷新间隔。
const flushInterval = 50 * time.Millisecond

// Model 是 tview 应用状态。
type Model struct {
	agent agent.AgentInterface

	// 顶层
	app   *tview.Application
	pages *tview.Pages
	root  *tview.Flex

	// 组件
	statusBar *tview.TextView
	chatView  *tview.TextView
	sidebar   *Sidebar
	input     *tview.InputField
	footer    *tview.TextView
	mainFlex  *tview.Flex

	// 事件
	events <-chan agent.Event
	chatCh chan string

	// 审批
	pendingApproval *agent.ApprovalRequest
	approvalWarning string

	// 运行时
	chatMode      bool
	chatRole      string
	lastCallCount int

	// 输入历史
	inputHistory []string
	historyIdx   int

	// 消息缓冲
	msgBuf     strings.Builder
	hasPending bool

	// 初始化
	initialTask string
}

func NewModel(ag agent.AgentInterface, initialTask string) *Model {
	return &Model{
		agent:       ag,
		initialTask: initialTask,
		chatCh:      make(chan string, 8),
	}
}

// Build 构建 tview widget 树并返回 Application。
func (m *Model) Build() *tview.Application {
	m.app = tview.NewApplication()

	m.buildStatusBar()
	m.buildChatView()
	m.buildSidebar()
	m.buildInput()
	m.buildFooter()
	m.buildLayout()
	m.setupGlobalKeys()

	m.pages = tview.NewPages().
		AddPage("main", m.root, true, true)

	m.app.SetRoot(m.pages, true)
	m.app.SetFocus(m.input)

	go m.eventLoop()

	if m.initialTask != "" {
		m.app.QueueUpdateDraw(func() {
			m.submitTask(m.initialTask)
		})
	}

	return m.app
}

// ── 组件构建 ──

func (m *Model) buildStatusBar() {
	m.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	m.statusBar.SetBackgroundColor(Theme.StatusBarBg)
	m.statusBar.SetTextColor(Theme.StatusBarFg)
	m.refreshStatusBar()
}

func (m *Model) buildChatView() {
	m.chatView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWordWrap(true)
	m.chatView.SetBorder(true).SetTitle(" 对话 ")
	m.chatView.SetBorderColor(Theme.ChatBorder)

	tasks := m.agent.LastTasks(10)
	for _, t := range tasks {
		fmt.Fprintf(m.chatView, "%s▸ [%s] %s\n", tagOpen("cyan"), t.Time, escapeTags(t.Task))
	}
}

func (m *Model) buildSidebar() {
	m.sidebar = NewSidebar()
	m.sidebar.SetModel(m.agent.ModelName(), m.agent.TokenCount())
	m.sidebar.SetBudget(0, 40)

	taskEntries := m.agent.LastTasks(8)
	var tasks []string
	for _, t := range taskEntries {
		tasks = append(tasks, t.Task)
	}
	m.sidebar.SetTasks(tasks)
}

func (m *Model) buildInput() {
	m.input = tview.NewInputField().
		SetPlaceholder("输入任务...  输入 / 后按 Tab 补全命令").
		SetFieldWidth(0).
		SetAutocompleteFunc(m.autocompleteCommands)
	m.input.SetBorder(true).SetTitle(" 输入 ").SetBorderColor(Theme.ChatBorder)



	m.input.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			text := strings.TrimSpace(m.input.GetText())
			if text == "" {
				return
			}
			m.input.SetText("")

			// 记录历史
			m.inputHistory = append(m.inputHistory, text)
			if len(m.inputHistory) > 100 {
				m.inputHistory = m.inputHistory[len(m.inputHistory)-100:]
			}
			m.historyIdx = len(m.inputHistory)

			m.chatCh <- text
		}
	})

	m.input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyUp:
			if len(m.inputHistory) > 0 && m.historyIdx > 0 {
				m.historyIdx--
				m.input.SetText(m.inputHistory[m.historyIdx])
			}
			return nil
		case tcell.KeyDown:
			if m.historyIdx < len(m.inputHistory)-1 {
				m.historyIdx++
				m.input.SetText(m.inputHistory[m.historyIdx])
			} else if m.historyIdx == len(m.inputHistory)-1 {
				m.historyIdx = len(m.inputHistory)
				m.input.SetText("")
			}
			return nil
		case tcell.KeyTab:
			// 输入 / 命令时，Tab 触发补全下拉；其他时候切换侧边栏
			if strings.HasPrefix(m.input.GetText(), "/") {
				return event // 放行给 InputField 的 autocomplete
			}
			m.sidebar.Toggle()
			m.adjustSidebarWidth()
			return nil
		case tcell.KeyCtrlW:
			m.sidebar.CycleMode()
			return nil
		}
		return event
	})
}

func (m *Model) buildFooter() {
	m.footer = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	m.footer.SetBackgroundColor(Theme.FooterBg)
	m.setFooterIdle()
}

func (m *Model) buildLayout() {
	m.mainFlex = tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(m.chatView, 0, 1, false)

	m.root = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(m.statusBar, 1, 0, false).
		AddItem(m.mainFlex, 0, 1, true).
		AddItem(m.input, 3, 0, true).
		AddItem(m.footer, 1, 0, false)

	m.adjustSidebarWidth()
}

func (m *Model) adjustSidebarWidth() {
	m.mainFlex.Clear()
	m.mainFlex.AddItem(m.chatView, 0, 1, false)
	if m.sidebar.Visible {
		m.mainFlex.AddItem(m.sidebar.Flex(), 28, 0, false)
	}
}

// ── 全局按键 ──

func (m *Model) setupGlobalKeys() {
	m.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if m.pendingApproval != nil {
			return nil
		}
		switch event.Key() {
		case tcell.KeyCtrlC:
			m.app.Stop()
			return nil
		}
		return event
	})
}

// ── 事件循环（带批处理缓冲）──

func (m *Model) eventLoop() {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	var pending []agent.Event

	// flush 将缓冲事件推送到 UI goroutine。由于 hasPending 只在
	// QueueUpdateDraw 回调（UI goroutine）中修改，从 eventLoop
	// goroutine 读取 msgBuf 是否存在待刷新数据有竞态风险。改为始终
	// 调用 flushMsgBuf——它内部检查 hasPending，空时是 no-op。
	flush := func() {
		events := pending
		pending = nil
		m.app.QueueUpdateDraw(func() {
			m.flushMsgBuf()
			for _, ev := range events {
				m.applyEvent(ev)
			}
		})
	}

	for {
		select {
		case text := <-m.chatCh:
			flush()
			m.handleInput(text)

		case ev, ok := <-m.events:
			if !ok {
				flush()
				m.app.QueueUpdateDraw(func() {
					m.flushMsgBuf()
					m.setFooterDone("✅ 完成")
					m.refreshStatusBar()
				})
				m.events = nil
				continue
			}

			// 审批事件立即处理（不缓冲）
			if ev.Type == agent.EventApprovalRequest {
				flush()
				m.app.QueueUpdateDraw(func() {
					m.applyEvent(ev)
				})
				continue
			}

			// 思考事件直接更新 footer
			if ev.Type == agent.EventThinking {
				m.app.QueueUpdateDraw(func() {
					if m.chatRole != "" {
						m.setFooterWork(fmt.Sprintf("⏳ %s 思考中...", roleLabel(m.chatRole)))
					} else {
						m.setFooterWork("⏳ 思考中...")
					}
				})
				continue
			}

			pending = append(pending, ev)

		case <-ticker.C:
			flush()
		}
	}
}

// ── 事件应用（在 UI goroutine 中执行）──

func (m *Model) applyEvent(ev agent.Event) {
	switch ev.Type {
	case agent.EventRoleChange:
		m.chatRole = ev.Role
		m.sidebar.SetChatRole(ev.Role, 0)
		m.writeMsgBuf(tagLine(Theme.RoleChg, "── "+ev.Message+" ──"))
		if m.chatRole != "" {
			m.setFooterWork(fmt.Sprintf("🔀 %s", roleLabel(m.chatRole)))
		}

	case agent.EventDone:
		m.flushMsgBuf()
		m.events = nil
		m.chatRole = ""
		m.sidebar.SetChatRole("", 0)
		m.writeMsgBuf("\n" + tagLine(Theme.TaskDone, ev.Message))
		m.setFooterDone(fmt.Sprintf("✅ 完成  [📊 %s]", formatTokens(ev.TokenCount)))
		m.refreshStatusBar()

	case agent.EventError:
		m.flushMsgBuf()
		m.events = nil
		m.chatRole = ""
		m.sidebar.SetChatRole("", 0)
		m.writeMsgBuf("\n" + tagLine(Theme.TaskErr, "✗ "+ev.Message))
		m.setFooterDone(fmt.Sprintf("✗ 错误  [📊 %s]", formatTokens(ev.TokenCount)))

	case agent.EventApprovalRequest:
		m.flushMsgBuf()
		m.showApprovalModal(ev)

	case agent.EventToolCall:
		m.lastCallCount = ev.CallCount
		m.sidebar.AddStep(ev.Step, ev.ToolName, "pending", "")
		icon := toolIcon(ev.ToolName)
		m.writeMsgBuf(tagLine(Theme.ToolCall,
			fmt.Sprintf("[%d] %s %s  %s", ev.Step, icon, ev.ToolName, ev.ToolArgs)))

	case agent.EventToolResult:
		status := "ok"
		if ev.IsError {
			status = "err"
			m.writeMsgBuf(tagLine(Theme.ToolErr, "    ✗ "+firstLine(ev.ToolResult)))
		} else {
			m.writeMsgBuf(tagLine(Theme.ToolOK, "    ✓ "+firstLine(ev.ToolResult)))
		}
		m.sidebar.AddStep(ev.Step, ev.ToolName, status, firstLine(ev.ToolResult))
		m.sidebar.SetModel(m.agent.ModelName(), m.agent.TokenCount())
		m.sidebar.SetBudget(m.lastCallCount, 40)
	}

	m.refreshStatusBar()
}

// ── 消息缓冲区（批量写入 chatView，减少 ScrollToEnd 调用）──

func (m *Model) writeMsgBuf(s string) {
	m.msgBuf.WriteString(s)
	m.hasPending = true
}

// flushMsgBuf 将缓冲区内容一次性写入 chatView。
// 必须在 UI goroutine（QueueUpdateDraw 回调）中调用。
func (m *Model) flushMsgBuf() {
	if !m.hasPending {
		return
	}
	m.chatView.Write([]byte(m.msgBuf.String()))
	m.msgBuf.Reset()
	m.hasPending = false
	m.chatView.ScrollToEnd()
}

// ── 输入处理 ──

func (m *Model) handleInput(text string) {
	if strings.HasPrefix(text, "/") {
		m.app.QueueUpdateDraw(func() {
			m.handleCommand(text)
		})
		return
	}
	m.submitTask(text)
}

func (m *Model) submitTask(text string) {
	m.app.QueueUpdateDraw(func() {
		m.flushMsgBuf()
		m.writeMsgBuf("\n" + tagLine(Theme.UserMsg, "▸ "+text))
		m.flushMsgBuf()
		m.setFooterWork("⏳ 执行中...")
	})
	m.events = m.agent.RunAsync(text)
}

// ── 审批弹窗 ──

func (m *Model) showApprovalModal(ev agent.Event) {
	m.pendingApproval = ev.ApprovalRequest
	m.approvalWarning = ev.ApprovalRequest.Warning

	warningText := ""
	if m.approvalWarning != "" {
		warningText = fmt.Sprintf("\n⚠️  %s", m.approvalWarning)
	}

	text := fmt.Sprintf("🔧 %s  %s%s\n\n是否允许执行？",
		ev.ToolName, ev.ApprovalRequest.Arguments, warningText)

	modal := tview.NewModal().
		SetText(text).
		AddButtons([]string{"允许 (y)", "始终允许 (a)", "拒绝 (n)"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			reply := agent.ApprovalReply{}
			switch buttonIndex {
			case 0:
				reply.Allowed = true
			case 1:
				reply.Allowed = true
				reply.Permanent = true
			case 2:
				reply.Allowed = false
			}
			m.pendingApproval.Callback <- reply
			m.pendingApproval = nil
			m.approvalWarning = ""
			m.pages.RemovePage("approval")
		})

	m.pages.AddPage("approval", modal, true, true)
}

// ── 命令处理 ──

var commandList = []struct {
	Name string
	Desc string
}{
	{"/help", "帮助信息"},
	{"/model", "查看当前模型"},
	{"/history", "查看最近对话记录"},
	{"/clear", "清空对话历史"},
	{"/compact", "压缩上下文节省 token"},
	{"/chatroom", "多角色协作（PM→Dev→QA→验收）"},
	{"/ollama", "切换使用 Ollama 本地模型"},
	{"/deepseek", "切换使用 DeepSeek 云端模型"},
	{"/exit", "退出程序"},
}

func (m *Model) autocompleteCommands(currentText string) (entries []string) {
	if strings.HasPrefix(currentText, "/") {
		for _, c := range commandList {
			if strings.HasPrefix(c.Name, currentText) {
				entries = append(entries, c.Name)
			}
		}
	}
	return
}

func (m *Model) handleCommand(cmd string) {
	cmd = strings.ToLower(strings.TrimSpace(cmd))

	switch cmd {
	case "/help":
		var sb strings.Builder
		sb.WriteString(tagLine(Theme.UserMsg, "命令列表："))
		for _, c := range commandList {
			sb.WriteString(fmt.Sprintf("  %-14s %s\n", c.Name, c.Desc))
		}
		m.flushMsgBuf()
		m.writeMsgBuf(sb.String())
		m.flushMsgBuf()

	case "/model":
		_, _, model, proModel := m.agent.ProviderInfo()
		m.writeMsgBuf(tagLine(Theme.UserMsg, fmt.Sprintf("当前: %s / %s", model, proModel)))

	case "/history":
		tasks := m.agent.LastTasks(10)
		var sb strings.Builder
		sb.WriteString(tagLine(Theme.UserMsg, "最近对话："))
		for _, t := range tasks {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", t.Time, escapeTags(t.Task)))
		}
		m.flushMsgBuf()
		m.writeMsgBuf(sb.String())
		m.flushMsgBuf()

	case "/clear":
		m.chatView.Clear()
		m.setFooterIdle()

	case "/compact":
		m.agent.Compact()
		m.writeMsgBuf(tagLine(Theme.UserMsg, "✓ 上下文已压缩"))

	case "/ollama":
		cfg := config.Load()
		m.agent.SwitchProvider(cfg.OllamaURL, "ollama", cfg.OllamaModel, cfg.OllamaModel)
		config.SaveProvider("ollama")
		m.writeMsgBuf(tagLine(Theme.UserMsg, fmt.Sprintf("✓ Ollama: %s", cfg.OllamaModel)))

	case "/deepseek":
		config.SaveProvider("deepseek")
		cfg := config.Load()
		m.agent.SwitchProvider(cfg.BaseURL, cfg.APIKey, cfg.Model, cfg.ProModel)
		m.writeMsgBuf(tagLine(Theme.UserMsg, fmt.Sprintf("✓ DeepSeek: %s", cfg.Model)))

	case "/chatroom":
		m.chatMode = true
		m.chatRole = ""
		m.sidebar.SetChatRole("", 0)
		m.writeMsgBuf(tagLine(Theme.UserMsg, "🔀 多角色协作说明：使用 gowhale \"设计并实现一个XX\" 即可自动触发 PM→Dev→QA→验收 流转。"))

	case "/exit":
		m.flushMsgBuf()
		m.app.Stop()

	default:
		m.writeMsgBuf(tagLine(Theme.TaskErr, fmt.Sprintf("未知命令: %s", cmd)))
	}
}

// ── Footer 辅助 ──

func (m *Model) setFooterIdle() {
	m.footer.Clear()
	fmt.Fprintf(m.footer, "[white] 就绪  [%s]%s",
		Theme.Dim, FooterHints)
}

func (m *Model) setFooterWork(msg string) {
	m.footer.Clear()
	fmt.Fprintf(m.footer, "[white] %s  [%s]%s",
		msg, Theme.Dim, FooterHints)
}

func (m *Model) setFooterDone(msg string) {
	m.footer.Clear()
	fmt.Fprintf(m.footer, "[white] %s  [%s]%s",
		msg, Theme.Dim, FooterHints)
}

// ── 其他辅助 ──

func (m *Model) refreshStatusBar() {
	model := m.agent.ModelName()
	tokens := m.agent.TokenCount()
	m.statusBar.Clear()
	left := fmt.Sprintf(" GoWhale  %s  │  %s token", model, formatTokens(tokens))
	if m.chatRole != "" {
		label := roleLabel(m.chatRole)
		if label != "" {
			left += fmt.Sprintf("  │  %s", label)
		}
	}
	fmt.Fprint(m.statusBar, left)
}

func roleLabel(role string) string {
	switch role {
	case "pm":
		return "🧑‍💼 PM"
	case "dev":
		return "👨‍💻 Dev"
	case "qa":
		return "🔬 QA"
	case "user_proxy":
		return "👤 验收"
	default:
		return role
	}
}

// tagOpen 返回打开颜色标签（不自动关闭，调用方需自行处理）。
func tagOpen(color string) string {
	return fmt.Sprintf("[%s]", color)
}
