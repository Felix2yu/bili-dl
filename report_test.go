package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Felix2yu/liuying-engine/site"
)

func TestBuildReportStates(t *testing.T) {
	ok := TargetReport{State: "succeeded", Outputs: []site.Output{{Path: "/a.mp4", Kind: "video", Bytes: 10}}}
	bad := TargetReport{State: "failed", Error: "boom"}

	cases := []struct {
		name     string
		targets  []TargetReport
		canceled bool
		want     string
	}{
		{"全部成功", []TargetReport{ok, ok}, false, "succeeded"},
		{"部分成功", []TargetReport{ok, bad}, false, "partial"},
		{"全部失败", []TargetReport{bad, bad}, false, "failed"},
		{"空结果算失败", nil, false, "failed"},
		{"取消优先", []TargetReport{ok}, true, "canceled"},
	}
	for _, c := range cases {
		if got := buildReport(c.targets, c.canceled).State; got != c.want {
			t.Errorf("%s: want %q, got %q", c.name, c.want, got)
		}
	}
}

func TestBuildReportAggregatesOutputs(t *testing.T) {
	targets := []TargetReport{
		{State: "succeeded", Outputs: []site.Output{
			{Path: "/a.mp4", Kind: "video", Bytes: 1},
			{Path: "/a.cover.jpg", Kind: "cover", Bytes: 2},
		}},
		{State: "succeeded", Outputs: []site.Output{
			{Path: "/b.mp4", Kind: "video", Bytes: 3},
		}},
	}
	rep := buildReport(targets, false)
	if len(rep.Outputs) != 3 {
		t.Fatalf("聚合产物应为 3 条，实际 %d", len(rep.Outputs))
	}
	if rep.Version != reportVersion {
		t.Errorf("版本应为 %q，实际 %q", reportVersion, rep.Version)
	}
	if rep.GeneratedAt == "" {
		t.Error("GeneratedAt 不应为空")
	}
}

func TestBuildReportOutputsNeverNil(t *testing.T) {
	// 空数组要序列化成 []，不能是 null：Swift 侧解码数组为 null 会抛错。
	rep := buildReport(nil, false)
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if _, ok := raw["outputs"].([]any); !ok {
		t.Errorf("outputs 应为数组，实际 %T: %v", raw["outputs"], raw["outputs"])
	}
}

func TestWriteReportAtomicAndReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "result.json")

	rep := buildReport([]TargetReport{{
		MediaID: "BV1", BV: "BV1", Page: 1, Title: "标题", State: "succeeded",
		Outputs: []site.Output{{Path: "/x.mp4", Kind: "video", Container: "mp4", Bytes: 42}},
	}}, false)

	if err := writeReport(path, rep); err != nil {
		t.Fatalf("writeReport 失败: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取报告失败: %v", err)
	}
	var got Report
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("报告不是合法 JSON: %v", err)
	}
	if got.State != "succeeded" || len(got.Targets) != 1 || got.Targets[0].BV != "BV1" {
		t.Errorf("报告内容错误: %+v", got)
	}
	if len(got.Outputs) != 1 || got.Outputs[0].Bytes != 42 || got.Outputs[0].Container != "mp4" {
		t.Errorf("产物内容错误: %+v", got.Outputs)
	}

	// 目录里不应残留临时文件。
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "result.json" {
			t.Errorf("残留文件: %s", e.Name())
		}
	}
}

func TestWriteReportEmptyPathIsNoop(t *testing.T) {
	if err := writeReport("", Report{}); err != nil {
		t.Errorf("空路径应为 no-op，实际报错: %v", err)
	}
}

func TestOutcomeCollectorSortsDeterministically(t *testing.T) {
	o := newOutcomeCollector()
	o.add(TargetReport{BV: "BV2", Page: 1})
	o.add(TargetReport{BV: "BV1", Page: 3})
	o.add(TargetReport{BV: "BV1", Page: 1})

	got := o.snapshot()
	want := []struct {
		bv   string
		page int
	}{{"BV1", 1}, {"BV1", 3}, {"BV2", 1}}
	for i := range want {
		if got[i].BV != want[i].bv || got[i].Page != want[i].page {
			t.Fatalf("排序错误：第 %d 条为 %s/%d", i, got[i].BV, got[i].Page)
		}
	}
}
