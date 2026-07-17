package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ---------- 项目验证 ----------

type VerifyTool struct{}

func (VerifyTool) Name() string                    { return "verify_project" }
func (VerifyTool) Review(json.RawMessage) Decision  { return Decision{} }

func (VerifyTool) Description() string {
	return "编译/语法检查项目。自动检测项目类型(Go/Maven/npm/Python)并运行对应检查。" +
		"输出编译错误和可能的原因、修复建议。"
}

func (VerifyTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要验证的项目目录，默认当前目录 '.'",
			},
		},
	}
}

func (VerifyTool) Execute(args json.RawMessage) (string, error) {
	var p struct{ Path string `json:"path"` }
	json.Unmarshal(args, &p)
	if p.Path == "" {
		p.Path = workspace
	}
	if err := CheckPath(p.Path); err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString(fmt.Sprintf("验证项目: %s\n\n", p.Path))

	// 检测项目类型并运行检查
	checks := detectChecks(p.Path)
	if len(checks) == 0 {
		out.WriteString("未检测到已知的项目类型（无 go.mod/pom.xml/package.json/setup.py）。\n")
		out.WriteString("仅检查了 Python 文件语法。\n")
		checks = append(checks, checkPythonSyntax(p.Path))
	}

	allPassed := true
	for _, ck := range checks {
		out.WriteString(ck.label + "\n")
		if ck.err != nil {
			out.WriteString(fmt.Sprintf("  ✗ 错误: %v\n", ck.err))
			allPassed = false
		} else if ck.output != "" {
			out.WriteString(ck.output + "\n")
		}
		if ck.warnings != "" {
			out.WriteString(fmt.Sprintf("  ⚠ %s\n", ck.warnings))
			allPassed = false
		}
	}

	if allPassed {
		out.WriteString("\n✓ 全部验证通过\n")
	} else {
		out.WriteString("\n✗ 验证发现错误。请根据上面的错误信息修复：\n")
		out.WriteString("  1. 仔细读错误输出——定位到具体文件和行号\n")
		out.WriteString("  2. 不要重试同样的写法——根据错误信息修改代码\n")
		out.WriteString("  3. 修改后用 verify_project 重新验证\n")
	}
	return out.String(), nil
}

type checkResult struct {
	label    string
	output   string
	warnings string
	err      error
}

func detectChecks(dir string) []checkResult {
	var checks []checkResult

	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		checks = append(checks, runCmd(dir, "go", "build", "./..."))
		checks = append(checks, runCmd(dir, "go", "vet", "./..."))
		return checks
	}
	if _, err := os.Stat(filepath.Join(dir, "pom.xml")); err == nil {
		checks = append(checks, runCmd(dir, "mvn", "compile", "-q"))
		return checks
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		if _, err := os.Stat(filepath.Join(dir, "node_modules")); err == nil {
			checks = append(checks, runCmd(dir, "npm", "run", "build", "--if-present"))
		} else {
			checks = append(checks, checkResult{label: "npm build", warnings: "node_modules 不存在，先执行 npm install"})
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		checks = append(checks, runCmd(dir, "cargo", "check"))
	}

	checks = append(checks, checkPythonSyntax(dir))
	return checks
}

func checkPythonSyntax(dir string) checkResult {
	files, _ := filepath.Glob(filepath.Join(dir, "**", "*.py"))
	if len(files) == 0 {
		return checkResult{label: "Python 语法", output: "  (无 .py 文件)"}
	}
	errs := 0
	for _, f := range files {
		cmd := exec.Command("python3", "-c", "compile(open('"+f+"').read(), '"+f+"', 'exec')")
		if _, e := exec.LookPath("python3"); e != nil {
			cmd = exec.Command("python", "-c", "compile(open('"+f+"').read(), '"+f+"', 'exec')")
		}
		if out, e := cmd.CombinedOutput(); e != nil {
			errs++
			if errs <= 3 {
				return checkResult{
					label:  "Python 语法",
					err:    fmt.Errorf("%s: %s", f, strings.TrimSpace(string(out))),
				}
			}
		}
	}
	if errs > 0 {
		return checkResult{label: "Python 语法", warnings: fmt.Sprintf("%d/%d 文件有语法错误", errs, len(files))}
	}
	return checkResult{label: "Python 语法", output: fmt.Sprintf("  ✓ %d 文件通过", len(files))}
}

