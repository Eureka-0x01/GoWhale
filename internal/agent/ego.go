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

// renderCompact 精简版宪法渲染（用于 egoBlock）。
func (c *Constitution) renderCompact() string {
	var b strings.Builder
	if len(c.ProtectedInvariants) > 0 {
		b.WriteString("\n项目规则（.aicode/constitution.json）：\n")
		for _, item := range c.ProtectedInvariants {
			b.WriteString(fmt.Sprintf("- %s\n", item))
		}
	}
	return b.String()
}

// ---------- 工作区身份构建 ----------

// egoBlock 构建统一的系统提示。所有规则集中在此，不再分离 skillRules。
func egoBlock(workspace string, c *Constitution) string {
	abs, _ := filepath.Abs(workspace)
	var b strings.Builder

	// ── 身份与环境 ──
	b.WriteString(fmt.Sprintf("你是 GoWhale CLI 编程助手。工作目录: %s，OS: %s/%s。\n", abs, runtime.GOOS, runtime.GOARCH))
	b.WriteString(envBlockCompact())
	b.WriteString("所有文件操作和命令限定在工作目录内，禁止越界。\n")

	// ── 宪法（如果存在）──
	if c != nil {
		b.WriteString(c.renderCompact())
	}

	// ── 操作规则（优先级从高到低）──
	b.WriteString("\n## 操作规则\n\n")
	b.WriteString("**1. 批量操作 —— 最重要**\n")
	b.WriteString("- 读 ≥2 个文件 → 用 read_file 的 `paths` 数组参数，一次完成。例: {\"paths\": [\"a.go\",\"b.go\"]}\n")
	b.WriteString("- 写 ≥2 个文件 → 必须用 batch_write 的 `files` map 参数一次提交。严禁逐个 write_file\n")
	b.WriteString("- 大文件分段读 → `start_line` + `max_lines`。禁止用 sed/head/tail 等 OS 命令读文件\n")
	b.WriteString("- 路径不存在 → 先 list_dir 确认，禁止猜测后重试\n\n")
	b.WriteString("**2. 收敛纪律**\n")
	b.WriteString("- 验证通过 → 立即输出「执行完成」并停止。不要追加确认或优化\n")
	b.WriteString("- 复杂任务(3+步) → 先 write_plan 制定计划\n")
	b.WriteString("- 连续只读不写 → 用已有信息总结当前状态，然后结束\n")
	b.WriteString("- 同一命令失败/被拒 2 次 → 换方案，不要换参数重试\n\n")
	b.WriteString("**3. 命令执行**\n")
	b.WriteString("- 后台服务: `background: true`，禁止 start/nohup/&。服务已运行别 kill\n")
	b.WriteString("- 失败先读错误输出和配置文件诊断根因。同一命令最多 2 种写法\n\n")
	b.WriteString("**4. 对话判断**\n")
	b.WriteString("- 用户闲聊/询问 → 直接文字回答，不调用工具\n")
	b.WriteString("- 用户要求执行操作 → 调用工具\n\n")
	b.WriteString("所有面向用户的输出使用简体中文。\n")

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

// envBlockCompact 精简版环境信息（Shell、命令语法、可用工具）。
func envBlockCompact() string {
	var b strings.Builder

	shellName := "sh"
	if runtime.GOOS == "windows" {
		shellName = "cmd"
	} else if _, err := exec.LookPath("bash"); err == nil {
		shellName = "bash"
	}
	b.WriteString(fmt.Sprintf("Shell: %s。", shellName))

	if runtime.GOOS == "windows" {
		b.WriteString(" 命令用 cmd 语法：dir/findstr/type/del/move/copy，禁止 grep/sed/awk/cat/ls/rm。")
	} else {
		b.WriteString(" 命令用 bash 语法。")
	}

	b.WriteString(" 可用工具: ")
	found := false
	for _, cmd := range []string{"go", "java", "python3", "python", "node", "npm", "git", "curl", "docker", "make", "cargo"} {
		if _, err := exec.LookPath(cmd); err == nil {
			if found { b.WriteString(", ") }
			b.WriteString(cmd)
			found = true
		}
	}
	if !found { b.WriteString("(无)") }
	b.WriteString("。\n")
	return b.String()
}
