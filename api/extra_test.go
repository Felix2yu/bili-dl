package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDanmakuXMLToASS(t *testing.T) {
	xmlBody := []byte(`<?xml version="1.0" encoding="UTF-8"?><i><chatserver>chat.bilibili.com</chatserver><d p="0.50000,5,25,16777215,1668765706,0,25621c3d,1188451900626970624,10">第一条</d><d p="94.29000,4,25,16711680,1,0,x,2,10">底部,弹幕</d></i>`)
	got := danmakuXMLToASS(xmlBody, "测试")
	if !strings.Contains(got, "[Script Info]") || !strings.Contains(got, "[Events]") {
		t.Fatalf("ASS 头缺失: %s", got)
	}
	if !strings.Contains(got, "第一条") {
		t.Error("缺少弹幕文本")
	}
	if !strings.Contains(got, "0:00:00.50") {
		t.Errorf("起始时间错误: %s", got)
	}
	if !strings.Contains(got, "&H00FFFFFF") {
		t.Errorf("白色 RGB 转换错误: %s", got)
	}
	// 含逗号的文本应被替换，避免破坏 ASS 字段分隔。
	if strings.Contains(got, "底部,弹幕") {
		t.Error("逗号未转义")
	}
}

func TestDanmakuXMLToASSInvalid(t *testing.T) {
	if got := danmakuXMLToASS([]byte("not xml"), "x"); got != "" {
		t.Errorf("非法 XML 应返回空，got %q", got)
	}
}

func TestASSTime(t *testing.T) {
	cases := map[float64]string{
		0:    "0:00:00.00",
		1.5:  "0:00:01.50",
		61.2: "0:01:01.20",
		3661: "1:01:01.00",
	}
	for in, want := range cases {
		if got := assTime(in); got != want {
			t.Errorf("assTime(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestSubtitleJSONToSRT(t *testing.T) {
	body := []byte(`{"body":[{"from":0.0,"to":2.5,"location":2,"content":"你好\n世界"},{"from":3.0,"to":4.0,"location":2,"content":"第二句"}]}`)
	got := subtitleJSONToSRT(body)
	if !strings.Contains(got, "00:00:00,000 --> 00:00:02,500") {
		t.Errorf("时间轴错误: %s", got)
	}
	if !strings.Contains(got, "你好 世界") {
		t.Errorf("换行未合并: %s", got)
	}
	if !strings.Contains(got, "2\n00:00:03,000 --> 00:00:04,000") {
		t.Errorf("第二句序列号错误: %s", got)
	}
}

func TestSubtitleJSONToSRTInvalid(t *testing.T) {
	if got := subtitleJSONToSRT([]byte("{}")); got != "" {
		t.Errorf("空 body 应返回空，got %q", got)
	}
}

func TestSRTTime(t *testing.T) {
	if got := srtTime(61.234); got != "00:01:01,234" {
		t.Errorf("srtTime = %q", got)
	}
}

func TestUGCSeasonPageShapeCompat(t *testing.T) {
	// B站新版 ugc_season.episodes[].page 从整数改为对象，旧版本仍可能为整数。
	// 两种形态都应能解析，并归一化到 Page，合集展开不受影响。
	newShape := []byte(`{"code":0,"data":{"bvid":"BV1","title":"主视频","owner":{"mid":123,"name":"UP"},"pages":[{"page":1,"cid":9,"part":"P1"}],"ugc_season":{"id":5,"title":"合集名","sections":[{"id":1,"episodes":[
		{"bvid":"BV2","cid":10,"title":"第二集","page":{"cid":10,"page":2,"from":"vupload","part":"P2","duration":197}},
		{"bvid":"BV3","cid":11,"title":"第三集","page":3}
	]}]}}}`)
	var resp biliVideoInfoResp
	if err := json.Unmarshal(newShape, &resp); err != nil {
		t.Fatalf("新版 page 对象解析失败: %v", err)
	}
	eps := resp.Data.UGCSeason.Sections[0].Episodes
	if len(eps) != 2 || eps[0].Page != 2 || eps[1].Page != 3 {
		t.Fatalf("page 归一化错误: %+v", eps)
	}
	v, err := applyVideoInfo(&Video{BV: "BV1"}, &resp)
	if err != nil {
		t.Fatal(err)
	}
	parts := ExpandUGCSeason(v)
	if len(parts) != 2 || parts[0].BV != "BV2" || parts[1].BV != "BV3" {
		t.Errorf("合集展开错误: %+v", parts)
	}
}

func TestPGCStructureRoundTrip(t *testing.T) {
	// 验证 ugcSeason/biliVideoInfoResp 的字段映射，防止 JSON tag 拼写错误。
	raw := []byte(`{"code":0,"data":{"bvid":"BV1","title":"主视频","owner":{"mid":123,"name":"UP"},"pages":[{"page":1,"cid":9,"part":"P1"}],"ugc_season":{"id":5,"title":"合集名","sections":[{"id":1,"episodes":[{"bvid":"BV2","cid":10,"title":"第二集"},{"bvid":"BV3","cid":11,"title":"第三集"}]}]}}}`)
	var resp biliVideoInfoResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	v := &Video{BV: "BV1"}
	if _, err := applyVideoInfo(v, &resp); err != nil {
		t.Fatal(err)
	}
	if v.Owner != "UP" || v.OwnerM != 123 {
		t.Errorf("owner 未填充: %+v", v)
	}
	if v.UGCSeason == nil || v.UGCSeason.Title != "合集名" {
		t.Fatalf("合集未填充: %+v", v.UGCSeason)
	}
	parts := ExpandUGCSeason(v)
	if len(parts) != 2 || parts[0].BV != "BV2" || parts[1].BV != "BV3" {
		t.Errorf("合集展开错误: %+v", parts)
	}
	if parts[0].Owner != "UP" {
		t.Errorf("合集中 owner 未继承: %+v", parts[0])
	}
}
