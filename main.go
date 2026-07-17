package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"gowhale/internal/agent"
	"gowhale/internal/config"
	"gowhale/internal/llm"
	"gowhale/internal/tools"
)

func main() {
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
