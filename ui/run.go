package ui

import (
	"os"

	"golang.org/x/term"

	"gowhale/internal/agent"
)

// Run 启动 tview TUI。
// initialTask 可选——非空时 TUI 启动后自动执行该任务。
func Run(ag agent.AgentInterface, initialTask string) error {
	// 保存终端状态，确保退出时恢复。
	// tview/tcell 在 Windows cmd.exe 上有时不能完整恢复控制台模式，
	// 导致退出后出现 ANSI 转义序列乱码。
	fd := int(os.Stdin.Fd())
	oldState, err := term.GetState(fd)
	if err == nil {
		defer term.Restore(fd, oldState)
	}

	m := NewModel(ag, initialTask)
	return m.Build().Run()
}
