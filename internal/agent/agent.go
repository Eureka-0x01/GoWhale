package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"gowhale/internal/llm"
	"gowhale/internal/tools"
)

// MaxToolCalls 单次任务总工具调用次数上限（含只读工具）。
const MaxToolCalls = 40

// Agent 用工具调用循环驱动大模型完成任务。
type Agent struct {
	client     *llm.Client
	registry   *tools.Registry
	approver   *Approver
	journal    *Journal
	debugLog   *DebugLog
	maxTurns   int
	spinner    Spinner
	history    []llm.Message
	recentCmds map[string]int
	fastModel  string // 快速模型（简单问题）
	proModel   string // 复杂模型（多步推理/代码生成）
	totalTokens int   // 累计 token 消耗
}

const skillRules = "" +
	"## 运行环境\n" +
	"- shell 类型和可用命令参见上方执行环境信息，严格按照检测到的 shell 写命令。\n" +
	"- 长期服务用 background=true，绝对不用 start/nohup/&。\n" +
	"- 你已在工作目录中，直接执行命令即可，不需要开头加 cd。\n" +
	"\n" +
	"## 文件操作（CRITICAL — 最高优先级）\n" +
	"### 强制批量规则（read_file + list_dir 通用）\n" +
	"1. **需要读取/列出 2 个及以上文件/目录时，必须使用 `paths` 参数一次完成**，不可逐个调用。\n" +
	"   `paths` 是一个字符串数组，如 [\"a.go\", \"b.go\"]，最多 20 个。\n" +
	"   read_file 和 list_dir 均支持 `path`（单个）+ `paths`（批量）。\n" +
	"2. **大文件读取：超过 200 行的文件默认只显示头尾各 50 行摘要。**\n" +
	"   如需查看中间部分，用 `start_line`（起始行号）+ `max_lines`（行数）参数指定行范围。\n" +
	"   **不要**用 sed/head/tail 等命令读文件，Windows 不可用且浪费工具调用轮次。\n" +
	"   示例：{\"path\": \"ui/app.go\", \"start_line\": 450, \"max_lines\": 130}\n" +
	"3. 调研项目结构时，先 list_dir 看目录，再一次性用 read_file+paths 批量读取关键文件。\n" +
	"   示例：{\"paths\": [\"main.go\", \"go.mod\", \"agent/agent.go\", \"config/config.go\"]}\n" +
	"\n" +
	"### 强制批量写入规则\n" +
	"1. **严禁对多个文件依次调用 write_file 工具**。每次模型响应中，针对 write_file 或 batch_write 的调用，最多只能出现 1 次。\n" +
	"2. 如果你的任务需要创建或修改 **2 个及以上**的文件，**必须**使用 batch_write 工具。\n" +
	"   batch_write 的 `files` 参数是一个 JSON 对象(Map)，键为文件路径（相对根目录），值为文件完整内容，一次性提交所有文件。\n" +
	"3. write_file **仅限**只需写 1 个文件时使用。任何多文件场景使用 write_file 都是违规。\n" +
	"4. 在执行写入前，请先通过 read_file（必要时用 paths 批量读取）和 list_dir 完成所有调研，制定完整计划，然后**一次性执行** batch_write。\n" +
	"   禁止「写 A → 读 A → 写 B」的循环操作模式。\n" +
	"5. 写入前先用 list_dir / read_file 了解项目结构，不要臆测。\n" +
	"6. **禁止猜测文件路径**。read_file / write_file 失败提示文件不存在时，立即用 list_dir 查看目录结构，不要换路径重试。\n" +
	"   每次 read_file 失败都要先确认文件是否真实存在，否则会浪费大量工具调用轮次。\n" +
	"\n" +
	"### 多文件创建示例（必须遵循）\n" +
	"**正确做法**——一次性 batch_write：\n" +
	"用户请求：「创建一个 index.html, style.css, app.js」\n" +
	"你的响应中必须有且仅有一个 batch_write 调用：\n" +
	"```json\n" +
	"{\"tool_calls\":[{\"name\":\"batch_write\",\"arguments\":{\"files\":{\"index.html\":\"<html>...</html>\",\"style.css\":\"body{...}\",\"app.js\":\"console.log('hi');\"}}}]}\n" +
	"```\n" +
	"**错误做法**——绝对禁止：分三次调用 write_file。（这会导致工具调用轮次耗尽，任务强制终止）\n" +
	"\n" +
	"## Python 执行\n" +
	"- 运行 Python 代码直接用 execute_python，**禁止**先写 .py 文件再执行。不需要把代码保存到工作目录。\n" +
	"- execute_python 自带沙箱隔离，会自动创建临时目录并在执行后清理。\n" +
	"\n" +
	"## 命令执行纪律\n" +
	"1. 执行构建/运行命令前,先用 read_file 确认相关配置文件。\n" +
	"2. **启动服务必须设 background=true**。绝对不用 start/nohup/&。\n" +
	"3. **命令失败先诊断**:仔细读错误输出,定位根因;同一命令最多 2 种写法。\n" +
	"4. **已确认在运行的服务不要 kill**。端口在监听→报告成功。\n" +
	"5. 命令输出如实报告,不粉饰失败、不猜测原因。\n" +
	"\n" +
	"## 任务拆解\n" +
	"1. 3 步以上的复杂任务,**第一个动作**必须是 write_plan 写出计划。\n" +
	"2. 每完成一步,write_plan 更新状态。\n" +
	"\n" +
	"## 工具调用预算\n" +
	"- 单次任务总工具调用次数限制为 40 次（含只读工具）。\n" +
	"- 当剩余调用次数不足 3 次时，系统会拒绝多个 write_file 调用并要求你合并为 batch_write。\n" +
	"- 合理规划：调研阶段用 2-3 次（list_dir + read_file），然后 1 次 batch_write 完成所有文件。\n" +
	"- 验证阶段（编译、测试）额外需要 2-3 次，总计控制在 10 次以内，不要挥霍。\n" +
	"\n" +
	"## 验证与完成\n" +
	"- **声称完成前**:read_file 确认改动、编译通过验证、所有 plan 步骤 done。\n" +
	"- 先读后写:未读过的文件不要凭记忆猜测。\n" +
	"- 审批被拒绝时解释原因、提供替代方案,不强行绕过。\n" +
	"- 完成后用两句话总结做了什么，最后一行只写「执行完成」。注意：只说执行完成，不要再多说其他话。"

