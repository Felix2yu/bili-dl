package api

import (
	"encoding/json"
	"testing"
)

func TestParsePGCSInput(t *testing.T) {
	cases := []struct {
		in       string
		wantKind string
		wantID   string
	}{
		{"ss23615", "ss", "23615"},
		{"ep123456", "ep", "123456"},
		{"https://www.bilibili.com/bangumi/play/ss38382", "ss", "38382"},
		{"https://www.bilibili.com/bangumi/play/ep999", "ep", "999"},
		{"season_id=777", "ss", "777"},
		{"https://www.bilibili.com/video/BV1XY411o7Cv", "", ""},
	}
	for _, tt := range cases {
		kind, id := ParsePGCSInput(tt.in)
		if kind != tt.wantKind || id != tt.wantID {
			t.Errorf("ParsePGCSInput(%q) = (%q,%q), want (%q,%q)", tt.in, kind, id, tt.wantKind, tt.wantID)
		}
	}
}

func TestPGSSeasonRespMapping(t *testing.T) {
	// 验证 pgcSeasonResp 字段映射：data.season_title、data.episodes、
	// data.section[].episode 都要能正确解析并合并去重。
	raw := []byte(`{"code":0,"data":{"season_id":1,"title":"番剧标题","season_title":"番剧季标题","episodes":[{"ep_id":10,"cid":100,"bvid":"BV10","title":"1","long_title":"第一集"},{"ep_id":11,"cid":101,"bvid":"BV11","title":"2","long_title":"第二集"}],"section":[{"id":1,"title":"正片","episode":[{"ep_id":10,"cid":100,"bvid":"BV10","title":"1"}]}]}}`)
	var resp pgcSeasonResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Code != 0 || resp.Data.SeasonTitle != "番剧季标题" {
		t.Fatalf("响应解析错误: %+v", resp)
	}
	if len(resp.Data.Episodes) != 2 || len(resp.Data.Section) != 1 {
		t.Fatalf("剧集/section 解析错误")
	}
	// 去重逻辑：episodes 与 section 都有 ep_id=10，应只保留一次。
	seen := map[int64]bool{}
	count := 0
	add := func(eps []pgcEpisode) {
		for _, e := range eps {
			if seen[e.EpID] {
				continue
			}
			seen[e.EpID] = true
			count++
		}
	}
	add(resp.Data.Episodes)
	for _, s := range resp.Data.Section {
		add(s.Episode)
	}
	if count != 2 {
		t.Errorf("去重后应有 2 集，got %d", count)
	}
}

func TestVideoPGCMarkers(t *testing.T) {
	v := Video{BV: "BV1", PGC: true, EpID: "123"}
	b, _ := json.Marshal(&v)
	var back Video
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.PGC || back.EpID != "123" {
		t.Errorf("PGC 标记未序列化: %s", b)
	}
}
