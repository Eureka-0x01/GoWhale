package agent

import (
	"encoding/json"
	"fmt"
	"time"
	"os"
	"strings"

	"gowhale/internal/llm"
	"gowhale/internal/tools"
)

// ── 角色定义 ──

// Role 标识多角色协作中的角色。
type Role string

const (
	RolePM        Role = "pm"
	RoleDev       Role = "dev"
	RoleQA        Role = "qa"
	RoleUserProxy Role = "user_proxy"
)

const (
	maxChatRoomRounds = 15  // 全局角色轮次上限（可自动扩展）
	maxDevToolCalls   = 8   // Dev 子循环最大工具调用次数
	maxQARetries      = 2   // QA→Dev 回退次数上限，超限强制验收
	maxUPRetries      = 1   // UserProxy 回退次数上限，超限强制通过
)

// ChatArtifacts 各角色产出物。
type ChatArtifacts struct {
	Spec        string // PM 产出的规格
	CodeSummary string // Dev 总结的改动
	TestReport  string // QA 产出的测试报告
}

// ── ChatRoom 结构体 ──

// ChatRoom 多角色协作 Agent。实现 AgentInterface。
type ChatRoom struct {
	client     *llm.Client
	registry   *tools.Registry
	approver   *Approver
	journal    *Journal
	debugLog   *DebugLog
	workspace  string
	totalTokens int

	// 共享上下文
	sharedHistory []llm.Message
	recentCmds   map[string]int

	// 模型配置
	fastModel string
	proModel  string
	timeout  time.Duration
	modelLock string
	readFiles    map[string]bool // 已完整读取的文件（去重）
	listDirs     map[string]bool // 已 list_dir 的参数（去重）
	grepPatterns map[string]bool // 已 grep_search 的 pattern（去重）
	workLog     []workEntry     // 工作记忆：每步操作的摘要
}

// NewChatRoom 创建多角色协作 Agent。
func NewChatRoom(client *llm.Client, registry *tools.Registry, approver *Approver, workspace, fastModel, proModel string) *ChatRoom {
	j := NewJournal(workspace)
	dl := NewDebugLog(workspace)
	return &ChatRoom{
		client:      client,
		registry:    registry,
		approver:    approver,
		journal:     j,
		debugLog:    dl,
		workspace:   workspace,
		recentCmds:  map[string]int{},
		readFiles:  map[string]bool{},
		listDirs:   map[string]bool{},
		grepPatterns: map[string]bool{},
		sharedHistory: make([]llm.Message, 0),
		fastModel:   fastModel,
		proModel:    proModel,
		timeout:     TaskTimeout,
	}
}

// ── AgentInterface 实现 ──

func (cr *ChatRoom) ModelName() string   { return cr.client.Model() }
func (cr *ChatRoom) TokenCount() int     { return cr.totalTokens }
func (cr *ChatRoom) LastTasks(n int) []TaskEntry { return cr.journal.LastTasks(n) }

func (cr *ChatRoom) ProviderName() string {
	if strings.Contains(cr.client.BaseURL(), "ollama") || strings.Contains(cr.client.BaseURL(), "11434") {
		return "ollama"
	}
	return "deepseek"
}

func (cr *ChatRoom) ProviderInfo() (name, baseURL, model, proModel string) {
	name = cr.ProviderName()
	baseURL = cr.client.BaseURL()
	model = cr.fastModel
	proModel = cr.proModel
	return
}

func (cr *ChatRoom) Messages() []llm.Message {
	cp := make([]llm.Message, len(cr.sharedHistory))
	copy(cp, cr.sharedHistory)
	return cp
}

func (cr *ChatRoom) SetTimeout(d time.Duration) { cr.timeout = d }

// SetModelLock 锁定模型：非空时固定使用该模型；空字符串恢复自动。
func (cr *ChatRoom) SetModelLock(name string) {
	cr.modelLock = strings.TrimSpace(name)
	if cr.modelLock != "" {
		cr.client.SetModel(cr.modelLock)
	}
}

// ModelLock 返回当前锁定的模型名（空 = 自动模式）。
func (cr *ChatRoom) ModelLock() string { return cr.modelLock }

func (cr *ChatRoom) Compact() bool { return false }

func (cr *ChatRoom) SwitchProvider(baseURL, apiKey, model, proModel string) {
	cr.client.SwitchTo(baseURL, apiKey, model, proModel)
	cr.fastModel = model
	cr.proModel = proModel
}

