package x

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Felix2yu/bili-dl/site"
)

// videoTweet 是带视频的推文响应样本。
//
// 字段形状取自 cdn.syndication.twimg.com/tweet-result 的真实响应：
// 信封为 id_str / text / user.*，视频信息在 mediaDetails[].video_info.variants。
// 视频部分没有抓到可用的公开推文，故按该结构构造；解析代码对缺字段是容错的。
const videoTweet = `{
  "id_str": "1234567890123456789",
  "text": "一段视频\n第二行不该进标题",
  "created_at": "2026-11-20T10:00:00.000Z",
  "user": {"id_str": "42", "name": "示例作者", "screen_name": "example"},
  "mediaDetails": [
    {
      "type": "video",
      "media_url_https": "https://pbs.twimg.com/cover1.jpg",
      "video_info": {
        "aspect_ratio": [1920, 1080],
        "duration_millis": 30000,
        "variants": [
          {"content_type": "application/x-mpegURL", "url": "https://video.twimg.com/a/pl.m3u8"},
          {"bitrate": 256000, "content_type": "video/mp4", "url": "https://video.twimg.com/a/256.mp4"},
          {"bitrate": 2176000, "content_type": "video/mp4", "url": "https://video.twimg.com/a/2176.mp4"},
          {"bitrate": 832000, "content_type": "video/mp4", "url": "https://video.twimg.com/a/832.mp4"}
        ]
      }
    },
    {
      "type": "video",
      "media_url_https": "https://pbs.twimg.com/cover2.jpg",
      "video_info": {
        "aspect_ratio": [1, 1],
        "variants": [
          {"content_type": "video/mp4", "url": "https://video.twimg.com/b/only.mp4"}
        ]
      }
    }
  ]
}`

// textOnlyTweet 是纯文字推文的响应样本（字段集合取自真实响应）。
const textOnlyTweet = `{
  "id_str": "20",
  "text": "just setting up my twttr",
  "user": {"id_str": "12", "name": "jack", "screen_name": "jack"}
}`

func newServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		if r.URL.Query().Get("id") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMatch(t *testing.T) {
	a := NewAdapter()
	yes := []string{
		"https://x.com/example/status/1234567890123456789",
		"https://twitter.com/example/status/1234567890123456789?s=20",
		"https://mobile.twitter.com/example/status/1234567890123456789",
		"https://x.com/i/web/status/1234567890123456789",
		"1234567890123456789",
	}
	for _, u := range yes {
		if !a.Match(u) {
			t.Errorf("应认领但未认领: %q", u)
		}
	}
	no := []string{
		"",
		"https://www.bilibili.com/video/BV1xx411c7mD",
		"https://www.youtube.com/watch?v=abc",
		"https://x.com/example", // 没有 status 段
	}
	for _, u := range no {
		if a.Match(u) {
			t.Errorf("不应认领但认领了: %q", u)
		}
	}
}

