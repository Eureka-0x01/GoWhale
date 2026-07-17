package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// workspace 是当前工作区根路径（启动时锁定的 os.Getwd 的绝对路径）。
// 所有文件和命令操作均不得越出此范围。
var workspace string

// SetWorkspace 在启动时设定工作区根路径。
func SetWorkspace(wd string) {
	if abs, err := filepath.Abs(wd); err == nil {
		workspace = filepath.Clean(abs)
	}
}

// Workspace 返回当前工作区。
func Workspace() string { return workspace }

// CheckPath 检查给定路径是否在工作区内。允许工作区内的相对/绝对路径；
// 试图越界（如 ../../etc/passwd）或绝对路径指向工作区外时返回错误。
func CheckPath(path string) error {
	if workspace == "" {
		return nil // 未初始化时不拦截
	}
	abs, err := filepath.Abs(filepath.Join(workspace, path))
	if err != nil {
		return fmt.Errorf("无法解析路径 %q: %w", path, err)
	}
	clean := filepath.Clean(abs)
	ws := filepath.Clean(workspace)
	if !strings.HasPrefix(clean+string(filepath.Separator), ws+string(filepath.Separator)) && clean != ws {
		return fmt.Errorf("路径越界: %q 不在工作区 %q 内。所有操作必须限定在工作区内", path, workspace)
	}
	return nil
}

// CheckShell 检查 shell 命令是否试图切换到工作区之外。
// 注意：这是尽力而为的检查，真正的安全边界是文件操作 CheckPath + 审批门。
func CheckShell(command string) error {
	if workspace == "" {
		return nil
	}
	lower := strings.ToLower(command)
	if !strings.Contains(lower, "cd ") {
		return nil
	}
	ws := filepath.Clean(workspace)
	parts := strings.Fields(command)
	for i, p := range parts {
		if strings.ToLower(p) != "cd" || i+1 >= len(parts) {
			continue
		}
		target := parts[i+1]
		// 移除引号
		target = strings.Trim(target, "\"'")

		// 1) 绝对路径：检查是否在工作区内
		if filepath.IsAbs(target) {
			clean := filepath.Clean(target)
			if !strings.HasPrefix(clean+string(filepath.Separator), ws+string(filepath.Separator)) && clean != ws {
				return fmt.Errorf("禁止 cd 到工作区之外: %q。所有操作限定在 %q 内", target, workspace)
			}
			return nil
		}

		// 2) 相对路径：如果包含 ..，检查是否会越出工作区
		if strings.Contains(target, "..") {
			abs := filepath.Clean(filepath.Join(workspace, target))
			if !strings.HasPrefix(abs+string(filepath.Separator), ws+string(filepath.Separator)) && abs != ws {
				return fmt.Errorf("禁止 cd 到工作区之外: %q 会解析到 %q。所有操作限定在 %q 内", target, abs, workspace)
			}
		}
	}
	return nil
}

// ensureWorkspaceDir 确保 .aicode 目录存在（供 plan/journal 等使用）。
func EnsureWorkspaceDir() (string, error) {
	if workspace == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		workspace = wd
	}
	dir := filepath.Join(workspace, ".aicode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
