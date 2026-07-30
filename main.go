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
	"gowhale/ui"
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
	{Text: "/chatroom", Description: "多角色协作（PM→Dev→QA→验收）"},
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

	approver := agent.NewApprover()
	workspace, _ := os.Getwd()
	tools.SetWorkspace(workspace)

	// ── 有参数 ──
	if len(os.Args) > 1 {
		// --classic → 旧 go-prompt 交互模式
		if os.Args[1] == "--classic" {
			ag := agent.New(client, registry, approver, cfg.MaxTurns, workspace, cfg.Model, cfg.ProModel)
			runClassic(ag, cfg, client, registry, approver, workspace)
			return
		}
		// --tui [task] → TUI 模式（可选附带初始任务）
		if os.Args[1] == "--tui" {
			task := ""
			if len(os.Args) > 2 {
				task = strings.Join(os.Args[2:], " ")
			}
			ag := agent.New(client, registry, approver, cfg.MaxTurns, workspace, cfg.Model, cfg.ProModel)
			if err := ui.Run(ag, task); err != nil {
				fmt.Fprintf(os.Stderr, "TUI 错误: %v\n", err)
				os.Exit(1)
			}
			return
		}
		// 一次性任务（自动检测是否需要多角色协作）
		task := strings.Join(os.Args[1:], " ")
		ag := selectAgent(task, client, cfg, registry, approver, workspace)
		ag.Run(task)
		return
	}

	// ── 无参数 → 默认 TUI 模式 ──
	ag := agent.New(client, registry, approver, cfg.MaxTurns, workspace, cfg.Model, cfg.ProModel)
	if err := ui.Run(ag, ""); err != nil {
		fmt.Fprintf(os.Stderr, "TUI 错误: %v\n", err)
		os.Exit(1)
	}
}

// selectAgent 根据任务复杂度选择 Agent 类型。
func selectAgent(task string, client *llm.Client, cfg config.Config, registry *tools.Registry, approver *agent.Approver, workspace string) agent.AgentInterface {
	if agent.ClassifyChatRoom(task, client) {
		fmt.Println("🔀 检测到复杂任务，启用多角色协作模式（产品经理→程序员→测试→用户代理）")
		return agent.NewChatRoom(client, registry, approver, workspace, cfg.Model, cfg.ProModel)
	}
	return agent.New(client, registry, approver, cfg.MaxTurns, workspace, cfg.Model, cfg.ProModel)
}

// runClassic 启动传统的 go-prompt 交互模式（通过 --classic 参数进入）。
func runClassic(ag agent.AgentInterface, cfg config.Config, client *llm.Client, registry *tools.Registry, approver *agent.Approver, workspace string) {
	printBanner(cfg)
	printHistory(ag)

	p := prompt.New(
		func(input string) {
			input = strings.TrimRight(input, "\r\n ")
			if input == "" {
				return
			}

			if strings.HasPrefix(input, "/") {
				exit := handleCommand(input, bufio.NewReader(os.Stdin), ag, client, cfg, registry, approver, workspace)
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

			fmt.Println()
			ag.Run(input)
		},
		completer,
		prompt.OptionPrefix("你 > "),
		prompt.OptionHistory([]string{}),
		prompt.OptionPrefixTextColor(prompt.Cyan),
		prompt.OptionPreviewSuggestionTextColor(prompt.Blue),
		prompt.OptionSelectedSuggestionTextColor(prompt.White),
		prompt.OptionSelectedSuggestionBGColor(prompt.DarkGray),
		prompt.OptionSuggestionTextColor(prompt.White),
		prompt.OptionSuggestionBGColor(prompt.Black),
		prompt.OptionLivePrefix(func() (string, bool) { return "你 > ", true }),
		prompt.OptionCompletionWordSeparator(" "),
		prompt.OptionCompletionOnDown(),
		// Shift+Enter (发送 \n / 0x0A) → 在输入中插入换行
		prompt.OptionAddASCIICodeBind(
			prompt.ASCIICodeBind{
				ASCIICode: []byte{0x0a},
				Fn: func(buf *prompt.Buffer) {
					buf.InsertText("\n", false, true)
				},
			},
		),
	)
	p.Run()
}

// completer 根据输入返回建议。
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

func dimC(s string) string {
	if s == "" { return "" }
	return "\033[2m" + s + "\033[0m"
}

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
	line := fmt.Sprintf("GoWhale — AI 编程助手 [%s]  %s / %s", provider, cfg.Model, cfg.ProModel)
	verTag := fmt.Sprintf("  v%s", version)
	width := 72
	pad := width - displayWidth(line)
	if pad < len(verTag) {
		pad = len(verTag)
	}
	fmt.Printf("%s%*s\n", line, pad, verTag)
	fmt.Println(strings.Repeat("─", width))
	fmt.Println("输入任务开始。输入 / 查看命令。默认使用 TUI 模式，用 --classic 参数可进入此模式。")
	fmt.Println()
}

