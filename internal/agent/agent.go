package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gowhale/internal/llm"
	"gowhale/internal/tools"
)

// Agent 用工具调用循环驱动大模型完成任务。
type Agent struct {
	client     *llm.Client
	registry   *tools.Registry
	approver   *Approver
	journal    *Journal
	maxTurns   int
	spinner    Spinner
	history    []llm.Message
	recentCmds map[string]int
}

const skillRules = "" +
	"## 运行环境(极其重要!)\n" +
	"- 你的命令在 sh/bash 中执行,不是 cmd.exe。即使 Windows 也用 sh 语法。\n" +
	"- Windows 下用: ls 而非 dir, grep 而非 findstr, cd /path 而非 cd /d X:\\path\n" +
	"- 重定向用 2>&1 而非 2>nul。不用 < 输入重定向(sh 不认)。\n" +
	"- **绝对不用 start**——它是 cmd 命令,sh 不认。长期服务用 background=true。\n" +
	"- 你已在工作目录中,直接执行命令即可,不需要开头加 cd。\n" +
	"\n" +
	"## 文件操作\n" +
	"1. **绝对禁止**逐文件调用 write_file。2 个及以上文件必须用 batch_write 一次性完成。\n" +
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
	"- 完成后用简洁的中文总结。"

// New 创建 Agent。maxTurns 为单次请求内的最大工具调用轮数。
func New(client *llm.Client, registry *tools.Registry, approver *Approver, maxTurns int, workspace string) *Agent {
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
	}
}

// Run 处理一条用户输入，期间可能多次调用工具，最后打印模型的回答。
func (a *Agent) Run(input string) {
	a.journal.Task(input)
	a.history = append(a.history, llm.Message{Role: "user", Content: input})
	defs := a.registry.Definitions()
	step := 0

	for turn := 0; turn < a.maxTurns; turn++ {
		stop := a.spinner.Start("思考中")
		msg, err := a.client.Chat(a.history, defs)
		stop()
		if err != nil {
			fmt.Printf("调用失败: %v\n", err)
			return
		}
		a.history = append(a.history, msg)

		if len(msg.ToolCalls) == 0 {
			fmt.Printf("\n%s %s\n", bold(blue("AI >")), msg.Content)
			a.journal.Note("✅ " + msg.Content)
			return
		}

		for _, tc := range msg.ToolCalls {
			step++
			label := formatToolLabel(step, tc)
			a.journal.Tool(tc.Function.Name, compactArgs(tc.Function.Arguments))

			fmt.Print(label + " ")
			result := a.doWithApproval(tc)
			fmt.Printf("→ %s\n", statusLine(result))

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

	out, err := tool.Execute(args)
	if err != nil {
		return "执行出错：" + err.Error()
	}
	return out
}

// ---------- 紧凑输出渲染 ----------

func formatToolLabel(step int, tc llm.ToolCall) string {
	icon := toolIcon(tc.Function.Name)
	name := gray(tc.Function.Name)
	args := dim(compactArgs(tc.Function.Arguments))
	return fmt.Sprintf("%s %s %-14s %s", gray(fmt.Sprintf("[%d]", step)), icon, name, args)
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
		return red("✗") + " " + red(s)
	case strings.Contains(result, "用户拒绝"):
		return yellow("⊘") + " " + yellow("已拒绝")
	case strings.HasPrefix(result, "错误"):
		return red("✗") + " " + red(s)
	default:
		return green("✓") + " " + dim(s)
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

func (a *Agent) hitLimit() {
	a.journal.Note(fmt.Sprintf("⚠️ 达到工具调用上限（%d 轮），任务可能尚未完成", a.maxTurns))
	fmt.Printf("\n%s 已连续调用工具 %d 轮仍未结束，任务可能较大。\n", yellow("⚠️"), a.maxTurns)
	fmt.Println("   进度已记录到 .aicode/journal.md。你可以：")
	fmt.Println("   1. 直接输入「继续」——上下文已保留，我会接着未完成的部分做；")
	fmt.Println("   2. 把任务拆成更小的步骤分次执行；")
	fmt.Println("   3. 用环境变量提高上限后重跑：AICODE_MAX_TURNS=100")
}
