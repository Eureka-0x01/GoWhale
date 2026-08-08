package agent

import (
	"bufio"
	"encoding/json"
	"time"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"gowhale/internal/context"
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
	projectOverview string   // 项目扫描摘要（注入 system prompt）
	modelLock       string   // 锁定模型（空=自动）
	readFiles       map[string]bool // 已完整读取的文件（去重）
	listDirs        map[string]bool // 已 list_dir 的参数（去重）
	grepPatterns    map[string]bool // 已 grep_search 的 pattern（去重）
	timeout         time.Duration // 单任务超时（0=不限）
}

// TaskTimeout 单次任务默认超时（10 分钟）。
// 可通过 SetTimeout 调整或通过 --timeout 参数配置。
const TaskTimeout = 10 * time.Minute

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

// reflectPrompt 在连续工具调用失败时注入，要求模型暂停并分析根因。
const reflectPrompt = "" +
	"【反思提示】已连续多次工具调用返回错误。请暂停当前策略，逐条回答以下问题：\n" +
	"1. 上述错误的根因是什么？（不要猜测，要基于错误信息分析）\n" +
	"2. 当前策略是否方向错误？是否需要换一个完全不同的思路？\n" +
	"3. 是否需要先用 list_dir 或 read_file 确认项目结构 / 文件内容？\n\n" +
	"输出你的分析后，再给出下一轮的工具调用。"

// New 创建 Agent。maxTurns 为单次请求内的最大工具调用轮数。
func New(client *llm.Client, registry *tools.Registry, approver *Approver, maxTurns int, workspace string, fastModel, proModel string) *Agent {
	if err := os.MkdirAll(workspace, 0o755); err == nil {
		ensureDefaultConstitution(workspace)
	}
	j := NewJournal(workspace)
	dl := NewDebugLog(workspace)
	constitution := loadConstitution(workspace)

	// 扫描项目，生成上下文摘要
	projInfo, scanErr := context.Scan(workspace)
	projectOverview := ""
	if scanErr == nil && projInfo != nil {
		projectOverview = projInfo.Format()
	}

	base := egoBlock(workspace, constitution) + "\n" + skillRules + "\n- 所有面向用户的输出必须使用简体中文。"

	history := []llm.Message{{Role: "system", Content: base}}
	if projectOverview != "" {
		history = append(history, llm.Message{Role: "system", Content: projectOverview})
	}
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
		readFiles:  map[string]bool{},
		listDirs:   map[string]bool{},
		grepPatterns: map[string]bool{},
		history:    history,
		fastModel:  fastModel,
		proModel:   proModel,
		projectOverview: projectOverview,
		timeout:         TaskTimeout,
	}
}

// ── 导出接口（供 TUI 使用）──

// SetTimeout 设置任务超时。
func (a *Agent) SetTimeout(d time.Duration) { a.timeout = d }

// SetModelLock 锁定模型：非空时所有任务固定使用该模型；空字符串恢复自动。
func (a *Agent) SetModelLock(name string) {
	a.modelLock = strings.TrimSpace(name)
	if a.modelLock != "" {
		a.client.SetModel(a.modelLock)
	}
}

