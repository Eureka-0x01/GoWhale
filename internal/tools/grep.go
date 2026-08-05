package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GrepTool 在工作区文件中搜索文本模式（类似 grep -r）。
// 只读操作，自动放行。
type GrepTool struct{}

func (GrepTool) Name() string                    { return "grep_search" }
func (GrepTool) Review(json.RawMessage) Decision { return Decision{} }

func (GrepTool) Description() string {
	return "在工作区文件中搜索文本或正则模式。返回匹配的行及文件名。用于了解代码中某个符号/模式的所有出现位置。"
}

func (GrepTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "搜索模式（正则表达式）。如 'func.*Handler' 或 'TODO'",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "搜索目录，默认为工作区根目录",
			},
			"include": map[string]any{
				"type":        "string",
				"description": "文件匹配模式，如 '*.go' 或 '*.md'。默认搜索所有文本文件",
			},
		},
		"required": []string{"pattern"},
	}
}

// maxGrepResults 单次搜索最大返回行数。
const maxGrepResults = 200

// maxGrepFileSize 跳过超过此大小的文件（1MB）。
const maxGrepFileSize = 1 << 20

func (GrepTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if strings.TrimSpace(p.Pattern) == "" {
		return "", fmt.Errorf("pattern 不能为空")
	}

	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return "", fmt.Errorf("正则表达式无效: %w", err)
	}

	searchDir := workspace
	if p.Path != "" {
		searchDir = filepath.Join(workspace, p.Path)
		if err := CheckPath(searchDir); err != nil {
			return "", err
		}
	}

	var results []string
	totalMatches := 0

	err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的文件
		}
		if info.IsDir() {
			base := filepath.Base(path)
			// 跳过隐藏目录和常见忽略目录
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" || base == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		// 跳过二进制和大文件
		if info.Size() > maxGrepFileSize {
			return nil
		}
		// 文件类型过滤
		if p.Include != "" {
			matched, _ := filepath.Match(p.Include, filepath.Base(path))
			if !matched {
				return nil
			}
		}
		// 跳过非文本文件（简单启发式：有常见文本扩展名或名称）
		if !IsTextFile(path) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		if !strings.Contains(content, re.String()[:min(20, len(re.String()))]) && len(re.String()) <= 20 {
			// 快速检查：如果模式是纯文本且不在文件中，跳过正则匹配
			// 只在模式<=20字符时做此优化（避免误判）
			if IsLiteral(re.String()) && !strings.Contains(content, re.String()) {
				return nil
			}
		}

		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if totalMatches >= maxGrepResults {
				return fmt.Errorf("limit") // 用 error 中断 Walk
			}
			if re.MatchString(line) {
				totalMatches++
				if len(results) < maxGrepResults {
					rel, _ := filepath.Rel(workspace, path)
					if rel == "" {
						rel = path
					}
					results = append(results, fmt.Sprintf("%s:%d:  %s", rel, i+1, strings.TrimRight(line, "\r")))
				}
			}
		}
		return nil
	})

	if err != nil && err.Error() != "limit" {
		return "", err
	}

	if len(results) == 0 {
		return fmt.Sprintf("grep_search: 未找到匹配 %q 的结果", p.Pattern), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("搜索: %q（共 %d 处匹配，显示 %d 条）\n\n", p.Pattern, totalMatches, len(results)))
	for _, r := range results {
		sb.WriteString(r + "\n")
	}

	out := sb.String()
	if len(out) > maxOutput {
		out = out[:maxOutput] + "\n...(输出过长已截断)"
	}
	return out, nil
}

// IsTextFile 通过扩展名判断是否为文本文件。
func IsTextFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	textExts := map[string]bool{
		".go": true, ".rs": true, ".py": true, ".js": true, ".ts": true,
		".jsx": true, ".tsx": true, ".vue": true, ".svelte": true,
		".java": true, ".kt": true, ".swift": true, ".c": true, ".cpp": true,
		".h": true, ".hpp": true, ".cs": true, ".rb": true, ".php": true,
		".sh": true, ".bash": true, ".zsh": true, ".ps1": true, ".bat": true,
		".md": true, ".txt": true, ".yaml": true, ".yml": true, ".toml": true,
		".json": true, ".xml": true, ".html": true, ".css": true, ".scss": true,
		".sql": true, ".proto": true, ".graphql": true, ".cfg": true, ".ini": true,
		".env": true, ".conf": true, ".dockerfile": true, ".makefile": true,
		".mod": true, ".sum": true, ".lock": true,
	}
	if textExts[ext] {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	if base == "makefile" || base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") {
		return true
	}
	return false
}

// IsLiteral 判断模式是否为纯文本（不含正则特殊字符）。
func IsLiteral(s string) bool {
	for _, c := range s {
		if strings.ContainsRune(`\.+*?()|[]{}^$`, c) {
			return false
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
