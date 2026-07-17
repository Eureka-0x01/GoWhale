package config

import (
	"os"
	"strconv"
	"strings"
)

// Config 保存调用大模型所需的配置。
// 借鉴 CodeWhale 的思路：provider / model / baseURL / 凭据是分开的选择，
// 都可以通过环境变量单独覆盖。
type Config struct {
	BaseURL  string // 大模型 API 地址（OpenAI 兼容）
	APIKey   string // 访问密钥
	Model    string // 日常模型（简单对话/只读操作）
	ProModel string // 复杂任务模型（多步推理/代码生成/调试）
	MaxTurns int    // 单次请求内最大工具调用轮数
}

// Load 先读 .env 文件，再读环境变量（环境变量优先）。
func Load() Config {
	loadDotEnv()
	return Config{
		BaseURL:  getenv("AICODE_BASE_URL", "https://api.deepseek.com/v1"),
		APIKey:   getenv("AICODE_API_KEY", ""),
		Model:    getenv("AICODE_MODEL", "deepseek-v4-flash"),
		ProModel: getenv("AICODE_PRO_MODEL", "deepseek-v4-pro"),
		MaxTurns: getenvInt("AICODE_MAX_TURNS", 40),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// loadDotEnv 读取当前目录的 .env 文件，把 KEY=VALUE 行设为环境变量（已有则跳过）。
func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return // 没有 .env 文件就算了
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
		// 去掉引号
		val = strings.Trim(val, "\"'")
		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}
