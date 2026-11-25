// Package xiaohongshu 是小红书的站点适配器。
//
// 取数路径：笔记页首屏的 window.__INITIAL_STATE__ 里带 noteDetailMap，
// 视频的 h264.masterUrl 与图文的 imageList.urlDefault 都是可直接下载的 CDN 地址。
// 分享短链（xhslink.cn）只需要跟随一次重定向即可拿到带 xsec_token 的正文页。
//
// 这条路径不依赖签名接口，但强依赖首屏 SSR 结构；结构一改，ssr 提取器会
// 立即报 upstream_changed，由每日 smoke 探针发现。
package xiaohongshu

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Felix2yu/liuying-engine/site"
	"github.com/Felix2yu/liuying-engine/site/ssr"
)

// StateMarker 是首屏数据所在的全局变量名。
const StateMarker = "window.__INITIAL_STATE__"

const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 " +
	"(KHTML, like Gecko) Version/17.0 Safari/605.1.15"

// browserAcceptEncoding 不能省：Go 默认只发 gzip，实测会被小红书风控跳登录页；
// 完整写出浏览器的一组编码后服务端返回正常 SSR（A/B/A/B 对照验证）。
const browserAcceptEncoding = "gzip, deflate, br"

var (
	reURL    = regexp.MustCompile(`https?://[^\s，。、）】"'<>]+`)
	reNoteID = regexp.MustCompile(`(?i)/(?:explore|discovery/item|note)/([0-9a-f]{24})`)
)

var hosts = []string{"xiaohongshu.com", "xhslink.cn", "xhslink.com", "xhslink.me"}

// Adapter 实现 site.Adapter 与 site.StreamProvider。
type Adapter struct {
	// ShortHosts 允许测试把本地服务当作短链域名；生产留空即使用 xhslink.*。
	ShortHosts []string
}

// NewAdapter 返回小红书适配器。
func NewAdapter() *Adapter { return &Adapter{} }

// Site 返回站点标识。
func (a *Adapter) Site() string { return "xiaohongshu" }

// Match 只判断域名和笔记 ID，不发请求。分享文案里的链接也会先被抽出来。
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
			// 短链没有笔记 ID，但其 path 本身就是入口。
			return a.isShortHost(host) || reNoteID.MatchString(u.Path)
		}
	}
	return false
}

// Resolve 展开短链、取首屏 JSON，并把视频码流或图文图片按 Part 分组保存。
func (a *Adapter) Resolve(ctx context.Context, rawURL string, opt site.ResolveOptions) ([]*site.Media, error) {
	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}

	target, err := a.canonicalURL(ctx, client, rawURL, opt.Auth)
	if err != nil {
		return nil, err
	}
	match := reNoteID.FindStringSubmatch(target)
	if len(match) != 2 {
		return nil, site.NewError(site.ErrUnsupportedURL, "链接里找不到小红书笔记 ID")
	}
	noteID := strings.ToLower(match[1])

	state, err := fetchState(ctx, client, target, opt.Auth)
	if err != nil {
		return nil, err
	}
	detail, ok := state.Note.DetailMap[noteID]
	if !ok {
		// 少数页面 noteId 与 URL 大小写不同；只有一条时直接用它。
		if len(state.Note.DetailMap) == 1 {
			for _, d := range state.Note.DetailMap {
				detail = d
			}
		} else {
			return nil, site.NewError(
				site.ErrUpstreamChanged,
				"首屏数据里没有这条笔记（可能需要 xsec_token 或登录态）",
			)
		}
	}
	note := detail.Note
	if note.NoteID != "" {
		noteID = note.NoteID
	}

	m := &site.Media{
		Site:   "xiaohongshu",
		ID:     noteID,
		Title:  noteTitle(note),
		Author: site.Author{ID: note.User.UserID, Name: note.User.Nickname},
		Cover:  coverOf(note),
		Extra: map[string]string{
			"page_url":   target,
			"xsec_token": note.XsecToken,
			"note_type":  note.Type,
		},
	}

	groups := map[string][]streamSpec{}
	var parts []site.Part
	if specs := videoStreams(note); len(specs) > 0 {
		groups["1"] = specs
		m.Duration = videoDuration(note)
		parts = append(parts, site.Part{Index: 1, ID: noteID, Title: m.Title, Duration: m.Duration})
	} else {
		for _, img := range note.ImageList {
			u := imageURL(img)
			if u == "" {
				continue
			}
			idx := len(parts) + 1
			groups[strconv.Itoa(idx)] = []streamSpec{{
				Kind:      "image",
				URL:       u,
				Container: extensionOf(u),
			}}
			parts = append(parts, site.Part{
				Index: idx,
				ID:    fmt.Sprintf("%s-%d", noteID, idx),
				Title: fmt.Sprintf("图片 #%d", idx),
			})
		}
	}
	if len(parts) == 0 {
		return nil, site.NewError(site.ErrNotFound, "这条笔记没有可下载的视频或图片")
	}
	m.Parts = parts

	raw, err := json.Marshal(groups)
	if err != nil {
		return nil, site.WrapError(site.ErrUpstreamChanged, err, "序列化码流失败")
	}
	m.Extra["streams"] = string(raw)
	return []*site.Media{m}, nil
}

