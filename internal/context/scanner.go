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
	SubModules  []string
	FileCounts  map[string]int
	EntryPoints []string
}

// Scan 扫描工作目录，生成项目信息摘要。
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

	// 扫描文件
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
				base == "__pycache__", base == "test_output", base == ".aicode":
				return filepath.SkipDir
			}
			// 记录顶级子模块
			parts := strings.SplitN(rel, string(filepath.Separator), 2)
			if len(parts) == 1 {
				sf, _ := os.ReadDir(path)
				for _, s := range sf {
					if strings.HasSuffix(s.Name(), ".go") {
						info.SubModules = append(info.SubModules, parts[0]+"/")
						break
					}
				}
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(fi.Name()))
		if ext == "" {
			ext = "(无后缀)"
		}
		info.FileCounts[ext]++
		base := strings.ToLower(fi.Name())
		if base == "main.go" || base == "index.js" || base == "index.ts" || base == "main.rs" {
			info.EntryPoints = append(info.EntryPoints, rel)
		}
		return nil
	})

	return info, nil
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
	if len(info.SubModules) > 0 {
		sb.WriteString(fmt.Sprintf("子模块: %s\n", strings.Join(info.SubModules, " ")))
	}
	if len(info.DirectDeps) > 0 {
		sb.WriteString(fmt.Sprintf("直接依赖 (%d): %s\n", len(info.DirectDeps),
			strings.Join(trimDeps(info.DirectDeps, 8), ", ")))
	}
	var extStats []string
	for ext, count := range info.FileCounts {
		extStats = append(extStats, fmt.Sprintf("%s=%d", ext, count))
	}
	sort.Strings(extStats)
	sb.WriteString(fmt.Sprintf("文件: %s\n", strings.Join(extStats, ", ")))
	sb.WriteString("</project_overview>")
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
