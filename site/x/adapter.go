// Package x 是 X（原 Twitter）的站点适配器。
//
// 取数路径：公开内容走 cdn.syndication.twimg.com 的 tweet-result 端点 ——
// 它不需要登录也不需要走 GraphQL，返回的 JSON 里带 mediaDetails，
// 视频的直链（mp4 变体）与 HLS 清单都在 variants 里。
//
// 受限内容（受保护账号、敏感内容）仍然需要 auth_token + ct0，
// 由调用方通过 auth 放进 context。
package x

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Felix2yu/liuying-engine/site"
)

// DefaultEndpoint 是公开发布用的 tweet-result 端点。
const DefaultEndpoint = "https://cdn.syndication.twimg.com/tweet-result"

// 移动端 UA：桌面 UA 有时会被返回降级页面。
const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 " +
	"(KHTML, like Gecko) Version/17.0 Safari/605.1.15"

var (
	reStatusID = regexp.MustCompile(`(?i)/(?:status|statuses)/(\d{1,25})`)
	reWebID    = regexp.MustCompile(`(?i)/i/(?:web/)?status/(\d{1,25})`)
	reDigits   = regexp.MustCompile(`^\d{1,25}$`)
)

// Adapter 实现 site.Adapter 与 site.StreamProvider。
type Adapter struct {
	// Endpoint 允许测试注入；生产用 DefaultEndpoint。
	Endpoint string
}

// NewAdapter 返回使用默认端点的适配器。
func NewAdapter() *Adapter { return &Adapter{Endpoint: DefaultEndpoint} }

// Site 返回站点标识。
func (a *Adapter) Site() string { return "x" }

var hosts = []string{"x.com", "twitter.com", "t.co", "mobile.twitter.com", "mobile.x.com"}

// Match 只判断域名与 URL 形态，不发请求。
func (a *Adapter) Match(rawURL string) bool {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		// 裸推文 ID 视为 X 的输入。
		return reDigits.MatchString(s)
	}
	host := strings.ToLower(u.Hostname())
	ok := false
	for _, h := range hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	return tweetID(s) != ""
}

// tweetID 从 URL 里取推文 ID。
func tweetID(raw string) string {
	if m := reStatusID.FindStringSubmatch(raw); len(m) == 2 {
		return m[1]
	}
	if m := reWebID.FindStringSubmatch(raw); len(m) == 2 {
		return m[1]
	}
	if u, err := url.Parse(raw); err == nil {
		for _, key := range []string{"id", "status_id"} {
			if v := u.Query().Get(key); reDigits.MatchString(v) {
				return v
			}
		}
	}
	if reDigits.MatchString(strings.TrimSpace(raw)) {
		return strings.TrimSpace(raw)
	}
	return ""
}

// Resolve 取推文详情并按媒体条目拆成 Parts。
func (a *Adapter) Resolve(ctx context.Context, rawURL string, opt site.ResolveOptions) ([]*site.Media, error) {
	client := opt.Client
	if client == nil {
		client = http.DefaultClient
	}
	target := strings.TrimSpace(rawURL)

	// t.co 短链：跟随一次重定向拿真实地址。
	if u, err := url.Parse(target); err == nil && strings.HasSuffix(strings.ToLower(u.Hostname()), "t.co") {
		final, err := follow(client, target)
		if err != nil {
			return nil, fmt.Errorf("展开 t.co 短链失败: %w", err)
		}
		target = final
	}

	id := tweetID(target)
	if id == "" {
		return nil, &site.ResolveError{
			Code:    site.ErrUnsupportedURL,
			Message: "无法从链接里识别推文 ID",
		}
	}

	tweet, err := a.fetchTweet(ctx, client, id, opt.Auth)
	if err != nil {
		return nil, err
	}
	if len(tweet.MediaDetails) == 0 {
		return nil, &site.ResolveError{
			Code:    site.ErrNotFound,
			Message: "这条推文没有可下载的视频（可能是纯文字或纯图片）",
		}
	}

	m := &site.Media{
		Site:   "x",
		ID:     id,
		Title:  firstLine(tweet.Text, 80),
		Author: site.Author{ID: tweet.User.IDStr, Name: displayName(tweet.User)},
		Cover:  coverOf(tweet),
		Extra:  map[string]string{"tweet_id": id},
	}
	for i, item := range tweet.MediaDetails {
		m.Parts = append(m.Parts, site.Part{
			Index: i + 1,
			ID:    fmt.Sprintf("%s-%d", id, i+1),
			Title: fmt.Sprintf("%s #%d", mediaKindLabel(item.Type), i+1),
		})
	}
	return []*site.Media{m}, nil
}

