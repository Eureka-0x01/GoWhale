package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"gowhale/internal/agent"
	"gowhale/internal/llm"
)

// StatusBar 顶部状态栏。
type StatusBar struct {
	agent *agent.Agent
}

func NewStatusBar(ag *agent.Agent) StatusBar {
	return StatusBar{agent: ag}
}

func (s *StatusBar) View() string {
	model := s.agent.ModelName()
	tokens := s.agent.TokenCount()
	return lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Padding(0, 1).
		Render(fmt.Sprintf(" GoWhale  %s  │  %s token",
			model, llm.FormatTokens(tokens)))
}
