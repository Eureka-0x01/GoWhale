package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	BaseURL     string
	APIKey      string
	Model       string
	ProModel    string
	OllamaURL   string
	OllamaModel string
	MaxTurns    int
}

func Load() Config {
	homeDir, _ := os.UserHomeDir()
	globalEnv := filepath.Join(homeDir, ".gowhale", ".env")

	loadDotEnv(globalEnv)
	loadDotEnv(".env")

	cfg := Config{
		BaseURL:     getenv("AICODE_BASE_URL", "https://api.deepseek.com/v1"),
		APIKey:      getenv("AICODE_API_KEY", ""),
		Model:       getenv("AICODE_MODEL", "deepseek-v4-flash"),
		ProModel:    getenv("AICODE_PRO_MODEL", "deepseek-v4-pro"),
		OllamaURL:   getenv("AICODE_OLLAMA_URL", ""),
		OllamaModel: getenv("AICODE_OLLAMA_MODEL", ""),
		MaxTurns:    getenvInt("AICODE_MAX_TURNS", 40),
	}

	if cfg.APIKey == "" {
		cfg.APIKey = promptKey(globalEnv)
	}

	return cfg
}

// PromptOllama 首次使用 Ollama 时交互式询问，保存到 ~/.gowhale/.env。
func PromptOllama(in *bufio.Reader) (url, model string) {
	homeDir, _ := os.UserHomeDir()
	savePath := filepath.Join(homeDir, ".gowhale", ".env")

	fmt.Print("\n首次使用 Ollama，需要配置：\n")
	fmt.Print("  Ollama 地址 [默认 http://localhost:11434/v1]: ")
	input, _ := in.ReadString('\n')
	url = strings.TrimSpace(input)
	if url == "" {
		url = "http://localhost:11434/v1"
	}

	fmt.Print("  模型名（如 qwen3-coder:30b，用 ollama list 查看）: ")
	input, _ = in.ReadString('\n')
	model = strings.TrimSpace(input)
	if model == "" {
		fmt.Println("  ✗ 模型名不能为空")
		return "", ""
	}

	// 保存到 ~/.gowhale/.env
	dir := filepath.Dir(savePath)
	os.MkdirAll(dir, 0o700)

	// 读取已有配置
	existing, _ := os.ReadFile(savePath)
	content := string(existing)
	if !strings.Contains(content, "AICODE_OLLAMA_URL") {
		content += fmt.Sprintf("\n# Ollama 本地模型\nAICODE_OLLAMA_URL=%s\nAICODE_OLLAMA_MODEL=%s\n", url, model)
		os.WriteFile(savePath, []byte(content), 0o600)
		fmt.Printf("  ✓ 已保存到 %s\n", savePath)
	}

	os.Setenv("AICODE_OLLAMA_URL", url)
	os.Setenv("AICODE_OLLAMA_MODEL", model)
	return
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
