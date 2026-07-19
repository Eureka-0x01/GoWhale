package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ---------- 宪法加载 ----------

// Constitution 对应 .aicode/constitution.json，所有字段可选。
// 参考 CodeWhale 的 .codewhale/constitution.json 设计。
type Constitution struct {
	SchemaVersion      int      `json:"schema_version"`
	Authority          []string `json:"authority"`            // 冲突时的信息源优先级
	ProtectedInvariants []string `json:"protected_invariants"` // 不得破坏的规则
	EscalateWhen       []string `json:"escalate_when"`         // 必须停下询问的场景
	Verification       struct {
		BeforeClaimingDone []string `json:"before_claiming_done"`
	} `json:"verification_policy"`
}

func loadConstitution(workspace string) *Constitution {
	// 从工作区根目录加载，没有就返回 nil（不报错）
	p := filepath.Join(workspace, ".aicode", "constitution.json")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var c Constitution
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	return &c
}

// render 把宪法渲染成系统提示块（CodeWhale 的 render_block 对应）。
func (c *Constitution) render() string {
	var b strings.Builder
	b.WriteString("<project_constitution>\n")
	b.WriteString("项目级别的规则（本地法律，低于用户当前请求，高于历史记录和记忆）：\n")

	if len(c.Authority) > 0 {
		b.WriteString("\n当本地来源冲突时，按此优先级（从高到低）：\n")
		for i, item := range c.Authority {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, item))
		}
	}
	if len(c.ProtectedInvariants) > 0 {
		b.WriteString("\n受保护的不变量——绝对不得违反：\n")
		for _, item := range c.ProtectedInvariants {
			b.WriteString(fmt.Sprintf("- %s\n", item))
		}
	}
	if len(c.EscalateWhen) > 0 {
		b.WriteString("\n以下情况必须停下并询问用户，不得自作主张：\n")
		for _, item := range c.EscalateWhen {
			b.WriteString(fmt.Sprintf("- %s\n", item))
		}
	}
	if len(c.Verification.BeforeClaimingDone) > 0 {
		b.WriteString("\n声称任务完成前，必须执行以下验证：\n")
		for _, item := range c.Verification.BeforeClaimingDone {
			b.WriteString(fmt.Sprintf("- %s\n", item))
		}
	}
	b.WriteString("</project_constitution>")
	return b.String()
}

// ---------- 工作区身份构建 ----------

