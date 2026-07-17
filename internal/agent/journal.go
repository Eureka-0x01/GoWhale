package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Journal 把 Agent 的工作记录到工作目录下的 .aicode/journal.md，
// 方便后续查阅，也会在下次启动时读回最近记录注入上下文。
type Journal struct {
	path    string
	enabled bool
}

// NewJournal 在锁定工作区准备 .aicode/journal.md。
func NewJournal(workspace string) *Journal {
	dir := filepath.Join(workspace, ".aicode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &Journal{}
	}
	return &Journal{path: filepath.Join(dir, "journal.md"), enabled: true}
}

func (j *Journal) append(line string) {
	if !j.enabled {
		return
	}
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, _ = f.WriteString(line)
}

// Task 记录一条新任务的开始。
func (j *Journal) Task(input string) {
	j.append(fmt.Sprintf("\n## %s  %s", time.Now().Format("2006-01-02 15:04:05"), input))
}

// Tool 记录一次工具调用。
func (j *Journal) Tool(name, args string) {
	j.append(fmt.Sprintf("- 🔧 %s %s", name, args))
}

// Note 记录一条备注（如总结、达到上限等）。
func (j *Journal) Note(text string) {
	j.append("- " + strings.ReplaceAll(strings.TrimSpace(text), "\n", " "))
}

// Recent 读取日志末尾最多 maxBytes 字节，用于注入历史上下文。
func (j *Journal) Recent(maxBytes int) string {
	if !j.enabled {
		return ""
	}
	data, err := os.ReadFile(j.path)
	if err != nil {
		return ""
	}
	if len(data) > maxBytes {
		data = data[len(data)-maxBytes:]
	}
	return string(data)
}
