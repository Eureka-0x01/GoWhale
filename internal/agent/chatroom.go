package agent

import (
	"encoding/json"
	"fmt"
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
	maxChatRoomRounds = 12  // 全局角色轮次上限
	maxDevToolCalls   = 10  // Dev 子循环最大工具调用次数
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
	workspace  string
	totalTokens int

	// 共享上下文
	sharedHistory []llm.Message
	recentCmds   map[string]int

	// 模型配置
	fastModel string
	proModel  string
}

// NewChatRoom 创建多角色协作 Agent。
func NewChatRoom(client *llm.Client, registry *tools.Registry, approver *Approver, workspace, fastModel, proModel string) *ChatRoom {
	j := NewJournal(workspace)
	return &ChatRoom{
		client:      client,
		registry:    registry,
		approver:    approver,
		journal:     j,
		workspace:   workspace,
		recentCmds:  map[string]int{},
		sharedHistory: make([]llm.Message, 0),
		fastModel:   fastModel,
		proModel:    proModel,
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
	// 使用 pro 模型（多角色任务通常是复杂任务）
	cr.client.SetModel(cr.proModel)

	cr.journal.Task(input + " [多角色协作]")
	ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

	cr.sharedHistory = []llm.Message{
		{Role: "system", Content: "当前是多角色协作模式。以下是各角色的工作记录："},
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

	for round := 0; round < maxChatRoomRounds; round++ {
		ch <- Event{Type: EventRoleChange, Role: string(role), Message: roleLabels[role], TokenCount: cr.totalTokens}

		switch role {
		case RolePM:
			content, next := cr.runPMTurn(input, transition, ch)
			artifacts.Spec = content
			cr.addToHistory(fmt.Sprintf("[产品经理]\n%s", content))
			role = next

		case RoleDev:
			summary, next := cr.runDevTurn(artifacts.Spec, ch)
			artifacts.CodeSummary = summary
			cr.addToHistory(fmt.Sprintf("[程序员]\n%s", summary))
			role = next

		case RoleQA:
			report, next := cr.runQATurn(artifacts, ch)
			artifacts.TestReport = report
			cr.addToHistory(fmt.Sprintf("[测试]\n%s", report))
			role = next

		case RoleUserProxy:
			decision, nextRole, done := cr.runUserProxyTurn(input, artifacts, ch)
			if done {
				ch <- Event{Type: EventDone, Message: "[用户代理 验收通过]\n" + decision, TokenCount: cr.totalTokens}
				cr.journal.Note("✅ " + decision)
				return
			}
			cr.addToHistory(fmt.Sprintf("[用户代理]\n%s", decision))
			role = nextRole
			transition = decision
		}
	}

	ch <- Event{
		Type:       EventError,
		Message:    fmt.Sprintf("多角色协作达到最大轮次（%d），任务未完成。当前进度已记录。", maxChatRoomRounds),
		TokenCount: cr.totalTokens,
	}
}

// ── 各角色执行方法 ──

// runPMTurn 产品经理：轻量工具调用循环，可阅读现有代码后输出技术规格。
func (cr *ChatRoom) runPMTurn(originalInput, transition string, ch chan<- Event) (string, Role) {
	ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

	contextBlock := cr.buildContextBlock()
	taskLine := fmt.Sprintf("## 用户原始需求\n%s", originalInput)
	if transition != "" {
		taskLine += fmt.Sprintf("\n\n## 用户代理反馈（需重新澄清）\n%s", transition)
	}

	content := cr.runRoleWithTools(pmSystemPrompt,
		fmt.Sprintf("%s\n\n%s", contextBlock, taskLine),
		[]string{"read_file", "list_dir", "grep_search"},
		3, ch)
	return content, RoleDev
}

// runDevTurn 程序员：带工具的子循环，按 PM 规格实现代码。
func (cr *ChatRoom) runDevTurn(spec string, ch chan<- Event) (string, Role) {
	ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

	contextBlock := cr.buildContextBlock()
	devHistory := []llm.Message{
		{Role: "system", Content: devSystemPrompt},
		{Role: "user", Content: fmt.Sprintf("%s\n\n## 产品经理规格\n%s", contextBlock, spec)},
	}

	allTools := cr.registry.Definitions()
	step := 0
	callCount := 0

	for toolTurn := 0; toolTurn < maxDevToolCalls; toolTurn++ {
		ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

		msg, usage, err := cr.client.Chat(devHistory, allTools)
		if err != nil {
			return fmt.Sprintf("Dev 调用失败: %v", err), RoleQA
		}
		cr.totalTokens += usage.TotalTokens
		devHistory = append(devHistory, msg)

		if len(msg.ToolCalls) == 0 {
			// 无工具调用 → Dev 完成
			return msg.Content, RoleQA
		}

		for _, tc := range msg.ToolCalls {
			callCount++
			step++

			toolArgs := compactArgs(tc.Function.Arguments)
			ch <- Event{
				Type:       EventToolCall,
				ToolName:   tc.Function.Name,
				ToolArgs:   toolArgs,
				Step:       step,
				CallCount:  callCount,
				TokenCount: cr.totalTokens,
			}

			result := cr.executeDevTool(tc, ch)
			ch <- Event{
				Type:       EventToolResult,
				ToolName:   tc.Function.Name,
				ToolResult: result,
				Step:       step,
				TokenCount: cr.totalTokens,
			}

			devHistory = append(devHistory, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
	}

	return "程序员达到工具调用上限，实现可能不完整。", RoleQA
}

// runQATurn 测试：轻量工具调用循环，可读文件+执行命令+搜索。
func (cr *ChatRoom) runQATurn(artifacts ChatArtifacts, ch chan<- Event) (string, Role) {
	ch <- Event{Type: EventThinking, TokenCount: cr.totalTokens}

	contextBlock := cr.buildContextBlock()
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

// runRoleWithTools 轻量工具调用循环，用于 PM/QA/UserProxy 等非 Dev 角色。
// 最多 maxIter 轮工具调用，每次工具调用都会发送事件并通过审批门。
func (cr *ChatRoom) runRoleWithTools(systemPrompt, userContent string, allowedTools []string, maxIter int, ch chan<- Event) string {
	history := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}
	tools := cr.filterTools(allowedTools)
	var lastContent string

	for i := 0; i < maxIter; i++ {
		msg, usage, err := cr.client.Chat(history, tools)
		if err != nil {
			return fmt.Sprintf("调用失败: %v", err)
		}
		cr.totalTokens += usage.TotalTokens
		history = append(history, msg)

		if len(msg.ToolCalls) == 0 {
			return msg.Content
		}
		// 保存最后的内容（如果工具调用后没有额外内容）
		lastContent = msg.Content

		for _, tc := range msg.ToolCalls {
			toolArgs := compactArgs(tc.Function.Arguments)
			ch <- Event{
				Type:      EventToolCall,
				ToolName:  tc.Function.Name,
				ToolArgs:  toolArgs,
				TokenCount: cr.totalTokens,
			}

			result := cr.executeDevTool(tc, ch)
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

// executeDevTool 在 Dev 子循环中执行单个工具调用（含审批流程）。
func (cr *ChatRoom) executeDevTool(tc llm.ToolCall, ch chan<- Event) string {
	tool, ok := cr.registry.Lookup(tc.Function.Name)
	if !ok {
		return fmt.Sprintf("错误：不存在名为 %q 的工具", tc.Function.Name)
	}
	args := json.RawMessage(tc.Function.Arguments)

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
		return "执行出错：" + err.Error()
	}
	return out
}

// filterTools 按名称白名单过滤工具定义。
func (cr *ChatRoom) filterTools(allowed []string) []llm.Tool {
	allDefs := cr.registry.Definitions()
	if len(allowed) == 0 {
		return allDefs
	}
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

const pmSystemPrompt = `你是一个资深产品经理。你需要将用户的模糊需求转化为清晰、可执行的技术规格。

## 工作流程
1. 分析用户原始需求，识别核心目标和约束
2. 如果需求有歧义，先提出假设并标注
3. 输出技术规格文档

## 输出格式
### 功能概述
[一段话描述要实现什么]

### 技术方案
- 文件结构：列出需要创建/修改的文件
- 技术选型：语言、框架、库（如适用）

### 接口/数据定义
- 输入参数
- 输出格式
- 关键数据结构

### 边界条件
- 正常流程
- 异常处理
- 性能约束（如适用）

### 验收标准
1. [具体可验证的标准]
2. ...
3. ...

完成后末尾标注 [NEXT:dev]

注意：
- 不要写代码实现，不要写具体的函数体
- 你可以使用 read_file / list_dir / grep_search 了解现有项目结构
- 规格要具体，程序员拿到可以直接开始编码`

const devSystemPrompt = `你是一个高级程序员。请根据产品经理的规格实现代码。

## 工作规则
- 使用 write_file / batch_write 创建或修改文件
- 多文件操作必须用 batch_write 一次性完成
- 实现后用 execute_shell 编译/运行验证
- 遵循项目现有代码风格和目录结构
- 不要修改规格之外的内容
- 不确定的地方先 read_file 确认，不要猜测

## 输出格式
实现完成后，用一段文字总结：
- 创建/修改了哪些文件
- 关键实现决策
- 编译/运行验证结果

末尾标注 [NEXT:qa]

注意：
- 你可以使用任何可用工具（读、写、执行命令）
- 每次工具调用都需审批，合理规划减少调用次数
- 如果实现过程中发现规格问题，标注在总结中`

const qaSystemPrompt = `你是一个测试工程师。请对照产品经理的规格和程序员的实现进行测试。

## 工作规则
- 使用 execute_shell 运行测试命令
- 使用 read_file 检查代码质量
- 逐条对照验收标准，标记通过/失败
- 发现问题要具体描述：哪条验收标准未满足、错误现象是什么

## 输出格式
### 测试结果
- ✅ 通过项：[列出]
- ❌ 失败项：[列出具体问题和复现步骤]

### 总体评估
[一句话总结]

按以下规则标注末尾：
- 如果全部通过 → 末尾标注 [NEXT:user_proxy]
- 如果发现 bug → 末尾标注 [NEXT:dev] 并附详细 bug 描述

注意：
- 你可以使用 execute_shell / read_file / grep_search / list_dir
- 不要修改任何代码
- 测试要具体，不要说"看起来没问题"`

const userProxySystemPrompt = `你是用户代理，代表提需求的最终用户进行验收。

## 审查项目
请综合审查以下内容：
1. 用户原始需求
2. 产品经理的技术规格
3. 程序员的代码实现
4. 测试工程师的测试报告

## 验收标准
- 实现是否完整覆盖原始需求？
- 测试是否充分？
- 是否有遗漏的功能？
- 代码质量是否可接受？

## 输出格式
### 验收结论
[一段话总结]

### 问题（如有）
[逐条列出]

按以下规则标注末尾：
- 完全满意，验收通过 → [DONE]
- 需要程序员小修（改几行代码）→ [NEXT:dev] 并说明要改什么
- 需求理解有偏差，需要产品经理重新澄清 → [NEXT:pm] 并说明哪里不对

注意：
- 你可以使用 read_file / list_dir 查看代码
- 从用户角度出发，不纠结于实现细节
- 如果原始需求简单但规格过度设计，指出过度工程化问题`

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
