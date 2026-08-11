// Package config 加载配置。
// 优先级：环境变量 > .env 文件 > ~/.gowhale/.env 文件 > 默认值。
package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Config 应用配置。
// 可通过 AICODE_API_KEY / AICODE_BASE_URL / AICODE_MODEL 环境变量覆盖，
// 或在 ~/.gowhale/.env / 当前目录 .env 中设置。
type Config struct {
	APIKey  string // API Key，默认 sk-placeholder
	BaseURL string // API 地址，默认 https://api.deepseek.com/v1
	Model   string // 模型名，默认 deepseek-v4-flash
}

func Load() Config {
	// 加载 .env 文件（不覆盖已有环境变量）
	home, _ := os.UserHomeDir()
	loadDotEnv(filepath.Join(home, ".gowhale", ".env"))
	loadDotEnv(".env")

	return Config{
		APIKey:  getenv("AICODE_API_KEY", "sk-placeholder"),
		BaseURL: getenv("AICODE_BASE_URL", "https://api.deepseek.com/v1"),
		Model:   getenv("AICODE_MODEL", "deepseek-v4-flash"),
	}
}

// loadDotEnv 读取 key=value 格式的 .env 文件，写入 os.Setenv（不覆盖已有值）。
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		val = strings.Trim(val, `"'`)
		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
