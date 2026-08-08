package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// ── 主题色 ──

// Theme 统一管理所有颜色常量。
// 使用 tcell.Color 定义背景/边框色，使用 tview 颜色标签定义前景文本色。
var Theme = struct {
	// Widget 背景/边框
	StatusBarBg   tcell.Color
	StatusBarFg   tcell.Color
	FooterBg      tcell.Color
	FooterFg      tcell.Color
	HintBg        tcell.Color
	FooterHintFg  tcell.Color
	ChatBorder    tcell.Color
	SidebarBorder tcell.Color

	// 侧边栏标签
	TabActiveBg   tcell.Color
	TabActiveFg   tcell.Color
	TabInactiveBg tcell.Color
	TabInactiveFg tcell.Color

	// 文本颜色标签（用于 tview DynamicColors）
	UserMsg   string // 用户输入
	ModelMsg  string // 模型思考/说明
	ToolCall  string // 工具调用
	ToolOK    string // 工具成功
	ToolErr   string // 工具失败
	RoleChg   string // 角色切换
	TaskDone  string // 任务完成
	TaskErr   string // 任务失败
	HighLight string // 高亮/强调
	Dim       string // 灰色/弱化
}{
	StatusBarBg:   tcell.ColorNavy,
	StatusBarFg:   tcell.ColorWhite,
	FooterBg:      tcell.ColorDarkSlateGray,
	FooterFg:      tcell.ColorWhite,
	HintBg:        tcell.ColorDarkSlateGray,
	FooterHintFg:  tcell.ColorDarkGray,
	ChatBorder:    tcell.ColorDarkCyan,
	SidebarBorder: tcell.ColorDarkCyan,

	TabActiveBg:   tcell.ColorDarkCyan,
	TabActiveFg:   tcell.ColorWhite,
	TabInactiveBg: tcell.ColorBlack,
	TabInactiveFg: tcell.ColorGray,

	UserMsg:   "cyan",
	ModelMsg:  "lightgreen",
	ToolCall:  "darkcyan",
	ToolOK:    "gray",
	ToolErr:   "red",
	RoleChg:   "yellow",
	TaskDone:  "green",
	TaskErr:   "red",
	HighLight: "yellow",
	Dim:       "gray",
}

// FooterHints 底部快捷键提示（显示在 footer 右侧）。
const FooterHints = "↑↓:历史 滚轮:聊天  Ctrl+Shift+C:复制  Ctrl+W:面板  Ctrl+M:协作  Ctrl+C:退出"

// ── 工具图标 ──

var toolIcons = map[string]string{
	"read_file":      "📖",
	"write_file":     "✏️",
	"batch_write":    "✏️",
	"list_dir":       "📁",
	"execute_shell":  "🔧",
	"write_plan":     "📋",
	"web_search":     "🔍",
	"grep_search":    "🔎",
	"verify_project": "✅",
	"execute_python": "🐍",
}

func toolIcon(name string) string {
	if icon, ok := toolIcons[name]; ok {
		return icon
	}
	return "🔧"
}

// ── 工具函数 ──

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	return s
}

func formatTokens(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// escapeTags 转义 tview 的颜色标签分隔符。
// tview 中 [ 表示颜色标签开始，[[] 表示字面 [。
func escapeTags(s string) string {
	return strings.ReplaceAll(s, "[", "[[]")
}

// tag 生成 tview 颜色标签包裹的文本。
func tag(color, text string) string {
	return fmt.Sprintf("[%s]%s[white]", color, text)
}

// tagLine 生成带颜色标签的行（自动加换行）。
func tagLine(color, text string) string {
	return tag(color, escapeTags(text)) + "\n"
}

// fmtTag 格式化写入带颜色标签的文本。
func fmtTag(sb *strings.Builder, color, format string, args ...interface{}) {
	sb.WriteString(fmt.Sprintf("[%s]%s[white]", color, fmt.Sprintf(format, args...)))
}