// New 创建 Agent。maxTurns 为单次请求内的最大工具调用轮数。
func New(client *llm.Client, registry *tools.Registry, approver *Approver, maxTurns int, workspace string, fastModel, proModel string) *Agent {
	if err := os.MkdirAll(workspace, 0o755); err == nil {
		ensureDefaultConstitution(workspace)
	}
	j := NewJournal(workspace)
	dl := NewDebugLog(workspace)
	constitution := loadConstitution(workspace)
	base := egoBlock(workspace, constitution) + "\n" + skillRules

	history := []llm.Message{{Role: "system", Content: base}}
	if recent := strings.TrimSpace(j.Recent(2000)); recent != "" {
		history = append(history, llm.Message{
			Role: "system",
			Content: "以下是你在本工作目录之前的工作记录（**仅作参考**）。\n" +
				"已完成的任务不需要重复；失败的操作需要先重新验证是否仍然失败。\n" +
				"如果记录中提及的目录不是当前工作目录，不要尝试去那里操作。\n\n" + recent,
		})
	}
	return &Agent{
		client:     client,
		registry:   registry,
		approver:   approver,
		journal:    j,
		debugLog:   dl,
		maxTurns:   maxTurns,
		recentCmds: map[string]int{},
		history:    history,
		fastModel:  fastModel,
		proModel:   proModel,
	}
}

// ── 导出接口（供 TUI 使用）──

// Messages 返回当前消息历史副本。
func (a *Agent) Messages() []llm.Message {
	cp := make([]llm.Message, len(a.history))
	copy(cp, a.history)
	return cp
}

// ModelName 返回当前使用的模型名。
func (a *Agent) ModelName() string { return a.client.Model() }

// GetApprover 返回审批器实例（供 TUI 注入决策）。
func (a *Agent) GetApprover() *Approver { return a.approver }

