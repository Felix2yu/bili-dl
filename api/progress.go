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

// NDJSON 进度事件。stdout 专用于机器可读输出；日志一律走 stderr。
// 事件类型：start / progress / done / error。
type progressEvent struct {
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

var progressMu sync.Mutex

func emitProgress(ev progressEvent) {
	if !c.ProgressJSON {
		return
	}
	ev.Timestamp = time.Now().UnixMilli()
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	progressMu.Lock()
	defer progressMu.Unlock()
	fmt.Fprintln(os.Stdout, string(b))
}

// progressWriter 包装 io.Writer，在 Copy 过程中按节流输出进度事件。
type progressWriter struct {
	w        io.Writer
	ev       progressEvent
	total    int64
	written  int64
	start    time.Time
	lastEmit time.Time
	lastPct  int
}

func newProgressWriter(w io.Writer, ev progressEvent, total int64) *progressWriter {
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
