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

// version 当前版本号，初始 0.1，后续手动升级。
const version = "0.2"

// slashCommands 所有 / 命令及其描述（用于下拉和建议）
var slashCommands = []prompt.Suggest{
	{Text: "/help", Description: "帮助信息"},
	{Text: "/model", Description: "查看当前模型"},
	{Text: "/clear", Description: "清空对话历史"},
	{Text: "/clear-key", Description: "清除已保存的 API Key"},
	{Text: "/history", Description: "查看最近对话记录"},
	{Text: "/compact", Description: "压缩上下文节省 token"},
	{Text: "/ollama", Description: "切换使用 Ollama 本地模型"},
	{Text: "/deepseek", Description: "切换使用 DeepSeek 云端模型"},
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
	printHistory(ag)

	// 用 go-prompt 替代 bufio.Reader，支持 / 命令下拉 + 模糊搜索 + Tab/方向键选择 + 历史记录
	p := prompt.New(
		func(input string) {
			input = strings.TrimSpace(input)
			if input == "" {
				return
			}

			if strings.HasPrefix(input, "/") {
				exit := handleCommand(input, reader, ag)
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
		// 选中项：白字深灰底，清晰可见
		prompt.OptionSelectedSuggestionTextColor(prompt.White),
		prompt.OptionSelectedSuggestionBGColor(prompt.DarkGray),
		// 未选中项：白字黑底
		prompt.OptionSuggestionTextColor(prompt.White),
		prompt.OptionSuggestionBGColor(prompt.Black),
		prompt.OptionLivePrefix(func() (string, bool) { return "你 > ", true }),
		prompt.OptionCompletionWordSeparator(" "),
		// Down 键触发补全激活，允许方向键导航补全列表
		prompt.OptionCompletionOnDown(),
	)
	p.Run()
}

// completer 根据输入返回建议——空输入不弹出，/ 开头过滤。
func completer(d prompt.Document) []prompt.Suggest {
	text := d.TextBeforeCursor()
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "/") {
		return prompt.FilterHasPrefix(slashCommands, text, true)
	}
	return nil
}

// displayWidth 计算字符串在终端中的显示宽度（ASCII=1, CJK=2）。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r <= 0x7F {
			w++
		} else {
			w += 2
		}
	}
	return w
}

func printBanner(cfg config.Config) {
	provider := cfg.Provider
	if provider == "" {
		provider = "deepseek"
	}
	// 第一行：主标题 + 版本号右对齐
	line := fmt.Sprintf("GoWhale — AI 编程助手 [%s]  %s / %s", provider, cfg.Model, cfg.ProModel)
	verTag := fmt.Sprintf("  v%s", version)
	width := 72
	pad := width - displayWidth(line)
	if pad < len(verTag) {
		pad = len(verTag)
	}
	fmt.Printf("%s%*s\n", line, pad, verTag)
	fmt.Println(strings.Repeat("─", width))
	fmt.Println("输入任务开始。输入 / 查看命令（Tab/方向键选择，Enter 执行）。")
	fmt.Println()
}

func handleCommand(input string, in *bufio.Reader, ag *agent.Agent) bool {
	cmd := strings.ToLower(strings.TrimSpace(input))
	switch cmd {
	case "/help":
		fmt.Println("\n命令列表（/ 下拉也可查看）：")
		for _, s := range slashCommands {
			fmt.Printf("  %-14s %s\n", s.Text, s.Description)
		}
		fmt.Printf("\n当前上下文用量: %s token\n", llm.FormatTokens(ag.TokenCount()))
		fmt.Println("直接输入自然语言开始任务。复杂任务自动路由到 pro 模型。")

	case "/model":
		cfg := config.Load()
		provider := cfg.Provider
		if provider == "" {
			provider = "deepseek"
		}
		fmt.Printf("\n提供商: %s\n简单任务: %s\n复杂任务: %s\n\n", provider, cfg.Model, cfg.ProModel)

	case "/clear":
		fmt.Println("✓ 对话历史已清空。输入 /exit 退出后重新进即可完全重置。")

	case "/clear-key":
		clearAPIKey(in)

	case "/history":
		printHistory(ag)

	case "/compact":
		before := ag.TokenCount()
		ag.Compact()
		after := ag.TokenCount()
		fmt.Printf("  节省: %s → %s token\n", llm.FormatTokens(before), llm.FormatTokens(after))

	case "/ollama":
		ollamaURL := os.Getenv("AICODE_OLLAMA_URL")
		ollamaModel := os.Getenv("AICODE_OLLAMA_MODEL")
		if ollamaURL == "" || ollamaModel == "" {
			ollamaURL, ollamaModel = config.PromptOllama(in)
			if ollamaModel == "" {
				break
			}
		}
		ag.SwitchProvider(ollamaURL, "ollama", ollamaModel, ollamaModel)
		config.SaveProvider("ollama")
		fmt.Printf("✓ 已切换到 Ollama (%s)\n", ollamaModel)

	case "/deepseek":
		cfg2 := config.Load()
		ag.SwitchProvider(
			cfg2.BaseURL,
			cfg2.APIKey,
			cfg2.Model,
			cfg2.ProModel,
		)
		config.SaveProvider("deepseek")
		fmt.Println("✓ 已切换到 DeepSeek")

	case "/exit", "/quit":
		return true

	default:
		fmt.Printf("未知命令: %s\n", cmd)
	}
	return false
}

func printHistory(ag *agent.Agent) {
	tasks := ag.LastTasks(3)
	if len(tasks) == 0 {
		return
	}
	fmt.Println()
	fmt.Println(strings.Repeat("─", 48))
	fmt.Println("最近对话：")
	for _, t := range tasks {
		fmt.Printf("  %s\n", t.Task)
		for _, r := range t.Replies {
			r = strings.TrimSpace(r)
			if len(r) > 80 {
				r = r[:80] + "…"
			}
			fmt.Printf("    ↳ %s\n", r)
		}
	}
	fmt.Println(strings.Repeat("─", 48))
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