func TestResolveVideoTweet(t *testing.T) {
	srv := newServer(t, http.StatusOK, videoTweet)
	a := &Adapter{Endpoint: srv.URL}

	media, err := a.Resolve(context.Background(), "https://x.com/example/status/1234567890123456789", site.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if len(media) != 1 {
		t.Fatalf("应返回 1 条媒体，实际 %d", len(media))
	}
	m := media[0]
	if m.Site != "x" || m.ID != "1234567890123456789" {
		t.Errorf("媒体标识错误: %+v", m)
	}
	// 标题只取正文首行。
	if m.Title != "一段视频" {
		t.Errorf("Title = %q", m.Title)
	}
	if m.Author.Name != "示例作者" || m.Author.ID != "42" {
		t.Errorf("Author 错误: %+v", m.Author)
	}
	if m.Cover != "https://pbs.twimg.com/cover1.jpg" {
		t.Errorf("Cover = %q", m.Cover)
	}
	if len(m.Parts) != 2 {
		t.Errorf("两条视频应解析出 2 个 Part，实际 %d", len(m.Parts))
	}
	if m.Extra["tweet_id"] != "1234567890123456789" {
		t.Errorf("Extra.tweet_id = %q", m.Extra["tweet_id"])
	}
}

func TestResolveTextOnlyIsNotFound(t *testing.T) {
	srv := newServer(t, http.StatusOK, textOnlyTweet)
	a := &Adapter{Endpoint: srv.URL}

	_, err := a.Resolve(context.Background(), "https://x.com/jack/status/20", site.ResolveOptions{})
	if err == nil {
		t.Fatal("纯文字推文应报错")
	}
	re, ok := site.AsResolveError(err)
	if !ok || re.Code != site.ErrNotFound {
		t.Errorf("应映射为 not_found，实际 %v", err)
	}
}

func TestResolveProtectedIsAuthRequired(t *testing.T) {
	srv := newServer(t, http.StatusNotFound, "")
	a := &Adapter{Endpoint: srv.URL}

	_, err := a.Resolve(context.Background(), "https://x.com/example/status/1234567890123456789", site.ResolveOptions{})
	re, ok := site.AsResolveError(err)
	if !ok || re.Code != site.ErrAuthRequired {
		t.Errorf("404 应映射为 auth_required（受保护/已删无法区分），实际 %v", err)
	}
}

func TestResolveMalformedJSON(t *testing.T) {
	srv := newServer(t, http.StatusOK, `{"broken":`)
	a := &Adapter{Endpoint: srv.URL}

	_, err := a.Resolve(context.Background(), "https://x.com/example/status/1234567890123456789", site.ResolveOptions{})
	re, ok := site.AsResolveError(err)
	if !ok || re.Code != site.ErrUpstreamChanged {
		t.Errorf("坏 JSON 应映射为 upstream_changed，实际 %v", err)
	}
}

func TestStreamsOrdersHLSFirstThenMP4ByBitrate(t *testing.T) {
	srv := newServer(t, http.StatusOK, videoTweet)
	a := &Adapter{Endpoint: srv.URL}

	media := &site.Media{
		Site:  "x",
		ID:    "1234567890123456789",
		Parts: []site.Part{{Index: 1}, {Index: 2}},
		Extra: map[string]string{"tweet_id": "1234567890123456789"},
	}
	set, err := a.Streams(context.Background(), media, site.Selection{})
	if err != nil {
		t.Fatalf("Streams 失败: %v", err)
	}
	if len(set.Videos) != 4 {
		t.Fatalf("应有 1 条 HLS + 3 条 mp4，实际 %d", len(set.Videos))
	}
	if !set.Videos[0].HLS {
		t.Error("HLS 变体应排在第一位")
	}
	// mp4 按码率降序。
	if set.Videos[1].Bandwidth != 2176000 || set.Videos[2].Bandwidth != 832000 || set.Videos[3].Bandwidth != 256000 {
		t.Errorf("mp4 未按码率降序: %+v", set.Videos[1:])
	}
	if set.Videos[1].Width != 1920 || set.Videos[1].Height != 1080 {
		t.Errorf("分辨率未从 aspect_ratio 推导: %+v", set.Videos[1])
	}
	if set.Videos[0].Headers.Get("Referer") != "https://x.com/" {
		t.Error("码流需要带 Referer")
	}
}

func TestStreamsSelectsSecondMedia(t *testing.T) {
	srv := newServer(t, http.StatusOK, videoTweet)
	a := &Adapter{Endpoint: srv.URL}
	media := &site.Media{
		Site:  "x",
		ID:    "1234567890123456789",
		Parts: []site.Part{{Index: 1}, {Index: 2}},
		Extra: map[string]string{"tweet_id": "1234567890123456789"},
	}
	set, err := a.Streams(context.Background(), media, site.Selection{PartIndex: 2})
	if err != nil {
		t.Fatalf("Streams 失败: %v", err)
	}
	if len(set.Videos) != 1 || !strings.HasSuffix(set.Videos[0].URL, "/b/only.mp4") {
		t.Errorf("第二个媒体应只有 only.mp4，实际 %+v", set.Videos)
	}
}

func TestStreamsPartOutOfRange(t *testing.T) {
	a := NewAdapter()
	media := &site.Media{
		Site:  "x",
		ID:    "1",
		Parts: []site.Part{{Index: 1}},
		Extra: map[string]string{"tweet_id": "1"},
	}
	_, err := a.Streams(context.Background(), media, site.Selection{PartIndex: 5})
	re, ok := site.AsResolveError(err)
	if !ok || re.Code != site.ErrNotFound {
		t.Errorf("越界应报 not_found，实际 %v", err)
	}
}

func TestFixturesParse(t *testing.T) {
	// testdata 是解析逻辑的回归基线：站点改版时这里会先红。
	for name, wantParts := range map[string]int{
		"tweet-video.json":     2,
		"tweet-text-only.json": 0,
	} {
		path := filepath.Join("testdata", name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", name, err)
		}
		var tw tweet
		if err := json.Unmarshal(body, &tw); err != nil {
			t.Fatalf("%s 解析失败: %v", name, err)
		}
		if len(tw.MediaDetails) != wantParts {
			t.Errorf("%s: mediaDetails = %d, want %d", name, len(tw.MediaDetails), wantParts)
		}
	}
}