// ── RunAsync：事件驱动执行 ──

// RunAsync 在独立 goroutine 中执行任务，通过 channel 发送事件。
// 调用方负责从 channel 读取并消费事件。完成后 channel 会被关闭。
func (a *Agent) RunAsync(input string) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		a.runLoop(input, ch)
	}()
	return ch
}

// runLoop 核心执行循环（在 goroutine 中运行）。
func (a *Agent) runLoop(input string, ch chan<- Event) {
	complex := a.classify(input)
	if complex {
		a.client.SetModel(a.proModel)
	} else {
		a.client.SetModel(a.fastModel)
	}

	a.journal.Task(input + fmt.Sprintf(" [模型: %s]", a.client.Model()))
	a.debugLog.SetTask(input)
	a.history = append(a.history, llm.Message{Role: "user", Content: input})
	defs := a.registry.Definitions()
	step := 0
	callCount := 0

	for turn := 0; turn < a.maxTurns; turn++ {
		ch <- Event{Type: EventThinking, TokenCount: a.totalTokens}

		a.debugLog.LLMRequest(len(a.history[0].Content), len(a.history), len(defs))
		msg, usage, err := a.client.Chat(a.history, defs)
		if err != nil {
			a.debugLog.Error(fmt.Sprintf("调用失败: %v", err))
			ch <- Event{Type: EventError, Message: fmt.Sprintf("调用失败: %v", err), TokenCount: a.totalTokens}
			return
		}
		a.totalTokens += usage.TotalTokens
		a.history = append(a.history, msg)

		// 记录 LLM 响应
		tcNames := make([]string, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			tcNames[i] = tc.Function.Name
		}
		a.debugLog.LLMResponse(a.client.Model(), len(msg.Content), tcNames, usage.PromptTokens, usage.CompletionTokens)

		// 无工具调用 → 任务完成
		if len(msg.ToolCalls) == 0 {
			a.debugLog.Done(msg.Content, a.totalTokens)
			ch <- Event{Type: EventDone, Message: msg.Content, TokenCount: a.totalTokens}
			a.journal.Note("✅ " + msg.Content)
			return
		}

		// ── 批量 read_file 强制拦截：≥2 个单独 read_file（没用 paths）→ 拒绝 ──
		readFileCount := 0
		hasBatchRead := false
		for _, tc := range msg.ToolCalls {
			if tc.Function.Name == "read_file" {
				readFileCount++
				var args struct {
					Paths []string `json:"paths"`
				}
				if json.Unmarshal([]byte(tc.Function.Arguments), &args) == nil && len(args.Paths) > 0 {
					hasBatchRead = true
				}
			}
		}
		if readFileCount >= 2 && !hasBatchRead {
			a.debugLog.LimitHit("多个单独 read_file 未合并", readFileCount, MaxToolCalls)
			warning := "检测到你尝试在同一轮调用 " + fmt.Sprint(readFileCount) + " 次 read_file。" +
				"这严重浪费工具调用轮次。请立即合并为一次 read_file 调用，使用 `paths` 参数传入所有文件路径的数组。" +
				"例如：{\"paths\": [\"a.go\", \"b.go\", \"c.go\"]}。不要再逐个调用。"
			for _, tc := range msg.ToolCalls {
				step++
				a.history = append(a.history, llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    warning,
				})
			}
			continue
		}

		// ── 工具调用预算检查 ──
		if callCount >= MaxToolCalls {
			a.debugLog.LimitHit("达到最大工具调用次数", callCount, MaxToolCalls)
			for _, tc := range msg.ToolCalls {
				a.history = append(a.history, llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    "已达到最大工具调用次数限制，任务终止。",
				})
			}
			ch <- Event{Type: EventError, Message: "达到最大工具调用次数限制", TokenCount: a.totalTokens}
			return
		}

		// 预算紧张时的警告
		if callCount >= MaxToolCalls-2 {
			hasBatchWrite := false
			writeFileCount := 0
			for _, tc := range msg.ToolCalls {
				if tc.Function.Name == "batch_write" {
					hasBatchWrite = true
				}
				if tc.Function.Name == "write_file" {
					writeFileCount++
				}
			}
			if !hasBatchWrite && writeFileCount > 0 {
				warning := "工具预算警告：剩余调用次数不足。请立即将未完成的所有文件合并为一次 batch_write 调用并重新提交，否则任务将强制终止。"
				for _, tc := range msg.ToolCalls {
					step++
					a.history = append(a.history, llm.Message{
						Role:       "tool",
						ToolCallID: tc.ID,
						Content:    warning,
					})
				}
				continue
			}
		}

		// 执行工具调用
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
				TokenCount: a.totalTokens,
			}
			a.journal.Tool(tc.Function.Name, toolArgs)
			a.debugLog.ToolCall(step, tc.Function.Name, tc.Function.Arguments)

			result := a.executeWithApproval(tc, ch)
			isErr := strings.HasPrefix(result, "执行出错：")
			a.debugLog.ToolResult(step, tc.Function.Name, result, isErr)
			ch <- Event{
				Type:       EventToolResult,
				ToolName:   tc.Function.Name,
				ToolResult: result,
				Step:       step,
				TokenCount: a.totalTokens,
				IsError:    isErr,
			}

			a.history = append(a.history, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
	}
	ch <- Event{Type: EventError, Message: fmt.Sprintf("达到最大轮数 %d，任务未完成", a.maxTurns), TokenCount: a.totalTokens}
}

