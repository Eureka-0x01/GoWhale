package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ProjectSettings 项目级持久化设置（保存在 .aicode/settings.json）。
// 每次启动 TUI 时读取恢复，退出/修改时保存，按工作目录隔离。
type ProjectSettings struct {
	ModelLock    string `json:"model_lock"`    // 锁定的模型（空 = 自动模式）
	UseChatRoom  bool   `json:"use_chatroom"`  // 是否启用多角色协作模式
	MouseEnabled bool   `json:"mouse_enabled"` // 是否启用鼠标模式（false = 原生复制）
}

// settingsPath 返回项目设置文件路径。
func settingsPath(workspace string) string {
	return filepath.Join(workspace, ".aicode", "settings.json")
}

// LoadProjectSettings 读取项目设置；文件不存在或损坏时返回零值。
// 兼容旧版设置文件：若未显式保存 mouse_enabled 字段（旧版无此字段），
// 视为用户未配置过鼠标模式，默认开启（true），避免旧设置导致滚轮失效。
func LoadProjectSettings(workspace string) ProjectSettings {
	var s ProjectSettings
	data, err := os.ReadFile(settingsPath(workspace))
	if err != nil {
		return s
	}
	if json.Unmarshal(data, &s) != nil {
		return ProjectSettings{}
	}
	// 检查 mouse_enabled 是否被显式保存过
	var raw map[string]any
	if json.Unmarshal(data, &raw) == nil {
		if _, ok := raw["mouse_enabled"]; !ok {
			s.MouseEnabled = true // 旧版设置无此字段 → 默认开启鼠标
		}
	}
	return s
}

// SaveProjectSettings 保存项目设置到 .aicode/settings.json。
func SaveProjectSettings(workspace string, s ProjectSettings) {
	dir := filepath.Dir(settingsPath(workspace))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(settingsPath(workspace), data, 0o644)
}
