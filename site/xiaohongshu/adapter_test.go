package xiaohongshu

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Felix2yu/bili-dl/site"
)

const noteID = "6b06d8f2000000002b006721"

// stateHTML 构造带干扰的首屏数据：字符串里有花括号，且含裸 undefined。
func stateHTML(noteJSON string) string {
	return `<!doctype html><html><body><div data-x="}">噪声}</div>` +
		`<script>window.__SSR__=true</script>` +
		`<script>window.__INITIAL_STATE__=` + noteJSON + `</script></body></html>`
}

func videoState() string {
	return fmt.Sprintf(`{"note":{"noteDetailMap":{"%s":{"note":{`+
		`"noteId":"%s","type":"video","title":"冷酷 } 测试","desc":"说明","xsecToken":"tok",`+
		`"user":{"userId":"u1","nickname":"作者"},"imageList":[{"urlDefault":"http://sns-webpic-qc.xhscdn.com/a/b/1.jpg"}],`+
		`"video":{"media":{"video":{"duration":8},"stream":{"h264":[`+
		`{"masterUrl":"http://sns-video-v6.xhscdn.com/v/low.mp4?sign=x","width":720,"height":1280,"avgBitrate":1000,"duration":7800,"videoCodec":"h264","format":"mp4"},`+
		`{"masterUrl":"http://sns-video-v6.xhscdn.com/v/high.mp4?sign=y","width":1440,"height":2560,"avgBitrate":2000,"duration":7800,"videoCodec":"h264","format":"mp4"}`+
		`]}}},"extra":undefined}}}}}`, noteID, noteID)
}

func imageState() string {
	return fmt.Sprintf(`{"note":{"noteDetailMap":{"%s":{"note":{`+
		`"noteId":"%s","type":"normal","title":"","desc":"图文首行\n第二行","xsecToken":"tok2",`+
		`"user":{"userId":"u2","nickname":"图文作者"},`+
		`"imageList":[`+
		`{"url":"http://sns-webpic-qc.xhscdn.com/a/1.jpg","urlPre":"http://sns-webpic-qc.xhscdn.com/a/1-pre.jpg","urlDefault":""},`+
		`{"urlDefault":"http://sns-webpic-qc.xhscdn.com/a/2.webp"}`+
		`],"video":{},"extra":undefined}}}}}`, noteID, noteID)
}

