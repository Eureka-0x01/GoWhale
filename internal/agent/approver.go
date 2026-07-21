package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Approver 负责在有副作用的操作执行前，向用户征求确认（审批门）。
// 支持「始终允许」：按作用域（目录/会话）记住授权，之后同作用域操作自动放行。
// 不直接读 stdin —— 由外部调用方（终端 goroutine 或 TUI）通过 SendDecision 注入决策。
type Approver struct {
	decisions    chan ApprovalReply // 外部注入决策
	approvedDirs []string           // 已授权的目录（前缀匹配，含子目录）
	approvedSess map[string]bool    // 已授权的会话级作用域（如 shell）
}

// NewApprover 创建审批器。
func NewApprover() *Approver {
	return &Approver{
		decisions:    make(chan ApprovalReply, 1),
		approvedSess: map[string]bool{},
	}
}

// SendDecision 外部向审批器发送一个决策。非阻塞（buffer=1）。
func (a *Approver) SendDecision(reply ApprovalReply) {
	select {
	case a.decisions <- reply:
	default:
	}
}

// Ask 根据审批决策决定是否放行。
// 如果已通过「始终允许」记忆放行，直接返回 true 不等待。
// 否则阻塞等待外部通过 SendDecision 注入决策。
func (a *Approver) Ask(name string, warning string, scopeKind string, scope string) bool {
	// 检查记忆
	if warning == "" && a.isApproved(scopeKind, scope) {
		return true
	}

	// 阻塞等待外部决策
	reply := <-a.decisions
	if reply.Permanent && warning == "" {
		a.remember(scopeKind, scope)
	}
	return reply.Allowed
}

// isApproved 检查给定作用域是否已被「始终允许」记忆覆盖。
func (a *Approver) isApproved(scopeKind, scope string) bool {
	switch scopeKind {
	case "dir":
		for _, ad := range a.approvedDirs {
			if underDir(scope, ad) {
				return true
			}
		}
	case "session":
		return a.approvedSess[scope]
	}
	return false
}

// remember 将当前作用域加入「始终允许」记忆。
func (a *Approver) remember(scopeKind, scope string) {
	switch scopeKind {
	case "dir":
		a.approvedDirs = append(a.approvedDirs, scope)
	case "session":
		a.approvedSess[scope] = true
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

// PrintPrompt 打印审批提示（供终端兼容模式使用）。
// 在事件驱动模式下，TUI 自行渲染审批界面，不使用此方法。
func PrintPrompt(name, warning string) string {
	if warning != "" {
		return fmt.Sprintf("\n   %s %s\n   %s\n",
			redC("⚠️ 高危："), warning,
			yellowC("是否允许？[y]是 / [N]否 "))
	}
	return fmt.Sprintf("\n   %s\n", yellowC("▶ [y]本次 / [a]始终允许 / [N]否 "))
}
