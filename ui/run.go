package ui

import (
	"os"

	"golang.org/x/term"

	"gowhale/internal/agent"
	"gowhale/internal/llm"
	"gowhale/internal/tools"
)

// Run 启动 tview TUI。
// initialTask 可选——非空时 TUI 启动后自动执行该任务。
func Run(ag agent.AgentInterface, initialTask string) error {
	// 保存终端状态，确保退出时恢复。
	fd := int(os.Stdin.Fd())
	oldState, err := term.GetState(fd)
	if err == nil {
		defer term.Restore(fd, oldState)
	}

	m := NewModel(ag, initialTask)
	return m.Build().Run()
}

// RunWithChatRoom 启动 TUI 并注册 ChatRoom 模式（供 Ctrl+M 或 /chatroom 切换）。
func RunWithChatRoom(ag agent.AgentInterface, client *llm.Client, registry *tools.Registry, approver *agent.Approver, workspace, fastModel, proModel string, initialTask string) error {
	fd := int(os.Stdin.Fd())
	oldState, err := term.GetState(fd)
	if err == nil {
		defer term.Restore(fd, oldState)
	}

	m := NewModel(ag, initialTask)
	m.InitChatRoom(client, registry, approver, workspace, fastModel, proModel)
	return m.Build().Run()
}
