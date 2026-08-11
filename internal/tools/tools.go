// Package tools 定义所有可用工具，供 trpc-agent-go Agent 使用。
package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// All 返回所有工具列表。
func All(workspace string) []tool.Tool {
	return []tool.Tool{
		readFileTool(workspace),
		writeFileTool(workspace),
		editFileTool(workspace),
		listDirTool(workspace),
		grepSearchTool(workspace),
		shellTool(workspace),
	}
}

// ── 类型定义 ──

type pathArgs struct {
	Path string `json:"path" jsonschema:"description=文件路径"`
}

type pathsArgs struct {
	Paths []string `json:"paths" jsonschema:"description=文件路径列表，最多 20 个"`
}

type readFileInput struct {
	Path      string   `json:"path,omitempty" jsonschema:"description=单个文件路径（与 paths 二选一）"`
	Paths     []string `json:"paths,omitempty" jsonschema:"description=批量文件路径列表，最多 20 个"`
	StartLine int      `json:"start_line,omitempty" jsonschema:"description=起始行号（1-based），仅单文件模式有效"`
	MaxLines  int      `json:"max_lines,omitempty" jsonschema:"description=最大行数，默认 200"`
}

type readFileOutput struct {
	Content string `json:"content"`
}

type writeFileInput struct {
	Path    string `json:"path" jsonschema:"description=文件路径,required"`
	Content string `json:"content" jsonschema:"description=文件完整内容,required"`
}

type writeFileOutput struct {
	Result string `json:"result"`
}

type editFileInput struct {
	Path      string `json:"path" jsonschema:"description=文件路径,required"`
	OldString string `json:"old_string" jsonschema:"description=要替换的原文（必须在文件中唯一）,required"`
	NewString string `json:"new_string" jsonschema:"description=替换后的新文本,required"`
}

type editFileOutput struct {
	Result string `json:"result"`
}

type listDirInput struct {
	Path  string   `json:"path,omitempty" jsonschema:"description=单个目录路径（与 paths 二选一）"`
	Paths []string `json:"paths,omitempty" jsonschema:"description=批量目录路径列表"`
}

type listDirOutput struct {
	Content string `json:"content"`
}

type grepSearchInput struct {
	Pattern string `json:"pattern" jsonschema:"description=搜索模式（支持正则）,required"`
	Path    string `json:"path,omitempty" jsonschema:"description=搜索路径，默认当前目录"`
	Include string `json:"include,omitempty" jsonschema:"description=文件过滤，如 *.go"`
}

type grepSearchOutput struct {
	Matches string `json:"matches"`
}

type shellInput struct {
	Command    string `json:"command" jsonschema:"description=要执行的命令,required"`
	Background bool   `json:"background,omitempty" jsonschema:"description=是否后台运行（长期服务设为 true）"`
}

type shellOutput struct {
	Result string `json:"result"`
}

// ── 工具实现 ──

func readFileTool(ws string) tool.Tool {
	return function.NewFunctionTool(func(ctx context.Context, in readFileInput) (readFileOutput, error) {
		files := in.Paths
		if len(files) == 0 && in.Path != "" {
			files = []string{in.Path}
		}
		if len(files) == 0 {
			return readFileOutput{}, fmt.Errorf("path 或 paths 不能同时为空")
		}
		if len(files) > 20 {
			return readFileOutput{}, fmt.Errorf("最多读取 20 个文件")
		}

		var sb strings.Builder
		for _, f := range files {
			f = filepath.Join(ws, f)
			data, err := os.ReadFile(f)
			if err != nil {
				sb.WriteString(fmt.Sprintf("--- %s ---\n[错误] %v\n\n", f, err))
				continue
			}
			content := string(data)
			lines := strings.Split(content, "\n")

			if in.StartLine > 0 && len(files) == 1 {
				sl := in.StartLine - 1
				ml := in.MaxLines
				if ml == 0 { ml = 200 }
				end := sl + ml
				if end > len(lines) { end = len(lines) }
				if sl >= len(lines) {
					return readFileOutput{}, fmt.Errorf("start_line %d 超出总行数 %d", in.StartLine, len(lines))
				}
				sb.WriteString(fmt.Sprintf("=== %s (行 %d-%d/%d) ===\n%s\n",
					f, sl+1, end, len(lines), strings.Join(lines[sl:end], "\n")))
			} else if len(lines) > 200 && in.MaxLines == 0 {
				sb.WriteString(fmt.Sprintf("=== %s (头尾各50行，共 %d 行) ===\n", f, len(lines)))
				sb.WriteString(strings.Join(lines[:50], "\n"))
				sb.WriteString("\n...\n")
				sb.WriteString(strings.Join(lines[len(lines)-50:], "\n"))
			} else {
				sb.WriteString(fmt.Sprintf("=== %s ===\n%s\n", f, content))
			}
		}
		return readFileOutput{Content: sb.String()}, nil
	}, function.WithName("read_file"),
		function.WithDescription("读取文件内容。用 paths 批量读多个文件。大文件自动截断，用 start_line+max_lines 分段读取。"))
}

