package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Felix2yu/bili-dl/site"
)

// Adapter 是哔哩哔哩站点的 site.Adapter 与 site.StreamProvider 实现。
//
// 它把「站点无关的 Media」与「哔哩哔哩的 Video/Stream」互相映射，
// 复用本包已有的解析、WBI 签名、选流与下载逻辑，不重复实现。
type Adapter struct{}

// NewAdapter 返回哔哩哔哩适配器。
func NewAdapter() *Adapter { return &Adapter{} }

// Site 返回站点标识。
func (a *Adapter) Site() string { return "bilibili" }

var (
	reMatchBV  = regexp.MustCompile(`BV[0-9A-Za-z]{10}`)
	reMatchAV  = regexp.MustCompile(`(?i)\bav(\d{1,15})\b`)
	reMatchSS  = regexp.MustCompile(`(?i)(?:^|[/?&=])(?:ss|season)(?:_id)?[=/]?(\d{1,12})`)
	reMatchEP  = regexp.MustCompile(`(?i)(?:^|[/?&=])(?:ep|ep_id)[=/]?(\d{1,12})`)
	reMatchURL = regexp.MustCompile(`https?://[^\s，。、）】"'<>]+`)
)

// bilibiliHosts 是本站点认领的主机后缀。
var bilibiliHosts = []string{
	"bilibili.com",
	"b23.tv",
	"bilibili.tv",
}

// Match 只做字符串与域名判断，不发网络请求。
func (a *Adapter) Match(rawURL string) bool {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return false
	}
	// 分享文案里通常混着中文与链接：先抽出第一个 URL 再判域名，
	// 否则 "…分享 https://b23.tv/xyz 真的好看" 会因为整体 Parse 失败而漏判。
	if m := reMatchURL.FindString(s); m != "" {
		s = strings.TrimRight(m, ".,;:!?，。；：！？)）]】")
	}

	if host := hostOf(s); host != "" {
		for _, h := range bilibiliHosts {
			if host == h || strings.HasSuffix(host, "."+h) {
				return true
			}
		}
	}
	// 裸 BV/av/ss/ep 号（无域名）。
	return reMatchBV.MatchString(s) || reMatchAV.MatchString(s) ||
		reMatchSS.MatchString(s) || reMatchEP.MatchString(s)
}

// Resolve 解析为一条或多条 Media。番剧按剧集展开，投稿按合集（可选）与分 P 展开。
func (a *Adapter) Resolve(ctx context.Context, rawURL string, opt site.ResolveOptions) ([]*site.Media, error) {
	_ = ctx // 解析走本包既有实现，取消信号由 c.Ctx 传导（P2 改为显式传入）。

	// 番剧/剧集：ss / ep 形态直接构造剧集列表。
	if kind, id := ParsePGCSInput(rawURL); kind != "" {
		seasonID, epID := "", ""
		if kind == "ss" {
			seasonID = id
		} else {
			epID = id
		}
		videos, err := SeasonVideos(seasonID, epID)
		if err != nil {
			return nil, err
		}
		return mediaList(videos, opt.ExpandCollections), nil
	}

	bv, _, err := ParseInput(rawURL)
	if err != nil {
		return nil, err
	}
	v, err := VideoFromBV(bv)
	if err != nil {
		return nil, err
	}

	// 合集展开：一条链接对应多个独立投稿时，按视频逐条返回。
	if opt.ExpandCollections && v.UGCSeason != nil {
		if parts := ExpandUGCSeason(v); len(parts) > 0 {
			return mediaList(parts, false), nil
		}
	}
	return []*site.Media{mediaOf(v)}, nil
}

// mediaList 把 Video 列表整体转成 Media 列表。
func mediaList(videos []Video, expand bool) []*site.Media {
	out := make([]*site.Media, 0, len(videos))
	for i := range videos {
		v := &videos[i]
		if expand && v.UGCSeason != nil {
			for _, part := range ExpandUGCSeason(v) {
				p := part
				out = append(out, mediaOf(&p))
			}
			continue
		}
		out = append(out, mediaOf(v))
	}
	return out
}

// mediaOf 把一条 Video 转成站点无关的 Media。
// Extra 里保留回程所需字段，供 Streams 重建 Video。
func mediaOf(v *Video) *site.Media {
	m := &site.Media{
		Site:   "bilibili",
		ID:     v.BV,
		Title:  v.Title,
		Author: site.Author{ID: strconv.FormatInt(v.OwnerM, 10), Name: v.Owner},
		Cover:  v.Pic,
		Extra:  map[string]string{},
	}
	if v.PGC {
		m.Extra["pgc"] = "1"
	}
	if v.EpID != "" {
		m.Extra["ep_id"] = v.EpID
	}
	if v.Cid != "" {
		m.Extra["cid"] = v.Cid
	}
	for i, p := range v.Pages {
		m.Parts = append(m.Parts, site.Part{
			Index:    i + 1,
			ID:       p.Cid.String(),
			Title:    p.Part,
			Duration: secondsDuration(p.Duration),
		})
	}
	if len(m.Parts) == 0 {
		m.Parts = []site.Part{{Index: 1, ID: v.Cid, Title: v.Title}}
	}
	return m
}