// executeWithApproval 执行单个工具调用（含审批流程）。
// 如果需要审批，发送 EventApprovalRequest 并通过其 Callback 等待外部决策。
func (a *Agent) executeWithApproval(tc llm.ToolCall, ch chan<- Event) string {
	tool, ok := a.registry.Lookup(tc.Function.Name)
	if !ok {
		return fmt.Sprintf("错误：不存在名为 %q 的工具", tc.Function.Name)
	}
	args := json.RawMessage(tc.Function.Arguments)

	// 重复命令检测
	if tc.Function.Name == "execute_shell" {
		key := compactArgs(tc.Function.Arguments)
		a.recentCmds[key]++
		if a.recentCmds[key] >= 3 {
			return "检测到你已连续 " + fmt.Sprint(a.recentCmds[key]) +
				" 次执行相同命令。系统已自动拒绝以打断可能的死循环。" +
				"请停下来分析根因：检查错误输出、读取配置文件、确认服务是否已在运行。" +
				"如果服务已在运行（netstat 确认端口在监听），不要 kill 它重来。"
		}
	}

	// 审批
	if apv, ok := tool.(tools.Approvable); ok {
		d := apv.Review(args)
		if d.NeedApproval {
			if d.Danger == "" && a.approver.isApproved(d.ScopeKind, d.Scope) {
				// 已授权，跳过审批
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
					return "用户拒绝执行该操作。请不要绕过，改为向用户说明情况。"
				}
				if reply.Permanent && d.Danger == "" {
					a.approver.remember(d.ScopeKind, d.Scope)
				}
			}
		}
	}

	// 执行
	out, err := tool.Execute(args)
	if err != nil {
		return "执行出错：" + err.Error()
	}
	return out
}

// ── Run：同步兼容方法（消费 RunAsync 事件并 fmt.Printf 输出）──

// Run 同步执行任务并输出到终端。用于一次性任务模式（gowhale "xxx"）
// 和旧交互模式（go-prompt）。保留所有原有终端输出行为。
func (a *Agent) Run(input string) {
	events := a.RunAsync(input)
	a.consumeEvents(events)
}

