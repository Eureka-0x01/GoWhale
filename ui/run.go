package ui

import (
	"gowhale/internal/agent"
)

// Run 启动 tview TUI。
// initialTask 可选——非空时 TUI 启动后自动执行该任务。
func Run(ag agent.AgentInterface, initialTask string) error {
	m := NewModel(ag, initialTask)
	return m.Build().Run()
}
