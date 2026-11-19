package util

import (
	"context"
	"io"
	"sync"
	"time"
)

// NewRateReader 把读取速率限制在 bytesPerSecond 以内（token bucket），
// 返回一个包装后的 reader。rate<=0 或 r 为 nil 时直接透传原 reader。
// 尊重 context 取消：等待配额期间收到取消会立即返回。
func NewRateReader(r io.Reader, rate int64, ctx context.Context) io.Reader {
	if rate <= 0 || r == nil {
		return r
	}
	now := time.Now()
	return &rateReader{
		r:      r,
		rate:   rate,
		tokens: float64(rate) * 0.2, // 允许 0.2s 的初始突发
		last:   now,
		burst:  float64(rate) * 0.2,
		ctx:    ctx,
	}
}

type rateReader struct {
	r    io.Reader
	rate int64
	ctx  context.Context

	mu     sync.Mutex
	tokens float64
	last   time.Time
	burst  float64
}

func (r *rateReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.wait(int64(n))
	}
	return n, err
}

// wait 消耗 n 字节的配额，不足时按补充速率等待。
// 单次读取可能大于桶容量（如 io.ReadAll 一次读 32KB），此时分块消费，
// 否则 token 补到 burst 上限后仍小于 n，会陷入无限等待。
func (r *rateReader) wait(n int64) {
	chunkCap := int64(r.burst)
	if chunkCap < 1 {
		chunkCap = 1
	}
	for n > 0 {
		chunk := n
		if chunk > chunkCap {
			chunk = chunkCap
		}
		if !r.consume(chunk) {
			return // context 取消
		}
		n -= chunk
	}
}

// consume 消耗 amount 字节配额（amount <= burst），不足则等待补充。
// 返回 false 表示 context 取消。
func (r *rateReader) consume(amount int64) bool {
	for {
		r.mu.Lock()
		now := time.Now()
		r.tokens += now.Sub(r.last).Seconds() * float64(r.rate)
		if r.tokens > r.burst {
			r.tokens = r.burst
		}
		r.last = now
		if r.tokens >= float64(amount) {
			r.tokens -= float64(amount)
			r.mu.Unlock()
			return true
		}
		deficit := float64(amount) - r.tokens
		waitDur := time.Duration(deficit / float64(r.rate) * float64(time.Second))
		if waitDur < time.Millisecond {
			waitDur = time.Millisecond
		}
		r.mu.Unlock()

		if r.ctx != nil {
			timer := time.NewTimer(waitDur)
			select {
			case <-r.ctx.Done():
				timer.Stop()
				return false
			case <-timer.C:
			}
		} else {
			time.Sleep(waitDur)
		}
	}
}
