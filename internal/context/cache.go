package context

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ── 缓存数据结构 ──

// FileMeta 单个文件的缓存元数据。
type FileMeta struct {
	Mtime   int64    `json:"mtime"`   // 文件修改时间（Unix 秒）
	Size    int64    `json:"size"`    // 文件大小
	Pkg     string   `json:"pkg"`     // 包名（Go）
	Imports []string `json:"imports"` // 导入列表
	Exports []string `json:"exports"` // 导出符号（首字母大写的函数/类型）
	Summary string   `json:"summary"` // 一行描述
}

// Manifest 项目缓存清单。
type Manifest struct {
	Version    int                `json:"version"`
	Module     string             `json:"module"`
	GoVersion  string             `json:"go_version"`
	Deps       []string           `json:"deps"`
	ScannedAt  int64              `json:"scanned_at"`
	Files      map[string]FileMeta `json:"files"`   // key: 相对路径
	FileTree   string             `json:"file_tree"`
}

// ── 缓存目录 ──

const cacheDir = ".aicode/project_cache"

func cachePath(workspace string) string {
	return filepath.Join(workspace, cacheDir)
}

func manifestPath(workspace string) string {
	return filepath.Join(cachePath(workspace), "manifest.json")
}

// ── 加载 / 保存 ──

// LoadManifest 从磁盘加载缓存清单，不存在返回 nil。
func LoadManifest(workspace string) *Manifest {
	data, err := os.ReadFile(manifestPath(workspace))
	if err != nil {
		return nil
	}
	var m Manifest
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	if m.Version != 1 {
		return nil
	}
	if m.Files == nil {
		m.Files = map[string]FileMeta{}
	}
	return &m
}

// SaveManifest 保存缓存清单到磁盘。
func (m *Manifest) Save(workspace string) error {
	dir := cachePath(workspace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	m.ScannedAt = time.Now().Unix()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(manifestPath(workspace), data, 0644)
}

// ── 扫描 ──

// ScanProject 扫描项目（增量：复用未变化文件的缓存）。
func ScanProject(workspace string) (*Manifest, error) {
	old := LoadManifest(workspace)
	m := &Manifest{
		Version: 1,
		Files:   map[string]FileMeta{},
	}

	// 解析 go.mod
	gomodPath := filepath.Join(workspace, "go.mod")
	if data, err := os.ReadFile(gomodPath); err == nil {
		parseGoMod(string(data), &ProjectInfo{})
		// 重新解析获取 module/deps
		m.Module, m.GoVersion, m.Deps = parseModMeta(string(data))
	}

	// 收集有效文件路径
	var goFiles []string
	var treeBuf strings.Builder

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
			if strings.HasPrefix(base, ".") || base == "node_modules" ||
				base == "vendor" || base == "__pycache__" || base == "test_output" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(fi.Name(), ".go") {
			goFiles = append(goFiles, filepath.ToSlash(rel))
		}
		return nil
	})

	// 构建文件树
	sort.Strings(goFiles)
	lastDir := ""
	for _, f := range goFiles {
		dir, file := filepath.Split(f)
		dir = strings.TrimRight(dir, "/\\")
		if dir != lastDir {
			if dir != "" {
				treeBuf.WriteString(fmt.Sprintf("  %s/\n", dir))
			}
			lastDir = dir
		}
		if dir == "" {
			treeBuf.WriteString(fmt.Sprintf("  %s\n", file))
		} else {
			treeBuf.WriteString(fmt.Sprintf("    %s\n", file))
		}
	}
	m.FileTree = treeBuf.String()

	// 分析每个 Go 文件
	for _, rel := range goFiles {
		fullPath := filepath.Join(workspace, rel)
		info, err := os.Stat(fullPath)
		if err != nil {
			continue
		}

		mtime := info.ModTime().Unix()
		size := info.Size()

		// 复用缓存：mtime 和 size 都未变
		if old != nil {
			if oldMeta, ok := old.Files[rel]; ok {
				if oldMeta.Mtime == mtime && oldMeta.Size == size {
					m.Files[rel] = oldMeta
					continue
				}
			}
		}

		// 新文件 或 已修改 → 重新分析
		meta := analyzeGoFile(fullPath, info)
		m.Files[rel] = meta
	}

	// 给入口文件加标注
	if meta, ok := m.Files["main.go"]; ok {
		if !strings.Contains(meta.Summary, "入口") {
			meta.Summary = "【入口】" + meta.Summary
			m.Files["main.go"] = meta
		}
	}

	return m, nil
}

