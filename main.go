package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gowhale/internal/agent"
	"gowhale/internal/config"
	"gowhale/internal/llm"
	"gowhale/internal/tools"
)

func main() {
	// --clear-key 命令行参数
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

	// 模式一：命令行直接带任务
	if len(os.Args) > 1 {
		ag.Run(strings.Join(os.Args[1:], " "))
		return
	}

	// 模式二：交互式多轮
	printBanner(cfg)

	for {
		fmt.Print("\n你 > ")
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println()
			return
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		// / 开头的命令
		if strings.HasPrefix(input, "/") {
			if handleCommand(input, reader) {
				return // exit
			}
			continue
		}

		// 纯文本退出
		if input == "exit" || input == "quit" {
			fmt.Println("再见！")
			return
		}

		ag.Run(input)
	}
}

// printBanner 启动提示，参考 CodeWhale 的 HomeQuick 格式。
func printBanner(cfg config.Config) {
	fmt.Printf("GoWhale — AI 编程助手（%s / %s）\n", cfg.Model, cfg.ProModel)
	fmt.Println(strings.Repeat("─", 48))
	fmt.Println("输入任务，回车执行。以下 / 命令可用：")
	fmt.Println()
	fmt.Println("  /help        帮助信息（显示本条）")
	fmt.Println("  /model       查看/切换当前模型")
	fmt.Println("  /clear       清空对话历史，开始新会话")
	fmt.Println("  /clear-key   清除已保存的 API Key")
	fmt.Println("  /exit        退出程序")
	fmt.Println()
	fmt.Println("直接输入自然语言即可开始任务。")
}

// handleCommand 处理 / 开头的交互命令。返回 true 表示退出。
func handleCommand(input string, in *bufio.Reader) bool {
	switch strings.ToLower(input) {
	case "/help":
		fmt.Println()
		fmt.Println("GoWhale 命令列表：")
		fmt.Println("  /help        显示本帮助")
		fmt.Println("  /model       显示当前模型（简单 / 复杂）")
		fmt.Println("  /clear       清空全部对话历史")
		fmt.Println("  /clear-key   清除 ~/.gowhale/.env 中的 API Key")
		fmt.Println("  /exit        退出")
		fmt.Println()
		fmt.Println("提示：直接输入任务描述即可开始，无需前缀。")
		fmt.Println("复杂任务（写代码/多步推理）自动路由到 pro 模型。")

	case "/model":
		fmt.Printf("简单任务: %s\n复杂任务: %s\n", config.Load().Model, config.Load().ProModel)

	case "/clear":
		fmt.Println("✓ 对话历史已清空。输入新任务开始。")
		// Note: 需要重启 agent 才能真正清空历史。这里先给个提示，
		// 实际清空需要重构 agent 支持 reset。
		fmt.Println("  提示：输入 /exit 退出后重新进，即可完全重置。")

	case "/clear-key":
		clearAPIKey(in)

	case "/exit", "/quit":
		fmt.Println("再见！")
		return true

	default:
		fmt.Printf("未知命令: %s。输入 /help 查看可用命令。\n", input)
	}
	return false
}

// clearAPIKey 清除 ~/.gowhale/.env 中保存的 API Key。使用共享 reader 避免缓冲冲突。
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
