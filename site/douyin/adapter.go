// Package douyin 是抖音的站点适配器。
//
// 取数路径：分享页第一次请求只返回 window._ROUTER_DATA 壳并下发 ttwid，
// 带这个 Cookie 再请求一次才返回 videoInfoRes.item_list[].video.play_addr.url_list。
// 因此这里必须用带 CookieJar 的会话连续请求两次，而不是两个无状态请求。
//
// 详情页 API（aweme/detail）的 Argus 签名体系不在这里伪造；分享页路径足够拿到直链。
// 若两次请求后仍没有直链，明确返回 upstream_changed 说明原因。
package douyin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Felix2yu/liuying-engine/site"
	"github.com/Felix2yu/liuying-engine/site/ssr"
)

// StateMarker 是分享页首屏数据所在的全局变量。
const StateMarker = "window._ROUTER_DATA"

const (
	mobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 " +
		"(KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
	noPlayable = "抖音分享页两次请求都没有视频直链（ttwid 会话可能被风控，或分享页结构已改）"
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

// Resolve 展开分享短链，用同一会话连续取两次分享页，再从首屏数据里提取直链。
func (a *Adapter) Resolve(ctx context.Context, rawURL string, opt site.ResolveOptions) ([]*site.Media, error) {
	client := sessionClient(opt.Client)

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
// 抖音的 play_addr 会 302 到 jspcdn.cn；本机常把该域名污染成 0.0.0.0，
// 因此在这里跟随一次重定向并改写成 real_host 指向的真实 CDN，避免依赖代理环境变量。
func (a *Adapter) Streams(ctx context.Context, m *site.Media, sel site.Selection) (*site.StreamSet, error) {
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
		url := spec.URL
		if needsCDNRewrite(url) {
			url = directMediaURL(ctx, url)
		}
		set.Videos = append(set.Videos, site.Stream{
			URL:       url,
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

// needsCDNRewrite 只对抖音自家的播放地址做重定向展开，避免测试用的本地地址被误触。
func needsCDNRewrite(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.Contains(host, "snssdk.com") ||
		strings.Contains(host, "douyin.com") ||
		strings.HasSuffix(host, "ixigua.com")
}

// directMediaURL 逐跳跟随播放地址的 302，并把被污染/不可达的 jspcdn.cn 域名
// 换成 Location 里 real_host 指定的真实 CDN。改写路径时要去掉重复的 hostname 前缀。
// 必须走到最终 2xx 才返回：真实 CDN 偶尔还会再跳一个 jspcdn 边缘域名。
func directMediaURL(ctx context.Context, raw string) string {
	client := &http.Client{
		Transport: http.DefaultClient.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	current := raw
	for hop := 0; hop < 5; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return raw
		}
		for k, v := range downloadHeaders() {
			req.Header[k] = v
		}
		req.Header.Set("Range", "bytes=0-0") // 探测重定向，不要把整段视频拉下来。
		resp, err := client.Do(req)
		if err != nil {
			return raw
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			return current
		}
		location := resp.Header.Get("Location")
		if location == "" {
			return current
		}
		current = rewriteCDNHost(location)
	}
	return current
}

// rewriteCDNHost 把 jspcdn.cn 边缘域名换成 real_host 声明的真实 CDN。
func rewriteCDNHost(location string) string {
	u, err := url.Parse(location)
	if err != nil || !strings.HasSuffix(strings.ToLower(u.Hostname()), "jspcdn.cn") {
		return location
	}
	encoded := u.Query().Get("real_host")
	if encoded == "" {
		return location
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.URLEncoding.DecodeString(encoded)
		if err != nil {
			return location
		}
	}
	realHost := string(decoded)
	if realHost == "" {
		return location
	}
	u.Host = realHost
	u.Path = strings.TrimPrefix(u.Path, "/"+realHost)
	return u.String()
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

// sessionClient 给调用方注入的客户端套上 CookieJar，复用其 Transport（代理、TLS 配置）。
// 抖音的 ttwid 必须通过同一会话带回，两个无状态请求只会拿到两次空壳。
func sessionClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return base
	}
	return &http.Client{
		Jar:           jar,
		Transport:     base.Transport,
		CheckRedirect: base.CheckRedirect,
		Timeout:       base.Timeout,
	}
}

func fetchState(ctx context.Context, client *http.Client, target string, auth *site.Auth) (any, error) {
	body, err := fetchPage(ctx, client, target, auth)
	if err != nil {
		return nil, err
	}
	// 第一响通常是壳（只有 _ROUTER_DATA 参数），带 ttwid 再取一次才有 videoInfoRes。
	if !hasPlayableHint(body) {
		body, err = fetchPage(ctx, client, target, auth)
		if err != nil {
			return nil, err
		}
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

func hasPlayableHint(body []byte) bool {
	return bytes.Contains(body, []byte("videoInfoRes")) || bytes.Contains(body, []byte("play_addr"))
}

func fetchPage(ctx context.Context, client *http.Client, target string, auth *site.Auth) ([]byte, error) {
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
	return body, nil
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
