package api

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Felix2yu/bili-dl/c"
)

// ProgressEvent 是一次下载动作的状态变化，与 NDJSON 事件字段一一对应。
// 事件类型：start / progress / done / error。
type ProgressEvent struct {
	Event     string  `json:"event"`
	BV        string  `json:"bv"`
	Page      int     `json:"page,omitempty"`
	Kind      string  `json:"kind,omitempty"` // video / audio / cover / danmaku / subtitle
	Current   int64   `json:"current,omitempty"`
	Total     int64   `json:"total,omitempty"`
	Percent   float64 `json:"percent,omitempty"`
	SpeedBPS  int64   `json:"speed_bps,omitempty"`
	Message   string  `json:"message,omitempty"`
	Timestamp int64   `json:"ts"` // 毫秒时间戳
}

var (
	progressMu   sync.Mutex
	progressHook func(ProgressEvent)
)

// SetProgressHook 注册进程内进度回调，供把 bili-dl 当库嵌入的调用方使用。
// 传 nil 取消注册。与 --progress-json 的 NDJSON 输出互不影响，可同时生效。
func SetProgressHook(fn func(ProgressEvent)) {
	progressMu.Lock()
	defer progressMu.Unlock()
	progressHook = fn
}

// progressEnabled 表示有人在观测字节级进度：NDJSON 输出或进程内 hook。
func progressEnabled() bool {
	progressMu.Lock()
	defer progressMu.Unlock()
	return c.ProgressJSON || progressHook != nil
}

func emitProgress(ev ProgressEvent) {
	ev.Timestamp = time.Now().UnixMilli()

	progressMu.Lock()
	hook := progressHook
	progressMu.Unlock()
	if hook != nil {
		hook(ev)
	}

	if !c.ProgressJSON {
		return
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	w := c.ProgressWriter
	if w == nil {
		w = os.Stdout
	}
	progressMu.Lock()
	defer progressMu.Unlock()
	fmt.Fprintln(w, string(b))
}

// progressWriter 包装 io.Writer，在 Copy 过程中按节流输出进度事件。
type progressWriter struct {
	w        io.Writer
	ev       ProgressEvent
	total    int64
	written  int64
	start    time.Time
	lastEmit time.Time
	lastPct  int
}

func newProgressWriter(w io.Writer, ev ProgressEvent, total int64) *progressWriter {
	now := time.Now()
	return &progressWriter{w: w, ev: ev, total: total, start: now, lastEmit: now}
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.written += int64(n)
	now := time.Now()
	pct := 0
	if p.total > 0 {
		pct = int(float64(p.written) / float64(p.total) * 100)
	}
	elapsed := now.Sub(p.lastEmit)
	shouldEmit := elapsed >= 200*time.Millisecond || pct >= p.lastPct+5
	if shouldEmit || err != nil {
		ev := p.ev
		ev.Event = "progress"
		ev.Current = p.written
		ev.Total = p.total
		ev.Percent = float64(pct)
		sec := now.Sub(p.start).Seconds()
		if sec > 0 {
			ev.SpeedBPS = int64(float64(p.written) / sec)
		}
		emitProgress(ev)
		p.lastEmit = now
		p.lastPct = pct
	}
	return n, err
}
