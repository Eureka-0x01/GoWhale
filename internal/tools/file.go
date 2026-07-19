package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---------- 读文件 ----------

type ReadFileTool struct{}

func (ReadFileTool) Name() string { return "read_file" }

func (ReadFileTool) Description() string {
	return "读取本地某个文件的文本内容。用于查看代码、配置等。"
}

func (ReadFileTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "文件路径，可为相对路径或绝对路径",
			},
		},
		"required": []string{"path"},
	}
}

func (ReadFileTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if strings.TrimSpace(p.Path) == "" {
		return "", errors.New("path 不能为空")
	}
	if err := CheckPath(p.Path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return "", err
	}
	content := string(data)
	if len(content) > maxOutput {
		content = content[:maxOutput] + "\n...(文件过长已截断)"
	}
	return content, nil
}

// ---------- 写文件 ----------

type WriteFileTool struct{}

func (WriteFileTool) Name() string { return "write_file" }

func (WriteFileTool) Description() string {
	return "写入**单个**文件（覆盖写）。自动创建父目录。⚠️ 仅用于只需写 1 个文件的场景。如需创建/修改 2 个及以上文件，必须用 batch_write 一次完成，逐个调用 write_file 会严重浪费工具调用轮次。"
}

// Review 让 write_file 走审批门。作用域按「目录」记忆：
// 用户对某个目录选择「始终允许」后，写入该目录及其子目录都自动放行。
func (WriteFileTool) Review(args json.RawMessage) Decision {
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(args, &p)
	dir := filepath.Dir(p.Path)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return Decision{NeedApproval: true, ScopeKind: "dir", Scope: dir}
}

func (WriteFileTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要写入的文件路径",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "要写入的完整文本内容",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (WriteFileTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if strings.TrimSpace(p.Path) == "" {
		return "", errors.New("path 不能为空")
	}
	if err := CheckPath(p.Path); err != nil {
		return "", err
	}
	if dir := filepath.Dir(p.Path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("创建目录失败: %w", err)
		}
	}
	snapshot(p.Path) // 写前备份
	if err := os.WriteFile(p.Path, []byte(p.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("已写入文件 %s（%d 字节）。如需写更多文件请用 batch_write 一次完成。", p.Path, len(p.Content)), nil
}

// ---------- 批量写文件 ----------

type BatchWriteTool struct{}

func (BatchWriteTool) Name() string { return "batch_write" }

func (BatchWriteTool) Description() string {
	return "一次写入多个文件。当你需要创建或修改 2 个及以上文件时，**必须**用这个工具。传入文件列表，每项包含 path（文件路径）和 content（文本内容）。自动创建父目录。一次调用完成所有文件，避免多次审批和浪费工具调用轮次。"


}

func (BatchWriteTool) Review(args json.RawMessage) Decision {
	// 多条文件集中在一次审批里完成。
	// Scope 取最深的公共目录，让目录级记忆尽可能匹配。
	var p struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
	_ = json.Unmarshal(args, &p)
	dir := commonParent(p.Files)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return Decision{NeedApproval: true, ScopeKind: "dir", Scope: dir}
}

func (BatchWriteTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"files": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string", "description": "文件路径，可为相对路径"},
						"content": map[string]any{"type": "string", "description": "要写入的文本内容"},
					},
					"required": []string{"path", "content"},
				},
				"description": "要写入的文件列表",
			},
		},
		"required": []string{"files"},
	}
}

// snapshot 在覆盖写文件前，把原文件备份到 .aicode/snapshots/，方便回滚。
// 如果文件不存在（新建），跳过。
func snapshot(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	// 检查原文件是否存在
	info, err := os.Stat(abs)
	if err != nil {
		return // 新建文件，无需快照
	}
	snapDir := filepath.Join(workspace, ".aicode", "snapshots")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		return
	}
	// 读原内容
	data, err := os.ReadFile(abs)
	if err != nil {
		return
	}
	// 生成快照文件名: 时间戳_路径
	ts := time.Now().Format("20060102_150405")
	safeName := strings.ReplaceAll(strings.TrimPrefix(abs, workspace+string(filepath.Separator)), string(filepath.Separator), "_")
	snapFile := filepath.Join(snapDir, fmt.Sprintf("%s_%s", ts, safeName))
	_ = os.WriteFile(snapFile, data, 0o600)
	// 记录到快照索引
	idxFile := filepath.Join(snapDir, "index.log")
	f, err := os.OpenFile(idxFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		defer f.Close()
		fmt.Fprintf(f, "%s | %s | %d bytes\n", ts, abs, info.Size())
	}
}

// maxBatchFiles 是 batch_write 一次调用允许的最大文件数。
const maxBatchFiles = 30

func (BatchWriteTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Files []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if len(p.Files) == 0 {
		return "", errors.New("files 不能为空")
	}
	if len(p.Files) > maxBatchFiles {
		return "", fmt.Errorf("单次最多写入 %d 个文件，你传了 %d 个。请拆成多次 batch_write 调用", maxBatchFiles, len(p.Files))
	}
	const summaryMax = 15 // 摘要里最多列 15 个文件名，再多只给计数
	var sb strings.Builder
	written := 0
	for _, f := range p.Files {
		if strings.TrimSpace(f.Path) == "" {
			continue
		}
		if err := CheckPath(f.Path); err != nil {
			return "", err
		}
		snapshot(f.Path)
		if dir := filepath.Dir(f.Path); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", fmt.Errorf("创建目录失败（%s）: %w", dir, err)
			}
		}
		if err := os.WriteFile(f.Path, []byte(f.Content), 0o644); err != nil {
			return "", fmt.Errorf("写入失败（%s）: %w → 检查路径和权限，不要重试同一路径", f.Path, err)
		}
		written++
		if written <= summaryMax {
			sb.WriteString(fmt.Sprintf("  %s（%d 字节）\n", f.Path, len(f.Content)))
		}
	}
	msg := fmt.Sprintf("已批量写入 %d 个文件：\n%s", written, sb.String())
	if written > summaryMax {
		msg += fmt.Sprintf("  …还有 %d 个文件（省略）\n", written-summaryMax)
	}
	return msg, nil
}

// commonParent 找到文件列表最深的公共父目录，没有则返回 "."。
func commonParent(files []struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}) string {
	if len(files) == 0 {
		return "."
	}
	first := filepath.Dir(files[0].Path)
	for _, f := range files[1:] {
		d := filepath.Dir(f.Path)
		for !strings.HasPrefix(d+string(filepath.Separator), first+string(filepath.Separator)) && first != "." {
			first = filepath.Dir(first)
		}
	}
	if first == "" {
		return "."
	}
	return first
}

// ---------- 列目录 ----------

type ListDirTool struct{}

func (ListDirTool) Name() string { return "list_dir" }

func (ListDirTool) Description() string {
	return "列出某个目录下的文件和子目录。用于浏览项目结构。"
}

func (ListDirTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "目录路径，默认为当前目录 .",
			},
		},
	}
}

func (ListDirTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(args, &p)
	if strings.TrimSpace(p.Path) == "" {
		p.Path = "."
	}
	if err := CheckPath(p.Path); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(p.Path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			b.WriteString("[目录] " + e.Name() + "/\n")
		} else {
			b.WriteString("[文件] " + e.Name() + "\n")
		}
	}
	if b.Len() == 0 {
		return "(空目录)", nil
	}
	return b.String(), nil
}
