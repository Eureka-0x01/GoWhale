package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"gowhale/internal/context"
	"gowhale/internal/llm"
)

// WorkspaceProvider 由调用方注入工作目录路径。
var WorkspaceProvider func() string

// ── project_overview 工具 ──

// ProjectOverviewTool 一次性返回完整项目结构和文件摘要。
type ProjectOverviewTool struct{}

func (t ProjectOverviewTool) Name() string        { return "project_overview" }
func (t ProjectOverviewTool) Description() string { return "获取完整项目概况：文件树、模块依赖、每个 Go 文件的摘要（包名/导出符号/导入）。用这个代替逐个 list_dir + read_file 探索项目。" }

func (t ProjectOverviewTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"refresh": map[string]any{
				"type":        "boolean",
				"description": "是否强制刷新缓存（默认 false，使用增量缓存）",
			},
		},
	}
}

func (t ProjectOverviewTool) Execute(args json.RawMessage) (string, error) {
	ws := ""
	if WorkspaceProvider != nil {
		ws = WorkspaceProvider()
	}
	if ws == "" {
		return "错误：未设置工作目录", nil
	}

	// 增量扫描
	m, err := context.ScanProject(ws)
	if err != nil {
		return fmt.Sprintf("扫描项目失败: %v", err), nil
	}

	if err := m.Save(ws); err != nil {
		// 保存失败不阻塞
	}

	return m.FormatOverview(), nil
}

// ── project_file 工具 ──

// ProjectFileTool 返回指定文件的缓存摘要。
type ProjectFileTool struct{}

func (t ProjectFileTool) Name() string        { return "project_file" }
func (t ProjectFileTool) Description() string { return "获取指定文件的摘要（包名、导出符号、导入、行数）。需要了解文件内容但不需要全部读完时使用。比 read_file 更轻量。" }

func (t ProjectFileTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"required": []string{"paths"},
		"properties": map[string]any{
			"paths": map[string]any{
				"type":        "array",
				"description": "文件路径列表，如 [\"main.go\", \"internal/agent/agent.go\"]",
				"items":       map[string]any{"type": "string"},
			},
		},
	}
}

func (t ProjectFileTool) Execute(args json.RawMessage) (string, error) {
	ws := ""
	if WorkspaceProvider != nil {
		ws = WorkspaceProvider()
	}
	if ws == "" {
		return "错误：未设置工作目录", nil
	}

	var r struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(args, &r); err != nil || len(r.Paths) == 0 {
		return "错误：请提供 paths 参数（文件路径列表）", nil
	}

	// 标准化路径分隔符（Windows \ → /）
	normPaths := make([]string, len(r.Paths))
	for i, p := range r.Paths {
		normPaths[i] = filepath.ToSlash(p)
	}

	m := context.LoadManifest(ws)
	if m == nil {
		// 缓存不存在，即时扫描
		var err error
		m, err = context.ScanProject(ws)
		if err != nil {
			return fmt.Sprintf("扫描失败: %v", err), nil
		}
		m.Save(ws)
	}

	return m.FormatFileList(normPaths), nil
}

// ── RegisterProjectTools 注册项目缓存相关工具 ──

func RegisterProjectTools(reg *Registry) {
	reg.Register(ProjectOverviewTool{})
	reg.Register(ProjectFileTool{})
}

// ── InvalidateCache 在 write_file/batch_write 后失效缓存 ──

func InvalidateCache(paths []string) {
	ws := ""
	if WorkspaceProvider != nil {
		ws = WorkspaceProvider()
	}
	if ws == "" {
		return
	}
	context.InvalidateFiles(ws, paths)
}

// ── 工具定义（供 llm.Tool 列表）──

func ProjectOverviewDef() llm.Tool {
	t := ProjectOverviewTool{}
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		},
	}
}

func ProjectFileDef() llm.Tool {
	t := ProjectFileTool{}
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Schema(),
		},
	}
}

// ── Registry 的 Register 方法 ──

func (r *Registry) Register(t Tool) {
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	r.tools[t.Name()] = t
}
