package tools

import (
	"regexp"
	"strings"
)

// dangerousPatterns 是被直接拒绝执行的危险命令模式。
// 命中任意一条，命令都不会执行，而是返回拦截说明给模型。
// 覆盖：递归删除、磁盘格式化/擦写、关机重启、fork 炸弹、远程管道执行、
// 覆盖系统关键路径、危险提权等。
var dangerousPatterns = []*regexp.Regexp{
	// 递归强制删除（rm -rf / rm -fr 各种写法）
	regexp.MustCompile(`\brm\s+(-[a-z]*\s+)*-[a-z]*[rf][a-z]*`),
	// Windows 递归删除
	regexp.MustCompile(`(?i)\b(del|erase)\b.*(/s|/q)`),
	regexp.MustCompile(`(?i)\b(rd|rmdir)\b.*/s`),
	// 磁盘格式化 / 底层擦写
	regexp.MustCompile(`(?i)\b(mkfs|format)\b`),
	regexp.MustCompile(`\bdd\b.*\bof=/dev/`),
	regexp.MustCompile(`>\s*/dev/(sd|hd|nvme|disk)`),
	// 关机 / 重启 / 停机
	regexp.MustCompile(`(?i)\b(shutdown|reboot|halt|poweroff|init\s+0|init\s+6)\b`),
	// fork 炸弹
	regexp.MustCompile(`:\(\)\s*\{.*\|.*&\s*\}`),
	// 远程内容直接管道给 shell 执行
	regexp.MustCompile(`(?i)\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(sh|bash|zsh)\b`),
	// 覆盖系统关键目录
	regexp.MustCompile(`>\s*/(etc|boot|sys|proc)/`),
	// 对根目录递归改权限 / 属主
	regexp.MustCompile(`(?i)\bchmod\b\s+-R\s+.*\s+/\s*$`),
	regexp.MustCompile(`(?i)\bchown\b\s+-R\s+.*\s+/\s*$`),
	// 危险提权后的破坏性操作前缀（sudo rm 等已被上面覆盖，这里兜底磁盘写入）
	regexp.MustCompile(`(?i)\bsudo\b.*\b(mkfs|dd|shutdown|reboot)\b`),
	// 借解释器内联执行删除类操作，绕过 rm 拦截（python/node/perl/ruby ...）
	regexp.MustCompile(`(?i)\b(python[0-9.]*|node|deno|bun|perl|ruby|php)\b.*\b(rmtree|rmdir|removedirs|os\.remove|os\.unlink|unlink|rmsync|rm\s*-rf)`),
	// 通过 -c/-e/-p 传入内联脚本且包含删除/擦除关键字
	regexp.MustCompile(`(?i)-[cep]\b.*\b(rmtree|unlink|rmsync|remove\(|shutil)`),
	// cd .. 跳出工作区 + 管道写入操作
	regexp.MustCompile(`\bcd\b\s+\.\.(/|\\)`),
	// echo/write 覆盖工作区外的文件
	regexp.MustCompile(`>\s*\.\./`),
}

// checkDanger 判断命令是否危险。返回命中的原因（空字符串表示安全）。
func checkDanger(command string) string {
	c := strings.TrimSpace(command)
	for _, re := range dangerousPatterns {
		if re.MatchString(c) {
			return "命中危险命令拦截规则：" + re.String()
		}
	}
	return ""
}
