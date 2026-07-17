package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 保存调用大模型所需的配置。加载优先级: 环境变量 > 当前目录 .env > ~/.gowhale/.env
type Config struct {
	BaseURL  string
	APIKey   string
	Model    string
	ProModel string
	MaxTurns int
}

// Load 按优先级加载配置。Key 缺失时交互式提示用户输入并保存到 ~/.gowhale/.env。
func Load() Config {
	homeDir, _ := os.UserHomeDir()
	globalEnv := filepath.Join(homeDir, ".gowhale", ".env")

	// 优先级从低到高加载（后面覆盖前面）
	loadDotEnv(globalEnv) // ① 全局默认
	loadDotEnv(".env")    // ② 当前目录

	cfg := Config{
		BaseURL:  getenv("AICODE_BASE_URL", "https://api.deepseek.com/v1"),
		APIKey:   getenv("AICODE_API_KEY", ""), // ③ 环境变量最高
		Model:    getenv("AICODE_MODEL", "deepseek-v4-flash"),
		ProModel: getenv("AICODE_PRO_MODEL", "deepseek-v4-pro"),
		MaxTurns: getenvInt("AICODE_MAX_TURNS", 40),
	}

	// Key 还是空的 → 交互式让用户输入
	if cfg.APIKey == "" {
		cfg.APIKey = promptKey(globalEnv)
	}

	return cfg
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
		val = strings.Trim(val, "\"'")
		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

// promptKey 没有 Key 时交互式询问，保存到 ~/.gowhale/.env。
func promptKey(savePath string) string {
	fmt.Fprintln(os.Stderr, "未检测到 API Key。")
	fmt.Fprint(os.Stderr, "请输入 DeepSeek API Key（如 sk-xxx）：")

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取失败，请设置环境变量 AICODE_API_KEY 后重试。")
		os.Exit(1)
	}
	key := strings.TrimSpace(input)
	if key == "" {
		fmt.Fprintln(os.Stderr, "Key 不能为空，请设置环境变量 AICODE_API_KEY 后重试。")
		os.Exit(1)
	}

	// 保存到 ~/.gowhale/.env
	dir := filepath.Dir(savePath)
	if err := os.MkdirAll(dir, 0o700); err == nil {
		content := fmt.Sprintf("# GoWhale 配置文件\nAICODE_API_KEY=%s\n", key)
		if err := os.WriteFile(savePath, []byte(content), 0o600); err == nil {
			fmt.Fprintf(os.Stderr, "✓ 已保存到 %s，下次无需重复输入。\n", savePath)
		}
	}

	os.Setenv("AICODE_API_KEY", key)
	return key
}
