package main

import (
	"log"
	"os"

	"gowhale/internal/agent"
	"gowhale/internal/app"
	"gowhale/internal/config"
)

func main() {
	cfg := config.Load()
	workspace, _ := os.Getwd()

	r, mem := agent.NewRunner(cfg, workspace)

	chat := app.New(r, mem, cfg.Model)
	if err := chat.Run(); err != nil {
		log.Fatalf("程序错误: %v", err)
	}
}
