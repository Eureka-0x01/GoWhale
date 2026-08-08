package agent

import "gowhale/internal/llm"

// AgentInterface 定义 Agent 的公共接口。
// 旧 Agent 和 FrameAgent 都实现此接口，使 TUI 无需关心底层实现。
type AgentInterface interface {
	// RunAsync 在独立 goroutine 中执行任务，通过 channel 发送事件。
	RunAsync(input string) <-chan Event

	// Run 同步执行任务并输出到终端（用于 classic 模式）。
	Run(input string)

	// ModelName 返回当前使用的模型名。
	ModelName() string

	// TokenCount 返回累计 token 数。
	TokenCount() int

	// LastTasks 返回最近 n 条任务记录。
	LastTasks(n int) []TaskEntry

	// ── 以下为 UI 命令支持（旧 Agent 实现，FrameAgent 返回零值）──

	// ProviderName 返回当前提供商名。
	ProviderName() string

	// ProviderInfo 返回提供商详细信息。
	ProviderInfo() (name, baseURL, model, proModel string)

	// Compact 压缩上下文，返回是否实际执行了截断。
	Compact() bool

	// Messages 返回当前消息历史副本。
	Messages() []llm.Message

	// SwitchProvider 切换 LLM 提供商。
	SwitchProvider(baseURL, apiKey, model, proModel string)

	// SetModelLock 锁定模型：name 非空时所有任务固定使用该模型；
	// 空字符串恢复自动模式（按任务复杂度切换 fast/pro）。
	SetModelLock(name string)

	// ModelLock 返回当前锁定的模型名（空 = 自动模式）。
	ModelLock() string
}