// Streams 按 Part 返回码流。码流在 Resolve 阶段已固化到 Extra，避免下载中途
// 直链签名过期；超时失效时下载层会报网络错误，可重试整条任务重新解析。
func (a *Adapter) Streams(_ context.Context, m *site.Media, sel site.Selection) (*site.StreamSet, error) {
	if m == nil || m.Extra == nil || m.Extra["streams"] == "" {
		return nil, site.NewError(site.ErrUnsupportedURL, "媒体里没有码流信息")
	}
	var groups map[string][]streamSpec
	if err := json.Unmarshal([]byte(m.Extra["streams"]), &groups); err != nil {
		return nil, site.WrapError(site.ErrUpstreamChanged, err, "码流信息已损坏")
	}
	idx := sel.PartIndex
	if idx <= 0 {
		idx = 1
	}
	specs := groups[strconv.Itoa(idx)]
	if len(specs) == 0 {
		return nil, site.NewError(site.ErrNotFound, "该图片序号超出范围")
	}

	set := &site.StreamSet{}
	for _, spec := range specs {
		set.Videos = append(set.Videos, site.Stream{
			Codec:     spec.Codec,
			Width:     spec.Width,
			Height:    spec.Height,
			Bandwidth: spec.Bandwidth,
			URL:       spec.URL,
			Headers:   downloadHeaders(),
			Container: spec.Container,
			Kind:      spec.Kind,
		})
	}
	return set, nil
}

// readBody 解压笔记页响应。手动设置 Accept-Encoding 后，Go 不会再自动解压。
func readBody(resp *http.Response) ([]byte, error) {
	var reader io.Reader = resp.Body
	switch strings.ToLower(resp.Header.Get("Content-Encoding")) {
	case "gzip":
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("gzip 解压失败: %w", err)
		}
		defer gz.Close()
		reader = gz
	case "deflate":
		zr, err := zlib.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("deflate 解压失败: %w", err)
		}
		defer zr.Close()
		reader = zr
	case "", "identity":
	default:
		return nil, fmt.Errorf("暂不支持 Content-Encoding=%s", resp.Header.Get("Content-Encoding"))
	}
	return io.ReadAll(io.LimitReader(reader, 4<<20))
}

// ---- 首屏数据 ----

type initialState struct {
	Note noteStore `json:"note"`
}

type noteStore struct {
	DetailMap map[string]noteDetail `json:"noteDetailMap"`
}

type noteDetail struct {
	Note note `json:"note"`
}

