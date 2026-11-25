package douyin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Felix2yu/bili-dl/site"
)

const aweme = "7710557661879733555"

func shellHTML() string {
	return `<html><script>window._ROUTER_DATA = {"loaderData":{"video_(id)/page":` +
		`{"itemId":"` + aweme + `","share_sign":"xxx","abParams":{}}},"errors":{}}</script></html>`
}

func playableHTML() string {
	return `<html><script>window._ROUTER_DATA = {"loaderData":{"video_(id)/page":{` +
		`"itemId":"` + aweme + `","aweme_detail":{"desc":"看三国杀插画猜人物",` +
		`"author":{"nickname":"攻心与汉桑"},` +
		`"video":{"play_addr":{"url_list":["https://aweme.snssdk.com/aweme/v1/play/?a=1"]},` +
		`"cover":{"url_list":["https://p3-imgen.snssdk.com/cover.jpg"]}}}}}}}}</script></html>`
}

func TestMatch(t *testing.T) {
	a := NewAdapter()
	cases := map[string]bool{
		"6.64 复制打开抖音，看看【作品】标题 https://v.douyin.com/fy9rPadJ1SU/ :9pm":    true,
		"https://www.iesdouyin.com/share/video/" + aweme + "/?region=CN": true,
		"https://www.douyin.com/video/" + aweme:                          true,
		"https://www.bilibili.com/video/BV1GJ411x7h7":                    false,
		"https://v.douyin.com/":                                          false,
	}
	for raw, want := range cases {
		if got := a.Match(raw); got != want {
			t.Errorf("Match(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestResolveShellReportsNoPlayable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, shellHTML())
	}))
	defer srv.Close()

	_, err := NewAdapter().Resolve(context.Background(), srv.URL+"/video/"+aweme, site.ResolveOptions{})
	se, ok := site.AsResolveError(err)
	if !ok || se.Code != site.ErrUpstreamChanged {
		t.Fatalf("应报 upstream_changed，实际 %v", err)
	}
	if !strings.Contains(se.Message, "风控壳") {
		t.Errorf("错误信息应说明原因，实际 %q", se.Message)
	}
}

func TestResolveShortLinkAndPlayableStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fy9rPadJ1SU") {
			http.Redirect(w, r, "/share/video/"+aweme+"/?region=CN", http.StatusFound)
			return
		}
		_, _ = fmt.Fprint(w, playableHTML())
	}))
	defer srv.Close()

	adapter := &Adapter{ShortHosts: []string{"127.0.0.1"}}
	shared := "复制打开抖音，看看作品 " + srv.URL + "/fy9rPadJ1SU/ :9pm"
	media, err := adapter.Resolve(context.Background(), shared, site.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if len(media) != 1 {
		t.Fatalf("应有 1 条媒体，实际 %d", len(media))
	}
	m := media[0]
	if m.ID != aweme || m.Title != "看三国杀插画猜人物" || m.Author.Name != "攻心与汉桑" {
		t.Errorf("媒体字段错误: %+v", m)
	}

	set, err := adapter.Streams(context.Background(), m, site.Selection{})
	if err != nil {
		t.Fatalf("Streams 失败: %v", err)
	}
	if len(set.Videos) != 1 || !strings.Contains(set.Videos[0].URL, "play/?a=1") {
		t.Fatalf("只应取播放直链，实际 %+v", set.Videos)
	}
	if set.Videos[0].Kind != "video" || set.Videos[0].Headers.Get("Referer") != "https://www.douyin.com/" {
		t.Errorf("码流字段错误: %+v", set.Videos[0])
	}
}

func TestCollectPlayableIgnoresCover(t *testing.T) {
	const raw = `{"loaderData":{"video_(id)/page":{"aweme_detail":{"desc":"标题",` +
		`"author":{"nickname":"作者"},"video":{"play_addr":{"url_list":["https://v/play"]},` +
		`"cover":{"url_list":["https://v/cover.jpg"]}}}}}}`
	var state any
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatal(err)
	}
	info, urls := collectPlayable(state)
	if len(urls) != 1 || strings.Contains(urls[0], "cover") {
		t.Errorf("不应把封面当视频: %v", urls)
	}
	if info.title == "" || info.author == "" {
		t.Errorf("未提取标题/作者: %+v", info)
	}
}