// consumeEvents 消费事件 channel 并输出到终端。
// 使用 ANSI 转义序列保存/恢复光标位置，避免干扰 go-prompt 的渲染。
func (a *Agent) consumeEvents(events <-chan Event) {
	// 保存光标位置（go-prompt 依赖此位置重新渲染提示符）
	fmt.Print("\033[s")
	for ev := range events {
		switch ev.Type {
		case EventDone:
			fmt.Printf("\n%s %s  %s\n", boldC(blueC("AI >")), ev.Message, tokenBadge(ev.TokenCount))
			a.journal.Note("✅ " + ev.Message)

		case EventError:
			fmt.Printf("\n调用失败: %s  %s\n", ev.Message, tokenBadge(ev.TokenCount))

		case EventThinking:
			// 终端模式下 spinner 由外部管理，不做额外输出

		case EventToolCall:
			label := formatToolLabel(ev.Step, llm.ToolCall{
				Function: llm.FunctionCall{Name: ev.ToolName, Arguments: ev.ToolArgs},
			})
			fmt.Print("\r" + label)

		case EventToolResult:
			fmt.Printf("→ %s  %s\r\n", statusLine(ev.ToolResult), tokenBadge(ev.TokenCount))
			if isError(ev.ToolResult) {
				fmt.Printf("     %s\n", indentLines(ev.ToolResult, 5))
			}

		case EventApprovalRequest:
			req := ev.ApprovalRequest
			fmt.Print(PrintPrompt(req.ToolName, req.Warning))
			os.Stdout.Sync()
			reply := readTerminalApproval(os.Stdin)
			req.Callback <- reply
		}
	}
	// 恢复光标位置，让 go-prompt 拿到干净的起点
	fmt.Print("\033[u")
}

// readTerminalApproval 从 stdin 直接读取一行并转为 ApprovalReply。
// 仅用于终端兼容模式（consumeEvents 上下文）。
func readTerminalApproval(f *os.File) ApprovalReply {
	r := bufio.NewReader(f)
	line, err := ReadStdinLine(r)
	if err != nil {
		return ApprovalReply{Allowed: false}
	}
	line = strings.ToLower(strings.TrimSpace(line))
	switch line {
	case "y", "yes":
		return ApprovalReply{Allowed: true}
	case "a", "all":
		return ApprovalReply{Allowed: true, Permanent: true}
	default:
		return ApprovalReply{Allowed: false}
	}
}

// ── 原有公共方法（保持不变）──

// Compact 压缩对话历史，保留系统提示和最近几轮，节省 token。
func (a *Agent) Compact() {
	if len(a.history) <= 4 {
		return
	}
	keep := 3
	if len(a.history) > keep+1 {
		a.history = append(
			a.history[:1],
			a.history[len(a.history)-keep:]...,
		)
	}
	a.totalTokens = 0
	a.recentCmds = map[string]int{}
}

// SwitchProvider 切换 LLM 提供商（DeepSeek ↔ Ollama）。
func (a *Agent) SwitchProvider(baseURL, apiKey, model, proModel string) {
	a.client.SwitchTo(baseURL, apiKey, model, proModel)
	a.fastModel = model
	a.proModel = proModel
}

// ProviderName 返回当前提供商名。
func (a *Agent) ProviderName() string {
	if strings.Contains(a.client.BaseURL(), "ollama") || strings.Contains(a.client.BaseURL(), "11434") {
		return "ollama"
	}
	return "deepseek"
}

// ProviderInfo 返回当前提供商信息。
func (a *Agent) ProviderInfo() (name, baseURL, model, proModel string) {
	name = a.ProviderName()
	baseURL = a.client.BaseURL()
	model = a.fastModel
	proModel = a.proModel
	return
}

// TokenCount 返回已使用的总 token 数。
func (a *Agent) TokenCount() int { return a.totalTokens }

// LastTasks 返回最近 n 条任务记录。
func (a *Agent) LastTasks(n int) []TaskEntry {
	return a.journal.LastTasks(n)
}

// ── 紧凑输出渲染 ──

func formatToolLabel(step int, tc llm.ToolCall) string {
	icon := toolIcon(tc.Function.Name)
	name := grayC(tc.Function.Name)
	args := dimC(compactArgs(tc.Function.Arguments))
	return fmt.Sprintf("%s %s %-14s %s", grayC(fmt.Sprintf("[%d]", step)), icon, name, args)
}

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
	default:
		return "🔹"
	}
}

func tokenBadge(n int) string {
	if n <= 0 {
		return ""
	}
	return dimC(fmt.Sprintf("[📊 %s]", llm.FormatTokens(n)))
}