type note struct {
	NoteID    string      `json:"noteId"`
	Type      string      `json:"type"`
	Title     string      `json:"title"`
	Desc      string      `json:"desc"`
	XsecToken string      `json:"xsecToken"`
	User      noteUser    `json:"user"`
	ImageList []imageItem `json:"imageList"`
	Video     noteVideo   `json:"video"`
}

type noteUser struct {
	UserID   string `json:"userId"`
	Nickname string `json:"nickname"`
}

type imageItem struct {
	URL        string `json:"url"`
	URLPre     string `json:"urlPre"`
	URLDefault string `json:"urlDefault"`
}

type noteVideo struct {
	Media struct {
		Video struct {
			Duration int `json:"duration"`
		} `json:"video"`
		Stream struct {
			H264 []h264Stream `json:"h264"`
		} `json:"stream"`
	} `json:"media"`
}

type h264Stream struct {
	MasterURL  string   `json:"masterUrl"`
	BackupURLs []string `json:"backupUrls"`
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	AvgBitrate int64    `json:"avgBitrate"`
	Duration   int64    `json:"duration"` // 毫秒
	VideoCodec string   `json:"videoCodec"`
	Format     string   `json:"format"`
}

// streamSpec 是存在 Media.Extra 里的紧凑码流表示。
type streamSpec struct {
	Kind      string `json:"kind"`
	URL       string `json:"url"`
	Container string `json:"container"`
	Codec     string `json:"codec,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Bandwidth int64  `json:"bandwidth,omitempty"`
}

func fetchState(ctx context.Context, client *http.Client, target string, auth *site.Auth) (*initialState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, site.WrapError(site.ErrUnsupportedURL, err, "构造页面请求失败")
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Accept-Encoding", browserAcceptEncoding)
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
		return nil, site.WrapError(site.ErrNetwork, err, "请求笔记页失败")
	}
	defer resp.Body.Close()
	if resp.Request != nil && resp.Request.URL != nil &&
		strings.Contains(strings.ToLower(resp.Request.URL.Path), "login") {
		return nil, site.NewError(site.ErrAuthRequired, "笔记页要求登录（xsec_token 可能已过期）")
	}
	if resp.StatusCode != http.StatusOK {
		code := site.ErrUpstreamChanged
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			code = site.ErrAuthRequired
		}
		return nil, site.NewError(code, "笔记页返回 HTTP %d（可能需要 xsec_token 或登录态）", resp.StatusCode)
	}
	body, err := readBody(resp)
	if err != nil {
		return nil, site.WrapError(site.ErrUpstreamChanged, err, "读取笔记页失败")
	}

	raw, err := ssr.ExtractObject(body, StateMarker)
	if err != nil {
		return nil, site.WrapError(site.ErrUpstreamChanged, err, "首屏数据提取失败")
	}
	// __INITIAL_STATE__ 是 JS 对象字面量，里面可能有裸 undefined。
	var state initialState
	if err := json.Unmarshal(ssr.SanitizeUndefined(raw), &state); err != nil {
		return nil, site.WrapError(site.ErrUpstreamChanged, err, "首屏数据不是合法 JSON")
	}
	if len(state.Note.DetailMap) == 0 {
		return nil, site.NewError(site.ErrUpstreamChanged, "首屏数据里没有 noteDetailMap（页面结构可能已改）")
	}
	return &state, nil
}

// canonicalURL 展开分享短链与文案，返回带 xsec_token 的正文页地址。
func (a *Adapter) canonicalURL(ctx context.Context, client *http.Client, raw string, auth *site.Auth) (string, error) {
	s := strings.TrimSpace(raw)
	if m := reURL.FindString(s); m != "" {
		s = strings.TrimRight(m, ".,;:!?，。；：！？)）]】")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", site.NewError(site.ErrUnsupportedURL, "无法识别小红书链接")
	}
	if !a.isShortHost(strings.ToLower(u.Hostname())) {
		return s, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s, nil)
	if err != nil {
		return "", site.WrapError(site.ErrUnsupportedURL, err, "构造短链请求失败")
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Accept-Encoding", browserAcceptEncoding)
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
	if reNoteID.FindStringSubmatch(final) == nil {
		// 短链在风控时会先跳登录页，但把正文页放在 redirectPath 里。
		// 这条路径不需要登录态，直接用它继续取首屏数据。
		if u, perr := url.Parse(final); perr == nil {
			if redirect := u.Query().Get("redirectPath"); reNoteID.MatchString(redirect) {
				// redirectPath 可能是相对路径，补上当前 host 再请求。
				if absolute, rerr := u.Parse(redirect); rerr == nil {
					return absolute.String(), nil
				}
				return redirect, nil
			}
		}
		return "", site.NewError(site.ErrUpstreamChanged, "短链没有跳到小红书正文页：%s", final)
	}
	return final, nil
}

func (a *Adapter) isShortHost(host string) bool {
	for _, h := range a.ShortHosts {
		if h != "" && (host == h || strings.HasSuffix(host, "."+h)) {
			return true
		}
	}
	return isShortHost(host)
}

// ---- 数据整理 ----

func videoStreams(note note) []streamSpec {
	var out []streamSpec
	for _, h := range note.Video.Media.Stream.H264 {
		u := h.MasterURL
		if u == "" && len(h.BackupURLs) > 0 {
			u = h.BackupURLs[0]
		}
		if u == "" {
			continue
		}
		container := strings.ToLower(h.Format)
		if container == "" {
			container = "mp4"
		}
		out = append(out, streamSpec{
			Kind:      "video",
			URL:       secureURL(u),
			Container: container,
			Codec:     h.VideoCodec,
			Width:     h.Width,
			Height:    h.Height,
			Bandwidth: h.AvgBitrate,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bandwidth > out[j].Bandwidth })
	return out
}

func videoDuration(note note) time.Duration {
	for _, h := range note.Video.Media.Stream.H264 {
		if h.Duration > 0 {
			return time.Duration(h.Duration) * time.Millisecond
		}
	}
	if d := note.Video.Media.Video.Duration; d > 0 {
		return time.Duration(d) * time.Second
	}
	return 0
}

func imageURL(img imageItem) string {
	for _, u := range []string{img.URLDefault, img.URLPre, img.URL} {
		if u != "" {
			return secureURL(u)
		}
	}
	return ""
}

func coverOf(note note) string {
	if len(note.ImageList) > 0 {
		return imageURL(note.ImageList[0])
	}
	return ""
}

func noteTitle(note note) string {
	for _, s := range []string{note.Title, strings.SplitN(strings.TrimSpace(note.Desc), "\n", 2)[0]} {
		if s = strings.TrimSpace(s); s != "" {
			r := []rune(s)
			if len(r) > 80 {
				return string(r[:80]) + "…"
			}
			return s
		}
	}
	return "小红书笔记"
}

func extensionOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "jpg"
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(u.Path)), ".")
	switch ext {
	case "jpg", "jpeg", "png", "webp", "gif":
		return ext
	default:
		return "jpg"
	}
}

// secureURL 把小红书 CDN 的 http 链接升级为 https；其他域名（例如测试服务器）保持原样。
func secureURL(raw string) string {
	if !strings.HasPrefix(raw, "http://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	h := strings.ToLower(u.Hostname())
	for _, suffix := range []string{"xhscdn.com", "xiaohongshu.com"} {
		if h == suffix || strings.HasSuffix(h, "."+suffix) {
			return "https://" + raw[len("http://"):]
		}
	}
	return raw
}

func isShortHost(host string) bool {
	for _, h := range []string{"xhslink.cn", "xhslink.com", "xhslink.me"} {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

func downloadHeaders() http.Header {
	h := http.Header{}
	h.Set("User-Agent", defaultUA)
	h.Set("Referer", "https://www.xiaohongshu.com/")
	return h
}