func writeFileTool(ws string) tool.Tool {
	return function.NewFunctionTool(func(ctx context.Context, in writeFileInput) (writeFileOutput, error) {
		p := filepath.Join(ws, in.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return writeFileOutput{}, err
		}
		if err := os.WriteFile(p, []byte(in.Content), 0644); err != nil {
			return writeFileOutput{}, err
		}
		return writeFileOutput{Result: fmt.Sprintf("已写入 %s (%d 字节)", in.Path, len(in.Content))}, nil
	}, function.WithName("write_file"),
		function.WithDescription("创建或覆盖单个文件。自动建父目录。多文件用 batch_write。"))
}

func editFileTool(ws string) tool.Tool {
	return function.NewFunctionTool(func(ctx context.Context, in editFileInput) (editFileOutput, error) {
		p := filepath.Join(ws, in.Path)
		data, err := os.ReadFile(p)
		if err != nil {
			return editFileOutput{}, fmt.Errorf("读取失败: %w", err)
		}
		text := string(data)
		count := strings.Count(text, in.OldString)
		if count == 0 {
			return editFileOutput{}, fmt.Errorf("未找到 old_string，请确认原文完全一致（含缩进和换行）")
		}
		if count > 1 {
			return editFileOutput{}, fmt.Errorf("old_string 出现了 %d 次，请包含更多上下文使其唯一", count)
		}
		text = strings.Replace(text, in.OldString, in.NewString, 1)
		if err := os.WriteFile(p, []byte(text), 0644); err != nil {
			return editFileOutput{}, err
		}
		return editFileOutput{Result: fmt.Sprintf("已修改 %s (替换 1 处)", in.Path)}, nil
	}, function.WithName("edit_file"),
		function.WithDescription("精确替换文件中的指定文本。old_string 必须在文件中唯一。用于小范围修改，不需要重写整个文件。"))
}

func listDirTool(ws string) tool.Tool {
	return function.NewFunctionTool(func(ctx context.Context, in listDirInput) (listDirOutput, error) {
		dirs := in.Paths
		if len(dirs) == 0 && in.Path != "" {
			dirs = []string{in.Path}
		}
		if len(dirs) == 0 {
			dirs = []string{"."}
		}

		var sb strings.Builder
		for _, d := range dirs {
			d = filepath.Join(ws, d)
			entries, err := os.ReadDir(d)
			if err != nil {
				sb.WriteString(fmt.Sprintf("[%s] 错误: %v\n", d, err))
				continue
			}
			sb.WriteString(fmt.Sprintf("=== %s ===\n", d))
			for _, e := range entries {
				tag := "[文件]"
				if e.IsDir() { tag = "[目录]" }
				sb.WriteString(fmt.Sprintf("%s %s\n", tag, e.Name()))
			}
		}
		return listDirOutput{Content: sb.String()}, nil
	}, function.WithName("list_dir"),
		function.WithDescription("列出目录内容。用 paths 参数批量列多个目录。"))
}

func grepSearchTool(ws string) tool.Tool {
	return function.NewFunctionTool(func(ctx context.Context, in grepSearchInput) (grepSearchOutput, error) {
		root := filepath.Join(ws, in.Path)
		if root == ws {
			root = ws
		}
		include := in.Include
		if include == "" { include = "*" }

		var matches []string
		filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() { return nil }
			match, _ := filepath.Match(include, fi.Name())
			if !match { return nil }
			rel, _ := filepath.Rel(ws, path)
			data, err := os.ReadFile(path)
			if err != nil { return nil }
			for i, line := range strings.Split(string(data), "\n") {
				if strings.Contains(line, in.Pattern) {
					matches = append(matches, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
					if len(matches) >= 50 { return filepath.SkipAll }
				}
			}
			return nil
		})
		if len(matches) == 0 {
			return grepSearchOutput{Matches: fmt.Sprintf("未找到匹配 \"%s\"", in.Pattern)}, nil
		}
		return grepSearchOutput{Matches: strings.Join(matches, "\n")}, nil
	}, function.WithName("grep_search"),
		function.WithDescription("在文件中搜索匹配的文本行。返回文件名:行号:内容。用 include 过滤文件类型(如 *.go)。"))
}

func shellTool(ws string) tool.Tool {
	return function.NewFunctionTool(func(ctx context.Context, in shellInput) (shellOutput, error) {
		if in.Background {
			go exec.Command("cmd", "/c", in.Command).Start()
			return shellOutput{Result: "已后台启动"}, nil
		}
		cmd := exec.Command("cmd", "/c", in.Command)
		cmd.Dir = ws
		out, err := cmd.CombinedOutput()
		result := string(out)
		if err != nil {
			return shellOutput{Result: fmt.Sprintf("退出码 %v\n%s", err, result)}, nil
		}
		if result == "" {
			result = "(无输出)"
		}
		return shellOutput{Result: result}, nil
	}, function.WithName("execute_shell"),
		function.WithDescription("执行 shell 命令。编译、运行、安装用此工具。background=true 后台运行长期服务。"))
}