// Run 同步执行（兼容 classic 模式）。
func (cr *ChatRoom) Run(input string) {
	events := cr.RunAsync(input)
	for ev := range events {
		switch ev.Type {
		case EventDone:
			fmt.Printf("\n%s %s  %s\n", boldC(blueC("AI >")), ev.Message, tokenBadge(ev.TokenCount))
			cr.journal.Note("✅ " + ev.Message)
		case EventError:
			fmt.Printf("\n调用失败: %s  %s\n", ev.Message, tokenBadge(ev.TokenCount))
		case EventToolCall:
			label := fmt.Sprintf("%s %s %-14s %s",
				grayC(fmt.Sprintf("[%d]", ev.Step)),
				toolIcon(ev.ToolName),
				grayC(ev.ToolName),
				dimC(ev.ToolArgs))
			fmt.Print("\r" + label)
		case EventToolResult:
			fmt.Printf("→ %s  %s\r\n", statusLine(ev.ToolResult), tokenBadge(ev.TokenCount))
			if isError(ev.ToolResult) {
				fmt.Printf("     %s\n", indentLines(ev.ToolResult, 5))
			}
		case EventApprovalRequest:
			req := ev.ApprovalRequest
			fmt.Print(PrintPrompt(req.ToolName, req.Warning))
			reply := readTerminalApproval(os.Stdin)
			req.Callback <- reply
		}
	}
}

// RunAsync 异步执行，通过 channel 发送事件。
func (cr *ChatRoom) RunAsync(input string) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		cr.runLoop(input, ch)
	}()
	return ch
}

// ── 主循环 ──

func (cr *ChatRoom) runLoop(input string, ch chan<- Event) {
	// 多角色协作强制使用 pro 模型。flash 模型无法胜任多步推理/代码生成/测试验收。
	cr.client.SetModel(cr.proModel)

	cr.journal.Task(input + " [多角色协作]")
	cr.debugLog.SetTask(input + " [多角色协作]")
	ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

	startTime := time.Now()
	cr.sharedHistory = []llm.Message{
		{Role: "system", Content: "当前是多角色协作模式。所有角色的输出、分析、总结必须使用简体中文。以下是各角色的工作记录："},
	}

	artifacts := ChatArtifacts{}
	role := RolePM
	transition := ""

	roleLabels := map[Role]string{
		RolePM:        "产品经理：分析需求并制定技术规格",
		RoleDev:       "程序员：根据规格实现代码",
		RoleQA:        "测试：验证实现是否符合规格",
		RoleUserProxy: "用户代理：验收并决定是否通过",
	}

	maxExpand := maxChatRoomRounds * 2 // 轮次自动扩展上限（初始的 2 倍）
	roundLimit := maxChatRoomRounds
	qaRetryCount := 0   // QA→Dev 回退次数
	upRetryCount := 0   // UserProxy 回退次数
	for round := 0; round < roundLimit; round++ {
		if round > 0 && cr.timeout > 0 && time.Since(startTime) > cr.timeout {
			msg := fmt.Sprintf("多角色协作超时（%v），已执行 %d 轮", cr.timeout.Round(time.Second), round)
			cr.finishWithSummary(msg, ch)
			return
		}

		// 接近上限时自动扩展（更克制：剩 3 轮时才扩展）
		if round >= roundLimit-3 && roundLimit < maxExpand {
			roundLimit *= 2
			cr.addToHistory(fmt.Sprintf("[系统] 轮次上限已自动扩展到 %d 轮，请加速收敛，不要开启新的子任务。", roundLimit))
		}

		// 轮次紧张时强制收敛：UserProxy 必须给出最终结论
		if role == RoleUserProxy && round >= roundLimit-4 {
			cr.addToHistory("[系统] 这是最终验收。如果实现基本满足原始需求，请直接标注 [DONE] 通过；只对阻塞性问题回退，不要追求完美。")
		}
		ch <- Event{Type: EventRoleChange, Role: string(role), Message: roleLabels[role], TokenCount: cr.totalTokens}

		switch role {
		case RolePM:
			content, next := cr.runPMTurn(input, transition, ch)
			artifacts.Spec = content
			cr.addToHistory(fmt.Sprintf("[产品经理]\n%s", content))
			role = next

		case RoleDev:
			summary, next := cr.runDevTurn(artifacts, ch)
			artifacts.CodeSummary = summary
			cr.addToHistory(fmt.Sprintf("[程序员]\n%s", summary))
			role = next

		case RoleQA:
			report, next := cr.runQATurn(artifacts, ch)
			artifacts.TestReport = report
			cr.addToHistory(fmt.Sprintf("[测试]\n%s", report))
			if next == RoleDev {
				qaRetryCount++
				if qaRetryCount >= maxQARetries {
					// 回退超限：不再允许回退 Dev，强制 UserProxy 最终验收
					cr.addToHistory(fmt.Sprintf("[系统] QA 已回退 %d 次（上限 %d）。不再允许回退 Dev。请 UserProxy 综合当前进度做最终验收：实现基本满足需求则通过，否则明确列出阻塞问题。", qaRetryCount, maxQARetries))
					role = RoleUserProxy
				} else {
					cr.addToHistory(fmt.Sprintf("[系统] QA 回退 Dev（第 %d/%d 次）。Dev 请一次性修复全部已列出的问题并完整验证，不要引入新问题。", qaRetryCount, maxQARetries))
					role = RoleDev
				}
			} else {
				role = RoleUserProxy
			}

		case RoleUserProxy:
			decision, nextRole, done := cr.runUserProxyTurn(input, artifacts, ch)
			if done {
				ch <- Event{Type: EventDone, Message: "[用户代理 验收通过]\n" + decision, TokenCount: cr.totalTokens}
				cr.journal.Note("✅ " + decision)
				return
			}
			cr.addToHistory(fmt.Sprintf("[用户代理]\n%s", decision))
			if nextRole == RoleDev || nextRole == RolePM {
				upRetryCount++
				if upRetryCount >= maxUPRetries {
					// 回退超限：调用 LLM 生成最终总结，而不是只丢一句"有遗留问题"
					cr.addToHistory(fmt.Sprintf("[系统] UserProxy 已回退 %d 次（上限 %d），协作强制结束。请基于各角色的工作记录生成最终总结。", upRetryCount, maxUPRetries))
					summary := cr.generateFinalSummary(ch)
					ch <- Event{Type: EventDone, Message: summary, TokenCount: cr.totalTokens}
					cr.journal.Note("⚠️ " + summary)
					return
				}
			}
			role = nextRole
			transition = decision
		}
	}

	cr.finishWithSummary(fmt.Sprintf("多角色协作达到最大轮次（%d）", roundLimit), ch)
}

