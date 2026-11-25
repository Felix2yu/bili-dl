// Package douyin 是抖音的站点适配器。
//
// 能力边界：分享页首屏通常只有 window._ROUTER_DATA 壳参数（itemId、share_sign），
// 不含 aweme_detail/play_addr；真正的详情 API 需要 a_bogus 签名。本适配器不伪造签名，
// 而是在拿不到直链时明确返回 upstream_changed 并说明原因，避免把风控误报成「链接不支持」。
//
// 同时保留通用提取：若某天分享页或 SSR 数据里重新出现 play_addr/download_addr，
// 无需改代码即可解析出直链；每日 smoke 探针会记录这一变化。
package douyin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Felix2yu/bili-dl/site"
	"github.com/Felix2yu/bili-dl/site/ssr"
)

// StateMarker 是分享页首屏数据所在的全局变量。
const StateMarker = "window._ROUTER_DATA"

const (
	mobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 " +
		"(KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
	noPlayable = "抖音分享页只有风控壳、没有视频直链（详情接口需要 a_bogus 签名或登录态）"
)

var (
	reURL     = regexp.MustCompile(`https?://[^\s，。、）】"'<>]+`)
	reAwemeID = regexp.MustCompile(`(?i)/(?:video|note|slides)/(\d{8,25})`)
)

var hosts = []string{"douyin.com", "iesdouyin.com", "douyin.com.cn"}

// Adapter 实现 site.Adapter 与 site.StreamProvider。
type Adapter struct {
	// ShortHosts 允许测试把本地服务当作短链域名；生产留空即使用 v.douyin.com。
	ShortHosts []string
}

// NewAdapter 返回抖音适配器。
func NewAdapter() *Adapter { return &Adapter{} }

// Site 返回站点标识。
func (a *Adapter) Site() string { return "douyin" }

// Match 认领抖音域名与分享口令，不发请求。
func (a *Adapter) Match(rawURL string) bool {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return false
	}
	if m := reURL.FindString(s); m != "" {
		s = strings.TrimRight(m, ".,;:!?，。；：！？)）]】")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			if reAwemeID.MatchString(u.Path) {
				return true
			}
			return a.isShortHost(host) && strings.Trim(u.Path, "/") != ""
		}
	}
	return false
}

// Resolve 展开分享短链并尝试从首屏数据里提取直链。
// 当前抖音风控下通常拿不到，返回明确的 upstream_changed。
func (a *Adapter) Resolve(ctx context.Context, rawURL string, opt site.ResolveOptions) ([]*site.Media, error) {
	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}

	target, err := a.canonicalURL(ctx, client, rawURL, opt.Auth)
	if err != nil {
		return nil, err
	}
	id := awemeID(target)
	if id == "" {
		return nil, site.NewError(site.ErrUnsupportedURL, "链接里找不到抖音作品 ID")
	}

	state, err := fetchState(ctx, client, target, opt.Auth)
	if err != nil {
		return nil, err
	}
	info, urls := collectPlayable(state)
	if len(urls) == 0 {
		return nil, site.NewError(site.ErrUpstreamChanged, "%s", noPlayable)
	}

	sort.Strings(urls)
	specs := make([]streamSpec, 0, len(urls))
	for _, u := range urls {
		specs = append(specs, streamSpec{Kind: "video", URL: u, Container: "mp4"})
	}
	raw, err := json.Marshal(specs)
	if err != nil {
		return nil, site.WrapError(site.ErrUpstreamChanged, err, "序列化码流失败")
	}
	title := info.title
	if title == "" {
		title = "抖音视频"
	}
	m := &site.Media{
		Site:   "douyin",
		ID:     id,
		Title:  title,
		Author: site.Author{Name: info.author},
		Extra: map[string]string{
			"aweme_id": id,
			"page_url": target,
			"streams":  string(raw),
		},
		Parts: []site.Part{{Index: 1, ID: id, Title: title}},
	}
	return []*site.Media{m}, nil
}

// Streams 返回 Resolve 阶段提取到的直链。
func (a *Adapter) Streams(_ context.Context, m *site.Media, sel site.Selection) (*site.StreamSet, error) {
	if m == nil || m.Extra == nil || m.Extra["streams"] == "" {
		return nil, site.NewError(site.ErrUnsupportedURL, "媒体里没有码流信息")
	}
	var specs []streamSpec
	if err := json.Unmarshal([]byte(m.Extra["streams"]), &specs); err != nil {
		return nil, site.WrapError(site.ErrUpstreamChanged, err, "码流信息已损坏")
	}
	idx := sel.PartIndex
	if idx <= 0 {
		idx = 1
	}
	if idx > len(m.Parts) {
		return nil, site.NewError(site.ErrNotFound, "媒体序号超出范围")
	}
	set := &site.StreamSet{}
	for _, spec := range specs {
		set.Videos = append(set.Videos, site.Stream{
			URL:       spec.URL,
			Codec:     "avc1",
			Container: spec.Container,
			Kind:      spec.Kind,
			Headers:   downloadHeaders(),
		})
	}
	if len(set.Videos) == 0 {
		return nil, site.NewError(site.ErrNotFound, "没有可用视频流")
	}
	return set, nil
}

