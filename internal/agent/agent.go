package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"gowhale/internal/llm"
	"gowhale/internal/tools"
)

// Agent 用工具调用循环驱动大模型完成任务。
type Agent struct {
	client      *llm.Client
	registry    *tools.Registry
	approver    *Approver
	journal     *Journal
	maxTurns    int
	spinner     Spinner
	history     []llm.Message
	recentCmds  map[string]int
	fastModel   string // 快速模型（简单问题）
	proModel    string // 复杂模型（多步推理/代码生成）
	totalTokens int    // 累计 token 消耗
}

const skillRules = "" +
	"## 运行环境\n" +
	"- shell 类型和可用命令参见上方执行环境信息，严格按照检测到的 shell 写命令。\n" +
	"- 长期服务用 background=true，绝对不用 start/nohup/&。\n" +
	"- 你已在工作目录中，直接执行命令即可，不需要开头加 cd。\n" +
	"\n" +
	"## 文件操作\n" +
	"1. **绝对禁止**逐文件调用 write_file。创建/修改 2 个及以上文件时，**第一个工具调用**就必须是 batch_write。\n" +
	"   先把所有文件内容和路径准备好，用 batch_write 一次写入。不要循环调用 write_file。\n" +
	"   write_file 仅允许在只需修改 1 个文件时使用。违反此规则会导致工具调用轮次耗尽。\n" +
	"2. 写入前先用 list_dir / read_file 了解项目结构,不要臆测。\n" +
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
		maxTurns:   maxTurns,
		recentCmds: map[string]int{},
		history:    history,
		fastModel:  fastModel,
		proModel:   proModel,
	}
}

// Run 处理一条用户输入，期间可能多次调用工具，最后打印模型的回答。
func (a *Agent) Run(input string) {
	// 复杂度分类：用 fast 模型判断是否需要切到 pro
	complex := a.classify(input)
	if complex {
		a.client.SetModel(a.proModel)
	} else {
		a.client.SetModel(a.fastModel)
	}

	a.journal.Task(input + fmt.Sprintf(" [模型: %s]", a.client.Model()))
	a.history = append(a.history, llm.Message{Role: "user", Content: input})
	defs := a.registry.Definitions()
	step := 0

	for turn := 0; turn < a.maxTurns; turn++ {
		stop := a.spinner.Start("思考中")
		msg, usage, err := a.client.Chat(a.history, defs)
		stop()
		if err != nil {
			fmt.Printf("调用失败: %v\n", err)
			return
		}
		a.totalTokens += usage.TotalTokens
		a.history = append(a.history, msg)

		if len(msg.ToolCalls) == 0 {
			fmt.Printf("\n%s %s  %s\n", boldC(blueC("AI >")), msg.Content, tokenBadge(a.totalTokens))
			a.journal.Note("✅ " + msg.Content)
			return
		}

		for _, tc := range msg.ToolCalls {
			step++
			label := formatToolLabel(step, tc)
			a.journal.Tool(tc.Function.Name, compactArgs(tc.Function.Arguments))

			fmt.Print("\r" + label)
			result := a.doWithApproval(tc)
			fmt.Printf("→ %s  %s\r\n", statusLine(result), tokenBadge(a.totalTokens))

			if isError(result) {
				fmt.Printf("     %s\n", indentLines(result, 5))
			}
			a.history = append(a.history, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
	}
	a.hitLimit()
}

// Compact 压缩对话历史，保留系统提示和最近几轮，节省 token。
func (a *Agent) Compact() {
	if len(a.history) <= 4 {
		return // 消息太少，不需要压缩
	}
	// 保留首条 system 消息 + 最近 2 条对话（user + assistant/tool）
	keep := 3
	if len(a.history) > keep+1 {
		a.history = append(
			a.history[:1],
			a.history[len(a.history)-keep:]...,
		)
	}
	a.totalTokens = 0 // 重置计数器
	a.recentCmds = map[string]int{}
	fmt.Printf("  %s 上下文已压缩，保留最近 %d 条消息\n", greenC("✓"), keep)
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

// doWithApproval 查找工具、审批、执行，返回结果文本。
func (a *Agent) doWithApproval(tc llm.ToolCall) string {
	tool, ok := a.registry.Lookup(tc.Function.Name)
	if !ok {
		return fmt.Sprintf("错误：不存在名为 %q 的工具", tc.Function.Name)
	}
	args := json.RawMessage(tc.Function.Arguments)

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

	if apv, ok := tool.(tools.Approvable); ok {
		d := apv.Review(args)
		if d.NeedApproval && !a.approver.Ask(tc.Function.Name, d) {
			return "用户拒绝执行该操作。请不要绕过，改为向用户说明情况。"
		}
	}

	// 审批通过后才启动 spinner，避免 spinner 的 \r 覆盖审批提示和用户输入
	stop := a.spinner.Start("执行中")
	out, err := tool.Execute(args)
	stop()
	if err != nil {
		return "执行出错：" + err.Error()
	}
	return out
}

// ---------- 紧凑输出渲染 ----------

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

// tokenBadge 在行末显示 token 用量标记。
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
	if files, ok := m["files"].([]any); ok {
		total := len(files)
		names := make([]string, 0, 4)
		for _, f := range files {
			if fm, ok := f.(map[string]any); ok {
				if p, ok := fm["path"].(string); ok {
					names = append(names, p)
				}
			}
		}
		suffix := ""
		if len(names) > 4 {
			names = names[:4]
			suffix = ", …"
		}
		return fmt.Sprintf("%d files (%s%s)", total, strings.Join(names, ", "), suffix)
	}
	if m["path"] != nil {
		return fmt.Sprint(m["path"])
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
// 只有复杂任务（多步推理/代码生成/调试）才值得切到 pro 模型。
func (a *Agent) classify(input string) bool {
	// 估算输入字符数，超过阈值直接切 pro 模型
	if utf8.RuneCountInString(input) > 200 {
		return true
	}
	// 保存当前模型，分类完恢复
	origModel := a.client.Model()
	a.client.SetModel(a.fastModel)

	classifyPrompt := "判断以下用户请求属于 '简单' 还是 '复杂'。" +
		"简单: 问候、闲聊、单一事实查询、读文件、只涉及 list_dir/read_file、不需要多步推理。" +
		"复杂: 写代码、生成文件、修改多文件、调试错误、多步任务、需要推理规划、编译运行验证。" +
		"\n\n用户请求: " + input + "\n\n只回答一个字: 简单 或 复杂"

	msg, _, err := a.client.Chat([]llm.Message{{Role: "user", Content: classifyPrompt}}, nil)
	if err != nil {
		a.client.SetModel(origModel)
		return false // 分类失败，保守用 fast
	}

	result := strings.ToLower(strings.TrimSpace(msg.Content))
	isComplex := strings.Contains(result, "复杂")

	fmt.Printf("%s %s\n", dimC(fmt.Sprintf("复杂度: %s → %s", result,
		map[bool]string{true: boldC(blueC("使用 " + a.proModel)), false: grayC("使用 " + a.fastModel)}[isComplex])),
		tokenBadge(a.totalTokens))
	a.client.SetModel(origModel) // 恢复，Run() 里会根据结果再设
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