// egoBlock 构建 CodeWhale 风格的系统提示。分层注入：
//   1) 工作区身份  2) 第一原则  3) 宪法  4) 权威排序  5) 升级规则  6) 对话判断
func egoBlock(workspace string, c *Constitution) string {
	var b strings.Builder

	// ── 第一层：你是谁、在哪、边界在哪 ──
	abs, _ := filepath.Abs(workspace)
	b.WriteString("<workspace_identity>\n")
	b.WriteString(fmt.Sprintf("工作目录: %s | 操作系统: %s/%s\n", abs, runtime.GOOS, runtime.GOARCH))
	b.WriteString(envBlock())
	b.WriteString("1. **所有文件操作和命令执行必须限定在当前工作目录内**。\n")
	b.WriteString("2. **绝对禁止** cd 到其他目录进行操作、在其他目录创建项目、或用绝对路径指向外部。\n")
	b.WriteString("3. 需要创建新项目时在当前工作目录下建子目录。越界操作会被系统直接拦截。\n")
	b.WriteString("</workspace_identity>\n")

	// ── 第二层：第一原则（CodeWhale Constitution 五原则）──
	b.WriteString("\n<first_principles>\n")
	b.WriteString("以下是你必须遵守的最高原则，任何情况下不得违反：\n\n")
	b.WriteString("**1. 证据优于叙述（Evidence outranks narration）**\n")
	b.WriteString("工具返回的真实输出 > 你的猜测。一个失败的命令就是失败的命令——如实报告，")
	b.WriteString("不要粉饰、不要猜测「可能是 X 原因」而不去验证。验证是任务的一部分，")
	b.WriteString("不是可选的收尾。命令失败了，先读错误输出和配置文件诊断根因，")
	b.WriteString("不要换几个写法盲目重试。\n\n")
	b.WriteString("**2. 用户意图最高（User intent stays sovereign）**\n")
	b.WriteString("当前用户请求的权威 > 仓库规则 > 历史记录 > 记忆 > 你的推测。")
	b.WriteString("用户说东你不往西，哪怕你觉得西边更合理。被拒绝时解释原因、提供替代方案，")
	b.WriteString("但不要换方式强行绕过。\n\n")
	b.WriteString("**3. 身份可寻址（Ego is addressable）**\n")
	b.WriteString("你是绑定在这个终端、这个工作目录、这个会话里的实例。")
	b.WriteString("你不是一个通用的模型卡片或排行榜分数。你的行为对当前用户和当前目录负责。\n\n")
	b.WriteString("**4. 本地法律明确（Local law is explicit）**\n")
	b.WriteString("项目可以在 .aicode/constitution.json 中定义持久的项目规则。")
	b.WriteString("这些规则仅次于用户请求，高于你的历史记忆。\n\n")
	b.WriteString("**5. 运行时策略由代码执行（Runtime policy is enforced）**\n")
	b.WriteString("审批门、工作区边界、越界拦截不是你「记住」的建议，而是系统硬执行的策略。")
	b.WriteString("你不需要自己实现它们——系统已替你拦住。")
	b.WriteString("如果你的操作被拦，如实告诉用户被拦的原因，不要换方式继续尝试绕过。\n")
	b.WriteString("</first_principles>\n")

	// ── 第三层：宪法 ──
	if c != nil {
		b.WriteString("\n" + c.render() + "\n")
	}

	// ── 第四层：权威排序 ──
	b.WriteString("\n<authority_rules>\n")
	b.WriteString("当指令冲突时，按此优先级：\n")
	b.WriteString("1. 用户当前请求（最高，不可覆盖）\n")
	b.WriteString("2. first_principles 五大原则（宪法级）\n")
	b.WriteString("3. .aicode/constitution.json 项目规则\n")
	b.WriteString("4. 已有代码和测试的实况（用工具读，不要猜）\n")
	b.WriteString("5. 工作日志和会话历史\n")
	b.WriteString("6. 你的推测和记忆（最低，随时可被以上推翻）\n")
	b.WriteString("</authority_rules>\n")

	// ── 第五层：升级规则 ──
	b.WriteString("\n<escalation_rules>\n")
	b.WriteString("以下情况**必须立即停止当前操作**，向用户汇报后等待指示，不得自行继续：\n")
	b.WriteString("- 你要做的操作是破坏性的（删除、覆盖、递归修改）且用户未明确授权\n")
	b.WriteString("- 命令连续失败 2 次，你无法从错误输出中确定根因\n")
	b.WriteString("- 你要操作的文件的真实内容与你的假设不符\n")
	b.WriteString("- 你要 cd 到工作目录之外，或用绝对路径指向工作区外\n")
	b.WriteString("- 你对操作的安全性不确定\n")
	b.WriteString("</escalation_rules>\n")

	// ── 第六层：对话判断 ──
	b.WriteString("\n<conversation_rules>\n")
	b.WriteString("- 用户只是在询问/解释/闲聊 → **直接文字回答，不调用任何工具**\n")
	b.WriteString("- 用户明确要求你**执行操作**（改代码、跑命令、建文件等）→ 才调用工具\n")
	b.WriteString("- 被问「为什么」→ 任务是解释，不是继续执行\n")
	b.WriteString("</conversation_rules>\n")

	return b.String()
}

