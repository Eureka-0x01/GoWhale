package tools

import (
	"context"
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// --- 框架工具适配器 ---
// 将现有的 tools.Tool 包装为 tRPC-Agent-Go 的 tool.Tool 接口。

// readFileInput read_file 工具的输入参数。
type readFileInput struct {
	Path      string   `json:"path,omitempty" description:"单个文件路径（与 paths 二选一）"`
	Paths     []string `json:"paths,omitempty" description:"批量文件路径列表（最多 20 个，与 path 二选一）"`
	StartLine int      `json:"start_line,omitempty" description:"起始行号（1-based），用于分页读取大文件"`
	MaxLines  int      `json:"max_lines,omitempty" description:"最大读取行数"`
}

// listDirInput list_dir 工具的输入参数。
type listDirInput struct {
	Path  string   `json:"path,omitempty" description:"单个目录路径（与 paths 二选一）"`
	Paths []string `json:"paths,omitempty" description:"批量目录路径列表（最多 20 个，与 path 二选一）"`
}

// shellInput execute_shell 工具的输入参数。
type shellInput struct {
	Command    string `json:"command" description:"要执行的 shell 命令"`
	Background bool   `json:"background,omitempty" description:"设为 true 时命令在后台运行，立即返回"`
}

// writeFileInput write_file 工具的输入参数。
type writeFileInput struct {
	Path    string `json:"path" description:"文件路径（相对于工作区）"`
	Content string `json:"content" description:"要写入的文件内容"`
}

// batchWriteInput batch_write 工具的输入参数。
type batchWriteInput struct {
	Files map[string]string `json:"files" description:"文件路径到内容的映射，一次性批量写入"`
}

// Adapter 持有原始工具注册表，将单个工具包装为框架兼容形式。
type Adapter struct {
	reg *Registry
}

// NewAdapter 创建框架工具适配器。
func NewAdapter(reg *Registry) *Adapter {
	return &Adapter{reg: reg}
}

// AllTools 返回所有已适配的框架工具列表。
func (a *Adapter) AllTools() []tool.Tool {
	return []tool.Tool{
		a.ReadFile(),
		a.ListDir(),
		a.Shell(),
		a.WriteFile(),
		a.BatchWrite(),
		a.WritePlan(),
		a.Python(),
		a.Search(),
		a.Verify(),
	}
}

// ReadFile 返回 read_file 框架工具。
func (a *Adapter) ReadFile() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in readFileInput) (string, error) {
			args := readFileArgs{Path: in.Path, Paths: in.Paths, StartLine: in.StartLine, MaxLines: in.MaxLines}
			b, _ := json.Marshal(args)
			t, _ := a.reg.Lookup("read_file")
			return t.Execute(b)
		},
		function.WithName("read_file"),
		function.WithDescription("读取文件内容。支持单个文件（path）或批量文件（paths 数组，最多 20 个）。"+
			"大文件默认只显示头尾摘要，用 start_line+max_lines 指定行范围查看中间部分。"),
	)
}

// ListDir 返回 list_dir 框架工具。
func (a *Adapter) ListDir() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in listDirInput) (string, error) {
			args := listDirArgs{Path: in.Path, Paths: in.Paths}
			b, _ := json.Marshal(args)
			t, _ := a.reg.Lookup("list_dir")
			return t.Execute(b)
		},
		function.WithName("list_dir"),
		function.WithDescription("列出目录内容。支持单个目录（path）或批量目录（paths 数组，最多 20 个）。"),
	)
}

// Shell 返回 execute_shell 框架工具。
func (a *Adapter) Shell() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in shellInput) (string, error) {
			b, _ := json.Marshal(in)
			t, _ := a.reg.Lookup("execute_shell")
			return t.Execute(b)
		},
		function.WithName("execute_shell"),
		function.WithDescription("执行 shell 命令。长期服务设 background=true。执行前需审批。"),
	)
}

// WriteFile 返回 write_file 框架工具。
func (a *Adapter) WriteFile() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in writeFileInput) (string, error) {
			b, _ := json.Marshal(in)
			t, _ := a.reg.Lookup("write_file")
			return t.Execute(b)
		},
		function.WithName("write_file"),
		function.WithDescription("写入单个文件（自动创建父目录）。仅限写入 1 个文件时使用，多文件场景请用 batch_write。"),
	)
}

// BatchWrite 返回 batch_write 框架工具。
func (a *Adapter) BatchWrite() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in batchWriteInput) (string, error) {
			b, _ := json.Marshal(in)
			t, _ := a.reg.Lookup("batch_write")
			return t.Execute(b)
		},
		function.WithName("batch_write"),
		function.WithDescription("批量写入多个文件。files 是一个 JSON 对象 {路径: 内容}，一次性提交所有文件。必须用于 ≥2 个文件的场景。"),
	)
}

// WritePlan 返回 write_plan 框架工具。
func (a *Adapter) WritePlan() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in map[string]any) (string, error) {
			b, _ := json.Marshal(in)
			t, _ := a.reg.Lookup("write_plan")
			return t.Execute(b)
		},
		function.WithName("write_plan"),
		function.WithDescription("创建/更新任务计划。3 步以上的复杂任务第一个动作必须是 write_plan。"),
	)
}

// Python 返回 execute_python 框架工具。
func (a *Adapter) Python() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in map[string]any) (string, error) {
			b, _ := json.Marshal(in)
			t, _ := a.reg.Lookup("execute_python")
			return t.Execute(b)
		},
		function.WithName("execute_python"),
		function.WithDescription("在沙箱中执行 Python 代码。不需要先写 .py 文件再执行。"),
	)
}

// Search 返回 grep_search 框架工具。
func (a *Adapter) Search() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in map[string]any) (string, error) {
			b, _ := json.Marshal(in)
			t, _ := a.reg.Lookup("grep_search")
			return t.Execute(b)
		},
		function.WithName("grep_search"),
		function.WithDescription("在项目中搜索文本/正则模式。返回匹配行及上下文。"),
	)
}

// Verify 返回 verify_project 框架工具。
func (a *Adapter) Verify() tool.Tool {
	return function.NewFunctionTool(
		func(ctx context.Context, in map[string]any) (string, error) {
			b, _ := json.Marshal(in)
			t, _ := a.reg.Lookup("verify_project")
			return t.Execute(b)
		},
		function.WithName("verify_project"),
		function.WithDescription("验证项目：运行编译检查和测试。写完代码后主动调用验证。"),
	)
}

// --- 内部辅助类型（映射到原有工具参数格式）---

type readFileArgs struct {
	Path      string   `json:"path,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	StartLine int      `json:"start_line,omitempty"`
	MaxLines  int      `json:"max_lines,omitempty"`
}

type listDirArgs struct {
	Path  string   `json:"path,omitempty"`
	Paths []string `json:"paths,omitempty"`
}
