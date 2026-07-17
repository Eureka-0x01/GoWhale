package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/c-bata/go-prompt"

	"gowhale/internal/agent"
	"gowhale/internal/config"
	"gowhale/internal/llm"
	"gowhale/internal/tools"
)

// slashCommands 所有 / 命令及其描述（用于下拉和建议）
var slashCommands = []prompt.Suggest{
	{Text: "/help", Description: "帮助信息"},
	{Text: "/model", Description: "查看当前模型"},
	{Text: "/clear", Description: "清空对话历史"},
	{Text: "/clear-key", Description: "清除已保存的 API Key"},
	{Text: "/exit", Description: "退出程序"},
}

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--clear-key" || os.Args[1] == "-clear-key") {
		clearAPIKey(bufio.NewReader(os.Stdin))
		return
	}

	cfg := config.Load()
	client := llm.NewClient(cfg)
	registry := tools.New(
		tools.WritePlanTool{},
		tools.ShellTool{},
		tools.PythonTool{},
		tools.SearchTool{},
		tools.ReadFileTool{},
		tools.WriteFileTool{},
		tools.BatchWriteTool{},
		tools.VerifyTool{},
		tools.RestoreTool{},
		tools.ListDirTool{},
	)

	reader := bufio.NewReader(os.Stdin)
	approver := agent.NewApprover(reader)
	workspace, _ := os.Getwd()
	tools.SetWorkspace(workspace)
	ag := agent.New(client, registry, approver, cfg.MaxTurns, workspace, cfg.Model, cfg.ProModel)

	if len(os.Args) > 1 {
		ag.Run(strings.Join(os.Args[1:], " "))
		return
	}

	printBanner(cfg)

	// 用 go-prompt 替代 bufio.Reader，支持 / 命令下拉 + 模糊搜索 + 上下选择 + 历史记录
	p := prompt.New(
		func(input string) {
			input = strings.TrimSpace(input)
			if input == "" {
				return
			}

			if strings.HasPrefix(input, "/") {
				exit := handleCommand(input, reader)
				if exit {
					fmt.Println("再见！")
					os.Exit(0)
				}
				return
			}

			if input == "exit" || input == "quit" {
				fmt.Println("再见！")
				os.Exit(0)
			}

			fmt.Println() // 输入提交后换行
			ag.Run(input)
		},
		completer,
		prompt.OptionPrefix("你 > "),
		prompt.OptionHistory([]string{}),
		prompt.OptionPrefixTextColor(prompt.Cyan),
		prompt.OptionPreviewSuggestionTextColor(prompt.Blue),
		prompt.OptionSelectedSuggestionBGColor(prompt.DarkGray),
		prompt.OptionSuggestionBGColor(prompt.Black),
		prompt.OptionLivePrefix(func() (string, bool) { return "你 > ", true }),
		prompt.OptionCompletionWordSeparator(" "),
	)
	p.Run()
}

// completer 根据输入返回建议——空输入显示全部命令，/ 开头过滤。
func completer(d prompt.Document) []prompt.Suggest {
	text := d.TextBeforeCursor()
	if text == "" {
		return nil // 空输入不弹出
	}
	if strings.HasPrefix(text, "/") {
		return prompt.FilterHasPrefix(slashCommands, text, true)
	}
	return nil
}

func printBanner(cfg config.Config) {
	fmt.Printf("GoWhale — AI 编程助手（%s / %s）\n", cfg.Model, cfg.ProModel)
	fmt.Println(strings.Repeat("─", 48))
	fmt.Println("输入任务开始。输入 / 查看命令（支持模糊搜索 + 方向键选择）。")
	fmt.Println()
}

func handleCommand(input string, in *bufio.Reader) bool {
	cmd := strings.ToLower(strings.TrimSpace(input))
	switch cmd {
	case "/help":
		fmt.Println("\n命令列表（/ 下拉也可查看）：")
		for _, s := range slashCommands {
			fmt.Printf("  %-14s %s\n", s.Text, s.Description)
		}
		fmt.Println("\n直接输入自然语言开始任务。复杂任务自动路由到 pro 模型。")

	case "/model":
		cfg := config.Load()
		fmt.Printf("\n简单任务: %s\n复杂任务: %s\n\n", cfg.Model, cfg.ProModel)

	case "/clear":
		fmt.Println("✓ 对话历史已清空。输入 /exit 退出后重新进即可完全重置。")

	case "/clear-key":
		clearAPIKey(in)

	case "/exit", "/quit":
		return true

	default:
		fmt.Printf("未知命令: %s\n", cmd)
	}
	return false
}

func clearAPIKey(in *bufio.Reader) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误：无法获取用户目录")
		os.Exit(1)
	}
	path := filepath.Join(homeDir, ".gowhale", ".env")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Println("没有已保存的 API Key（~/.gowhale/.env 不存在）。")
		return
	}

	fmt.Print("确认要清除已保存的 API Key 吗？[y/N] ")
	line, _ := in.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "y" {
		fmt.Println("已取消。")
		return
	}

	if err := os.Remove(path); err != nil {
		fmt.Fprintf(os.Stderr, "清除失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ 已清除 ~/.gowhale/.env。下次运行 gowhale 时会提示输入新 Key。")
}
