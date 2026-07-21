package agent

// EventType 标识 Agent 对外发出的事件类别。
type EventType int

const (
	EventThinking       EventType = iota // LLM 调用中
	EventToolCall                        // 工具即将执行
	EventToolResult                      // 工具执行完成
	EventToken                           // 流式 token（预留，暂未实现）
	EventDone                            // 任务完成
	EventError                           // 致命错误
	EventApprovalRequest                 // 需要用户审批
)

// Event 是 Agent 与外部通信的统一消息体。
// 调用方（兼容终端模式或 TUI）从 RunAsync 返回的 channel 读取并处理。
type Event struct {
	Type       EventType
	Message    string // 展示文本
	ToolName   string // 工具名（EventToolCall/EventToolResult 时）
	ToolArgs   string // 紧凑参数
	ToolResult string // 工具输出
	IsError    bool   // 工具结果是否为错误
	Step       int    // 当前步数
	CallCount  int    // 累计调用次数
	TokenCount int    // 累计 token

	// 审批专用（EventApprovalRequest 时填充）
	ApprovalRequest *ApprovalRequest
}

// ApprovalRequest 审批请求，包含回调 channel。
// Agent 发送此事件后阻塞等待 Callback 回复。
type ApprovalRequest struct {
	ToolName  string
	Arguments string           // 紧凑参数
	Warning   string           // 危险命令警告（可为空）
	Callback  chan ApprovalReply
}

// ApprovalReply 审批回复。
type ApprovalReply struct {
	Allowed   bool
	Permanent bool // 本次会话始终允许（对应 "a" 选项）
}
