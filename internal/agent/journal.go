package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Journal struct {
	path    string
	enabled bool
}

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

func (j *Journal) Task(input string) {
	j.append(fmt.Sprintf("\n## %s  %s", time.Now().Format("2006-01-02 15:04:05"), input))
}

func (j *Journal) Tool(name, args string) {
	j.append(fmt.Sprintf("- 🔧 %s %s", name, args))
}

func (j *Journal) Note(text string) {
	j.append("- " + strings.ReplaceAll(strings.TrimSpace(text), "\n", " "))
}

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

// TaskEntry 一次对话任务的摘要。
type TaskEntry struct {
	Time    string
	Task    string
	Replies []string
}

// LastTasks 读取日志中最近 n 条任务记录。
func (j *Journal) LastTasks(n int) []TaskEntry {
	if !j.enabled {
		return nil
	}
	data, err := os.ReadFile(j.path)
	if err != nil {
		return nil
	}

	var entries []TaskEntry
	var current *TaskEntry

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			// 新任务
			parts := strings.SplitN(line[3:], "  ", 2)
			if len(parts) >= 2 {
				entries = append(entries, TaskEntry{Time: parts[0], Task: parts[1]})
				current = &entries[len(entries)-1]
			}
		} else if strings.HasPrefix(line, "- ✅ ") && current != nil {
			// AI 的回复
			reply := strings.TrimPrefix(line, "- ✅ ")
			current.Replies = append(current.Replies, reply)
		}
	}

	// 取最后 n 条
	if len(entries) > n {
		return entries[len(entries)-n:]
	}
	return entries
}
