package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"

	"gowhale/internal/memory"
)

// ChatUI tview 聊天界面。
type ChatUI struct {
	app    *tview.Application
	view   *tview.TextView
	runner runner.Runner
	memory *memory.Store
	ctx    context.Context
	userID string
	sessID string
	model  string
}

// New 创建聊天界面。
func New(r runner.Runner, mem *memory.Store, modelName string) *ChatUI {
	return &ChatUI{
		app:    tview.NewApplication(),
		view:   tview.NewTextView(),
		runner: r,
		memory: mem,
		ctx:    context.Background(),
		userID: "user-1",
		sessID: "session-1",
		model:  modelName,
	}
}

// Run 启动主循环。
func (c *ChatUI) Run() error {
	c.view.
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetBorder(true).
		SetTitle(" 对话 ")

	input := tview.NewInputField()
	input.SetLabel("> ")
	input.SetFieldWidth(0)
	input.SetBorder(true)
	input.SetTitle(" 输入（Enter 发送，Ctrl+C 退出）")

	input.SetDoneFunc(func(key tcell.Key) {
		if key != tcell.KeyEnter {
			return
		}
		text := strings.TrimSpace(input.GetText())
		if text == "" {
			return
		}
		input.SetText("")
		go c.send(text)
	})

	// 状态栏
	status := tview.NewTextView().
		SetDynamicColors(true).
		SetText(fmt.Sprintf("[white:blue] GoWhale [%s]  Ctrl+C 退出 ", c.model))

	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(status, 1, 0, false).
		AddItem(c.view, 0, 1, false).
		AddItem(input, 3, 0, true)

	c.app.SetRoot(layout, true).SetFocus(input)

	c.printHistory()

	if err := c.app.Run(); err != nil {
		return err
	}
	return nil
}

// printHistory 显示记忆中的最近对话。
func (c *ChatUI) printHistory() {
	if memText := c.memory.Format(); memText != "" {
		fmt.Fprintf(c.view, "%s\n", memText)
	}
}

func (c *ChatUI) send(text string) {
	c.memory.Add("user", text)
	c.write(fmt.Sprintf("\n[yellow::b][你] %s[white]\n", text))

	msg := model.NewUserMessage(text)
	events, err := c.runner.Run(c.ctx, c.userID, c.sessID, msg)
	if err != nil {
		c.write(fmt.Sprintf("\n[red][错误] %v[white]\n", err))
		return
	}

	var answer strings.Builder
	for ev := range events {
		if ev.Error != nil {
			c.write(fmt.Sprintf("\n[red][错误] %s[white]\n", ev.Error.Message))
			break
		}
		if ev.Response == nil {
			continue
		}

		for _, choice := range ev.Response.Choices {
			// 思考内容
			if choice.Delta.ReasoningContent != "" {
				c.write("[#888888]" + choice.Delta.ReasoningContent + "[white]")
			}
			if choice.Message.ReasoningContent != "" {
				c.write("[#888888]" + choice.Message.ReasoningContent + "[white]")
			}
			// 工具调用
			for _, tc := range choice.Message.ToolCalls {
				c.write(fmt.Sprintf("\n[cyan]🔧 %s %s[white]\n", tc.Function.Name, string(tc.Function.Arguments)))
			}
			// 工具结果
			if ev.Response.Object == model.ObjectTypeToolResponse {
				c.write(fmt.Sprintf("[#888888]  → %s[white]\n", choice.Message.Content))
			}
			// 文本
			if choice.Delta.Content != "" {
				answer.WriteString(choice.Delta.Content)
			}
			if choice.Message.Content != "" && choice.Message.Role == model.RoleAssistant {
				answer.WriteString(choice.Message.Content)
			}
		}
	}

	if final := strings.TrimSpace(answer.String()); final != "" {
		c.memory.Add("assistant", final)
		c.write(fmt.Sprintf("\n[green][AI] %s[white]\n", final))
	}
}

func (c *ChatUI) write(text string) {
	c.app.QueueUpdateDraw(func() {
		fmt.Fprint(c.view, text)
		c.view.ScrollToEnd()
	})
}
