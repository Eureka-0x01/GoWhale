package agent

import (
	tagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"gowhale/internal/config"
	"gowhale/internal/memory"
	"gowhale/internal/tools"
)

// ── 宪法与角色提示词 ──

// constitution 通用行为准则，所有 Agent 共享。
const constitution = `## 行为准则（必须遵守）

1. **真实优先**：所有输出基于工具返回的真实结果，严禁编造。工具失败如实报告，不掩盖。

2. **产出导向**：每次任务必须有可验证产出。连续 3 轮未调用写入/命令类工具，必须主动报告进度。

3. **最小惊讶**：高风险操作前确认。被拒绝时提供替代方案，不重复相同请求。

4. **资源意识**：优先批量操作。不重复读已读文件。

5. **渐进交付**：每完成一个可验证的子任务就输出进度。遇阻塞主动寻求澄清，不猜测。

6. **自我纠错**：工具调用返回错误时分析根因后重试。连续 2 次相同调用失败，切换策略。`

// devInstruction 开发者角色——三步循环。
const devInstruction = `## 角色
你是软件开发者。收到任务后按 "探索 → 行动 → 验收" 循环完成。

## 工作流
### 第一步：探索
将探索任务委派给 explorer 子代理，它会输出结构化的项目模块地图。
你也可以自己用 read_file / grep_search 补充细节。

### 第二步：行动
用 write_file / batch_write / edit_file 产出代码，execute_shell 编译验证。
核心阶段——必须产出实际代码。

### 第三步：验收
编译通过 → 报告"完成"。不通过 → 回到第一步，最多循环 3 次。
3 次仍不通过 → 报告阻塞原因，请求帮助。

## 输出
所有输出使用简体中文。完成时总结修改了哪些文件、验证结果。`

// explorerInstruction 探索子代理——只读，只产出结构化摘要。
const explorerInstruction = `## 角色
你是项目探索专家。只读不改，产出结构化模块地图。

## 探索规则
1. **目录优先**：先 list_dir 看结构 → grep_search 定位 → read_file 确认
2. **抽样阅读**：每目录最多读 3 个文件，每文件最多 300 行
3. **关键词驱动**：始终用任务关键词过滤目标，不撒网式浏览
4. **3 层深度**：不进入超过项目根目录 3 层的子目录
5. **5 文件上限**：最多完整读取 5 个文件，其他只读关键片段

## 输出格式（严格遵循）
### 项目结构
[目录树概览]

### 关键模块
- 模块名: 路径 | 职责 | 核心文件

### 相关代码
- 文件: 路径 | 相关函数/类型 | 行号范围

### 疑点
[需确认的部分，标注"需确认"，不猜测]

所有输出使用简体中文。`

// ── Runner 工厂 ──

// NewRunner 创建主 Runner + 记忆存储。
func NewRunner(cfg config.Config, workspace string) (runner.Runner, *memory.Store) {
	mem := memory.Load(workspace)
	instruction := constitution + "\n\n" + devInstruction +
		"\n\n当需要探索项目时，将任务委派给 explorer 子代理。"
	if memText := mem.Format(); memText != "" {
		instruction += "\n\n" + memText
	}

	llm := openai.New(cfg.Model,
		openai.WithAPIKey(cfg.APIKey),
		openai.WithBaseURL(cfg.BaseURL),
	)

	allTools := tools.All(workspace)

	// 探索子代理（只读工具）
	var explorerTools []tool.Tool
	for _, t := range allTools {
		if t.Declaration().Name == "read_file" || t.Declaration().Name == "list_dir" || t.Declaration().Name == "grep_search" {
			explorerTools = append(explorerTools, t)
		}
	}
	explorer := llmagent.New("explorer",
		llmagent.WithModel(llm),
		llmagent.WithInstruction(constitution+"\n\n"+explorerInstruction),
		llmagent.WithTools(explorerTools),
	)

	dev := llmagent.New("dev",
		llmagent.WithModel(llm),
		llmagent.WithInstruction(instruction),
		llmagent.WithTools(allTools),
		llmagent.WithSubAgents([]tagent.Agent{explorer}),
	)

	return runner.NewRunner("gowhale", dev,
		runner.WithSessionService(inmemory.NewSessionService()),
	), mem
}
