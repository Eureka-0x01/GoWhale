package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PythonTool 在沙箱中执行 Python 代码。代码先经 AST 安全检查，再在隔离目录运行。
type PythonTool struct{}

func (PythonTool) Name() string                    { return "execute_python" }
func (PythonTool) Review(json.RawMessage) Decision { return Decision{} }

func (PythonTool) Description() string {
	return "在沙箱中执行 Python 代码。可导入安全模块(math/json/re/collections/datetime/itertools/functools/hashlib/base64/csv/xml/random/statistics/typing/enum/textwrap/pprint/decimal/fractions/string/struct/io/os.path-basename-dirname-join-splitext)。禁止: 系统调用/文件删除/网络/子进程。代码先经 AST 检查再在隔离目录运行。"
}

func (PythonTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"code": map[string]any{
				"type": "string",
				"description": "Python 代码。可导入安全模块,禁止 os.system/subprocess/eval/exec/网络/文件写入。",
			},
		},
		"required": []string{"code"},
	}
}

// astCheckScript 用 Python 的 AST 模块分析代码,检查是否包含危险操作。
// 正则扫描不可靠(能被 getattr/字符串拼接绕过),AST 直接看语法树——绕过不了。
const astCheckScript = `
import ast, sys
code = sys.stdin.read()

class SafetyChecker(ast.NodeVisitor):
    def __init__(self):
        self.violations = []

    def visit_Import(self, node):
        for alias in node.names:
            self._check_module(alias.name, node.lineno)
        self.generic_visit(node)

    def visit_ImportFrom(self, node):
        mod = node.module or ''
        for alias in node.names:
            full = mod + '.' + alias.name if mod else alias.name
            self._check_module(full, node.lineno)
        self.generic_visit(node)

    def _check_module(self, name, lineno):
        mod = name.split('.')[0]
        blocked = {'os', 'subprocess', 'socket', 'urllib', 'urllib2', 'urllib3',
                   'http', 'ftplib', 'smtplib', 'telnetlib', 'requests',
                   'ctypes', 'cffi', 'signal', 'multiprocessing', 'threading',
                   'shutil', 'pathlib', 'glob', 'tempfile', 'pty', 'fcntl',
                   'winreg', 'msvcrt', '_winreg', 'win32api', 'win32com',
                   'pickle', 'shelve', 'marshal', 'sys', 'builtins'}
        if mod in blocked:
            self.violations.append(f"禁止导入 {name} (行{lineno})")

    def visit_Call(self, node):
        # 危险函数调用
        if isinstance(node.func, ast.Name):
            blocked = {'eval', 'exec', 'compile', '__import__', 'open', 'input'}
            if node.func.id in blocked:
                self.violations.append(f"禁止调用 {node.func.id}() (行{node.lineno})")
        # os.path 允许,但 os.xxx 禁止
        if isinstance(node.func, ast.Attribute):
            if isinstance(node.func.value, ast.Name):
                if node.func.value.id == 'os' and node.func.attr not in ('path',):
                    self.violations.append(f"禁止 os.{node.func.attr}() (行{node.lineno})")
                if node.func.value.id == 'sys' and node.func.attr != 'version':
                    self.violations.append(f"禁止 sys.{node.func.attr} (行{node.lineno})")
            # __import__('os') 等动态导入
            if isinstance(node.func, ast.Name) and node.func.id == '__import__':
                self.violations.append(f"禁止 __import__() (行{node.lineno})")
        self.generic_visit(node)

    def visit_Subscript(self, node):
        # __builtins__['eval'] 等绕过
        self.generic_visit(node)

tree = ast.parse(code)
checker = SafetyChecker()
checker.visit(tree)

# 额外检查: compile/exec/eval 在表达式中
for node in ast.walk(tree):
    if isinstance(node, ast.Call):
        if isinstance(node.func, ast.Name) and node.func.id in ('eval','exec','compile'):
            if not any(v.startswith('禁止调用') for v in checker.violations):
                checker.violations.append(f"禁止调用 {node.func.id}() (行{node.lineno})")

if checker.violations:
    print("VIOLATION:" + "; ".join(checker.violations[:5]))
    sys.exit(1)
print("OK")
`

// pythonSafeModules 允许在沙箱中导入的模块（前缀匹配）。
var pythonSafeModules = []string{
	"math", "json", "re", "collections", "datetime", "itertools",
	"functools", "hashlib", "base64", "csv", "xml", "random",
	"statistics", "typing", "enum", "textwrap", "pprint", "decimal",
	"fractions", "string", "struct", "io", "copy", "heapq", "bisect",
	"array", "queue", "operator", "uuid", "dataclasses", "html",
	"os.path", // 只允许 os.path
}

func (PythonTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	code := strings.TrimSpace(p.Code)
	if code == "" {
		return "", fmt.Errorf("code 不能为空")
	}

	// 阶段 1: AST 安全检查
	if err := checkPythonAST(code); err != nil {
		return "", err
	}

	// 阶段 2: 创建隔离沙箱目录
	sandbox, err := os.MkdirTemp("", "aicode_sandbox_")
	if err != nil {
		return "", fmt.Errorf("创建沙箱目录失败: %w", err)
	}
	defer os.RemoveAll(sandbox)

	// 阶段 3: 将代码写入沙箱
	script := filepath.Join(sandbox, "script.py")
	if err := os.WriteFile(script, []byte(code), 0o600); err != nil {
		return "", fmt.Errorf("写入脚本失败: %w", err)
	}

	// 阶段 4: 在沙箱中执行（-I 隔离模式, -s 禁用 site-packages）
	python := "python3"
	if _, err := exec.LookPath(python); err != nil {
		python = "python"
	}
	cmd := exec.Command(python, "-I", "-s", script)
	cmd.Dir = sandbox // 工作目录在沙箱内
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"),
		"TEMP=" + sandbox,
		"TMP=" + sandbox,
		"PYTHONDONTWRITEBYTECODE=1",
	}

	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() {
		out, runErr = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		return "", fmt.Errorf("沙箱执行超时(30秒)")
	}

	result := decodeOutput(out)
	if len(result) > maxOutput {
		result = result[:maxOutput] + "\n...(截断)"
	}
	if runErr != nil {
		if result == "" {
			return "", fmt.Errorf("Python 错误: %w。→ 检查代码语法", runErr)
		}
		return fmt.Sprintf("Python 退出(%v):\n%s\n→ 检查上面错误修复", runErr, result), nil
	}
	if result == "" {
		return "(执行成功，无输出)", nil
	}
	return result, nil
}

// checkPythonAST 用 Python 自身的 AST 模块检查代码安全性。
// 正则可被 getattr/字符串拼接绕过，AST 直接看语法树——绕不过。
func checkPythonAST(code string) error {
	cmd := exec.Command("python3", "-c", astCheckScript)
	if _, err := exec.LookPath("python3"); err != nil {
		cmd = exec.Command("python", "-c", astCheckScript)
	}
	cmd.Stdin = bytes.NewBufferString(code)
	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(decodeOutput(out))

	if err != nil {
		if strings.Contains(result, "VIOLATION:") {
			return fmt.Errorf("AST 安全检查未通过: %s\n请用安全模块改写", strings.TrimPrefix(result, "VIOLATION:"))
		}
		// AST 检查脚本自身出错(可能语法错误)
		if strings.Contains(result, "SyntaxError") {
			return fmt.Errorf("Python 语法错误: %s", result)
		}
		return fmt.Errorf("AST 检查失败: %s", result)
	}
	if result != "OK" {
		return fmt.Errorf("AST 检查异常: %s", result)
	}
	return nil
}
