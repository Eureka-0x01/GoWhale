package agent

import (
	"bufio"
	"fmt"
	"path/filepath"
	"strings"

	"gowhale/internal/tools"
)

// Approver 负责在有副作用的操作执行前，向用户征求确认（审批门）。
// 支持「始终允许」：按作用域（目录/会话）记住授权，之后同作用域操作自动放行。
// 与主交互循环共用同一个 stdin reader，避免缓冲冲突。
type Approver struct {
	in           *bufio.Reader
	approvedDirs []string        // 已授权的目录（前缀匹配，含子目录）
	approvedSess map[string]bool // 已授权的会话级作用域（如 shell）
}

func NewApprover(in *bufio.Reader) *Approver {
	return &Approver{in: in, approvedSess: map[string]bool{}}
}

// Ask 根据审批决策决定是否放行。
// 调用前已打印工具标签（label），本函数在**新行**接上审批询问。
func (a *Approver) Ask(name string, d tools.Decision) bool {
	if d.Danger == "" && a.isApproved(d) {
		fmt.Printf("\n   %s\n", greenC("→ ✓ 已授权自动放行"))
		return true
	}

	if d.Danger != "" {
		fmt.Printf("\n   %s %s\n", redC("⚠️ 高危："), d.Danger)
		fmt.Printf("   %s", yellowC("是否允许？[y]是 / [N]否 "))
	} else {
		fmt.Printf("\n   %s", yellowC("▶ [y]本次 / [a]始终允许 / [N]否 "))
	}

	line, err := readLine(a.in)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "a", "all":
		if d.Danger != "" {
			return false
		}
		a.remember(d)
		return true
	default:
		return false
	}
}

// readLine 从 bufio.Reader 读取一行。
// 兼容 go-prompt 设置的 Windows 终端 raw mode：
// 在 raw mode 下 Enter 只发送 \r，不是 \n，所以不能用 ReadString('\n')。
// 逐字节读取，遇到 \r 或 \n 结束，同时处理 \r\n 组合。
func readLine(r *bufio.Reader) (string, error) {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '\r' {
			// Windows raw mode: Enter 发 \r
			// 尝试吃掉紧跟的 \n（cooked mode 下 Enter 发 \r\n）
			next, err := r.ReadByte()
			if err != nil || next != '\n' {
				if err == nil {
					r.UnreadByte()
				}
			}
			break
		}
		if b == '\n' {
			break
		}
		buf = append(buf, b)
	}
	return string(buf), nil
}

func (a *Approver) isApproved(d tools.Decision) bool {
	switch d.ScopeKind {
	case "dir":
		for _, ad := range a.approvedDirs {
			if underDir(d.Scope, ad) {
				return true
			}
		}
	case "session":
		return a.approvedSess[d.Scope]
	}
	return false
}

func (a *Approver) remember(d tools.Decision) {
	switch d.ScopeKind {
	case "dir":
		a.approvedDirs = append(a.approvedDirs, d.Scope)
		fmt.Printf("   %s 目录 %s 及子目录\n", greenC("✓ 已记住"), dimC(d.Scope))
	case "session":
		a.approvedSess[d.Scope] = true
		fmt.Printf("   %s\n", greenC("✓ 已记住会话"))
	}
}

// underDir 判断 target 是否等于 base 或位于 base 之下。
func underDir(target, base string) bool {
	if target == base {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(base, sep) {
		base += sep
	}
	return strings.HasPrefix(target, base)
}
