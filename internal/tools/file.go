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
	return "读取本地某个文件的文本内容。用于查看代码、配置等。超过 200 行的文件会自动截断为摘要+头尾。"
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
	info, err := os.Stat(p.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return "", err
	}

	// ── 读取溢出（Read Spillover）：大文件返回摘要+头尾，避免撑爆上下文 ──
	const spilloverLines = 200
	content := string(data)
	lines := strings.Split(content, "\n")
	if len(lines) > spilloverLines {
		headLines := 50
		tailLines := 50
		if len(lines) < headLines+tailLines {
			headLines = spilloverLines / 2
			tailLines = spilloverLines / 2
		}
		head := strings.Join(lines[:headLines], "\n")
		tail := strings.Join(lines[len(lines)-tailLines:], "\n")
		return fmt.Sprintf(
			"文件摘要：%s（总 %d 行，%d 字节）\n\n--- 前 %d 行 ---\n%s\n--- 后 %d 行 ---\n%s\n\n内容过长已截断。如需查看中间部分，请使用 grep_search 或指定参数重新读取。",
			p.Path, len(lines), info.Size(), headLines, head, tailLines, tail,
		), nil
	}

	// 小文件直接返回
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
	return "一次写入多个文件。当你需要创建或修改 2 个及以上文件时，**必须**用这个工具。传入一个 JSON 对象(Map)，键为文件路径，值为文件完整内容。自动创建父目录，原子性写入（任一文件失败则全部回滚）。一次调用完成所有文件，避免多次审批和浪费工具调用轮次。"
}

func (BatchWriteTool) Review(args json.RawMessage) Decision {
	// 多条文件集中在一次审批里完成。
	// Scope 取最深的公共目录，让目录级记忆尽可能匹配。
	var p struct {
		Files map[string]string `json:"files"`
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
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
				"description":          "键为文件路径（相对根目录），值为文件完整内容。",
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

// snapEntry 记录一个待写入文件的快照信息，用于失败回滚。
type snapEntry struct {
	path     string
	origData []byte // nil 表示原文件不存在（新建），回滚时需删除
	content  string
}

func (BatchWriteTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Files map[string]string `json:"files"`
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

	// ── 第一阶段：验证所有路径 + 预创建目录 + 保存快照（内存） ──
	snaps := make([]snapEntry, 0, len(p.Files))
	for fpath, content := range p.Files {
		if strings.TrimSpace(fpath) == "" {
			continue
		}
		if err := CheckPath(fpath); err != nil {
			return "", err
		}
		// 预创建父目录
		if dir := filepath.Dir(fpath); dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", fmt.Errorf("创建目录失败（%s）: %w", dir, err)
			}
		}
		// 读取原始内容（如存在），用于回滚
		orig, _ := os.ReadFile(fpath) // err 表示原文件不存在
		snaps = append(snaps, snapEntry{path: fpath, origData: orig, content: content})
	}

	// ── 第二阶段：原子性写入，任一失败则回滚所有已写入文件 ──
	const summaryMax = 15
	var sb strings.Builder
	written := 0
	for i, s := range snaps {
		if err := os.WriteFile(s.path, []byte(s.content), 0o644); err != nil {
			// 回滚：恢复已写入的 i 个文件
			rollbackErr := rollbackSnaps(snaps[:i])
			if rollbackErr != nil {
				return "", fmt.Errorf("写入失败（%s）: %w；回滚也失败: %v", s.path, err, rollbackErr)
			}
			return "", fmt.Errorf("写入失败（%s）: %w → 已回滚之前写入的 %d 个文件。请检查路径和权限，不要重试同一路径", s.path, err, i)
		}
		written++
		// 写入成功后做磁盘快照（备份原内容到 .aicode/snapshots）
		if s.origData == nil {
			// 新建文件：不做磁盘快照
		} else {
			snapshot(s.path) // 备份原内容（现在已变成旧内容）
		}
		if written <= summaryMax {
			sb.WriteString(fmt.Sprintf("  %s（%d 字节）\n", s.path, len(s.content)))
		}
	}

	msg := fmt.Sprintf("已批量写入 %d 个文件：\n%s", written, sb.String())
	if written > summaryMax {
		msg += fmt.Sprintf("  …还有 %d 个文件（省略）\n", written-summaryMax)
	}
	return msg, nil
}

// rollbackSnaps 回滚已写入的文件：恢复原内容或删除新建文件。
func rollbackSnaps(entries []snapEntry) error {
	for _, s := range entries {
		if s.origData != nil {
			if err := os.WriteFile(s.path, s.origData, 0o644); err != nil {
				return fmt.Errorf("回滚恢复 %s 失败: %w", s.path, err)
			}
		} else {
			if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("回滚删除新文件 %s 失败: %w", s.path, err)
			}
		}
	}
	return nil
}

// commonParent 找到文件路径列表中最深的公共父目录，没有则返回 "."。
func commonParent(filesMap map[string]string) string {
	if len(filesMap) == 0 {
		return "."
	}
	// 提取所有路径
	paths := make([]string, 0, len(filesMap))
	for p := range filesMap {
		paths = append(paths, p)
	}
	first := filepath.Dir(paths[0])
	for _, p := range paths[1:] {
		d := filepath.Dir(p)
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
