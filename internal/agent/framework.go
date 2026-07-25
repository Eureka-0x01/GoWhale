package agent

import (
	"context"
	"fmt"
	"strings"

	"gowhale/internal/tools"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	ftool "trpc.group/trpc-go/trpc-agent-go/tool"
)

// FrameAgent 基于 tRPC-Agent-Go 框架的 Agent 实现。
// 保持与现有 TUI 兼容——通过 chan Event 发送事件。
type FrameAgent struct {
	runner      trunner.Runner
	model       *openai.Model
	adapter     *tools.Adapter
	approver    *Approver
	workspace   string
	totalTokens int

	// 审批通道：BeforeTool 回调中创建的审批请求通过此通道转发到事件循环。
	approvalCh chan *ApprovalRequest

	// 系统指令
	instruction string
}

// NewFrameAgent 创建框架版 Agent。
func NewFrameAgent(
	baseURL, apiKey, modelName string,
	reg *tools.Registry,
	approver *Approver,
	workspace string,
	instruction string,
) *FrameAgent {
	m := openai.New(modelName,
		openai.WithBaseURL(baseURL),
		openai.WithAPIKey(apiKey),
	)

	adapter := tools.NewAdapter(reg)
	ag := &FrameAgent{
		model:       m,
		adapter:     adapter,
		approver:    approver,
		workspace:   workspace,
		approvalCh:  make(chan *ApprovalRequest, 1),
		instruction: instruction,
	}

	// 创建工具回调（审批门）
	toolCbs := ftool.NewCallbacks()
	toolCbs.RegisterBeforeTool(ag.beforeTool)

	// 创建 LLMAgent
	llmAg := llmagent.New("gowhale",
		llmagent.WithModel(m),
		llmagent.WithTools(adapter.AllTools()),
		llmagent.WithInstruction(instruction),
		llmagent.WithToolCallbacks(toolCbs),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:    false,
			MaxTokens: func() *int { v := 8192; return &v }(),
		}),
	)

	ag.runner = trunner.NewRunner("gowhale-app", llmAg)
	return ag
}

// NewFrameAgentFromWorkspace 从工作区创建框架版 Agent。
// 自动构建系统指令（ego + constitution + skill rules）。
func NewFrameAgentFromWorkspace(
	baseURL, apiKey, modelName string,
	reg *tools.Registry,
	approver *Approver,
	workspace string,
) *FrameAgent {
	ensureDefaultConstitution(workspace)
	constitution := loadConstitution(workspace)
	instruction := egoBlock(workspace, constitution) + "\n" + skillRules
	return NewFrameAgent(baseURL, apiKey, modelName, reg, approver, workspace, instruction)
}

// ModelName 返回当前模型名。
func (a *FrameAgent) ModelName() string { return a.model.Info().Name }

// Run 同步执行任务（用于 classic 终端模式）。
func (a *FrameAgent) Run(input string) {
	events := a.RunAsync(input)
	for ev := range events {
		if ev.Type == EventDone {
			fmt.Println(ev.Message)
		} else if ev.Type == EventError {
			fmt.Println("错误:", ev.Message)
		}
	}
}

// TokenCount 返回累计 token。
func (a *FrameAgent) TokenCount() int { return a.totalTokens }

// LastTasks 返回空列表。
func (a *FrameAgent) LastTasks(n int) []TaskEntry { return nil }

// ── UI 命令支持（框架版暂不支持，返回零值）──

func (a *FrameAgent) ProviderName() string                            { return "deepseek" }
func (a *FrameAgent) ProviderInfo() (string, string, string, string)  { return "deepseek", "", a.ModelName(), a.ModelName() }
func (a *FrameAgent) Compact() bool                                   { return false }
func (a *FrameAgent) SwitchProvider(baseURL, apiKey, model, proModel string) {}

