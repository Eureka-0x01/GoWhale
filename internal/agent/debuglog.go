package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DebugLog 记录每次 LLM 调用和工具执行的详细日志。
// 写入工作目录下的 gowhale_debug.log，格式为 JSON 行，方便用工具分析。
type DebugLog struct {
	mu  sync.Mutex
	f   *os.File
	task string // 当前任务摘要
}

// NewDebugLog 在工作目录下创建或追加调试日志。
func NewDebugLog(workspace string) *DebugLog {
	path := filepath.Join(workspace, "gowhale_debug.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return &DebugLog{} // 静默降级
	}
	dl := &DebugLog{f: f}
	dl.write("=== 会话开始 " + time.Now().Format("2006-01-02 15:04:05") + " ===")
	return dl
}

func (dl *DebugLog) write(s string) {
	if dl.f == nil {
		return
	}
	dl.mu.Lock()
	defer dl.mu.Unlock()
	line := time.Now().Format("15:04:05 ") + s
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	dl.f.WriteString(line)
}

// SetTask 设置当前任务描述。
func (dl *DebugLog) SetTask(desc string) {
	dl.task = desc
	dl.write("📋 TASK: " + desc)
}

// LLMRequest 记录发送给 LLM 的请求（最后一条 user 消息）。
func (dl *DebugLog) LLMRequest(systemPromptLen, historyLen int, toolCount int) {
	dl.write(fmt.Sprintf("⬆ REQ  history=%d msgs  tools=%d  system_prompt=%d chars",
		historyLen, toolCount, systemPromptLen))
}

// LLMResponse 记录 LLM 返回的响应。
func (dl *DebugLog) LLMResponse(model string, contentLen int, toolCalls []string, tokensIn, tokensOut int) {
	tcStr := ""
	if len(toolCalls) > 0 {
		tcStr = " tool_calls=[" + strings.Join(toolCalls, ", ") + "]"
	}
	dl.write(fmt.Sprintf("⬇ RESP model=%s  content=%d chars  in=%d  out=%d%s",
		model, contentLen, tokensIn, tokensOut, tcStr))
}

// ToolCall 记录一次工具调用（包含完整参数和结果摘要）。
func (dl *DebugLog) ToolCall(step int, name string, argsJSON string) {
	dl.write(fmt.Sprintf("  [%d] 🔧 %s  args=%s", step, name, argsJSON))
}

// ToolResult 记录工具调用结果。
func (dl *DebugLog) ToolResult(step int, name string, result string, isErr bool) {
	tag := "✓"
	if isErr {
		tag = "✗"
	}
	// 截断过长的结果
	if len(result) > 500 {
		result = result[:500] + fmt.Sprintf("... (共 %d 字符)", len(result))
	}
	dl.write(fmt.Sprintf("  [%d] %s %s → %s", step, tag, name, strings.ReplaceAll(result, "\n", "\\n")))
}

// LimitHit 记录达到上限。
func (dl *DebugLog) LimitHit(reason string, callCount, maxCalls int) {
	dl.write(fmt.Sprintf("⛔ LIMIT %s  used=%d  max=%d", reason, callCount, maxCalls))
}

// Done 记录任务完成。
func (dl *DebugLog) Done(msg string, totalTokens int) {
	dl.write(fmt.Sprintf("✅ DONE  tokens=%d  msg=%s", totalTokens, truncateStr(msg, 200)))
}

// Error 记录错误。
func (dl *DebugLog) Error(errMsg string) {
	dl.write(fmt.Sprintf("❌ ERROR %s", errMsg))
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return strings.ReplaceAll(s, "\n", " ")
	}
	return strings.ReplaceAll(s[:max], "\n", " ") + "..."
}
