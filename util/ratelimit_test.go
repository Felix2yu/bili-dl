package util

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

func TestNewRateReaderPassthrough(t *testing.T) {
	src := bytes.NewReader([]byte("hello"))
	if got := NewRateReader(src, 0, context.Background()); got != src {
		t.Error("rate<=0 应透传原 reader")
	}
}

func TestRateReaderLimitsSpeed(t *testing.T) {
	// 限速 10KB/s，读取 20KB 至少需要约 1.8s（扣除 0.2s 初始突发）。
	data := bytes.Repeat([]byte("x"), 20*1024)
	rate := int64(10 * 1024)
	r := NewRateReader(bytes.NewReader(data), rate, context.Background())
	start := time.Now()
	got, err := io.ReadAll(r)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(data) {
		t.Fatalf("读取长度 %d != %d", len(got), len(data))
	}
	if elapsed < 1500*time.Millisecond {
		t.Errorf("限速未生效：读 %d 字节仅用 %v", len(data), elapsed)
	}
}

func TestRateReaderCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	data := bytes.Repeat([]byte("y"), 200*1024)
	r := NewRateReader(bytes.NewReader(data), 1024, ctx)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, _ = io.ReadAll(r)
	// 取消后应快速返回（不会等满 200s）。
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("取消后未及时返回，用时 %v", elapsed)
	}
}