func runCmd(dir, name string, args ...string) checkResult {
	label := fmt.Sprintf("%s %s", name, strings.Join(args, " "))
	if _, err := exec.LookPath(name); err != nil {
		return checkResult{label: label, warnings: fmt.Sprintf("%s 未安装，跳过", name)}
	}
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(decodeOutput(out))
	if err != nil {
		return checkResult{label: label, err: fmt.Errorf("%v\n%s", err, result)}
	}
	if result != "" {
		return checkResult{label: label, output: "  ✓ 通过\n  " + truncateLines(result, 3)}
	}
	return checkResult{label: label, output: "  ✓ 通过"}
}

func truncateLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
		lines = append(lines, fmt.Sprintf("  ...(%d 行省略)", len(strings.Split(s, "\n"))-n))
	}
	return strings.Join(lines, "\n  ")
}

// ---------- 快照回滚 ----------

type RestoreTool struct{}

func (RestoreTool) Name() string                    { return "restore_snapshot" }
func (RestoreTool) Review(json.RawMessage) Decision  { return Decision{NeedApproval: true, ScopeKind: "session", Scope: "restore"} }

func (RestoreTool) Description() string {
	return "查看或回滚文件修改。不带参数列出所有快照；带 file 和 timestamp 参数回滚到指定版本。"
}

func (RestoreTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "list（查看快照列表）或 restore（回滚指定文件）",
			},
			"file": map[string]any{
				"type":        "string",
				"description": "要回滚的文件路径（action=restore 时必填）",
			},
			"timestamp": map[string]any{
				"type":        "string",
				"description": "快照时间戳，如 20260101_120000。不填则用最新的",
			},
		},
		"required": []string{"action"},
	}
}

func (RestoreTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Action    string `json:"action"`
		File      string `json:"file"`
		Timestamp string `json:"timestamp"`
	}
	json.Unmarshal(args, &p)

	snapDir := filepath.Join(workspace, ".aicode", "snapshots")
	idxFile := filepath.Join(snapDir, "index.log")

	if p.Action == "list" || p.Action == "" {
		data, err := os.ReadFile(idxFile)
		if err != nil {
			return "(无快照记录——尚未修改任何文件)", nil
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) > 20 {
			lines = lines[len(lines)-20:]
		}
		return "快照记录（按时间倒序）:\n" + strings.Join(lines, "\n"), nil
	}

	if p.Action == "restore" {
		if p.File == "" {
			return "", fmt.Errorf("restore 操作需要指定 file 参数")
		}
		// 找到匹配的快照文件
		abs, _ := filepath.Abs(p.File)
		safeName := strings.ReplaceAll(strings.TrimPrefix(abs, workspace+string(filepath.Separator)), string(filepath.Separator), "_")

		entries, _ := os.ReadDir(snapDir)
		var matches []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.Contains(e.Name(), safeName) {
				matches = append(matches, e.Name())
			}
		}
		if len(matches) == 0 {
			return "", fmt.Errorf("未找到 %s 的快照", p.File)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(matches)))
		match := matches[0] // 最新

		data, err := os.ReadFile(filepath.Join(snapDir, match))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return "", err
		}
		return fmt.Sprintf("✓ 已从快照 %s 恢复 %s（%d 字节）", match[:15], p.File, len(data)), nil
	}

	return "", fmt.Errorf("未知 action: %s（应为 list 或 restore）", p.Action)
}

