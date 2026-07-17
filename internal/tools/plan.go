package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WritePlanTool 把任务拆解结构写到 .aicode/plan.md，
// 让模型"先规划再执行"，每完成一步更新状态。
type WritePlanTool struct{}

func (WritePlanTool) Name() string { return "write_plan" }

func (WritePlanTool) Description() string {
	return "创建或更新任务计划。对于需要多个步骤的复杂任务，" +
		"先写计划再按步执行，完成一步更新一步（status 用 pending/in_progress/done）。" +
		"计划保存在 .aicode/plan.md，方便跨会话继续。"
}

func (WritePlanTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"steps": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":     map[string]any{"type": "string", "description": "步骤编号，如 1、2a、2b"},
						"title":  map[string]any{"type": "string", "description": "步骤简述"},
						"status": map[string]any{"type": "string", "description": "pending | in_progress | done"},
					},
					"required": []string{"id", "title", "status"},
				},
				"description": "计划步骤列表。每次调用传当前全部步骤和最新状态，会整体覆盖。",
			},
		},
		"required": []string{"steps"},
	}
}

// 计划文件不需要审批（副作用低，且频繁更新）。
func (WritePlanTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Steps []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if len(p.Steps) == 0 {
		return "", fmt.Errorf("steps 不能为空")
	}

	dir, err := EnsureWorkspaceDir()
	if err != nil {
		return "", err
	}

	var md strings.Builder
	md.WriteString("# 任务计划\n\n")
	md.WriteString("| # | 状态 | 任务 |\n")
	md.WriteString("|---|------|------|\n")
	done := 0
	for _, s := range p.Steps {
		icon := map[string]string{
			"pending":    "⬜",
			"in_progress": "🔄",
			"done":       "✅",
		}[s.Status]
		if icon == "" {
			icon = "⬜"
		}
		if s.Status == "done" {
			done++
		}
		md.WriteString(fmt.Sprintf("| %s | %s | %s |\n", s.ID, icon, s.Title))
	}
	md.WriteString(fmt.Sprintf("\n> 进度：%d/%d 完成\n", done, len(p.Steps)))

	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(md.String()), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("计划已更新（%d/%d 完成）", done, len(p.Steps)), nil
}
