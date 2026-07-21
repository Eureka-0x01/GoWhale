package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// maxOutput 限制返回给模型的输出长度，避免刷屏和撑爆上下文。
const maxOutput = 4000

// ShellTool 执行 shell 命令。是否放行由审批门决定（见 Review）。
type ShellTool struct{}

func (ShellTool) Name() string { return "execute_shell" }

func (ShellTool) Description() string {
	return "执行 shell 命令。Linux/macOS 用 sh，Windows 用 cmd。执行前需审批。长期服务设 background=true。"
}

// Review 让 execute_shell 走审批门：始终需要确认，并对危险命令给出警告原因。
// 作用域为会话级——用户「始终允许」后，本次会话内的非危险命令自动放行。
func (ShellTool) Review(args json.RawMessage) Decision {
	var p struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(args, &p)
	return Decision{
		NeedApproval: true,
		Danger:       checkDanger(p.Command),
		ScopeKind:    "session",
		Scope:        "shell",
	}
}

func (ShellTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "要执行的命令。根据上方运行环境中的操作系统和 shell 类型，使用对应的命令语法。" +
					"Windows cmd 语法：dir, type, findstr, del, move, copy, echo %VAR%。" +
					"Linux sh 语法：ls, cat, grep, rm, mv, cp, echo $VAR。" +
					"长期服务用 background=true。",
			},
			"background": map[string]any{
				"type":        "boolean",
				"description": "设为 true 时命令在后台运行，立即返回（用于启动长期运行的服务如 Web 服务器）。默认 false。",
			},
		},
		"required": []string{"command"},
	}
}

func (ShellTool) Execute(args json.RawMessage) (string, error) {
	var p struct {
		Command    string `json:"command"`
		Background bool   `json:"background"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if strings.TrimSpace(p.Command) == "" {
		return "", errors.New("command 不能为空")
	}
	if err := CheckShell(p.Command); err != nil {
		return "", err
	}

	// 后台模式：启动不等待，立即返回 PID（用于服务）
	if p.Background {
		return runBackground(p.Command), nil
	}

	// 统一用 sh -c 执行（Windows 下用 cmd /c）。
	name, flag := "sh", "-c"
	if runtime.GOOS == "windows" {
		name, flag = "cmd", "/c"
	}

	ctxCmd := exec.Command(name, flag, p.Command)
	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() {
		out, runErr = ctxCmd.CombinedOutput()
		close(done)
	}()

	// 等待命令完成或超时（60 秒）。
	// 进度反馈由 Agent 层的 spinner 统一管理，这里只做控制。
	timeout := time.After(60 * time.Second)
	select {
	case <-done:
		// 正常完成
	case <-timeout:
		_ = ctxCmd.Process.Kill()
		return "", fmt.Errorf("命令执行超时（60 秒）。→ 不要重试同样的命令。如果是长期服务请用 background=true；如果是命令卡住请换替代方案")
	}

	result := strings.TrimSpace(decodeOutput(out))
	if len(result) > maxOutput {
		result = result[:maxOutput] + "\n...(输出过长已截断)"
	}
	if runErr != nil {
		if result == "" {
			return "", fmt.Errorf("命令执行失败: %w。→ 不要重试此命令。请检查命令拼写、依赖是否安装、路径是否正确，然后换一种不同的方式", runErr)
		}
		return fmt.Sprintf("命令返回非零退出码（%v）。输出：\n%s\n→ 不要重试此命令。请根据上面的错误输出，换一种完全不同的方式解决问题", runErr, result), nil
	}
	if result == "" {
		result = "(命令执行成功，无输出)"
	}
	return result, nil
}

// runBackground 在后台启动命令，立即返回，不等待完成。
// 确保从工作区目录启动，这样 mvn/npm 等命令能找到配置文件。
func runBackground(cmdStr string) string {
	if runtime.GOOS == "windows" {
		c := exec.Command("cmd", "/c", "start", "/B", "cmd", "/c", cmdStr)
		c.Dir = workspace // 确保从工作区启动
		if err := c.Start(); err != nil {
			return fmt.Sprintf("后台启动失败: %v", err)
		}
		return fmt.Sprintf("命令已在后台启动。用 netstat/curl 验证服务是否就绪。\n命令: %s", cmdStr)
	}
	c := exec.Command("sh", "-c", cmdStr+" &")
	c.Dir = workspace
	if err := c.Start(); err != nil {
		return fmt.Sprintf("后台启动失败: %v", err)
	}
	return fmt.Sprintf("命令已在后台启动（PID %d）。用 curl/netstat 验证服务是否就绪。\n命令: %s", c.Process.Pid, cmdStr)
}

// decodeOutput 把子进程输出转成 UTF-8。
// Windows 中文控制台（javac、cmd 等）输出多为 GBK/GB18030 编码，
// 直接当 UTF-8 会乱码。策略：已是合法 UTF-8 就原样返回；否则按 GB18030 解码。
func decodeOutput(b []byte) string {
	if len(b) == 0 || utf8.Valid(b) {
		return string(b)
	}
	if dec, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b); err == nil && utf8.Valid(dec) {
		return string(dec)
	}
	return string(b) // 兜底：无法识别则原样返回
}
