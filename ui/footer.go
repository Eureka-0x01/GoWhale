package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"gowhale/internal/llm"
)

// Footer 底部状态栏。
type Footer struct {
	message    string
	tokenCount int
}

func NewFooter() Footer {
	return Footer{}
}

func (f *Footer) SetWorking(msg string) {
	f.message = msg
}

func (f *Footer) SetTokens(n int) {
	f.tokenCount = n
}

func (f *Footer) View(width int) string {
	left := f.message
	right := ""
	if f.tokenCount > 0 {
		right = fmt.Sprintf("[📊 %s token]", llm.FormatTokens(f.tokenCount))
	}
	return lipgloss.NewStyle().
		Width(width).
		Background(lipgloss.Color("8")).
		Foreground(lipgloss.Color("7")).
		Padding(1, 1).
		Render(lipgloss.JoinHorizontal(lipgloss.Left, left, right))
}