// videoOf 从 Media 重建一条 Video，按 Selection.PartIndex 定位分 P。
func videoOf(m *site.Media, sel site.Selection) (Video, error) {
	v := Video{
		Title: m.Title,
		BV:    m.ID,
		Owner: m.Author.Name,
		Pic:   m.Cover,
	}
	if m.Author.ID != "" {
		if mid, err := strconv.ParseInt(m.Author.ID, 10, 64); err == nil {
			v.OwnerM = mid
		}
	}
	if m.Extra != nil {
		v.PGC = m.Extra["pgc"] == "1"
		v.EpID = m.Extra["ep_id"]
	}

	idx := sel.PartIndex
	if idx <= 0 {
		idx = 1
	}
	if idx > len(m.Parts) {
		return v, errPartOutOfRange(idx, len(m.Parts))
	}
	p := m.Parts[idx-1]
	v.ActivePage = idx
	v.Cid = p.ID
	// Pages 保留完整分 P 列表：文件名模板要靠它生成 " - 01 - 分P名"。
	for i, part := range m.Parts {
		v.Pages = append(v.Pages, biliVideoPage{
			Page:     i + 1,
			Cid:      jsonNumberOf(part.ID),
			Part:     part.Title,
			Duration: int(part.Duration.Seconds()),
		})
	}
	return v, nil
}

// Streams 列出所选分 P 的视频/音频直链。
//
// 本期仍依赖 c 包的凭据与取消信号（HTTP 层），但选流参数已改为按调用传入，
// 因此不同任务可以用不同的画质/编码偏好，不再互相踩参数。
func (a *Adapter) Streams(ctx context.Context, m *site.Media, sel site.Selection) (*site.StreamSet, error) {
	v, err := videoOf(m, sel)
	if err != nil {
		return nil, err
	}

	cfg := streamConfigFrom(sel)
	stream, err := cfg.getStreamCtx(ctx, v)
	if err != nil {
		return nil, err
	}

	set := &site.StreamSet{}
	if stream.V != "" {
		set.Videos = append(set.Videos, site.Stream{
			Codec:     stream.VCodec,
			Width:     int(stream.VWidth),
			Height:    int(stream.VHeight),
			URL:       stream.V,
			Headers:   bilibiliHeaders(),
			Container: codecExt(stream.VCodec),
		})
	}
	if stream.A != "" {
		set.Audios = append(set.Audios, site.Stream{
			Codec:     stream.ACodec,
			URL:       stream.A,
			Headers:   bilibiliHeaders(),
			Container: audioExt(stream.ACodec),
		})
	}
	return set, nil
}

// streamConfigFrom 把站点无关的 Selection 映射成哔哩哔哩的选流参数。
// MaxHeight 换算成最接近的档位短名，其余字段直接对应。
func streamConfigFrom(sel site.Selection) streamConfig {
	cfg := streamConfig{
		AudioOnly:      sel.AudioOnly,
		VCodecPriority: sel.VCodecPriority,
		VideoQuality:   sel.VideoQuality,
		AudioQuality:   sel.AudioQuality,
		Resolution:     resolutionForHeight(sel.MaxHeight),
	}
	// Container 目前不参与选流（容器由编码决定），保留字段供上层校验。
	return cfg
}

// resolutionForHeight 把像素高度映射成哔哩哔哩的画质档位短名。
// 未指定（0）返回空串，表示不按档位过滤。
func resolutionForHeight(h int) string {
	switch {
	case h <= 0:
		return ""
	case h >= 4320:
		return "8k"
	case h >= 2160:
		return "4k"
	case h >= 1080:
		return "1080p"
	case h >= 720:
		return "720p"
	case h >= 480:
		return "480p"
	default:
		return "360p"
	}
}

// bilibiliHeaders 是码流下载必须带的请求头。
func bilibiliHeaders() http.Header {
	h := http.Header{}
	h.Set("Referer", "https://www.bilibili.com/")
	h.Set("User-Agent", defaultUA)
	return h
}

// hostOf 取 URL 的主机名；无 scheme 时返回空串。
func hostOf(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// secondsDuration 把秒数转成 Duration。
func secondsDuration(sec int) time.Duration {
	return time.Duration(sec) * time.Second
}

// jsonNumberOf 把字符串 cid 包成 json.Number，保持既有 Video.Pages 的字段类型。
func jsonNumberOf(s string) json.Number {
	return json.Number(s)
}

// errPartOutOfRange 复用既有的越界错误文案。
func errPartOutOfRange(page, total int) error {
	return fmt.Errorf("分 P 页码 %d 超出范围（共 %d P）", page, total)
}
