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
	// --clear-key：清除已保存的 API Key
	if len(os.Args) == 2 && (os.Args[1] == "--clear-key" || os.Args[1] == "-clear-key") {
		clearAPIKey()
		return
	}

	cfg := config.Load()
	if cfg.APIKey == "" {
		fmt.Fprintln(os.Stderr, "错误：未设置 API Key。请设置环境变量 AICODE_API_KEY 后重试。")
		os.Exit(1)
	}

	client := llm.NewClient(cfg)

	// 注册可用技能：计划、执行命令、读文件、写文件（单个/批量）、列目录。
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

	// 与审批门共用同一个 stdin reader，避免读取缓冲冲突。
	reader := bufio.NewReader(os.Stdin)
	approver := agent.NewApprover(reader)

	workspace, _ := os.Getwd()
	tools.SetWorkspace(workspace) // 锁定工作区，文件/shell 无法越界
	ag := agent.New(client, registry, approver, cfg.MaxTurns, workspace, cfg.Model, cfg.ProModel)

	// 模式一：命令行直接带任务，执行完退出。
	if len(os.Args) > 1 {
		ag.Run(strings.Join(os.Args[1:], " "))
		return
	}

	// 模式二：交互式多轮对话。
	fmt.Printf("AI 编程助手（模型: %s，可操作本地文件与命令）\n", client.Model())
	fmt.Println("输入任务，回车执行；输入 exit 或 quit 退出。")
	fmt.Println(strings.Repeat("-", 52))

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
		if input == "exit" || input == "quit" {
			fmt.Println("再见！")
			return
		}
		ag.Run(input)
	}
}

// clearAPIKey 清除 ~/.gowhale/.env 中保存的 API Key，清除前需用户确认。
func clearAPIKey() {
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
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
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