// ── 各角色执行方法 ──

// generateFinalSummary 强制验收通过时，调用 LLM 生成完整的交付总结。
// 与 finishWithSummary 不同，这个不是"失败"场景，而是"接受当前状态"。
func (cr *ChatRoom) generateFinalSummary(ch chan<- Event) string {
	prompt := "协作达到回退上限，请基于以上各角色的工作记录，用简体中文生成一份最终交付总结。格式：\n" +
		"### 已完成\n[逐条列出实际完成的功能和改动]\n" +
		"### 存在的问题\n[逐条列出 QA 发现但未修复的问题，及严重程度]\n" +
		"### 当前状态\n[一句话说明项目是否可运行、主要功能是否可用]\n" +
		"### 建议的后续步骤\n[用户接下来应该做什么]"
	history := append(append([]llm.Message{}, cr.sharedHistory...), llm.Message{Role: "user", Content: prompt})

	msg, usage, err := cr.client.Chat(history, nil)
	cr.totalTokens += usage.TotalTokens
	if err != nil {
		return fmt.Sprintf("协作强制结束（%v）", err)
	}
	return "【协作完成 · 达到回退上限】\n" + msg.Content
}

// finishWithSummary 多角色协作异常终止时，调用大模型基于各角色工作记录生成总结。
func (cr *ChatRoom) finishWithSummary(reason string, ch chan<- Event) {
	prompt := fmt.Sprintf("多角色协作因 %s 而终止。请基于各角色的工作记录，用简体中文生成总结：\n"+
		"### 已完成\n[逐条]\n### 未完成\n[逐条]\n### 建议\n[下一步做什么]", reason)
	history := append(append([]llm.Message{}, cr.sharedHistory...), llm.Message{Role: "user", Content: prompt})

	msg, usage, err := cr.client.Chat(history, nil)
	cr.totalTokens += usage.TotalTokens
	if err != nil {
		ch <- Event{Type: EventError, Message: fmt.Sprintf("协作终止（%s），且生成总结失败: %v", reason, err), TokenCount: cr.totalTokens}
		return
	}
	finalMsg := fmt.Sprintf("【协作终止 · %s】\n%s", reason, msg.Content)
	ch <- Event{Type: EventDone, Message: finalMsg, TokenCount: cr.totalTokens}
	cr.journal.Note("⚠️ " + reason + "：" + msg.Content)
}

// runPMTurn 产品经理：整合需求，基于项目概况直接输出技术规格。
// 不调用任何工具——探索代码是程序员的职责，PM 只做需求分析与规格设计。
func (cr *ChatRoom) runPMTurn(originalInput, transition string, ch chan<- Event) (string, Role) {
	ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

	contextBlock := cr.buildContextBlock()
	cr.debugLog.LLMRequest(len(pmSystemPrompt), 0, 0)
	taskLine := fmt.Sprintf("## 用户原始需求\n%s", originalInput)
	if transition != "" {
		taskLine += fmt.Sprintf("\n\n## 用户代理反馈（需重新澄清）\n%s", transition)
	}

	// 单次 LLM 调用直接输出规格（无工具）
	history := []llm.Message{
		{Role: "system", Content: pmSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("%s\n\n%s", contextBlock, taskLine)},
	}
	msg, usage, err := cr.client.Chat(history, nil)
	cr.totalTokens += usage.TotalTokens
	if err != nil {
		return fmt.Sprintf("PM 调用失败: %v", err), RoleDev
	}
	// 输出 PM 的思考/说明
	if strings.TrimSpace(msg.Content) != "" {
		ch <- Event{Type: EventMessage, Message: strings.TrimSpace(msg.Content), TokenCount: cr.totalTokens}
	}
	return msg.Content, RoleDev
}

