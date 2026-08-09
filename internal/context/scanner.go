package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ProjectInfo 项目扫描结果的结构化摘要。
type ProjectInfo struct {
	Language    string
	ModuleName  string
	GoVersion   string
	DirectDeps  []string
	FileCounts  map[string]int
	EntryPoints []string
	FileTree    string // 完整文件树（目录+文件名，不含内容）
	KeyFiles    string // 关键文件摘要（main.go 头部、go.mod 依赖等）
}

// Scan 扫描工作目录，生成项目信息摘要（含完整文件树，避免模型逐个探索）。
func Scan(workspace string) (*ProjectInfo, error) {
	info := &ProjectInfo{
		FileCounts: map[string]int{},
	}

	// 解析 go.mod（内联解析，不依赖 golang.org/x/mod）
	gomodPath := filepath.Join(workspace, "go.mod")
	if data, err := os.ReadFile(gomodPath); err == nil {
		info.Language = "Go"
		parseGoMod(string(data), info)
	}

	// 检测其他语言
	if info.Language == "" {
		info.Language = detectLanguage(workspace)
	}

	// 读取关键文件摘要
	info.KeyFiles = readKeyFiles(workspace)

	// 构建文件树
	var treeBuf strings.Builder
	var fileTreeEntries []string // 收集所有条目再排序
	treeBuf.WriteString("项目文件树:\n")

	filepath.Walk(workspace, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(workspace, path)
		if rel == "." {
			return nil
		}
		if fi.IsDir() {
			base := filepath.Base(path)
			switch {
			case strings.HasPrefix(base, "."), base == "node_modules", base == "vendor",
				base == "__pycache__", base == "test_output", base == ".aicode",
				base == "Python", base == "third_party", base == "docs":
				return filepath.SkipDir
			}
			return nil
		}
		// 跳过非代码文件
		ext := strings.ToLower(filepath.Ext(fi.Name()))
		if ext == "" {
			ext = "(无后缀)"
		}
		info.FileCounts[ext]++
		base := strings.ToLower(fi.Name())
		if base == "main.go" || base == "index.js" || base == "index.ts" || base == "main.rs" {
			info.EntryPoints = append(info.EntryPoints, rel)
		}

		// 收集文件条目（带行数和目录层次）
		dir := filepath.Dir(rel)
		if dir == "." {
			dir = ""
		}
		fileTreeEntries = append(fileTreeEntries, fmt.Sprintf("%s/%s", dir, fi.Name()))
		return nil
	})

	// 按目录分组排序，生成缩进文件树
	sort.Strings(fileTreeEntries)
	lastDir := ""
	for _, entry := range fileTreeEntries {
		dir, file := filepath.Split(entry)
		dir = strings.TrimRight(dir, "/\\")
		if dir != lastDir {
			treeBuf.WriteString(fmt.Sprintf("  %s/\n", dir))
			lastDir = dir
		}
		treeBuf.WriteString(fmt.Sprintf("    %s\n", file))
	}
	info.FileTree = treeBuf.String()

	return info, nil
}

// readKeyFiles 读取关键文件头部摘要，让模型无需逐个探索。
func readKeyFiles(workspace string) string {
	var sb strings.Builder

	// main.go 头部（前 30 行）
	if data, err := os.ReadFile(filepath.Join(workspace, "main.go")); err == nil {
		lines := strings.Split(string(data), "\n")
		sb.WriteString("main.go 头部:\n")
		n := len(lines)
		if n > 30 {
			n = 30
		}
		sb.WriteString(strings.Join(lines[:n], "\n"))
		sb.WriteString("\n\n")
	}

	// go.mod 全文（很小但关键）
	if data, err := os.ReadFile(filepath.Join(workspace, "go.mod")); err == nil {
		sb.WriteString("go.mod:\n")
		sb.WriteString(string(data))
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// Format 生成注入 LLM system prompt 的文本块。
func (info *ProjectInfo) Format() string {
	var sb strings.Builder
	sb.WriteString("<project_overview>\n")
	sb.WriteString(fmt.Sprintf("语言: %s\n", info.Language))
	if info.ModuleName != "" {
		sb.WriteString(fmt.Sprintf("模块: %s", info.ModuleName))
		if info.GoVersion != "" {
			sb.WriteString(fmt.Sprintf(" (go %s)", info.GoVersion))
		}
		sb.WriteString("\n")
	}
	if len(info.EntryPoints) > 0 {
		sb.WriteString(fmt.Sprintf("入口: %s\n", strings.Join(info.EntryPoints, ", ")))
	}
	if len(info.DirectDeps) > 0 {
		sb.WriteString(fmt.Sprintf("直接依赖: %s\n",
			strings.Join(trimDeps(info.DirectDeps, 8), ", ")))
	}
	sb.WriteString("\n" + info.FileTree + "\n")
	if info.KeyFiles != "" {
		sb.WriteString("\n" + info.KeyFiles)
	}
	sb.WriteString("</project_overview>\n")
	return sb.String()
}

func parseGoMod(content string, info *ProjectInfo) {
	lines := strings.Split(content, "\n")
	inRequire := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "module ") {
			info.ModuleName = strings.TrimPrefix(line, "module ")
			continue
		}
		if strings.HasPrefix(line, "go ") {
			info.GoVersion = strings.TrimPrefix(line, "go ")
			continue
		}
		if line == "require (" {
			inRequire = true
			continue
		}
		if inRequire && line == ")" {
			inRequire = false
			continue
		}
		if inRequire {
			// 跳过 indirect 依赖
			if strings.Contains(line, "// indirect") {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				info.DirectDeps = append(info.DirectDeps, parts[0])
			}
		}
	}
}

func detectLanguage(workspace string) string {
	if _, err := os.Stat(filepath.Join(workspace, "package.json")); err == nil {
		return "Node.js"
	}
	if _, err := os.Stat(filepath.Join(workspace, "Cargo.toml")); err == nil {
		return "Rust"
	}
	if _, err := os.Stat(filepath.Join(workspace, "pyproject.toml")); err == nil {
		return "Python"
	}
	return "Unknown"
}

func trimDeps(deps []string, max int) []string {
	if len(deps) <= max {
		return deps
	}
	return append(deps[:max], fmt.Sprintf("...及其他 %d 个", len(deps)-max))
}
