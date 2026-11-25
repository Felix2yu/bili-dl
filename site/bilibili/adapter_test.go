package bilibili

import (
	"encoding/json"
	"testing"

	"github.com/Felix2yu/liuying-engine/site"
)

func TestAdapterMatch(t *testing.T) {
	a := NewAdapter()
	yes := []string{
		"https://www.bilibili.com/video/BV1xx411c7mD",
		"https://b23.tv/abcdEFG",
		"https://m.bilibili.com/video/BV1xx411c7mD",
		"https://www.bilibili.tv/en/video/123",
		"BV1xx411c7mD",
		"av12345",
		"https://www.bilibili.com/bangumi/play/ss23615",
		"https://www.bilibili.com/bangumi/play/ep123456",
		"#在抖音记录美好生活# 分享 https://b23.tv/xyz 真的好看",
	}
	for _, u := range yes {
		if !a.Match(u) {
			t.Errorf("应认领但未认领: %q", u)
		}
	}

	no := []string{
		"",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ",
		"https://www.douyin.com/video/7123456789",
		"https://www.xiaohongshu.com/explore/abc",
		"not a url at all",
	}
	for _, u := range no {
		if a.Match(u) {
			t.Errorf("不应认领但认领了: %q", u)
		}
	}
}

func TestMediaOfPartsAndRoundTrip(t *testing.T) {
	v := &Video{
		Title:  "总标题",
		BV:     "BV1xx411c7mD",
		Cid:    "111",
		Owner:  "某UP",
		OwnerM: 42,
		Pic:    "https://i0.hdslb.com/cover.jpg",
		Pages: []biliVideoPage{
			{Page: 1, Cid: json.Number("111"), Part: "第一P", Duration: 60},
			{Page: 2, Cid: json.Number("222"), Part: "第二P", Duration: 90},
		},
	}

	m := mediaOf(v)
	if m.Site != "bilibili" || m.ID != "BV1xx411c7mD" || m.Title != "总标题" {
		t.Fatalf("Media 字段映射错误: %+v", m)
	}
	if m.Author.Name != "某UP" || m.Author.ID != "42" {
		t.Fatalf("Author 映射错误: %+v", m.Author)
	}
	if len(m.Parts) != 2 {
		t.Fatalf("Parts 数量应为 2，实际 %d", len(m.Parts))
	}
	if m.Parts[1].ID != "222" || m.Parts[1].Title != "第二P" {
		t.Fatalf("Part 字段映射错误: %+v", m.Parts[1])
	}

	// 往返：指定第 2 P，Video 应指向 cid=222 且 Pages 保留完整列表。
	got, err := videoOf(m, site.Selection{PartIndex: 2})
	if err != nil {
		t.Fatalf("videoOf 失败: %v", err)
	}
	if got.Cid != "222" {
		t.Errorf("Cid 应为 222，实际 %q", got.Cid)
	}
	if got.ActivePage != 2 {
		t.Errorf("ActivePage 应为 2，实际 %d", got.ActivePage)
	}
	if len(got.Pages) != 2 {
		t.Errorf("Pages 应保留 2 条，实际 %d", len(got.Pages))
	}
	if got.Pages[0].Part != "第一P" || got.Pages[1].Duration != 90 {
		t.Errorf("Pages 内容错误: %+v", got.Pages)
	}

	// PartIndex 越界必须是错误而不是静默取第一个。
	if _, err := videoOf(m, site.Selection{PartIndex: 3}); err == nil {
		t.Error("PartIndex 越界应报错")
	}

	// 零值 PartIndex 视为第 1 P。
	first, err := videoOf(m, site.Selection{})
	if err != nil {
		t.Fatalf("零值 PartIndex 不应报错: %v", err)
	}
	if first.Cid != "111" || first.ActivePage != 1 {
		t.Errorf("零值 PartIndex 应取第 1 P，实际 cid=%q page=%d", first.Cid, first.ActivePage)
	}
}

func TestMediaOfEmptyPages(t *testing.T) {
	// 单 P 视频的 Pages 可能为空，此时应至少合成一条，避免下游拿到 0 个 Part。
	m := mediaOf(&Video{Title: "单P", BV: "BV1xx411c7mD", Cid: "999"})
	if len(m.Parts) != 1 {
		t.Fatalf("应合成 1 条 Part，实际 %d", len(m.Parts))
	}
	if m.Parts[0].ID != "999" {
		t.Errorf("合成 Part 的 ID 应为 cid，实际 %q", m.Parts[0].ID)
	}
}

func TestResolutionForHeight(t *testing.T) {
	cases := map[int]string{
		0: "", 360: "360p", 480: "480p", 720: "720p",
		1080: "1080p", 1440: "1080p", 2160: "4k", 4320: "8k",
	}
	for h, want := range cases {
		if got := resolutionForHeight(h); got != want {
			t.Errorf("height=%d: want %q, got %q", h, want, got)
		}
	}
}

func TestStreamConfigFromSelection(t *testing.T) {
	sel := site.Selection{
		VideoQuality:   127,
		AudioQuality:   30280,
		VCodecPriority: []string{"av1", "hevc"},
		MaxHeight:      1080,
		AudioOnly:      false,
	}
	cfg := streamConfigFrom(sel)
	if cfg.VideoQuality != 127 || cfg.AudioQuality != 30280 {
		t.Errorf("画质 id 未透传: %+v", cfg)
	}
	if cfg.Resolution != "1080p" {
		t.Errorf("MaxHeight 应映射成 1080p，实际 %q", cfg.Resolution)
	}
	if len(cfg.VCodecPriority) != 2 || cfg.VCodecPriority[0] != "av1" {
		t.Errorf("编码优先级未透传: %+v", cfg.VCodecPriority)
	}
}