func compactArgs(raw string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return raw
	}
	if files, ok := m["files"].(map[string]any); ok {
		total := len(files)
		names := make([]string, 0, 4)
		for p := range files {
			names = append(names, p)
		}
		suffix := ""
		if len(names) > 4 {
			names = names[:4]
			suffix = ", …"
		}
		return fmt.Sprintf("%d files (%s%s)", total, strings.Join(names, ", "), suffix)
	}
	if m["path"] != nil {
		s := fmt.Sprint(m["path"])
		if sl, ok := m["start_line"].(float64); ok && sl > 0 {
			s += fmt.Sprintf(" L%d", int(sl))
			if ml, ok := m["max_lines"].(float64); ok && ml > 0 {
				s += fmt.Sprintf("-%d", int(sl)+int(ml)-1)
			}
		}
		return s
	}
	if paths, ok := m["paths"].([]any); ok {
		total := len(paths)
		names := make([]string, 0, 4)
		for _, p := range paths {
			names = append(names, fmt.Sprint(p))
		}
		suffix := ""
		if len(names) > 4 {
			names = names[:4]
			suffix = ", …"
		}
		return fmt.Sprintf("%d files (%s%s)", total, strings.Join(names, ", "), suffix)
	}
	if m["command"] != nil {
		cmd := fmt.Sprint(m["command"])
		if len(cmd) > 80 {
			cmd = cmd[:80] + "…"
		}
		return cmd
	}
	if steps, ok := m["steps"].([]any); ok {
		return fmt.Sprintf("%d 步骤", len(steps))
	}
	b, _ := json.Marshal(m)
	s := string(b)
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

func statusLine(result string) string {
	s := result
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	switch {
	case strings.Contains(result, "执行出错："):
		return redC("✗") + " " + redC(s)
	case strings.Contains(result, "用户拒绝"):
		return yellowC("⊘") + " " + yellowC("已拒绝")
	case strings.HasPrefix(result, "错误"):
		return redC("✗") + " " + redC(s)
	default:
		return greenC("✓") + " " + dimC(s)
	}
}

func isError(result string) bool {
	return strings.Contains(result, "执行出错：") ||
		strings.HasPrefix(result, "错误") ||
		strings.Contains(result, "失败")
}

func indentLines(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// classify 用 fast 模型判断用户请求是否复杂任务。
func (a *Agent) classify(input string) bool {
	if utf8.RuneCountInString(input) > 200 {
		return true
	}
	origModel := a.client.Model()
	a.client.SetModel(a.fastModel)

	classifyPrompt := "判断以下用户请求属于 '简单' 还是 '复杂'。" +
		"简单: 问候、闲聊、单一事实查询、读文件、只涉及 list_dir/read_file、不需要多步推理。" +
		"复杂: 写代码、生成文件、修改多文件、调试错误、多步任务、需要推理规划、编译运行验证。" +
		"\n\n用户请求: " + input + "\n\n只回答一个字: 简单 或 复杂"

	msg, _, err := a.client.Chat([]llm.Message{{Role: "user", Content: classifyPrompt}}, nil)
	if err != nil {
		a.client.SetModel(origModel)
		return false
	}

	result := strings.ToLower(strings.TrimSpace(msg.Content))
	isComplex := strings.Contains(result, "复杂")

	// classify 结果不直接输出到 stdout——终端模式下 go-prompt 无法清除残留文本。
	// 改为写入 debug 日志供后续排查。
	a.debugLog.write(fmt.Sprintf("CLASSIFY result=%s complex=%v", result, isComplex))
	a.client.SetModel(origModel)
	return isComplex
}

func (a *Agent) hitLimit() {
	a.journal.Note(fmt.Sprintf("⚠️ 达到工具调用上限（%d 轮），任务可能尚未完成", a.maxTurns))
	fmt.Printf("\n%s 已连续调用工具 %d 轮仍未结束，任务可能较大。\n", yellowC("⚠️"), a.maxTurns)
	fmt.Println("   进度已记录到 .aicode/journal.md。你可以：")
	fmt.Println("   1. 直接输入「继续」——上下文已保留，我会接着未完成的部分做；")
	fmt.Println("   2. 把任务拆成更小的步骤分次执行；")
	fmt.Println("   3. 用环境变量提高上限后重跑：AICODE_MAX_TURNS=100")
}
