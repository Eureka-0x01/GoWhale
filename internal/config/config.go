package config

import (
	"os"
	"strconv"
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

// Load 从环境变量读取配置，并提供合理的默认值（默认走 DeepSeek）。
// 注意：默认 APIKey 为方便本地测试而硬编码，请勿把它提交到公共仓库。
func Load() Config {
	return Config{
		BaseURL:  getenv("AICODE_BASE_URL", "https://api.deepseek.com/v1"),
		APIKey:   getenv("AICODE_API_KEY", "sk-e4e33b6d22c84b8ab316510758c0a259"),
		Model:    getenv("AICODE_MODEL", "deepseek-chat"),
		ProModel: getenv("AICODE_PRO_MODEL", "deepseek-reasoner"),
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
