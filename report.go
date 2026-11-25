package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Felix2yu/bili-dl/site"
)

// reportVersion 是 --result-json 的格式版本。字段只增不改；
// 需要破坏性变更时递增，消费方据此判断。
const reportVersion = "1"

// TargetReport 是单个下载目标的结果。
type TargetReport struct {
	MediaID string        `json:"media_id,omitempty"`
	BV      string        `json:"bv,omitempty"`
	Page    int           `json:"page,omitempty"`
	Title   string        `json:"title,omitempty"`
	State   string        `json:"state"` // succeeded / failed
	Outputs []site.Output `json:"outputs,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// Report 是 --result-json 写出的完整结果。
//
// Outputs 是全部目标的产物聚合，方便调用方只读一层就能入库；
// Targets 保留逐目标明细（含失败原因），便于排障与按条重试。
type Report struct {
	Version     string         `json:"version"`
	GeneratedAt string         `json:"generated_at"`
	State       string         `json:"state"` // succeeded / partial / failed / canceled
	Outputs     []site.Output  `json:"outputs"`
	Targets     []TargetReport `json:"targets"`
}

// outcomeCollector 汇总并发下载的结果，保证多 goroutine 写入安全。
type outcomeCollector struct {
	mu    sync.Mutex
	items []TargetReport
}

func newOutcomeCollector() *outcomeCollector {
	return &outcomeCollector{}
}

func (o *outcomeCollector) add(r TargetReport) {
	o.mu.Lock()
	o.items = append(o.items, r)
	o.mu.Unlock()
}

// snapshot 返回排序后的副本：并发完成顺序不确定，排序才能让结果可复现。
func (o *outcomeCollector) snapshot() []TargetReport {
	o.mu.Lock()
	out := make([]TargetReport, len(o.items))
	copy(out, o.items)
	o.mu.Unlock()

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].BV != out[j].BV {
			return out[i].BV < out[j].BV
		}
		return out[i].Page < out[j].Page
	})
	return out
}

// buildReport 由目标明细与取消标记算出整体状态。
func buildReport(targets []TargetReport, canceled bool) Report {
	rep := Report{
		Version:     reportVersion,
		GeneratedAt: time.Now().Format(time.RFC3339),
		Targets:     targets,
		Outputs:     []site.Output{},
	}
	succeeded := 0
	for _, t := range targets {
		if t.State == "succeeded" {
			succeeded++
		}
		rep.Outputs = append(rep.Outputs, t.Outputs...)
	}

	switch {
	case canceled:
		rep.State = "canceled"
	case len(targets) == 0:
		rep.State = "failed"
	case succeeded == len(targets):
		rep.State = "succeeded"
	case succeeded == 0:
		rep.State = "failed"
	default:
		rep.State = "partial"
	}
	return rep
}

// writeReport 原子写入报告：先写同目录临时文件再 rename，
// 避免调用方读到写了一半的 JSON。
func writeReport(path string, rep Report) error {
	if path == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建报告目录失败: %w", err)
		}
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化报告失败: %w", err)
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".result-*.json")
	if err != nil {
		return fmt.Errorf("创建临时报告失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时报告失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时报告失败: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("设置报告权限失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("落盘报告失败: %w", err)
	}
	return nil
}