func TestMatch(t *testing.T) {
	a := NewAdapter()
	cases := map[string]bool{
		"https://www.xiaohongshu.com/explore/" + noteID + "?xsec_token=abc": true,
		"https://www.xiaohongshu.com/discovery/item/" + noteID:              true,
		"冷酷 https://xhslink.cn/o/16vUpJWByhV 拷走链接，去【小红书】逛逛分享~":              true,
		"https://xhslink.cn/o/16vUpJWByhV":                                  true,
		"https://www.bilibili.com/video/BV1GJ411x7h7":                       false,
		"https://www.xiaohongshu.com/":                                      false,
	}
	for raw, want := range cases {
		if got := a.Match(raw); got != want {
			t.Errorf("Match(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestResolveVideoAndStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, stateHTML(videoState()))
	}))
	defer srv.Close()

	a := NewAdapter()
	media, err := a.Resolve(context.Background(), srv.URL+"/explore/"+noteID+"?xsec_token=tok", site.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if len(media) != 1 {
		t.Fatalf("应有 1 条媒体，实际 %d", len(media))
	}
	m := media[0]
	if m.Site != "xiaohongshu" || m.ID != noteID || m.Title != "冷酷 } 测试" {
		t.Errorf("媒体字段错误: %+v", m)
	}
	if m.Author.Name != "作者" || m.Cover == "" || m.Duration.Seconds() != 7.8 {
		t.Errorf("作者/封面/时长错误: author=%+v cover=%s duration=%v", m.Author, m.Cover, m.Duration)
	}
	if len(m.Parts) != 1 || m.Extra["xsec_token"] != "tok" {
		t.Errorf("Parts/Extra 错误: %+v %+v", m.Parts, m.Extra)
	}

	set, err := a.Streams(context.Background(), m, site.Selection{})
	if err != nil {
		t.Fatalf("Streams 失败: %v", err)
	}
	if len(set.Videos) != 2 {
		t.Fatalf("应有 2 档码流，实际 %d", len(set.Videos))
	}
	if !strings.Contains(set.Videos[0].URL, "high.mp4") {
		t.Errorf("应按码率降序，首条 URL=%s", set.Videos[0].URL)
	}
	for _, s := range set.Videos {
		if !strings.HasPrefix(s.URL, "https://") || s.Kind != "video" || s.Container != "mp4" {
			t.Errorf("码流字段错误: %+v", s)
		}
		if s.Headers.Get("Referer") != "https://www.xiaohongshu.com/" {
			t.Errorf("缺少 Referer: %+v", s.Headers)
		}
	}
}

func TestResolveShortLinkAndImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/o/") {
			http.Redirect(w, r, "/discovery/item/"+noteID+"?xsec_token=tok2&xsec_source=app_share", http.StatusFound)
			return
		}
		_, _ = fmt.Fprint(w, stateHTML(imageState()))
	}))
	defer srv.Close()

	shared := "冷酷 " + srv.URL + "/o/16vUpJWByhV 拷走链接，去【小红书】逛逛分享~"
	adapter := &Adapter{ShortHosts: []string{"127.0.0.1"}}
	media, err := adapter.Resolve(context.Background(), shared, site.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if len(media) != 1 {
		t.Fatalf("应有 1 条媒体，实际 %d", len(media))
	}
	m := media[0]
	if m.Title != "图文首行" || m.Author.Name != "图文作者" {
		t.Errorf("标题/作者回退失败: %+v", m)
	}
	if len(m.Parts) != 2 {
		t.Fatalf("应有 2 张图片，实际 %d", len(m.Parts))
	}
	if !strings.Contains(m.Extra["page_url"], "xsec_token=tok2") {
		t.Errorf("展开短链后应保留 xsec_token: %s", m.Extra["page_url"])
	}

	set, err := adapter.Streams(context.Background(), m, site.Selection{PartIndex: 2})
	if err != nil {
		t.Fatalf("Streams 失败: %v", err)
	}
	if len(set.Videos) != 1 {
		t.Fatalf("第 2 张图片应只有 1 条流，实际 %d", len(set.Videos))
	}
	s := set.Videos[0]
	if s.Kind != "image" || s.Container != "webp" || !strings.HasPrefix(s.URL, "https://") {
		t.Errorf("图片码流错误: %+v", s)
	}
}

func TestShortLinkLoginRedirectPath(t *testing.T) {
	page := "/discovery/item/" + noteID + "?xsec_token=tok3"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/o/") {
			http.Redirect(w, r, "/login?redirectPath="+url.QueryEscape(page), http.StatusFound)
			return
		}
		if r.URL.Path == "/login" {
			return // 短链的第一跳，写空响应让客户端继续跟到 redirectPath
		}
		if r.URL.Path != "/discovery/item/"+noteID {
			t.Errorf("未跳回 redirectPath，实际 %s", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, stateHTML(videoState()))
	}))
	defer srv.Close()

	adapter := &Adapter{ShortHosts: []string{"127.0.0.1"}}
	media, err := adapter.Resolve(context.Background(), srv.URL+"/o/token", site.ResolveOptions{})
	if err != nil {
		t.Fatalf("登录页 redirectPath 回退失败: %v", err)
	}
	if len(media) != 1 || media[0].Extra["xsec_token"] != "tok" {
		t.Errorf("回退后解析结果错误: %+v", media)
	}
}

func TestResolveMissingMarkerReportsUpstreamChanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `<html>页面改版，没有首屏数据</html>`)
	}))
	defer srv.Close()

	_, err := NewAdapter().Resolve(context.Background(), srv.URL+"/explore/"+noteID, site.ResolveOptions{})
	se, ok := site.AsResolveError(err)
	if !ok || se.Code != site.ErrUpstreamChanged {
		t.Fatalf("应报 upstream_changed，实际 %v", err)
	}
}