// ---- 取数 ----

type streamSpec struct {
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Container string `json:"container"`
}

type pageInfo struct {
	title  string
	author string
}

func (a *Adapter) canonicalURL(ctx context.Context, client *http.Client, raw string, auth *site.Auth) (string, error) {
	s := strings.TrimSpace(raw)
	if m := reURL.FindString(s); m != "" {
		s = strings.TrimRight(m, ".,;:!?，。；：！？)）]】")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", site.NewError(site.ErrUnsupportedURL, "无法识别抖音链接")
	}
	if !a.isShortHost(strings.ToLower(u.Hostname())) {
		return s, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s, nil)
	if err != nil {
		return "", site.WrapError(site.ErrUnsupportedURL, err, "构造短链请求失败")
	}
	req.Header.Set("User-Agent", mobileUA)
	if auth != nil {
		for _, c := range auth.Cookies {
			req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", site.WrapError(site.ErrNetwork, err, "展开短链失败")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	final := s
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	if awemeID(final) == "" {
		return "", site.NewError(site.ErrUpstreamChanged, "短链没有跳到抖音作品页：%s", final)
	}
	return final, nil
}

func fetchState(ctx context.Context, client *http.Client, target string, auth *site.Auth) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, site.WrapError(site.ErrUnsupportedURL, err, "构造页面请求失败")
	}
	req.Header.Set("User-Agent", mobileUA)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	if auth != nil {
		for _, c := range auth.Cookies {
			req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
		}
		for k, v := range auth.Headers {
			req.Header.Set(k, v)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, site.WrapError(site.ErrNetwork, err, "请求分享页失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		code := site.ErrUpstreamChanged
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			code = site.ErrAuthRequired
		}
		return nil, site.NewError(code, "分享页返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, site.WrapError(site.ErrNetwork, err, "读取分享页失败")
	}
	raw, err := ssr.ExtractObject(body, StateMarker)
	if err != nil {
		return nil, site.WrapError(site.ErrUpstreamChanged, err, "首屏数据提取失败")
	}
	var state any
	if err := json.Unmarshal(ssr.SanitizeUndefined(raw), &state); err != nil {
		return nil, site.WrapError(site.ErrUpstreamChanged, err, "首屏数据不是合法 JSON")
	}
	return state, nil
}

// collectPlayable 在首屏 JSON 里递归找 play_addr/download_addr 的 url_list。
// 只对这两类键取值，避免把封面、头像当视频地址。
func collectPlayable(state any) (pageInfo, []string) {
	var info pageInfo
	var urls []string
	seen := map[string]bool{}
	var walk func(any, int)
	walk = func(v any, depth int) {
		if depth > 24 || v == nil {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				switch k {
				case "desc":
					if s, ok := child.(string); ok && info.title == "" {
						info.title = strings.TrimSpace(s)
					}
				case "nickname":
					if s, ok := child.(string); ok && info.author == "" {
						info.author = s
					}
				case "play_addr", "download_addr", "playApi":
					for _, u := range urlList(child) {
						if !seen[u] {
							seen[u] = true
							urls = append(urls, u)
						}
					}
				}
				walk(child, depth+1)
			}
		case []any:
			for _, child := range x {
				walk(child, depth+1)
			}
		}
	}
	walk(state, 0)
	return info, urls
}

func urlList(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	items, ok := m["url_list"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok && strings.HasPrefix(s, "http") && !seenIn(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func seenIn(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// ---- 工具 ----

func awemeID(raw string) string {
	if m := reAwemeID.FindStringSubmatch(raw); len(m) == 2 {
		return m[1]
	}
	if u, err := url.Parse(raw); err == nil {
		for _, key := range []string{"modal_id", "item_id", "aweme_id"} {
			if v := u.Query().Get(key); regexp.MustCompile(`^\d{8,25}$`).MatchString(v) {
				return v
			}
		}
	}
	return ""
}

func (a *Adapter) isShortHost(host string) bool {
	for _, h := range a.ShortHosts {
		if h != "" && (host == h || strings.HasSuffix(host, "."+h)) {
			return true
		}
	}
	return host == "v.douyin.com" || strings.HasSuffix(host, ".v.douyin.com")
}

func downloadHeaders() http.Header {
	h := http.Header{}
	h.Set("User-Agent", mobileUA)
	h.Set("Referer", "https://www.douyin.com/")
	return h
}
