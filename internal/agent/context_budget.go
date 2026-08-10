package agent

import (
	"fmt"
	"strings"

	"gowhale/internal/llm"
)

// truncateResult 按工具类型分级截断，控制上下文体积。Agent 和 ChatRoom 共用。
func truncateResult(toolName, result string) string {
	switch toolName {
	case "read_file":
		if len(result) > 1200 {
			return result[:1200] + "\n...(已截断，用 start_line+max_lines 分段读取)"
		}
	case "execute_shell":
		if len(result) > 800 {
			return "(前段省略)\n" + result[len(result)-800:]
		}
	case "grep_search":
		lines := strings.Split(result, "\n")
		if len(lines) > 10 {
			return strings.Join(lines[:10], "\n") + fmt.Sprintf("\n...(共 %d 条匹配)", len(lines))
		}
	case "list_dir":
		if len(result) > 1500 {
			return result[:1500] + "\n...(已截断)"
		}
	}
	return result
}

// estMsgSize 估算消息列表的总字节数（供 workLogBlob 显示预算）。
func estMsgSize(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, tc := range m.ToolCalls {
			n += len(tc.Function.Arguments)
		}
	}
	return n
}

// estimateCtxSize 估算上下文总字节数。
func estimateCtxSize(msgs []llm.Message) int {
	return estMsgSize(msgs)
}

// ContextBudget 导出给 UI——返回上下文字节数和百分比。
func ContextBudget(msgs []llm.Message) string {
	n := estMsgSize(msgs)
	pct := n * 100 / 16000
	return fmt.Sprintf("%dKB/%d%%", n/1024, pct)
}