// Streams 列出所选媒体条目的码流。HLS 变体排在前面（通常是最高画质）。
func (a *Adapter) Streams(ctx context.Context, m *site.Media, sel site.Selection) (*site.StreamSet, error) {
	_ = ctx
	if m == nil || m.Extra == nil {
		return nil, &site.ResolveError{Code: site.ErrUnsupportedURL, Message: "媒体为空"}
	}
	id := m.Extra["tweet_id"]
	if id == "" {
		id = m.ID
	}
	idx := sel.PartIndex
	if idx <= 0 {
		idx = 1
	}
	if idx > len(m.Parts) {
		return nil, &site.ResolveError{
			Code:    site.ErrNotFound,
			Message: fmt.Sprintf("媒体序号 %d 超出范围（共 %d 条）", idx, len(m.Parts)),
		}
	}

	// Streams 阶段需要重新拿一次详情：Media 里只存了条目数，没存直链。
	// 直链有时效（几分钟到几小时），提前缓存反而会在慢任务里失效。
	tweet, err := a.fetchTweet(ctx, http.DefaultClient, id, nil)
	if err != nil {
		return nil, err
	}
	if idx > len(tweet.MediaDetails) {
		return nil, &site.ResolveError{Code: site.ErrNotFound, Message: "推文的媒体条目已变化，请重试"}
	}
	item := tweet.MediaDetails[idx-1]

	var hls []site.Stream
	var mp4 []site.Stream
	w, h := aspect(item.VideoInfo.AspectRatio)
	for _, v := range item.VideoInfo.Variants {
		switch {
		case strings.Contains(strings.ToLower(v.ContentType), "mpegurl"):
			hls = append(hls, site.Stream{
				URL:       v.URL,
				Codec:     "avc1",
				Width:     w,
				Height:    h,
				Container: "mp4",
				HLS:       true,
				Headers:   headers(),
			})
		case strings.HasPrefix(strings.ToLower(v.ContentType), "video/mp4"):
			mp4 = append(mp4, site.Stream{
				URL:       v.URL,
				Codec:     "avc1",
				Width:     w,
				Height:    h,
				Bandwidth: int64(v.Bitrate),
				Container: "mp4",
				Headers:   headers(),
			})
		}
	}
	sort.SliceStable(mp4, func(i, j int) bool { return mp4[i].Bandwidth > mp4[j].Bandwidth })

	set := &site.StreamSet{Videos: append(hls, mp4...)}
	if len(set.Videos) == 0 {
		return nil, &site.ResolveError{Code: site.ErrNotFound, Message: "该媒体没有可下载的视频流"}
	}
	return set, nil
}

// ---- 取数与解析 ----

type tweet struct {
	IDStr        string       `json:"id_str"`
	Text         string       `json:"text"`
	MediaDetails []mediaItem  `json:"mediaDetails"`
	User         tweetUser    `json:"user"`
	Photos       []photoThumb `json:"photos"`
}

type tweetUser struct {
	IDStr      string `json:"id_str"`
	Name       string `json:"name"`
	ScreenName string `json:"screen_name"`
}

type photoThumb struct {
	URL string `json:"url"`
}

type mediaItem struct {
	Type      string    `json:"type"` // video / animated_gif / photo
	MediaURL  string    `json:"media_url_https"`
	VideoInfo videoInfo `json:"video_info"`
}

type videoInfo struct {
	DurationMillis int       `json:"duration_millis"`
	AspectRatio    []int     `json:"aspect_ratio"`
	Variants       []variant `json:"variants"`
}

type variant struct {
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
	Bitrate     int    `json:"bitrate"`
}

// fetchTweet 请求 tweet-result 端点。
func (a *Adapter) fetchTweet(ctx context.Context, client *http.Client, id string, auth *site.Auth) (*tweet, error) {
	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	u := fmt.Sprintf("%s?id=%s&lang=en&token=a", endpoint, url.QueryEscape(id))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Accept", "application/json")
	applyAuth(req, auth)

	resp, err := client.Do(req)
	if err != nil {
		// 必须分类为网络错误：否则连接重置会被误报成「站点改版」，
		// 每日 smoke 就会把偶发网络抖动当成真正的取数失效。
		return nil, site.WrapError(site.ErrNetwork, err, "请求推文详情失败")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden:
		// 受保护账号、敏感内容、已删除都会走到这里；三者无法从状态码区分。
		return nil, &site.ResolveError{
			Code:    site.ErrAuthRequired,
			Message: "推文不可见：可能已删除、为受保护账号或含敏感内容（需要登录态 cookie）",
		}
	default:
		return nil, fmt.Errorf("推文详情返回 HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var t tweet
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, &site.ResolveError{
			Code:    site.ErrUpstreamChanged,
			Message: "推文详情不是预期的 JSON（接口可能已调整）",
			Err:     err,
		}
	}
	if t.IDStr == "" && t.Text == "" {
		return nil, &site.ResolveError{Code: site.ErrUpstreamChanged, Message: "推文详情为空"}
	}
	return &t, nil
}

// applyAuth 把 context 里的 cookie/header 附加到请求上。
func applyAuth(req *http.Request, auth *site.Auth) {
	if auth == nil {
		return
	}
	for _, c := range auth.Cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	for k, v := range auth.Headers {
		req.Header.Set(k, v)
	}
}

// follow 跟随一次重定向，返回最终地址。
func follow(client *http.Client, raw string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultUA)
	resp, err := client.Do(req)
	if err != nil {
		return "", site.WrapError(site.ErrNetwork, err, "展开 t.co 短链失败")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String(), nil
	}
	return raw, nil
}

func headers() http.Header {
	h := http.Header{}
	h.Set("User-Agent", defaultUA)
	h.Set("Referer", "https://x.com/")
	return h
}

func aspect(ratio []int) (int, int) {
	if len(ratio) == 2 && ratio[0] > 0 && ratio[1] > 0 {
		return ratio[0], ratio[1]
	}
	return 0, 0
}

func coverOf(t *tweet) string {
	if len(t.Photos) > 0 {
		return t.Photos[0].URL
	}
	for _, m := range t.MediaDetails {
		if m.MediaURL != "" {
			return m.MediaURL
		}
	}
	return ""
}

func displayName(u tweetUser) string {
	if u.Name != "" {
		return u.Name
	}
	if u.ScreenName != "" {
		return "@" + u.ScreenName
	}
	return ""
}

func mediaKindLabel(kind string) string {
	switch strings.ToLower(kind) {
	case "video":
		return "视频"
	case "animated_gif":
		return "动图"
	case "photo":
		return "图片"
	default:
		return kind
	}
}

// firstLine 取正文首行并按 rune 截断，避免标题里带换行。
func firstLine(text string, max int) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	if line == "" {
		return "X 视频"
	}
	r := []rune(line)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return line
}
