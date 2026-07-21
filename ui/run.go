package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"gowhale/internal/agent"
)

// Run 启动 Bubble Tea TUI。
func Run(ag *agent.Agent) error {
	m := NewModel(ag)
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