// runDevTurn 程序员：带工具的子循环，按 PM 规格实现代码。
// 如果是 QA 回退的修复轮次（artifacts.TestReport 非空），使用更严格的约束。
func (cr *ChatRoom) runDevTurn(artifacts ChatArtifacts, ch chan<- Event) (string, Role) {
	ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

	isRetry := artifacts.TestReport != ""

	contextBlock := cr.buildContextBlock()
	var userMsg string
	if isRetry {
		userMsg = fmt.Sprintf("%s\n\n## 产品经理规格\n%s\n\n## QA 发现的问题（只修这些，不要做其他改动）\n%s",
			contextBlock, artifacts.Spec, artifacts.TestReport)
	} else {
		userMsg = fmt.Sprintf("%s\n\n## 产品经理规格\n%s", contextBlock, artifacts.Spec)
	}

	sysPrompt := devSystemPrompt
	maxRounds := maxDevToolCalls
	if isRetry {
		sysPrompt = devFixPrompt
		maxRounds = 4 // 修复模式：更少轮次
	}

	cr.debugLog.LLMRequest(len(sysPrompt), 0, len(cr.registry.Definitions()))
	devHistory := []llm.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: userMsg},
	}

	allTools := cr.registry.Definitions()
	step := 0
	callCount := 0
	readOnlyTurns := 0

	for toolTurn := 0; toolTurn < maxRounds; toolTurn++ {
		ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

		// 注入工作记忆
		if blob := workLogBlob(cr.workLog, toolTurn, callCount); blob != "" {
			devHistory = append(devHistory, llm.Message{Role: "system", Content: blob})
		}

		msg, usage, err := cr.client.Chat(devHistory, allTools)
		if err != nil {
			return fmt.Sprintf("Dev 调用失败: %v", err), RoleQA
		}
		cr.totalTokens += usage.TotalTokens
		cr.debugLog.LLMResponse(cr.client.Model(), len(msg.Content), toolCallNames(msg.ToolCalls), usage.PromptTokens, usage.CompletionTokens)
		devHistory = append(devHistory, msg)

		if len(msg.ToolCalls) > 0 && strings.TrimSpace(msg.Content) != "" {
			ch <- Event{Type: EventMessage, Message: strings.TrimSpace(msg.Content), TokenCount: cr.totalTokens}
		}

		if len(msg.ToolCalls) == 0 {
			return msg.Content, RoleQA
		}

		// ── 收敛检测 ──
		allReadOnly := true
		roundHasNewRead := false
		for _, tc := range msg.ToolCalls {
			if !isReadOnlyTool(tc.Function.Name) {
				allReadOnly = false
			}
			if tc.Function.Name == "read_file" && cr.hasNewReadTarget(tc.Function.Arguments) {
				roundHasNewRead = true
			}
		}

		// 只读轮次追踪（仅对重复读取计数）
		if allReadOnly && !roundHasNewRead {
			readOnlyTurns++
		} else {
			readOnlyTurns = 0
		}

		// ── 渐进引导：催促写代码但不禁止必要的读取 ──
		if !isRetry {
			// 首次开发：渐进式引导写代码，允许继续读关键文件
			if toolTurn == 2 {
				devHistory = append(devHistory, llm.Message{
					Role: "system",
					Content: "【进度提醒】已探索 3 轮。如果你已理解项目结构，请开始写代码。如果还需要读文件，限制在 1-2 个最关键的文件，读完后立即写代码。",
				})
			} else if toolTurn == 4 {
				devHistory = append(devHistory, llm.Message{
					Role: "system",
					Content: "【开始写代码】已进行 5 轮。现在应该已经充分了解了项目。请用 batch_write 一次性完成所有需要的修改。如果确实还需要读文件，最多再读 1 个。",
				})
			}
		} else {
			// 修复模式：QA 已指明问题，快速修复
			if toolTurn == 1 {
				devHistory = append(devHistory, llm.Message{
					Role: "system",
					Content: "【修复模式】QA 已指明具体问题。请最多再读 1 个文件确认位置，然后在下一轮用 batch_write 修复所有问题。",
				})
			}
		}

		// 重复阅读提醒（已读文件再读 = 浪费）
		if readOnlyTurns >= 3 {
			devHistory = append(devHistory, llm.Message{
				Role: "system",
				Content: fmt.Sprintf("【注意】已连续 %d 轮读取已看过的文件。请基于已知信息开始写代码。", readOnlyTurns),
			})
			readOnlyTurns = 0
		}

		// 同轮 read_file 合并检查
		readCount, hasBatchRead := 0, false
		for _, tc := range msg.ToolCalls {
			if tc.Function.Name == "read_file" {
				readCount++
				var rargs struct{ Paths []string `json:"paths"` }
				if json.Unmarshal([]byte(tc.Function.Arguments), &rargs) == nil && len(rargs.Paths) > 0 {
					hasBatchRead = true
				}
			}
		}
		if readCount >= 2 && !hasBatchRead {
			warning := "请合并为一次 read_file，使用 paths 参数。"
			for _, tc := range msg.ToolCalls {
				devHistory = append(devHistory, llm.Message{Role: "tool", ToolCallID: tc.ID, Content: warning})
			}
			continue
		}

		for _, tc := range msg.ToolCalls {
			callCount++
			step++

			toolArgs := compactArgs(tc.Function.Arguments)
			ch <- Event{Type: EventToolCall, ToolName: tc.Function.Name, ToolArgs: toolArgs, Step: step, CallCount: callCount, TokenCount: cr.totalTokens}

			result := cr.executeDevTool(0, tc, ch)
			isErr := strings.HasPrefix(result, "执行出错：")
			cr.workLog = append(cr.workLog, workEntry{tool: tc.Function.Name, args: compactArgs(tc.Function.Arguments), result: workSummary(result), isErr: isErr})
			ch <- Event{Type: EventToolResult, ToolName: tc.Function.Name, ToolResult: result, Step: step, TokenCount: cr.totalTokens}

			devHistory = append(devHistory, llm.Message{Role: "tool", ToolCallID: tc.ID, Content: result})
		}
	}

	return "程序员达到工具调用上限，实现可能不完整。", RoleQA
}

