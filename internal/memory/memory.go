// Package memory 提供对话记忆持久化。
// 保存最近 10 条对话摘要到工作目录 .aicode/memory.json。
package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Entry 一条记忆。
type Entry struct {
	Role    string `json:"role"`    // user / assistant
	Content string `json:"content"` // 摘要（截断到 200 字符）
}

// Store 记忆存储。
type Store struct {
	path    string
	entries []Entry
}

// Load 加载记忆，不存在则创建空存储。
func Load(workspace string) *Store {
	dir := filepath.Join(workspace, ".aicode")
	os.MkdirAll(dir, 0755)
	p := filepath.Join(dir, "memory.json")

	s := &Store{path: p}
	data, err := os.ReadFile(p)
	if err != nil {
		return s
	}
	json.Unmarshal(data, &s.entries)
	// 只保留最近 10 条
	if len(s.entries) > 10 {
		s.entries = s.entries[len(s.entries)-10:]
	}
	return s
}

// Add 添加一条记忆并保存。
func (s *Store) Add(role, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	if len(content) > 200 {
		content = content[:200]
	}
	s.entries = append(s.entries, Entry{Role: role, Content: content})
	if len(s.entries) > 10 {
		s.entries = s.entries[len(s.entries)-10:]
	}
	s.save()
}

// Format 生成注入系统提示的文本。
func (s *Store) Format() string {
	if len(s.entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 最近对话记忆\n")
	for _, e := range s.entries {
		label := "用户"
		if e.Role == "assistant" {
			label = "助手"
		}
		b.WriteString("- [" + label + "] " + e.Content + "\n")
	}
	return b.String()
}

func (s *Store) save() {
	data, _ := json.MarshalIndent(s.entries, "", "  ")
	dir := filepath.Dir(s.path)
	os.MkdirAll(dir, 0755)
	os.WriteFile(s.path, data, 0644)
}
