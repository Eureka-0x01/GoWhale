# GoWhale 项目规则

> 自动生成 + 手动维护。CodeWhale 每次对话会读取此文件。

## 项目结构

```
main.go                     — 入口
internal/
  config/config.go          — 配置加载
  llm/client.go             — LLM 调用（OpenAI 兼容）
  tools/                    — 工具定义
  agent/                    — Agent 循环 + 审批
```

## 教训（必须遵守）

### 1. 右侧面板 / 分栏布局
- **绝对不要**试图在 go-prompt 之上实现右侧面板或分栏布局。
- go-prompt 每次渲染会 `EraseDown()` 清除输入行以下所有内容，任何额外输出都会被抹掉。
- PanelWriter 注入、防抖渲染、executor 输出等所有方案均失败。
- 要做真正的分栏布局，必须换掉 go-prompt，用 bubbletea/ratatui 等完整 TUI 框架。但重构工作量巨大。

### 2. Spinner 闪烁
- Spinner 的 Start/Stop 必须互斥。用 `sync.Mutex` + `stopped chan`，不能用 `atomic.Bool`。
- Start 时先关闭旧 goroutine 并 `<-s.stopped` 等待退出，再创建新 goroutine。
- 多个 goroutine 同时写 stdout 会导致闪烁。

### 3. ANSI 颜色
- Windows cmd.exe 只支持基础 16 色（`\033[31m` 等）。
- 256 色（`\033[38;5;Nm`）和真彩色在 cmd.exe 上不工作。
- 用 `go-isatty` 而非 `os.Stdout.Stat()` 检测终端。

### 4. 编译安装
- Windows 上 go install 会被运行中的进程锁住，无法覆盖。
- 需要先 `taskkill /F /IM gowhale.exe` 再编译。
- 记住：**改完代码要 commit 并 push，不只是本地编译**。

### 5. Git 回退
- 回退后如果问题依旧，检查二进制是否真的被替换了（`findstr` 搜关键字确认）。
- 源码回退 + 二进制替换 两步都要做。

## Ollama 接入

Ollama 提供 OpenAI 兼容 API，**已支持**，只需配置环境变量：

```
AICODE_BASE_URL = http://localhost:11434/v1
AICODE_MODEL    = qwen3-coder:30b
AICODE_API_KEY  = ollama   （Ollama 不需要 key，传任意非空值即可）
```

当前已安装模型：`qwen3-coder:30b`（18GB）

Ollama 的 function calling 支持取决于模型。qwen3-coder 支持 tool calling。

**不需要改任何代码**，`llm/client.go` 已是 OpenAI 兼容格式。

## 关键命令

| 操作 | 命令 |
|------|------|
| 编译 | `C:\temp\b.bat` |
| 替换二进制 | `copy /Y C:\temp\gowhale_p.exe C:\Users\ms\go\bin\gowhale.exe` |
| 杀进程 | `taskkill /F /IM gowhale.exe` |
| 回退 | `git reset --hard <commit> && git push --force github master` |
| Ollama 测试 | `ollama run qwen3-coder:30b` |