// toolCallNames 提取工具调用名称列表（用于 debug log）。
func toolCallNames(tcs []llm.ToolCall) []string {
	names := make([]string, len(tcs))
	for i, tc := range tcs {
		names[i] = tc.Function.Name
	}
	return names
}

// runQATurn 测试：轻量工具调用循环，可读文件+执行命令+搜索。
func (cr *ChatRoom) runQATurn(artifacts ChatArtifacts, ch chan<- Event) (string, Role) {
	ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

	contextBlock := cr.buildContextBlock()
	cr.debugLog.LLMRequest(len(qaSystemPrompt), 0, 4)
	context := fmt.Sprintf("%s\n\n## 产品经理规格\n%s\n\n## 程序员实现\n%s",
		contextBlock, artifacts.Spec, artifacts.CodeSummary)

	content := cr.runRoleWithTools(qaSystemPrompt, context,
		[]string{"read_file", "list_dir", "execute_shell", "grep_search"},
		5, ch)

	if strings.Contains(content, "[NEXT:dev]") {
		return content, RoleDev
	}
	return content, RoleUserProxy
}

// runUserProxyTurn 用户代理：轻量工具调用循环，只读，决定是否通过。
func (cr *ChatRoom) runUserProxyTurn(originalInput string, artifacts ChatArtifacts, ch chan<- Event) (string, Role, bool) {
	ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

	contextBlock := cr.buildContextBlock()
	cr.debugLog.LLMRequest(len(userProxySystemPrompt), 0, 2)
	context := fmt.Sprintf("%s\n\n## 用户原始需求\n%s\n\n## 产品经理规格\n%s\n\n## 程序员实现\n%s\n\n## 测试报告\n%s",
		contextBlock, originalInput, artifacts.Spec, artifacts.CodeSummary, artifacts.TestReport)

	content := cr.runRoleWithTools(userProxySystemPrompt, context,
		[]string{"read_file", "list_dir"},
		3, ch)

	if strings.Contains(content, "[DONE]") {
		return content, "", true
	}
	if strings.Contains(content, "[NEXT:dev]") {
		return content, RoleDev, false
	}
	// 默认回 PM
	return content, RolePM, false
}

// ── 辅助方法 ──

