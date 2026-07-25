package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"gowhale/internal/agent"
)

// Run 启动 Bubble Tea TUI。
// initialTask 可选——非空时 TUI 启动后自动执行该任务。
func Run(ag agent.AgentInterface, initialTask string) error {
	m := NewModel(ag, initialTask)
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
