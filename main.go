package main

import (
	"log"
	"os"

	"github.com/awesome-gocui/gocui"

	"gowhale/internal/agent"
	"gowhale/internal/app"
	"gowhale/internal/config"
)

func main() {
	cfg := config.Load()
	workspace, _ := os.Getwd()

	r := agent.NewRunner(cfg, workspace)

	g, err := gocui.NewGui(gocui.OutputNormal, true)
	if err != nil {
		log.Fatalf("启动 GUI 失败: %v", err)
	}
	defer g.Close()

	chat := app.New(g, r)
	if err := chat.Run(); err != nil && err != gocui.ErrQuit {
		log.Fatalf("主循环错误: %v", err)
	}
}