// runRoleWithTools 轻量工具调用循环，用于 QA/UserProxy 等非 Dev 角色。
// 最多 maxIter 轮工具调用，每次工具调用都会发送事件并通过审批门。
func (cr *ChatRoom) runRoleWithTools(systemPrompt, userContent string, allowedTools []string, maxIter int, ch chan<- Event) string {
	history := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}
	tools := cr.filterTools(allowedTools)
	var lastContent string
	readOnlyTurns := 0 // 连续只读轮次计数

	for i := 0; i < maxIter; i++ {
		// 连续 2 轮只读 → 注入收敛提示
		if readOnlyTurns >= 2 {
			history = append(history, llm.Message{
				Role:    "system",
				Content: "【收敛提示】你已连续多轮只读取/查看。基于已有信息，请立即输出结论，不要继续探索。",
			})
			readOnlyTurns = 0
		}

		// 注入工作记忆
		if blob := workLogBlob(cr.workLog, i, i+1); blob != "" {
			history = append(history, llm.Message{Role: "system", Content: blob})
		}

		msg, usage, err := cr.client.Chat(history, tools)
		if err != nil {
			return fmt.Sprintf("调用失败: %v", err)
		}
		cr.totalTokens += usage.TotalTokens
		var tcNames []string
		for _, tc := range msg.ToolCalls {
			tcNames = append(tcNames, tc.Function.Name)
		}
		cr.debugLog.LLMResponse(cr.client.Model(), len(msg.Content), tcNames, usage.PromptTokens, usage.CompletionTokens)
		history = append(history, msg)

		// 输出模型思考/说明文字
		if len(msg.ToolCalls) > 0 && strings.TrimSpace(msg.Content) != "" {
			ch <- Event{Type: EventMessage, Message: strings.TrimSpace(msg.Content), TokenCount: cr.totalTokens}
		}

		if len(msg.ToolCalls) == 0 {
			return msg.Content
		}
		// 保存最后的内容
		lastContent = msg.Content

		// 只读轮次追踪
		allReadOnly := true
		for _, tc := range msg.ToolCalls {
			if !isReadOnlyTool(tc.Function.Name) {
				allReadOnly = false
				break
			}
		}
		if allReadOnly {
			readOnlyTurns++
		} else {
			readOnlyTurns = 0
		}

		// 同轮 read_file 合并检查：2+ 个单独 read_file 且无 paths → 拒绝并提示
		readCount, hasBatchRead := 0, false
		for _, tc := range msg.ToolCalls {
			if tc.Function.Name != "read_file" {
				continue
			}
			readCount++
			var rargs struct {
				Paths []string `json:"paths"`
			}
			if json.Unmarshal([]byte(tc.Function.Arguments), &rargs) == nil && len(rargs.Paths) > 0 {
				hasBatchRead = true
			}
		}
		if readCount >= 2 && !hasBatchRead {
			warning := "检测到你尝试在同一轮调用 " + fmt.Sprint(readCount) + " 次 read_file。请立即合并为一次调用，使用 paths 参数（如 {\"paths\": [\"a.go\", \"b.go\"]}）。"
			for _, tc := range msg.ToolCalls {
				history = append(history, llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    warning,
				})
			}
			continue
		}

		for _, tc := range msg.ToolCalls {
			toolArgs := compactArgs(tc.Function.Arguments)
			ch <- Event{
				Type:      EventToolCall,
				ToolName:  tc.Function.Name,
				ToolArgs:  toolArgs,
				TokenCount: cr.totalTokens,
			}

			result := cr.executeDevTool(0, tc, ch)
			isErr := strings.HasPrefix(result, "执行出错：")
			cr.workLog = append(cr.workLog, workEntry{
				tool:   tc.Function.Name,
				args:   compactArgs(tc.Function.Arguments),
				result: workSummary(result),
				isErr:  isErr,
			})
			ch <- Event{
				Type:       EventToolResult,
				ToolName:   tc.Function.Name,
				ToolResult: result,
				TokenCount: cr.totalTokens,
			}

			history = append(history, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
	}

	// 达到上限，返回最后已知内容
	if lastContent != "" {
		return lastContent
	}
	return "（达到工具调用上限，以上为已收集的信息）"
}

// isReadOnlyTool 判断工具是否只读（不修改任何状态）。
func isReadOnlyTool(name string) bool {
	switch name {
	case "read_file", "list_dir", "grep_search":
		return true
	}
	return false
}

// isProductiveTool 判断工具是否有实质性产出（修改文件/执行命令/更新计划）。
// 用于停滞检测：连续多轮无产出 → 强制收敛。
func isProductiveTool(name string) bool {
	switch name {
	case "write_file", "batch_write", "execute_shell", "execute_python",
		"write_plan", "verify", "restore":
		return true
	}
	return false
}

// hasNewReadTarget 检查 read_file 参数中是否包含尚未完整读取的文件。
// 用于区分"探索新文件"（有意义的）和"重复读旧文件"（浪费轮次）。
func (cr *ChatRoom) hasNewReadTarget(argsRaw string) bool {
	var r struct {
		Path      string   `json:"path"`
		Paths     []string `json:"paths"`
		StartLine int      `json:"start_line"`
	}
	if json.Unmarshal([]byte(argsRaw), &r) != nil {
		return true // 解析失败，给 benefit of doubt
	}
	paths := r.Paths
	if len(paths) == 0 && r.Path != "" {
		paths = []string{r.Path}
	}
	isFullRead := r.StartLine <= 1
	for _, f := range paths {
		if !cr.readFiles[f] {
			return true // 至少有一个文件没完整读过 → 新探索
		}
		// 已完整读过但这次是分段读（start_line > 1）→ 也视为新探索
		if cr.readFiles[f] && !isFullRead {
			return true
		}
	}
	return false // 全部都是已完整读过的文件 → 重复
}