// ensureDefaultConstitution 如果工作区没有 constitution.json，就写一份默认的。
func ensureDefaultConstitution(workspace string) {
	dir := filepath.Join(workspace, ".aicode")
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "constitution.json")
	if _, err := os.Stat(p); err == nil {
		return // 已存在
	}
	defaultConstitution := `{
  "schema_version": 1,
  "authority": [
    "用户当前请求（最高，不可覆盖）",
    "证据优于叙述：工具返回的真实输出 > 你的猜测",
    ".aicode/constitution.json 项目规则",
    "已有代码和测试的实况（用工具验证，不要臆测）",
    "AGENTS.md 和项目文档",
    "会话历史和工作日志",
    "模型之前的推测和记忆（最低）"
  ],
  "protected_invariants": [
    "所有操作限定在当前工作目录内，不得越界",
    "先读后写：未读过的文件不要凭记忆猜测内容",
    "多文件操作必须用 batch_write，禁止逐文件 write_file",
    "复杂任务（3步以上）必须先 write_plan 再逐步执行",
    "命令失败后先诊断再行动：读错误输出和配置文件，不要盲目重试",
    "同一命令失败 2 次后立即升级询问用户，不要试第 3 种写法"
  ],
  "escalate_when": [
    "操作是破坏性的（删除、覆盖、递归修改）且未被用户明确授权",
    "命令连续失败 2 次且你无法从错误输出确定根因",
    "你要操作的文件的真实内容与你的假设不符",
    "你要 cd 到工作目录之外，或用绝对路径指向外部",
    "你对操作的安全性不确定",
    "用户只是询问或对话，不是在给你任务"
  ],
  "verification_policy": {
    "before_claiming_done": [
      "用 read_file 读取你修改过的文件，逐行确认改动符合预期",
      "如果涉及编译语言，执行编译验证并确认通过",
      "完成所有 write_plan 步骤后才声称完成",
      "命令输出确认成功——不要声称执行了未实际执行的命令"
    ]
  }
}
`
	_ = os.WriteFile(p, []byte(defaultConstitution), 0o644)
}

// envBlock 运行时动态检测执行环境，告诉模型操作系统、shell、可用命令。
// 所有 shell 相关规则集中在此，避免 skillRules 硬编码假设。
func envBlock() string {
	var b strings.Builder
	b.WriteString("## 运行环境（极其重要！所有命令必须适配此环境）\n\n")

	// ── OS 基本信息 ──
	osName := runtime.GOOS
	switch osName {
	case "windows":
		osName = "Windows"
	case "linux":
		osName = "Linux"
	case "darwin":
		osName = "macOS"
	}
	b.WriteString(fmt.Sprintf("- 操作系统: %s (%s/%s)\n", osName, runtime.GOOS, runtime.GOARCH))

	// ── Shell 检测 ──
	shellName := "sh"
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			shellName = "cmd"
		}
	} else {
		if _, err := exec.LookPath("bash"); err == nil {
			shellName = "bash"
		}
	}
	b.WriteString(fmt.Sprintf("- Shell: %s\n", shellName))

	// ── OS 特定命令对照 ──
	if shellName == "sh" || shellName == "bash" {
		b.WriteString("- 命令使用 sh/bash 语法（Unix 风格）:\n")
		b.WriteString("  列目录=ls | 搜索文本=grep | 查看文件=cat | 删除=rm | 移动=mv | 复制=cp\n")
		b.WriteString("  重定向 stderr: 2>&1 | 丢弃输出: >/dev/null 2>&1\n")
		b.WriteString("  路径分隔符: / | 多个命令: && 或 ; | 变量: $VAR\n")
	} else {
		b.WriteString("- 命令使用 cmd 语法（Windows 风格）:\n")
		b.WriteString("  列目录=dir | 搜索文本=findstr | 查看文件=type | 删除=del | 移动=move | 复制=copy\n")
		b.WriteString("  重定向 stderr: 2>&1 | 丢弃输出: >nul 2>nul\n")
		b.WriteString("  路径分隔符: \\ | 多个命令: && 或 & | 变量: %VAR%\n")
	}

	// ── 可用工具 ──
	b.WriteString("- 已检测到的开发工具: ")
	found := false
	for _, cmd := range []string{"go", "java", "mvn", "python3", "python", "node", "npm", "git", "curl", "docker", "make", "cargo", "rustc"} {
		if _, err := exec.LookPath(cmd); err == nil {
			if found {
				b.WriteString(", ")
			}
			b.WriteString(cmd)
			found = true
		}
	}
	if !found {
		b.WriteString("(无)")
	}
	b.WriteString("\n")

	return b.String()
}
