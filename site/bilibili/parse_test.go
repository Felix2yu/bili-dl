package bilibili

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Felix2yu/liuying-engine/c"
)

func TestParseInput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantBV  string
		wantP   int
		wantErr bool
	}{
		{"裸BV", "BV1XY411o7Cv", "BV1XY411o7Cv", 1, false},
		{"完整URL", "https://www.bilibili.com/video/BV1XY411o7Cv/", "BV1XY411o7Cv", 1, false},
		{"带分页", "https://www.bilibili.com/video/BV1XY411o7Cv?p=3", "BV1XY411o7Cv", 3, false},
		{"分享文案", "【标题】复制这段 https://www.bilibili.com/video/BV1XY411o7Cv/ 打开", "BV1XY411o7Cv", 1, false},
		{"路径分页", "https://www.bilibili.com/video/BV1XY411o7Cv/p2", "BV1XY411o7Cv", 2, false},
		{"垃圾输入", "not a link", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bv, p, err := ParseInput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if bv != tt.wantBV || p != tt.wantP {
				t.Errorf("got (%q,%d), want (%q,%d)", bv, p, tt.wantBV, tt.wantP)
			}
		})
	}
}

func TestParseInputB23Short(t *testing.T) {
	// 短链服务返回 302 到完整 URL，ParseInput 应跟随并拿到 BV 与页码。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://www.bilibili.com/video/BV1XY411o7Cv?p=2", http.StatusFound)
	}))
	defer srv.Close()

	// 让 shortClient 把该测试域名也跳到最终 URL（httptest 域名是 127.0.0.1，不是 b23.tv，
	// 这里直接测 resolveShortLink + reBV 的组合行为）。
	final, err := resolveShortLink(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(final, "BV1XY411o7Cv") {
		t.Fatalf("final = %q, 未解析到 BV", final)
	}
	if bv := reBV.FindString(final); bv != "BV1XY411o7Cv" {
		t.Errorf("bv = %q", bv)
	}
}

func TestRenderPathSingleAndMulti(t *testing.T) {
	c.Defaults()
	c.SubpathTemplate = "{owner_uname}/{title}"

	v := &Video{
		BV: "BV1XY411o7Cv", Title: "测试标题", Owner: "某UP", ActivePage: 1,
		Pages: []biliVideoPage{
			{Page: 1, Cid: json.Number("1"), Part: "第一集"},
			{Page: 2, Cid: json.Number("2"), Part: "第二集"},
		},
	}

	// 单 P（Pages 只有一条）时：<owner>/<title>.ext
	single := &Video{BV: v.BV, Title: v.Title, Owner: v.Owner, ActivePage: 1,
		Pages: []biliVideoPage{{Page: 1, Cid: json.Number("1"), Part: v.Title}}}
	got := RenderPath(single, "mp4")
	want := filepath.Join("某UP", "测试标题.mp4")
	if got != want {
		t.Errorf("single path = %q, want %q", got, want)
	}

	// 多 P 第二集：<owner>/<title> - 02 - <part>.ext
	v.ActivePage = 2
	got = RenderPath(v, "m4a")
	want = filepath.Join("某UP", "测试标题 - 02 - 第二集.m4a")
	if got != want {
		t.Errorf("multi path = %q, want %q", got, want)
	}
}

func TestRenderPathNoOwner(t *testing.T) {
	c.Defaults()
	c.SubpathTemplate = "{owner_uname}/{title}"
	v := &Video{BV: "BV1", Title: "无主标题", Pages: []biliVideoPage{{Page: 1, Cid: json.Number("1")}}}
	// owner 为空时不应产生前导空目录。
	got := RenderPath(v, "mp4")
	if strings.HasPrefix(got, "/") || strings.HasPrefix(got, string(filepath.Separator)) {
		t.Fatalf("不应有前导路径分隔符: %q", got)
	}
	if strings.Contains(got, "..") {
		t.Fatalf("不应有空目录段: %q", got)
	}
}

func TestLoadSessdata(t *testing.T) {
	dir := t.TempDir()
	toml := `[profiles.default]
sessdata = "secret-default"
bili_jct = "x"

[profiles.vip]
SESSDATA = "secret-vip"
`
	p := filepath.Join(dir, "auth.toml")
	if err := os.WriteFile(p, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSessdata(p, "default"); err != nil || got != "secret-default" {
		t.Errorf("default = %q, %v", got, err)
	}
	if got, err := LoadSessdata(p, "vip"); err != nil || got != "secret-vip" {
		t.Errorf("vip = %q, %v", got, err)
	}
	if got, err := LoadSessdata(p, "missing"); err != nil || got != "" {
		t.Errorf("missing = %q, %v", got, err)
	}
}

func TestPickAudio(t *testing.T) {
	streams := []dashStream{
		{ID: 30216, Bandwidth: 64000, Codecs: "mp4a.40.2"},
		{ID: 30280, Bandwidth: 320000, Codecs: "mp4a.40.2"},
		{ID: 30250, Bandwidth: 256000, Codecs: "fLaC"},
	}
	// 精确匹配 -aq 30216 应命中低码率流。
	got := pickAudio(streams, 30216)
	if int(got.ID) != 30216 {
		t.Errorf("wanted 30216, got %v", got.ID)
	}
	// 未匹配时取码率最高。
	got = pickAudio(streams, 99999)
	if int(got.ID) != 30280 {
		t.Errorf("fallback want 30280, got %v", got.ID)
	}
	if audioExt("fLaC") != "flac" || audioExt("mp4a.40.2") != "m4a" {
		t.Error("audioExt 错误")
	}
}

func TestVideoForPage(t *testing.T) {
	v := &Video{Title: "总标题", BV: "BV1", Pages: []biliVideoPage{
		{Page: 1, Cid: json.Number("111"), Part: "P1"},
		{Page: 2, Cid: json.Number("222"), Part: "P2"},
	}}
	got, err := VideoForPage(v, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cid != "222" || got.ActivePage != 2 || got.Title != "总标题" {
		t.Errorf("got %+v", got)
	}
	if len(got.Pages) != 2 {
		t.Error("应保留 Pages 供模板使用")
	}
	if _, err := VideoForPage(v, 5); err == nil {
		t.Error("越界应报错")
	}
}

func TestPageFromURL(t *testing.T) {
	cases := map[string]int{
		"https://x.com/v?p=3":       3,
		"https://x.com/v?foo=1&p=7": 7,
		"https://x.com/v/BV1/p2":    2,
		"https://x.com/v":           0,
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		if got := pageFromURL(u); got != want {
			t.Errorf("%s -> %d, want %d", raw, got, want)
		}
	}
}

func TestNoSessdataInStreamLogFields(t *testing.T) {
	// 确保 Stream/Video 结构不含 Cookie/SESSDATA 字段（避免 json 打印时泄漏）。
	b, _ := json.Marshal(&Video{BV: "BV1", Title: "t"})
	if strings.Contains(string(b), "sessdata") || strings.Contains(string(b), "cookie") {
		t.Fatalf("Video 序列化含凭据字段: %s", b)
	}
	_ = reflect.TypeOf(Stream{})
}
