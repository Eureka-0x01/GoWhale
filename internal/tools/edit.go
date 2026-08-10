package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// EditFileTool 精确字符串替换——类似 Claude Code 的 Edit。
// 模型只需提供要替换的原文和替换后的内容，不需要生成完整文件。
type EditFileTool struct{}

func (EditFileTool) Name() string        { return "edit_file" }
func (EditFileTool) Description() string { return "精确修改文件中的指定文本。找到 old_string 并替换为 new_string（原文必须在文件中唯一存在）。用于小范围修改，不需要重写整个文件。" }

func (EditFileTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"required": []string{"path", "old_string", "new_string"},
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要修改的文件路径",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "要替换的原文（必须在文件中唯一）",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "替换后的新文本",
			},
		},
	}
}

// Approvable 实现——修改文件需要审批。
func (EditFileTool) Review(args json.RawMessage) Decision {
	var p struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(args, &p) == nil && p.Path != "" {
		dir := p.Path
		if idx := strings.LastIndex(dir, "/"); idx >= 0 {
			dir = dir[:idx]
		}
		if idx := strings.LastIndex(dir, "\\"); idx >= 0 {
			dir = dir[:idx]
		}
		if dir == "" {
			dir = "."
		}
		return Decision{NeedApproval: true, ScopeKind: "dir", Scope: dir}
	}
	return Decision{NeedApproval: true}
}

func (EditFileTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if p.Path == "" {
		return "", errors.New("path 不能为空")
	}
	if p.OldString == "" {
		return "", errors.New("old_string 不能为空")
	}
	if err := CheckPath(p.Path); err != nil {
		return "", err
	}

	content, err := os.ReadFile(p.Path)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}

	text := string(content)
	count := strings.Count(text, p.OldString)
	if count == 0 {
		return "", fmt.Errorf("未找到 old_string。请用 read_file 确认文件当前内容，确保 old_string 与原文完全一致（包括缩进和换行）。")
	}
	if count > 1 {
		return "", fmt.Errorf("old_string 在文件中出现了 %d 次，不唯一。请包含更多上下文使 old_string 唯一（如多包含前后几行）。", count)
	}

	snapshot(p.Path)
	newText := strings.Replace(text, p.OldString, p.NewString, 1)
	if err := os.WriteFile(p.Path, []byte(newText), 0o644); err != nil {
		return "", err
	}
	InvalidateCache([]string{p.Path})
	return fmt.Sprintf("已修改 %s：替换 1 处（原文 %d 字符 → 新文 %d 字符）。", p.Path, len(p.OldString), len(p.NewString)), nil
}