func handleCommand(input string, in *bufio.Reader, ag agent.AgentInterface, client *llm.Client, cfg config.Config, registry *tools.Registry, approver *agent.Approver, workspace string) bool {
	cmd := strings.ToLower(strings.TrimSpace(input))
	switch cmd {
	case "/help":
		fmt.Println("\n命令列表（/ 下拉也可查看）：")
		for _, s := range slashCommands {
			fmt.Printf("  %-14s %s\n", s.Text, s.Description)
		}
		fmt.Printf("\n当前上下文用量: %s token\n", llm.FormatTokens(ag.TokenCount()))

	case "/model":
		cfg := config.Load()
		provider := cfg.Provider
		if provider == "" { provider = "deepseek" }
		fmt.Printf("\n提供商: %s\n简单任务: %s\n复杂任务: %s\n\n", provider, cfg.Model, cfg.ProModel)

	case "/clear":
		fmt.Println("✓ 对话历史已清空。输入 /exit 退出后重新进即可完全重置。")

	case "/clear-key":
		clearAPIKey(in)

	case "/history":
		printHistory(ag)

	case "/compact":
		before := ag.TokenCount()
		didCompact := ag.Compact()
		after := ag.TokenCount()
		if didCompact {
			fmt.Printf("  节省: %s → ~%s token\n", llm.FormatTokens(before), llm.FormatTokens(after))
		} else {
			fmt.Printf("  消息不足无需压缩（当前 %s token）\n", llm.FormatTokens(after))
		}

	case "/tui":
		fmt.Println("正在启动 TUI 模式...")
		if err := ui.Run(ag, ""); err != nil {
			fmt.Fprintf(os.Stderr, "TUI 错误: %v\n", err)
		}

	case "/chatroom":
		fmt.Print("\n请输入任务（将使用多角色协作模式）：")
		task, _ := in.ReadString('\n')
		task = strings.TrimSpace(task)
		if task == "" {
			fmt.Println("已取消。")
			break
		}
		fmt.Println("\n🔀 启动多角色协作（产品经理→程序员→测试→用户代理）...")
		cr := agent.NewChatRoom(client, registry, approver, workspace, cfg.Model, cfg.ProModel)
		cr.Run(task)

	case "/ollama":
		ollamaURL := os.Getenv("AICODE_OLLAMA_URL")
		ollamaModel := os.Getenv("AICODE_OLLAMA_MODEL")
		if ollamaURL == "" || ollamaModel == "" {
			ollamaURL, ollamaModel = config.PromptOllama(in)
			if ollamaModel == "" { break }
		}
		ag.SwitchProvider(ollamaURL, "ollama", ollamaModel, ollamaModel)
		config.SaveProvider("ollama")
		fmt.Printf("✓ 已切换到 Ollama (%s)\n", ollamaModel)

	case "/deepseek":
		config.SaveProvider("deepseek")
		cfg2 := config.Load()
		ag.SwitchProvider(cfg2.BaseURL, cfg2.APIKey, cfg2.Model, cfg2.ProModel)
		fmt.Println("✓ 已切换到 DeepSeek")

	case "/exit", "/quit":
		return true

	default:
		fmt.Printf("未知命令: %s\n", cmd)
	}
	return false
}

func printHistory(ag agent.AgentInterface) {
	tasks := ag.LastTasks(3)
	fmt.Println()
	if len(tasks) == 0 {
		// 首次使用——显示欢迎页
		fmt.Println("  👋 欢迎使用 GoWhale！")
		fmt.Println()
		fmt.Println("  快速开始：")
		fmt.Println("    直接输入任务，如「检查项目」「创建一个 hello world」")
		fmt.Println("    输入 / 查看所有命令")
		fmt.Println("    输入 /tui 切换到 TUI 分栏模式")
		fmt.Println("    输入 /chatroom 启用多角色协作模式")
		fmt.Println()
		fmt.Println("  提示：")
		fmt.Println("    · 只读操作自动放行，写文件/执行命令需审批确认")
		fmt.Println("    · 审批时按 a = 本次会话始终允许，不再重复询问")
		fmt.Println("    · 按 Tab 浏览命令补全")
		fmt.Println()
		return
	}

	fmt.Println(strings.Repeat("─", 48))
	fmt.Println("📝 最近对话：")
	for _, t := range tasks {
		fmt.Printf("  %s\n", dimC(t.Task))
		for _, r := range t.Replies {
			r = strings.TrimSpace(r)
			if len(r) > 80 {
				r = r[:80] + "…"
			}
			fmt.Printf("    ↳ %s\n", dimC(r))
		}
	}
	fmt.Println(strings.Repeat("─", 48))
	fmt.Println()
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