// executeDevTool 在 Dev 子循环中执行单个工具调用（含审批流程）。
func (cr *ChatRoom) executeDevTool(step int, tc llm.ToolCall, ch chan<- Event) string {
	// read_file 去重：重复读取已读文件 → 不执行
	if tc.Function.Name == "read_file" {
		var r struct {
			Path      string   `json:"path"`
			Paths     []string `json:"paths"`
			StartLine int      `json:"start_line"`
		}
		if json.Unmarshal([]byte(tc.Function.Arguments), &r) == nil {
			paths := r.Paths
			if len(paths) == 0 && r.Path != "" {
				paths = []string{r.Path}
			}
			isFullRead := r.StartLine <= 1
			if len(paths) > 0 {
				var dup []string
				for _, f := range paths {
					if cr.readFiles[f] {
						dup = append(dup, f)
					} else if isFullRead {
						cr.readFiles[f] = true
					}
				}
				if len(dup) > 0 {
					return fmt.Sprintf("（去重）以下文件已完整读取，内容在上文上下文中，请直接使用，不要重复或分段读取：%s", strings.Join(dup, ", "))
				}
			}
		}
	} else if tc.Function.Name == "list_dir" {
		// list_dir 去重：相同参数重复执行 → 提示
		key := compactArgs(tc.Function.Arguments)
		if key != "" {
			if cr.listDirs[key] {
				return fmt.Sprintf("（去重）相同参数的 list_dir 已执行过，结果在上文上下文中，请直接使用，不要重复列出：%s", key)
			}
			cr.listDirs[key] = true
		}
	} else if tc.Function.Name == "grep_search" {
		// grep_search 去重：相同 pattern 重复执行 → 提示
		var g struct {
			Pattern string `json:"pattern"`
		}
		if json.Unmarshal([]byte(tc.Function.Arguments), &g) == nil && g.Pattern != "" {
			if cr.grepPatterns[g.Pattern] {
				return fmt.Sprintf("（去重）相同 pattern 的 grep_search 已执行过，结果在上文上下文中，请直接使用：%s", g.Pattern)
			}
			cr.grepPatterns[g.Pattern] = true
		}
	}
	tool, ok := cr.registry.Lookup(tc.Function.Name)
	if !ok {
		return fmt.Sprintf("错误：不存在名为 %q 的工具", tc.Function.Name)
	}
	args := json.RawMessage(tc.Function.Arguments)
	cr.debugLog.ToolCall(step, tc.Function.Name, tc.Function.Arguments)

	// 重复命令检测
	if tc.Function.Name == "execute_shell" {
		key := compactArgs(tc.Function.Arguments)
		cr.recentCmds[key]++
		if cr.recentCmds[key] >= 3 {
			return "检测到连续 " + fmt.Sprint(cr.recentCmds[key]) +
				" 次执行相同命令。已自动拒绝以打破死循环。请分析根因，不要重复执行。"
		}
	}

	// 审批
	if apv, ok := tool.(tools.Approvable); ok {
		d := apv.Review(args)
		if d.NeedApproval {
			if d.Danger == "" && cr.approver.isApproved(d.ScopeKind, d.Scope) {
				// 已授权，跳过
			} else {
				replyCh := make(chan ApprovalReply, 1)
				ch <- Event{
					Type:     EventApprovalRequest,
					ToolName: tc.Function.Name,
					ToolArgs: compactArgs(tc.Function.Arguments),
					ApprovalRequest: &ApprovalRequest{
						ToolName:  tc.Function.Name,
						Arguments: compactArgs(tc.Function.Arguments),
						Warning:   d.Danger,
						Callback:  replyCh,
					},
				}
				reply := <-replyCh
				if !reply.Allowed {
					return "用户拒绝执行该操作。请考虑替代方案。"
				}
				if reply.Permanent && d.Danger == "" {
					cr.approver.remember(d.ScopeKind, d.Scope)
				}
			}
		}
	}

	out, err := tool.Execute(args)
	if err != nil {
		res := "执行出错：" + err.Error()
		cr.debugLog.ToolResult(step, tc.Function.Name, res, true)
		return res
	}
	cr.debugLog.ToolResult(step, tc.Function.Name, out, false)
	return out
}