// analyzeGoFile 分析单个 Go 文件，提取元数据。
func analyzeGoFile(path string, fi os.FileInfo) FileMeta {
	meta := FileMeta{
		Mtime: fi.ModTime().Unix(),
		Size:  fi.Size(),
	}

	// AST 解析
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		// 解析失败，用基础信息
		meta.Pkg = "?"
		meta.Summary = fmt.Sprintf("解析失败: %v", err)
		return meta
	}

	meta.Pkg = f.Name.Name

	// 提取 imports
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		meta.Imports = append(meta.Imports, path)
	}

	// 提取导出符号（首字母大写的函数/类型/变量）
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				meta.Exports = append(meta.Exports, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						meta.Exports = append(meta.Exports, s.Name.Name)
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.IsExported() {
							meta.Exports = append(meta.Exports, name.Name)
						}
					}
				}
			}
		}
	}

	// 限制导出数量
	if len(meta.Exports) > 12 {
		meta.Exports = meta.Exports[:12]
	}

	// 生成摘要
	var parts []string
	parts = append(parts, fmt.Sprintf("%d 行", fset.File(f.Pos()).LineCount()))
	parts = append(parts, fmt.Sprintf("package %s", meta.Pkg))
	if len(meta.Exports) > 0 {
		parts = append(parts, fmt.Sprintf("导出: %s", strings.Join(meta.Exports, ", ")))
	}
	if len(meta.Imports) > 0 {
		imports := meta.Imports
		if len(imports) > 5 {
			imports = imports[:5]
		}
		parts = append(parts, fmt.Sprintf("导入: %s", strings.Join(imports, ", ")))
	}
	meta.Summary = strings.Join(parts, " | ")

	return meta
}

func parseModMeta(content string) (module, goVersion string, deps []string) {
	lines := strings.Split(content, "\n")
	inRequire := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			module = strings.TrimPrefix(line, "module ")
		}
		if strings.HasPrefix(line, "go ") {
			goVersion = strings.TrimPrefix(line, "go ")
		}
		if line == "require (" {
			inRequire = true
			continue
		}
		if inRequire && line == ")" {
			inRequire = false
			continue
		}
		if inRequire && !strings.Contains(line, "// indirect") {
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				deps = append(deps, parts[0])
			}
		}
	}
	return
}

// ── 失效 ──

// InvalidateFiles 标记指定文件缓存失效（write_file/batch_write 后调用）。
func InvalidateFiles(workspace string, paths []string) {
	m := LoadManifest(workspace)
	if m == nil {
		return
	}
	for _, p := range paths {
		delete(m.Files, p)
	}
	m.Save(workspace)
}

// ── 格式化输出 ──

// FormatOverview 生成注入 prompt 或工具返回的文本。
func (m *Manifest) FormatOverview() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("项目: %s", m.Module))
	if m.GoVersion != "" {
		sb.WriteString(fmt.Sprintf(" (go %s)", m.GoVersion))
	}
	sb.WriteString(fmt.Sprintf(" | 文件数: %d | 缓存时间: %s\n\n",
		len(m.Files), time.Unix(m.ScannedAt, 0).Format("15:04:05")))

	// 文件树
	sb.WriteString("文件树:\n")
	sb.WriteString(m.FileTree)
	sb.WriteString("\n")

	// 依赖
	if len(m.Deps) > 0 {
		deps := m.Deps
		if len(deps) > 8 {
			deps = deps[:8]
		}
		sb.WriteString(fmt.Sprintf("\n直接依赖: %s\n", strings.Join(deps, ", ")))
	}

	// 每个文件的摘要
	sb.WriteString("\n文件摘要:\n")
	// 排序输出
	var paths []string
	for p := range m.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		meta := m.Files[p]
		sb.WriteString(fmt.Sprintf("  %s → %s\n", p, meta.Summary))
	}

	return sb.String()
}

// FormatFileList 生成紧凑的文件摘要列表（用于 project_file 工具返回）。
func (m *Manifest) FormatFileList(paths []string) string {
	var sb strings.Builder
	found := 0
	for _, raw := range paths {
		p := filepath.ToSlash(raw)
		if meta, ok := m.Files[p]; ok {
			sb.WriteString(fmt.Sprintf("%s: %s\n", p, meta.Summary))
			found++
		} else {
			sb.WriteString(fmt.Sprintf("%s: (未缓存)\n", p))
		}
	}
	if found == 0 {
		sb.WriteString("(没有匹配的缓存文件)\n")
	}
	sb.WriteString(fmt.Sprintf("\n共 %d/%d 个文件命中缓存。", found, len(paths)))
	return sb.String()
}