// beforeTool 在每个工具执行前被框架调用。
// 通过审批通道同步等待 TUI 决策。
func (a *FrameAgent) beforeTool(ctx context.Context, args *ftool.BeforeToolArgs) (*ftool.BeforeToolResult, error) {
	// 检查是否已有授权记忆
	danger, scopeKind, scope := a.classifyTool(args.ToolName, string(args.Arguments))
	if danger == "" && a.approver.isApproved(scopeKind, scope) {
		return &ftool.BeforeToolResult{}, nil
	}

	// 创建回复通道
	replyCh := make(chan ApprovalReply, 1)

	// 通知事件循环：有工具需要审批
	req := &ApprovalRequest{
		ToolName:  args.ToolName,
		Arguments: compactArgsStr(string(args.Arguments)),
		Warning:   danger,
		Callback:  replyCh,
	}

	// 发送到审批通道（非阻塞，buffer=1）
	select {
	case a.approvalCh <- req:
	default:
		// 如果通道已满，跳过审批直接放行（避免死锁）
		return &ftool.BeforeToolResult{}, nil
	}

	// 同步等待 TUI 决策
	reply := <-replyCh
	if !reply.Allowed {
		return &ftool.BeforeToolResult{
			CustomResult: "用户拒绝执行该操作。",
		}, nil
	}
	if reply.Permanent && danger == "" {
		a.approver.remember(scopeKind, scope)
	}
	return &ftool.BeforeToolResult{}, nil
}

// classifyTool 判断工具是否需要审批及危险级别。
func (a *FrameAgent) classifyTool(name string, argsJSON string) (danger, scopeKind, scope string) {
	switch name {
	case "read_file", "list_dir", "grep_search", "verify_project":
		return "", "", "" // 只读工具不需要审批
	case "execute_shell":
		return tools.CheckDanger(argsJSON), "session", "shell"
	case "write_file", "batch_write":
		return "", "dir", a.workspace
	case "write_plan", "execute_python":
		return "", "session", "safe"
	default:
		return "", "session", "default"
	}
}

// ── RunAsync：事件驱动执行 ──

// RunAsync 在独立 goroutine 中执行任务，通过 channel 发送事件。
func (a *FrameAgent) RunAsync(input string) <-chan Event {
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)

		ctx := context.Background()
		userMsg := model.NewUserMessage(input)
		fwEvents, err := a.runner.Run(ctx, "user", "session", userMsg)
		if err != nil {
			ch <- Event{Type: EventError, Message: fmt.Sprintf("启动失败: %v", err)}
			return
		}

		step := 0
		for {
			select {
			case fwEv, ok := <-fwEvents:
				if !ok {
					// 框架事件通道关闭 → 任务结束
					return
				}

				if fwEv.Response == nil {
					continue
				}

				// 更新 token 统计
				if fwEv.Response.Usage != nil {
					a.totalTokens += fwEv.Response.Usage.TotalTokens
				}

				// 处理 choices
				for _, choice := range fwEv.Response.Choices {
					// 工具调用
					for _, tc := range choice.Message.ToolCalls {
						step++
						ch <- Event{
							Type:      EventToolCall,
							ToolName:  tc.Function.Name,
							ToolArgs:  compactArgsStr(string(tc.Function.Arguments)),
							Step:      step,
							TokenCount: a.totalTokens,
						}
					}

					// 文本内容（最终响应或无工具调用的消息）
					if content := strings.TrimSpace(choice.Message.Content); content != "" {
						ch <- Event{
							Type:       EventDone,
							Message:    content,
							TokenCount: a.totalTokens,
						}
						return
					}
				}

			case req := <-a.approvalCh:
				// 将审批请求转发为 AgentEvent
				ch <- Event{
					Type:            EventApprovalRequest,
					ToolName:        req.ToolName,
					ToolArgs:        req.Arguments,
					ApprovalRequest: req,
				}
			}
		}
	}()
	return ch
}

// ── 辅助函数 ──

// compactArgsStr 将 JSON 参数字符串压缩为紧凑摘要。
func compactArgsStr(raw string) string {
	return compactArgs(raw)
}

// 注意：checkDanger 已在 tools/guard.go 中定义，这里复用。