// filterTools 按名称白名单过滤工具定义。
// 注意：空列表 = 不提供任何工具（角色只能直接输出，不能调用工具）。
func (cr *ChatRoom) filterTools(allowed []string) []llm.Tool {
	if len(allowed) == 0 {
		return nil
	}
	allDefs := cr.registry.Definitions()
	allow := make(map[string]bool, len(allowed))
	for _, n := range allowed {
		allow[n] = true
	}
	var filtered []llm.Tool
	for _, t := range allDefs {
		if allow[t.Function.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// buildContextBlock 构建共享上下文的文本块。
func (cr *ChatRoom) buildContextBlock() string {
	var b strings.Builder
	for _, m := range cr.sharedHistory {
		if m.Role == "system" {
			b.WriteString(m.Content + "\n")
		} else {
			b.WriteString(m.Content + "\n\n")
		}
	}
	return b.String()
}

// addToHistory 追加一条记录到共享历史。
func (cr *ChatRoom) addToHistory(content string) {
	cr.sharedHistory = append(cr.sharedHistory, llm.Message{
		Role:    "assistant",
		Content: content,
	})
}

// ── 角色 System Prompt ──

const pmSystemPrompt = `你是资深产品经理。将用户需求转化为清晰、可执行的技术规格。

## 核心纪律
1. **一次到位**：直接基于项目概况和用户需求输出规格，不要发散。不需要调用工具探索代码。
2. **聚焦核心**：只设计用户明确要求的功能，不追加"锦上添花"的扩展。
3. **可执行**：规格要具体到程序员可以直接开始编码的程度。

## 输出格式
### 功能概述 + 技术方案（文件结构、选型）
### 接口/数据定义（关键的数据结构和接口）
### 边界条件 + 验收标准（逐条可验证）

末尾标注 [NEXT:dev]。全部输出（分析、规格、说明）必须使用简体中文。如对代码不确定，标注「需程序员确认」。`

const devSystemPrompt = `你是高级程序员。请根据产品经理的规格实现代码。

## 核心纪律
1. **快速交付**：调研 1-2 轮 → 写代码 1 轮 → 验证 1-2 轮，总计控制在 5 轮以内。
2. **做完即停**：编译通过就输出 [NEXT:qa]，不要追加修改。
3. **调研适度**：最多 list_dir 1 次 + 批量 read_file 1 次，然后必须开始写代码。禁止反复读取。
4. **批量操作**：多文件读取用 paths 数组，多文件写入用 batch_write，严禁逐个操作。

## 输出
总结创建/修改的文件、关键决策、验证结果，末尾标注 [NEXT:qa]。全部输出必须使用简体中文。`

const devFixPrompt = `你是高级程序员。QA 已发现问题，你需要修复代码。不要重新探索项目——你已经了解代码结构。

## 核心纪律
1. **只修 QA 列出的问题**：不要改其他文件，不要优化无关代码。
2. **1 轮内开始写代码**：最多 1 次 read_file 确认问题位置，然后立即 batch_write 修复。
3. **修复后编译验证**：用 execute_shell 确认编译通过。
4. **批量操作**：多个文件用 batch_write 一次性修复。

## 输出
列出修复的文件和验证结果，末尾标注 [NEXT:qa]。全部输出必须使用简体中文。`

const qaSystemPrompt = `你是测试工程师。对照规格和实现进行测试验证。

## 核心纪律
1. **快速验证**：1-2 轮内完成测试并输出结论，不要反复读取。
2. **直接判断**：全部通过 → [NEXT:user_proxy]；有 bug → [NEXT:dev] + 具体 bug 描述。
3. **批量读取**：需检查多个文件时用 read_file paths 一次完成。

## 输出
✅ 通过项 / ❌ 失败项 + 总体评估。末尾标注 [NEXT:user_proxy] 或 [NEXT:dev]。全部输出必须使用简体中文。`

const userProxySystemPrompt = `你是用户代理，代表用户验收最终交付。

## 核心纪律
1. **快速决策**：1-2 轮内必须给出最终结论。不要反复读取文件。
2. **务实验收**：基本满足需求 → [DONE]；需要小修 → [NEXT:dev] + 具体说明；需求偏差 → [NEXT:pm]。
3. **批量读取**：需检查多个文件时用 read_file paths 一次完成。

## 输出
验收结论 + 问题列表（如有）。末尾标注 [DONE]、[NEXT:dev] 或 [NEXT:pm]。全部输出必须使用简体中文。`

// ── 任务复杂度判定 ──

// ClassifyChatRoom 用轻量 LLM 调用判断任务是否需要多角色协作。
// 返回 true 表示应该使用 ChatRoom 模式。
func ClassifyChatRoom(input string, client *llm.Client) bool {
	complexKeywords := []string{
		"设计", "架构", "重构", "系统", "完整项目", "框架",
		"从零", "搭建", "实现一个", "开发一个", "创建项目",
		"多个模块", "多文件", "前后端", "数据库", "API",
	}
	lower := strings.ToLower(input)
	for _, kw := range complexKeywords {
		if strings.Contains(lower, kw) {
			// 先用关键词做快速筛选，命中后让 LLM 最终判断
			return classifyByLLM(input, client)
		}
	}
	return false
}

func classifyByLLM(input string, client *llm.Client) bool {
	prompt := `判断以下用户请求是否适合使用"多角色协作模式"（产品经理→程序员→测试→用户代理轮流执行）。

适合多角色协作的特征：
- 需求描述模糊，需要先澄清再实现
- 涉及完整的系统/模块设计+代码实现
- 需要测试验证的复杂功能
- 用户说了"设计并实现"、"从零搭建"、"开发一个XX系统"

不适合的特征：
- 简单的代码修改、bug 修复
- 单一文件的读写
- 查询、解释、闲聊
- 用户给出了非常具体的实现细节

用户请求: ` + input + `

只回答一个字: 是 或 否`

	msg, _, err := client.Chat([]llm.Message{{Role: "user", Content: prompt}}, nil)
	if err != nil {
		return false
	}

	result := strings.TrimSpace(msg.Content)
	return strings.HasPrefix(result, "是")
}
