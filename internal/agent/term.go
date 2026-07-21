package agent

import (
	"bufio"
	"fmt"
	"os"
	"sync"
	"time"
)

// ---------- ANSI 颜色 ----------

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[36m"
	gray   = "\033[90m"
)

var colorEnabled = func() bool {
	fi, _ := os.Stdout.Stat()
	return fi != nil && (fi.Mode()&os.ModeCharDevice) != 0
}()

func c(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + reset
}

func greenC(s string) string  { return c(green, s) }
func redC(s string) string    { return c(red, s) }
func yellowC(s string) string { return c(yellow, s) }
func blueC(s string) string   { return c(blue, s) }
func grayC(s string) string   { return c(gray, s) }
func boldC(s string) string   { return c(bold, s) }
func dimC(s string) string    { return c(dim, s) }

// ---------- 转圈动画 ----------

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type Spinner struct {
	mu      sync.Mutex
	done    chan struct{}
	stopped chan struct{}
}

func (s *Spinner) Start(prefix string) func() {
	if !colorEnabled {
		return func() {}
	}
	s.mu.Lock()
	if s.done != nil {
		close(s.done)
		<-s.stopped
		fmt.Print("\r\033[K")
	}
	s.done = make(chan struct{})
	s.stopped = make(chan struct{})
	s.mu.Unlock()

	go func() {
		defer close(s.stopped)
		i := 0
		for {
			select {
			case <-s.done:
				return
			default:
				fmt.Printf("\r%s %s %s", grayC(prefix), blueC(spinnerFrames[i%len(spinnerFrames)]), "  ")
				i++
				time.Sleep(120 * time.Millisecond)
			}
		}
	}()

	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.done != nil {
			close(s.done)
			<-s.stopped
			s.done = nil
			s.stopped = nil
			fmt.Print("\r\033[K")
		}
	}
}

// ---------- stdin 读取（Windows raw mode 兼容）----------

// ReadStdinLine 从 bufio.Reader 读取一行。
// 兼容 go-prompt 设置的 Windows 终端 raw mode：
// 在 raw mode 下 Enter 只发送 \r，不是 \n。
// 逐字节读取，遇到 \r 或 \n 结束，同时处理 \r\n 组合。
func ReadStdinLine(r *bufio.Reader) (string, error) {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '\r' {
			next, err := r.ReadByte()
			if err != nil || next != '\n' {
				if err == nil {
					r.UnreadByte()
				}
			}
			break
		}
		if b == '\n' {
			break
		}
		buf = append(buf, b)
	}
	return string(buf), nil
}
