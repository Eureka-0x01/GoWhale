package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/awesome-gocui/gocui"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// ChatUI gocui 聊天界面。
type ChatUI struct {
	g      *gocui.Gui
	runner runner.Runner
	ctx    context.Context
	userID string
	sessID string
}

// New 创建聊天界面。
func New(g *gocui.Gui, r runner.Runner) *ChatUI {
	return &ChatUI{
		g:      g,
		runner: r,
		ctx:    context.Background(),
		userID: "user-1",
		sessID: "session-1",
	}
}

// Run 启动主循环。
func (c *ChatUI) Run() error {
	c.g.SetManagerFunc(c.layout)
	c.g.SetKeybinding("", gocui.KeyCtrlC, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		return gocui.ErrQuit
	})
	return c.g.MainLoop()
}

func (c *ChatUI) layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()

	msgView, err := g.SetView("messages", 0, 0, maxX-1, maxY-4, 0)
	if err != nil && err != gocui.ErrUnknownView {
		return err
	}
	msgView.Title = " 对话 "
	msgView.Autoscroll = true
	msgView.Wrap = true
	msgView.Editable = false

	inputView, err := g.SetView("input", 0, maxY-3, maxX-1, maxY-1, 0)
	if err != nil && err != gocui.ErrUnknownView {
		return err
	}
	inputView.Title = " 输入（Enter 发送，Ctrl+C 退出）"
	inputView.Editable = true
	inputView.Wrap = true

	if _, err := g.SetCurrentView("input"); err != nil {
		return err
	}

	g.SetKeybinding("input", gocui.KeyEnter, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		text := strings.TrimSpace(v.Buffer())
		if text == "" {
			return nil
		}
		go c.send(text)
		v.Clear()
		v.SetCursor(0, 0)
		return nil
	})

	return nil
}

func (c *ChatUI) send(text string) {
	c.appendMsg("messages", fmt.Sprintf("\n[你] %s\n", text))

	msg := model.NewUserMessage(text)
	events, err := c.runner.Run(c.ctx, c.userID, c.sessID, msg)
	if err != nil {
		c.appendMsg("messages", fmt.Sprintf("[错误] %v", err))
		return
	}

	var response strings.Builder
	for ev := range events {
		if ev.Error != nil {
			c.appendMsg("messages", fmt.Sprintf("[错误] %s", ev.Error.Message))
			break
		}
		if ev.Response == nil {
			continue
		}

		// 工具调用
		for _, choice := range ev.Response.Choices {
			for _, tc := range choice.Message.ToolCalls {
				c.appendMsg("messages", fmt.Sprintf("🔧 %s %s", tc.Function.Name, truncate(string(tc.Function.Arguments), 120)))
			}
			// 工具结果
			if ev.Response.Object == model.ObjectTypeToolResponse {
				content := choice.Message.Content
				c.appendMsg("messages", fmt.Sprintf("  → %s", truncate(content, 200)))
			}
			// 流式文本
			if choice.Delta.Content != "" {
				response.WriteString(choice.Delta.Content)
			}
			// 完整消息
			if choice.Message.Content != "" && choice.Message.Role == model.RoleAssistant {
				response.WriteString(choice.Message.Content)
			}
		}
	}

	// 输出最终回复
	if final := strings.TrimSpace(response.String()); final != "" {
		c.appendMsg("messages", fmt.Sprintf("[AI] %s\n", final))
	}
}

func (c *ChatUI) appendMsg(viewName, text string) {
	c.g.Update(func(g *gocui.Gui) error {
		v, err := g.View(viewName)
		if err != nil {
			return err
		}
		fmt.Fprintln(v, text)
		return nil
	})
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