// ModelLock 返回当前锁定的模型名（空 = 自动模式）。
func (a *Agent) ModelLock() string { return a.modelLock }

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
	if a.modelLock != "" {
		// 已锁定模型：固定使用
		a.client.SetModel(a.modelLock)
	} else {
		// 自动模式：按任务复杂度选择
		complex := a.classify(input)
		if complex {
			a.client.SetModel(a.proModel)
		} else {
			a.client.SetModel(a.fastModel)
		}
	}

	a.journal.Task(input + fmt.Sprintf(" [模型: %s]", a.client.Model()))
	a.debugLog.SetTask(input)
	a.history = append(a.history, llm.Message{Role: "user", Content: input})
	defs := a.registry.Definitions()
	step := 0
	callCount := 0
	consecutiveErrors := 0
	const maxConsecutiveErrors = 2
	stageInjected := false
	readOnlyCalls := 0 // 累计单独 read_file 调用次数（跨轮统计）
	exploreTurns := 0   // 连续只读轮次（探索上限）

	startTime := time.Now()
	maxExpand := a.maxTurns * 4 // 轮次自动扩展上限（初始的 4 倍）
	for turn := 0; ; turn++ {
		// 超时检查（跳过首轮——至少要启动一次）
		if turn > 0 && a.timeout > 0 && time.Since(startTime) > a.timeout {
			msg := fmt.Sprintf("任务超时（%v），已执行 %d 轮", a.timeout.Round(time.Second), turn)
			a.debugLog.LimitHit("超时", turn, int(a.timeout.Seconds()))
			a.finishWithSummary(msg, ch)
			return
		}

		// 达到轮次上限：自动扩展一次（最多扩到初始的 4 倍）
		if turn >= a.maxTurns {
			if a.maxTurns < maxExpand {
				a.maxTurns *= 2
				a.debugLog.LimitHit("轮次自动扩展", turn, a.maxTurns)
				a.history = append(a.history, llm.Message{
					Role:    "system",
					Content: fmt.Sprintf("轮次上限已自动扩展到 %d。请继续完成剩余工作，不要再重复已完成的操作。", a.maxTurns),
				})
			} else {
				msg := fmt.Sprintf("达到最大轮数 %d", a.maxTurns)
				a.debugLog.LimitHit("达到最大轮数", turn, a.maxTurns)
				a.finishWithSummary(msg, ch)
				return
			}
		}
		ch <- Event{Type: EventThinking, TokenCount: a.totalTokens}

		a.debugLog.LLMRequest(len(a.history[0].Content), len(a.history), len(defs))
		a.injectStageContext(turn, callCount, &stageInjected)

		// 跨轮 read_file 合并警告：累计单独读取 >= 3 次 → 强制合并
		if readOnlyCalls >= 3 {
			a.history = append(a.history, llm.Message{
				Role:    "system",
				Content: fmt.Sprintf("【警告】你已累计 %d 次单独 read_file（无 paths 参数），严重浪费调用轮次。后续读取必须一次合并：用 {\"paths\": [\"a.go\", \"b.go\", \"c.go\"]} 批量读取，最多 20 个文件。再出现单独读取将直接拒绝。", readOnlyCalls),
			})
			readOnlyCalls = 0
		}

		// 上下文过大时自动压缩（消息 > 30 条 或 token > 8000）
		if a.totalTokens <= 0 {
			a.totalTokens = estimateTokens(a.history)
		}
		if len(a.history) > 30 || a.totalTokens > 8000 {
			a.Compact()
		}

		msg, usage, err := a.client.Chat(a.history, defs)
		if err != nil {
			a.debugLog.Error(fmt.Sprintf("调用失败: %v", err))
			ch <- Event{Type: EventError, Message: fmt.Sprintf("调用失败: %v", err), TokenCount: a.totalTokens}
			return
		}
		a.totalTokens += usage.TotalTokens
		// Ollama 模型可能不返回 token 统计（usage=0），用估算兜底
		if a.totalTokens <= 0 {
			a.totalTokens = estimateTokens(a.history) + estimateTokens([]llm.Message{msg})
		}
		a.history = append(a.history, msg)

		// 输出模型思考/说明文字（调用工具前的 reasoning）
		if len(msg.ToolCalls) > 0 && strings.TrimSpace(msg.Content) != "" {
			ch <- Event{Type: EventMessage, Message: strings.TrimSpace(msg.Content), TokenCount: a.totalTokens}
		}

		// 记录 LLM 响应
		tcNames := make([]string, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			tcNames[i] = tc.Function.Name
		}
		a.debugLog.LLMResponse(a.client.Model(), len(msg.Content), tcNames, usage.PromptTokens, usage.CompletionTokens)

		// 无工具调用 → 任务完成
		if len(msg.ToolCalls) == 0 {
			finalMsg := msg.Content
			if strings.TrimSpace(finalMsg) == "" {
				finalMsg = "任务完成。"
			}
			a.debugLog.Done(finalMsg, a.totalTokens)
			ch <- Event{Type: EventDone, Message: finalMsg, TokenCount: a.totalTokens}
			a.journal.Note("✅ " + finalMsg)
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
		if callCount >= a.maxTurns {
			a.debugLog.LimitHit("达到最大工具调用次数", callCount, a.maxTurns)
			for _, tc := range msg.ToolCalls {
				a.history = append(a.history, llm.Message{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    "已达到当前轮次上限，系统将自动扩展后继续。",
				})
			}
			continue
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
		roundHadError := false
		for _, tc := range msg.ToolCalls {
			callCount++
			step++

			// 统计单文件 read_file 调用（无 paths 参数）
			if tc.Function.Name == "read_file" {
				var rargs struct {
					Paths []string `json:"paths"`
				}
				if json.Unmarshal([]byte(tc.Function.Arguments), &rargs) != nil || len(rargs.Paths) == 0 {
					readOnlyCalls++
				}
			}

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

			// 工具去重：read_file/list_dir/grep_search 重复调用 → 不执行
			var result string
			switch tc.Function.Name {
			case "read_file":
				if dedupMsg := a.dedupReadFile(tc.Function.Arguments); dedupMsg != "" {
					result = dedupMsg
				}
			case "list_dir":
				if dedupMsg := a.dedupListDir(tc.Function.Arguments); dedupMsg != "" {
					result = dedupMsg
				}
			case "grep_search":
				if dedupMsg := a.dedupGrep(tc.Function.Arguments); dedupMsg != "" {
					result = dedupMsg
				}
			}
			if result == "" {
				result = a.executeWithApproval(tc, ch)
			}
			isErr := strings.HasPrefix(result, "执行出错：")
			if isErr {
				roundHadError = true
			}
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

		// ── 探索上限：连续多轮只读（无写/执行）→ 强制收敛 ──
		roundAllReadOnly := true
		for _, tc := range msg.ToolCalls {
			if !isReadOnlyTool(tc.Function.Name) {
				roundAllReadOnly = false
				break
			}
		}
		if roundAllReadOnly {
			exploreTurns++
			if exploreTurns >= 5 {
				a.history = append(a.history, llm.Message{
					Role:    "system",
					Content: "【探索上限】你已连续多轮只调用读取/搜索工具。基于已获得的信息，请立即输出结论，或执行有实际效果的操作（写文件/执行命令/调用计划工具）。不要再重复探索。",
				})
				exploreTurns = 0
			}
		} else {
			exploreTurns = 0
		}

		// ── 失败触发反思：连续 2 轮有工具错误 → 注入反思提示 ──
		if roundHadError {
			consecutiveErrors++
		} else {
			consecutiveErrors = 0
		}
		if consecutiveErrors >= maxConsecutiveErrors {
			a.history = append(a.history, llm.Message{
				Role:    "system",
				Content: reflectPrompt,
			})
			consecutiveErrors = 0
		}
	}
}

// dedupReadFile 检查 read_file 调用是否重复读取已读文件。
// 返回非空字符串表示拦截结果（去重提示）；返回空串表示正常执行。
// 规则：
//   - start_line <= 1（含无 start_line）视为完整读取，记录到已读集合；
//   - 已完整读取的文件，任何再次读取（含分段 start_line）都拦截。
func (a *Agent) dedupReadFile(argsRaw string) string {
	var r struct {
		Path      string   `json:"path"`
		Paths     []string `json:"paths"`
		StartLine int      `json:"start_line"`
	}
	if json.Unmarshal([]byte(argsRaw), &r) != nil {
		return ""
	}
	paths := r.Paths
	if len(paths) == 0 && r.Path != "" {
		paths = []string{r.Path}
	}
	if len(paths) == 0 {
		return ""
	}
	isFullRead := r.StartLine <= 1 // 无 start_line 或从开头读 → 视为完整读取

	var dup []string
	for _, f := range paths {
		if a.readFiles[f] {
			dup = append(dup, f) // 已完整读取 → 重复
		} else if isFullRead {
			a.readFiles[f] = true // 首次完整读取 → 记录
		}
		// 未完整读取过的分段读取（start_line > 1）→ 允许
	}
	if len(dup) > 0 {
		return fmt.Sprintf("（去重）以下文件已完整读取，内容在上文上下文中，请直接使用，不要重复或分段读取：%s", strings.Join(dup, ", "))
	}
	return ""
}

// dedupListDir 检查 list_dir 调用是否重复（相同参数）。
func (a *Agent) dedupListDir(argsRaw string) string {
	key := compactArgs(argsRaw)
	if key == "" {
		return ""
	}
	if a.listDirs[key] {
		return fmt.Sprintf("（去重）相同参数的 list_dir 已执行过，结果在上文上下文中，请直接使用，不要重复列出：%s", key)
	}
	a.listDirs[key] = true
	return ""
}

// dedupGrep 检查 grep_search 调用是否重复（相同 pattern）。
func (a *Agent) dedupGrep(argsRaw string) string {
	var r struct {
		Pattern string `json:"pattern"`
	}
	if json.Unmarshal([]byte(argsRaw), &r) != nil || r.Pattern == "" {
		return ""
	}
	if a.grepPatterns[r.Pattern] {
		return fmt.Sprintf("（去重）相同 pattern 的 grep_search 已执行过，结果在上文上下文中，请直接使用：%s", r.Pattern)
	}
	a.grepPatterns[r.Pattern] = true
	return ""
}

// parseReadArgs 解析 read_file 参数，返回文件列表和是否局部读取。
func parseReadArgs(raw string) (paths []string, hasRange bool) {
	var r struct {
		Path      string   `json:"path"`
		Paths     []string `json:"paths"`
		StartLine int      `json:"start_line"`
		MaxLines  int      `json:"max_lines"`
	}
	if json.Unmarshal([]byte(raw), &r) != nil {
		return nil, false
	}
	hasRange = r.StartLine > 0
	if len(r.Paths) > 0 {
		paths = r.Paths
	} else if r.Path != "" {
		paths = []string{r.Path}
	}
	return paths, hasRange
}

// finishWithSummary 任务异常终止（超时/轮次耗尽/调用失败）时，
// 再调用一次大模型基于当前进度生成总结，确保用户始终有返回。
func (a *Agent) finishWithSummary(reason string, ch chan<- Event) {
	prompt := fmt.Sprintf("任务未能完成（%s）。请基于当前对话历史，用简洁的中文总结：1. 已完成的工作 2. 遇到的问题 3. 剩余未完成事项 4. 建议的下一步。不要提及轮次限制本身。", reason)
	a.history = append(a.history, llm.Message{Role: "user", Content: prompt})

	msg, usage, err := a.client.Chat(a.history, nil)
	a.totalTokens += usage.TotalTokens
	if err != nil {
		ch <- Event{Type: EventError, Message: fmt.Sprintf("任务未完成（%s），且生成总结失败: %v", reason, err), TokenCount: a.totalTokens}
		return
	}
	a.debugLog.Done("总结生成", a.totalTokens)
	finalMsg := fmt.Sprintf("【任务未完成 · %s】\n%s", reason, msg.Content)
	ch <- Event{Type: EventDone, Message: finalMsg, TokenCount: a.totalTokens}
	a.journal.Note("⚠️ " + reason + "：" + msg.Content)
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

		case EventMessage:
			// 模型思考/说明文字
			fmt.Printf("\r%s %s\n", dimC("AI >"), ev.Message)

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

// compactKeep 压缩时保留的最近消息条数。
const compactKeep = 20

// Compact 压缩对话历史：保留 system prompt 和最近的消息，确保不截断 tool 配对。
// 返回 true 表示实际执行了截断，false 表示消息不足无需压缩。

// injectStageContext 根据当前阶段注入上下文提示。
func (a *Agent) injectStageContext(turn, callCount int, stageInjected *bool) {
	if !*stageInjected && turn == 0 && callCount == 0 {
		// 规划阶段：提示模型先调研项目结构
		a.history = append(a.history, llm.Message{
			Role: "system",
			Content: "[规划阶段] 任务刚开始。请先分析需求，用 write_plan 制定计划，用 list_dir / read_file 了解项目结构，不要急于写代码。",
		})
		*stageInjected = true
	} else if callCount >= MaxToolCalls-10 {
		// 预算紧张（每5轮提示一次，避免干扰）
		if callCount%5 == 0 {
			a.history = append(a.history, llm.Message{
				Role: "system",
				Content: fmt.Sprintf("[执行阶段] 工具调用预算紧张（剩余 %d 次）。请优先完成核心功能，跳过非必要的验证步骤。", MaxToolCalls-callCount),
			})
		}
	}
}

// Compact 截断对话上下文：保留 system prompt + 最近的消息，确保不截断 tool 配对。
// 返回 true 表示实际执行了截断，false 表示消息不足无需压缩。
func (a *Agent) Compact() bool {
	if len(a.history) <= compactKeep {
		return false
	}
	if a.totalTokens <= 0 {
		a.totalTokens = estimateTokens(a.history)
	}
	beforeTokens := a.totalTokens

	n := len(a.history)
	sysCount := 0
	for sysCount < n && a.history[sysCount].Role == "system" {
		sysCount++
	}

	cutIdx := n - compactKeep
	if cutIdx <= sysCount {
		cutIdx = sysCount
	}

	// 不截断 tool 配对
	for cutIdx < n && a.history[cutIdx].Role == "tool" {
		if cutIdx <= sysCount {
			break
		}
		cutIdx--
	}

	if n-cutIdx <= compactKeep/2 {
		return false // 压缩收益太小，不处理
	}

	summary := fmt.Sprintf(
		"【上下文已压缩】之前的对话已截断（约 %s token，%d 条消息 → %d 条）。"+
			"如果当前任务依赖之前的上下文，请先回顾下方保留的最近消息；"+
			"如有必要让用户重新描述需求。",
		llm.FormatTokens(beforeTokens), n, n-cutIdx+sysCount,
	)

	newHistory := make([]llm.Message, 0, sysCount+1+(n-cutIdx))
	newHistory = append(newHistory, a.history[:sysCount]...)
	newHistory = append(newHistory, llm.Message{Role: "system", Content: summary})
	newHistory = append(newHistory, a.history[cutIdx:]...)
	a.history = newHistory

	a.totalTokens = estimateTokens(newHistory)
	a.recentCmds = map[string]int{}
	return true
}

// simpleCompact 已整合到 Compact 中，保留此空壳以保证编译。TODO: remove after cleanup
func (a *Agent) simpleCompact(beforeTokens int) bool {
	_ = beforeTokens
	return a.Compact()
}

func estimateTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content) / 4
		for _, tc := range m.ToolCalls {
			total += len(tc.Function.Arguments) / 4
		}
	}
	if total < 1 {
		total = 1
	}
	return total
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
// 如果计数器异常归零（历史遗留或 Compact 后未更新），回退到消息长度估算。
func (a *Agent) TokenCount() int {
	if a.totalTokens <= 0 && len(a.history) > 2 {
		a.totalTokens = estimateTokens(a.history)
	}
	return a.totalTokens
}

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

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
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
			cmd = truncateRunes(cmd, 80) + "…"
		}
		return cmd
	}
	if steps, ok := m["steps"].([]any); ok {
		return fmt.Sprintf("%d 步骤", len(steps))
	}
	b, _ := json.Marshal(m)
	s := string(b)
	if len([]rune(s)) > 80 {
		s = truncateRunes(s, 80) + "…"
	}
	return s
}

func statusLine(result string) string {
	s := result
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len([]rune(s)) > 120 {
		s = truncateRunes(s, 120) + "…"
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
