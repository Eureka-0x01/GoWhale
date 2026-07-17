package agent

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

// ---------- ANSI 颜色 ----------

const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[36m"
	colorGray   = "\033[90m"
)

// 检测是否支持颜色（非终端/管道时不输出 ANSI 码，避免乱码）
var colorEnabled = func() bool {
	fi, _ := os.Stdout.Stat()
	return fi != nil && (fi.Mode()&os.ModeCharDevice) != 0
}()

// c 包裹 ANSI 颜色码，不支持颜色时原样返回。
func c(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + colorReset
}

// 快捷颜色函数
func green(s string) string  { return c(colorGreen, s) }
func red(s string) string    { return c(colorRed, s) }
func yellow(s string) string { return c(colorYellow, s) }
func blue(s string) string   { return c(colorBlue, s) }
func gray(s string) string   { return c(colorGray, s) }
func bold(s string) string   { return c(colorBold, s) }
func dim(s string) string    { return c(colorDim, s) }

// ---------- 转圈动画 ----------

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner 在被调用时在终端显示转圈动画。
type Spinner struct {
	running atomic.Bool
	done    chan struct{}
}

// Start 开始转圈，前缀文字显示在转圈左边。返回 stop 函数。
func (s *Spinner) Start(prefix string) func() {
	if !colorEnabled {
		return func() {} // 管道/非终端时不转
	}
	s.running.Store(true)
	s.done = make(chan struct{})
	go func() {
		i := 0
		for {
			select {
			case <-s.done:
				return
			default:
				fmt.Printf("\r%s %s %s", gray(prefix), blue(spinnerFrames[i%len(spinnerFrames)]), "  ")
				i++
				time.Sleep(120 * time.Millisecond)
			}
		}
	}()
	return func() {
		if s.running.CompareAndSwap(true, false) {
			close(s.done)
			// 清除转圈，光标回到行首
			fmt.Print("\r\033[K")
		}
	}
}
