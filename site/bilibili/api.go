package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	url2 "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Felix2yu/bili-dl/auth"
	"github.com/Felix2yu/bili-dl/c"
	"github.com/Felix2yu/bili-dl/site"
	"github.com/Felix2yu/bili-dl/util"
)

var client = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		//禁止复用连接，防止同一个连接长时间大流量被限速
		DisableKeepAlives: true,
	},
}

const defaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36 Edg/123.0.0.0"

// newAPIRequest 统一构造 B 站 API 请求：带 context、UA、Referer，
// 仅在 SESSDATA 非空时才写入 cookie（避免发出 Value="" 的畸形 cookie）。
//
// 凭据来源优先级：context 里的按调用凭据 > c 包全局（兼容 bili-history 的嵌入调用）。
// 这样同一进程内多个任务可以带不同账号并发，不会互相串。
func newAPIRequest(ctx context.Context, rawURL string) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	ua := defaultUA
	if v, ok := auth.HeaderValue(ctx, "User-Agent"); ok && v != "" {
		ua = v
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", "https://www.bilibili.com/")

	sessdata := c.Cookie
	if v, ok := auth.CookieValue(ctx, "SESSDATA"); ok && v != "" {
		sessdata = v
	}
	if sessdata != "" {
		req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: sessdata})
	}
	return req, nil
}

// apiCtx 返回遗留路径使用的 context：优先 c.Ctx（CLI 注入取消信号），否则空 context。
// 引擎路径不走这里，而是把调用方的 ctx 显式传下去。
func apiCtx() context.Context {
	if c.Ctx != nil {
		return c.Ctx
	}
	return context.Background()
}

type biliVideoPage struct {
	Page     int         `json:"page"`
	Cid      json.Number `json:"cid"`
	Part     string      `json:"part"`
	Duration int         `json:"duration"`
}

type biliVideoInfoResp struct {
	Code int `json:"code"`
	Data struct {
		BVID  string      `json:"bvid"`
		Cid   json.Number `json:"cid"`
		Title string      `json:"title"`
		Pic   string      `json:"pic"`
		Owner struct {
			MID  int64  `json:"mid"`
			Name string `json:"name"`
		} `json:"owner"`
		Page struct {
			Count int `json:"count"`
		} `json:"page"`
		Pages     []biliVideoPage `json:"pages"`
		UGCSeason *ugcSeason      `json:"ugc_season"`
	} `json:"data"`
}

// dashInfo 是 playurl 响应里的 dash 载荷（UGC 在 data.dash，PGC 在 result.dash）。
type dashInfo struct {
	Video []dashStream `json:"video"`
	Audio []dashStream `json:"audio"`
	Dolby struct {
		Audio []dashStream `json:"audio"`
	} `json:"dolby"`
	Flac struct {
		Audio []dashStream `json:"audio"`
	} `json:"flac"`
}

type biliDashResp struct {
	Code int `json:"code"`
	Data struct {
		Dash dashInfo `json:"dash"`
	} `json:"data"`
}

type dashStream struct {
	ID        float64 `json:"id"`
	Codecs    string  `json:"codecs"`
	Width     float64 `json:"width"`
	Height    float64 `json:"height"`
	Bandwidth float64 `json:"bandwidth"`
	BaseURL   string  `json:"base_url"`
}

type biliVlistResp struct {
	Data struct {
		Page struct {
			Count int `json:"count"`
		} `json:"page"`
		List struct {
			VList []struct {
				BV string `json:"bvid"`
			} `json:"vlist"`
		} `json:"list"`
	} `json:"data"`
}

func videoInfo(bv string) ([]byte, error) { return videoInfoCtx(apiCtx(), bv) }

// videoInfoCtx 与 videoInfo 相同，但由调用方控制 context（取消信号与按调用凭据）。
func videoInfoCtx(ctx context.Context, bv string) ([]byte, error) {
	url := "https://api.bilibili.com/x/web-interface/view"
	parse, _ := url2.Parse(url)
	query := parse.Query()
	query.Add("bvid", bv)
	parse.RawQuery = query.Encode()
	url = parse.String()
	req, err := newAPIRequest(ctx, url)

	if err != nil {
		log.Println(err)
		return nil, err
	}
	req.Header.Add("User-Agent", "Apifox/1.0.0 (https://www.apifox.cn)")

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	return body, nil
}

func ResolveVideo(v *Video) (*Video, error) {
	info, err := videoInfo(v.BV)
	if err != nil {
		return nil, err
	}
	var resp biliVideoInfoResp
	if err := json.Unmarshal(info, &resp); err != nil {
		return nil, err
	}
	return applyVideoInfo(v, &resp)
}

// applyVideoInfo 把投稿信息填入 Video：owner、封面、全部分 P。
// 单 P 时 Pages 也有一条记录，供调用方统一处理；ActivePage 保持调用方传入值。
func applyVideoInfo(v *Video, resp *biliVideoInfoResp) (*Video, error) {
	if resp.Code != 0 {
		return nil, fmt.Errorf("B站返回错误 code=%d", resp.Code)
	}
	v.Cid = resp.Data.Cid.String()
	v.Title = resp.Data.Title
	v.Owner = resp.Data.Owner.Name
	v.OwnerM = resp.Data.Owner.MID
	v.Pic = resp.Data.Pic
	v.UGCSeason = resp.Data.UGCSeason
	if v.BV == "" {
		v.BV = resp.Data.BVID
	}
	if len(resp.Data.Pages) > 0 {
		v.Pages = resp.Data.Pages
	} else {
		v.Pages = []biliVideoPage{{
			Page: 1, Cid: resp.Data.Cid, Part: resp.Data.Title,
		}}
	}
	return v, nil
}

// VideoForPage 返回某个分 P 对应的下载目标（复制一份， cid/title 指向该 P）。
// 保留 Pages 供文件名模板生成分 P 标签；用 ActivePage 标识当前 P，调用方据此避免重复展开。
// page 为 1-based；越界时返回错误。
func VideoForPage(v *Video, page int) (Video, error) {
	out := *v
	out.ActivePage = page
	if page <= 0 || page > len(v.Pages) {
		return out, fmt.Errorf("分 P 页码 %d 超出范围（共 %d P）", page, len(v.Pages))
	}
	p := v.Pages[page-1]
	out.Cid = p.Cid.String()
	// 主标题保持视频总标题；part 由模板从 Pages[page-1] 取，
	// 这样多 P 文件名形如 "总标题 - 01 - 分P名"。
	return out, nil
}

func videoFromUP(ctx context.Context, mid string, pn int) (rt []byte, err error) {
	url := "https://api.bilibili.com/x/space/wbi/arc/search?order=pubdate&ps=49"
	parse, _ := url2.Parse(url)
	query := parse.Query()
	query.Add("mid", mid)
	query.Add("pn", strconv.Itoa(pn))
	parse.RawQuery = query.Encode()
	url = parse.String()
	url, err = sign(url)
	if err != nil {
		return nil, err
	}

	req, err := newAPIRequest(ctx, url)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return body, err
}

type Video struct {
	Title  string `json:"title,omitempty"`
	BV     string `json:"bv,omitempty"`
	Cid    string `json:"cid,omitempty"`
	Owner  string `json:"owner,omitempty"`
	OwnerM int64  `json:"owner_mid,omitempty"`
	Pic    string `json:"pic,omitempty"`
	// Pages 是投稿视频的全部分 P。单个视频（未展开）时为空，
	// 此时用上面的 Cid/Title 表示当前下载目标。
	Pages []biliVideoPage `json:"pages,omitempty"`
	// ActivePage 是本次要下载的分 P（1-based），0 表示单 P / 未指定。
	ActivePage int `json:"active_page,omitempty"`
	// PGC 标记番剧/剧集（走 pgc/player/web/playurl，响应包在 result 而非 data）。
	PGC  bool   `json:"pgc,omitempty"`
	EpID string `json:"ep_id,omitempty"`
	// UGCSeason 是投稿所属合集（仅主视频解析时填充，用于展开）。
	UGCSeason *ugcSeason `json:"-"`
}

func AllVideo(mid string) ([]Video, error) {
	// 第一页只请求一次：既拿总数，也复用它的列表，避免原来的重复请求。
	first, err := videoFromUP(apiCtx(), mid, 1)
	if err != nil {
		return nil, err
	}
	var firstResp biliVlistResp
	if err := json.Unmarshal(first, &firstResp); err != nil {
		return nil, err
	}
	count := firstResp.Data.Page.Count
	if count == 0 {
		count = 1
	}
	pn := (count + 48) / 49
	if pn < 1 {
		pn = 1
	}

	var videos []Video
	collect := func(raw []byte) {
		var pageResp biliVlistResp
		if err := json.Unmarshal(raw, &pageResp); err != nil {
			log.Printf("解析 UP 主列表失败: %v", err)
			return
		}
		for _, v := range pageResp.Data.List.VList {
			if v.BV != "" {
				videos = append(videos, Video{BV: v.BV})
			}
		}
	}
	collect(first) // 复用第一页
	for i := 2; i <= pn; i++ {
		if c.Ctx != nil {
			select {
			case <-c.Ctx.Done():
				return nil, c.Ctx.Err()
			default:
			}
		}
		time.Sleep(time.Second)
		bytes, err := videoFromUP(apiCtx(), mid, i)
		if err != nil {
			log.Printf("获取 UP 主列表第 %d 页失败: %v, continuing...", i, err)
			continue
		}
		collect(bytes)
	}
	return videos, nil
}

func codec2i(codec string) int {
	if strings.HasPrefix(codec, "avc") {
		return 1
	} else if strings.HasPrefix(codec, "hev") {
		return 2
	} else if strings.HasPrefix(codec, "av01") {
		return 3
	}
	return 0
}

func matchResolution(id float64, width float64, resolution string) bool {
	switch resolution {
	case "8k":
		return id == 127 || width >= 7680
	case "dolby":
		return id == 126
	case "4k":
		return id == 120
	case "1080p":
		return id == 116 || id == 112 || id == 80
	case "720p":
		return id == 64
	case "480p":
		return id == 32
	case "360p":
		return id == 16
	default:
		return true
	}
}

func matchCodec(codecs string, codec string) bool {
	switch codec {
	case "av1":
		return strings.HasPrefix(codecs, "av01")
	case "hevc":
		return strings.HasPrefix(codecs, "hev")
	case "avc":
		return strings.HasPrefix(codecs, "avc")
	default:
		return true
	}
}

// pickCodec 按 --download-vcodec-priority 的顺序，返回第一档在候选流里存在的编码。
// restrict 为 false 表示不限制编码（优先级与旧 -codec 都为空）。
// 优先级列表全部落空时返回 ("", true)，让调用方得到空集并报错，而不是悄悄选别的编码。
func pickCodec(streams []dashStream, priorities []string, legacy string) (codec string, restrict bool) {
	if len(priorities) > 0 {
		for _, candidate := range priorities {
			for _, s := range streams {
				if matchCodec(s.Codecs, candidate) {
					return candidate, true
				}
			}
		}
		return "", true
	}
	if legacy != "" {
		return legacy, true
	}
	return "", false
}

func codecExt(codecs string) string {
	if strings.HasPrefix(codecs, "av01") {
		return "mkv"
	} else if strings.HasPrefix(codecs, "hev") {
		return "mov"
	}
	return "mp4"
}

type Stream struct {
	V string
	A string
	Video
	VCodec  string
	VWidth  float64
	VHeight float64
	// 音频流编码：用于无损音轨（flac 等）保留容器，普通 AAC 为 m4a。
	ACodec string
	// relBase 是模板渲染出的相对路径（不含扩展名），由 Dl 计算，DV/DA/Merge 共用。
	relBase string
}

// fetchDash 获取 DASH 流信息。UGC 投稿走 x/player/wbi/playurl，
// PGC 番剧走 pgc/player/web/playurl（响应包在 result 字段）。
func fetchDash(v Video) (*biliDashResp, error) { return fetchDashCtx(apiCtx(), v) }

// fetchDashCtx 由调用方控制 context（取消信号与按调用凭据）。
func fetchDashCtx(ctx context.Context, v Video) (*biliDashResp, error) {
	base := "https://api.bilibili.com/x/player/wbi/playurl?fnver=0&fnval=3216&fourk=1&qn=127"
	if v.PGC {
		base = "https://api.bilibili.com/pgc/player/web/playurl?fnver=0&fnval=3216&fourk=1&qn=127"
	}
	parse, _ := url2.Parse(base)
	query := parse.Query()
	query.Add("bvid", v.BV)
	query.Add("cid", v.Cid)
	if v.PGC && v.EpID != "" {
		query.Add("ep_id", v.EpID)
	}
	parse.RawQuery = query.Encode()
	u, err := sign(parse.String())
	if err != nil {
		return nil, err
	}
	req, err := newAPIRequest(ctx, u)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	// UGC 响应是 {code, data:{dash:{...}}}；PGC 响应是 {code, result:{dash:{...}}}。
	// 两者只差外层字段名，dash 载荷结构一致，统一收敛成 biliDashResp 供下游使用。
	var envelope struct {
		Code   int `json:"code"`
		Result *struct {
			Dash dashInfo `json:"dash"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	if envelope.Result != nil {
		out := &biliDashResp{}
		out.Data.Dash = envelope.Result.Dash
		return out, nil
	}
	var ugc biliDashResp
	if err := json.Unmarshal(body, &ugc); err != nil {
		return nil, err
	}
	if ugc.Code != 0 {
		return nil, fmt.Errorf("获取播放地址失败 code=%d（番剧可能需要登录/大会员）", ugc.Code)
	}
	return &ugc, nil
}

// GetStream 用当前全局配置选流，保留旧签名供 CLI 与 bili-history 使用。
// 新代码应调用 currentStreamConfig().getStream 或直接构造 streamConfig。
func GetStream(v Video) (*Stream, error) {
	return currentStreamConfig().getStream(v)
}

// getStream 按显式配置选流，不读任何全局变量（除 HTTP 层的凭据与取消信号）。
func (cfg streamConfig) getStream(v Video) (*Stream, error) { return cfg.getStreamCtx(apiCtx(), v) }

// getStreamCtx 是选流的真正实现，context 由调用方提供。
func (cfg streamConfig) getStreamCtx(ctx context.Context, v Video) (*Stream, error) {
	dashResp, err := fetchDashCtx(ctx, v)
	if err != nil {
		return nil, err
	}

	var videoURL, vCodec string
	var vWidth, vHeight float64

	if !cfg.AudioOnly {
		videoStreams := dashResp.Data.Dash.Video
		if len(videoStreams) == 0 {
			return nil, fmt.Errorf("no video streams available")
		}

		// 先按分辨率收窄，再按编码优先级取第一档有候选的编码。
		// 不能只用 --vcodec 的单一编码硬过滤：视频可能没有 AV1，
		// 这时应回退到 hevc/avc，而不是直接报「没有找到匹配的视频流」。
		var byResolution []dashStream
		for _, s := range videoStreams {
			if matchResolution(s.ID, s.Width, cfg.Resolution) {
				byResolution = append(byResolution, s)
			}
		}
		if len(byResolution) == 0 {
			return nil, fmt.Errorf("没有找到匹配的视频流 (分辨率: %s, 编码: %s)", cfg.Resolution, cfg.codecLabel())
		}

		codec, restrict := pickCodec(byResolution, cfg.VCodecPriority, cfg.Codec)
		var filtered []dashStream
		for _, s := range byResolution {
			if restrict && !matchCodec(s.Codecs, codec) {
				continue
			}
			filtered = append(filtered, s)
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("没有找到匹配的视频流 (分辨率: %s, 编码: %s)", cfg.Resolution, cfg.codecLabel())
		}

		// -q 指定画质 id 时优先精确命中；未指定或无命中则按编码优先级 + 分辨率择优。
		bestIdx := selectVideoStream(filtered, cfg.VideoQuality)
		videoURL = filtered[bestIdx].BaseURL
		vCodec = filtered[bestIdx].Codecs
		vWidth = filtered[bestIdx].Width
		vHeight = filtered[bestIdx].Height
	}

	// 候选音频：普通 audio + dolby + flac。按 -aq 指定的 id 精确匹配，否则取码率最高。
	allAudio := make([]dashStream, 0, len(dashResp.Data.Dash.Audio))
	allAudio = append(allAudio, dashResp.Data.Dash.Audio...)
	allAudio = append(allAudio, dashResp.Data.Dash.Dolby.Audio...)
	allAudio = append(allAudio, dashResp.Data.Dash.Flac.Audio...)
	if len(allAudio) == 0 {
		return nil, fmt.Errorf("no audio streams available")
	}
	best := pickAudio(allAudio, cfg.AudioQuality)
	stream := &Stream{
		V: videoURL, A: best.BaseURL, Video: v,
		VCodec: vCodec, VWidth: vWidth, VHeight: vHeight,
		ACodec: best.Codecs,
	}
	return stream, nil
}

// pickAudio 按音质 id 选流。wanted > 0 时优先精确匹配 id；
// 未命中则退回码率最高的普通音轨。flac/dolby 保留其原始编码供容器决策。
func pickAudio(streams []dashStream, wanted int) dashStream {
	if wanted > 0 {
		for _, s := range streams {
			if int(s.ID) == wanted {
				return s
			}
		}
	}
	best := streams[0]
	for _, s := range streams[1:] {
		if s.Bandwidth > best.Bandwidth {
			best = s
		}
	}
	return best
}

// selectVideoStream 返回 filtered 中最优流的下标。wanted > 0 时先在指定画质 id 内
// 按编码优先级（av1>hevc>avc）取优；id 未命中则退回全局同规则择优。
func selectVideoStream(streams []dashStream, wanted int) int {
	best := -1
	if wanted > 0 {
		for i, s := range streams {
			if int(s.ID) != wanted {
				continue
			}
			if best < 0 || codec2i(s.Codecs) > codec2i(streams[best].Codecs) {
				best = i
			}
		}
	}
	if best < 0 {
		best = 0
		for i := 1; i < len(streams); i++ {
			ci, cj := codec2i(streams[i].Codecs), codec2i(streams[best].Codecs)
			if ci > cj || (ci == cj && streams[i].Width > streams[best].Width) {
				best = i
			}
		}
	}
	return best
}

// audioExt 根据音频编码返回容器扩展名。无损（flac）保留 flac，其余用 m4a。
func audioExt(codecs string) string {
	lower := strings.ToLower(codecs)
	if strings.Contains(lower, "flac") {
		return "flac"
	}
	return "m4a"
}

func PrintDASHInfo(v Video) error {
	url := "https://api.bilibili.com/x/player/wbi/playurl?fnver=0&fnval=3216&fourk=1&qn=127"
	parse, _ := url2.Parse(url)
	query := parse.Query()
	query.Add("bvid", v.BV)
	query.Add("cid", v.Cid)
	parse.RawQuery = query.Encode()
	url = parse.String()
	var err error
	url, err = sign(url)
	if err != nil {
		return err
	}

	req, err := newAPIRequest(apiCtx(), url)
	if err != nil {
		return err
	}

	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	var dashResp biliDashResp
	if err := json.Unmarshal(body, &dashResp); err != nil {
		return fmt.Errorf("解析DASH信息失败: %w", err)
	}

	fmt.Printf("===== 视频DASH信息 =====\n")
	fmt.Printf("标题: %s\n", v.Title)
	fmt.Printf("BV号: %s\n", v.BV)
	fmt.Printf("CID: %s\n", v.Cid)
	fmt.Println()

	videoStreams := dashResp.Data.Dash.Video
	fmt.Printf("视频流 (%d 个):\n", len(videoStreams))
	for i, vs := range videoStreams {
		fmt.Printf("  [%d] 画质ID: %.0f | 分辨率: %.0fx%.0f | 编码: %s | 码率: %.0f bps\n",
			i+1, vs.ID, vs.Width, vs.Height, vs.Codecs, vs.Bandwidth)
		fmt.Printf("      URL: %s\n", vs.BaseURL)
	}
	fmt.Println()

	audioStreams := dashResp.Data.Dash.Audio
	fmt.Printf("音频流 (%d 个):\n", len(audioStreams))
	for i, as := range audioStreams {
		fmt.Printf("  [%d] 音质ID: %.0f | 编码: %s | 码率: %.0f bps\n",
			i+1, as.ID, as.Codecs, as.Bandwidth)
		fmt.Printf("      URL: %s\n", as.BaseURL)
	}
	fmt.Println("========================")
	return nil
}

func Dl(stream *Stream) error {
	// 模板渲染出相对路径（含 owner 子目录、分 P 标签），扩展名按视频编码决定。
	videoExt := codecExt(stream.VCodec)
	stream.relBase = strings.TrimSuffix(RenderPath(&stream.Video, videoExt), "."+videoExt)
	// 确保目标子目录存在。
	if err := ensureDir(filepath.Join(c.O, stream.relBase+"."+videoExt)); err != nil {
		return err
	}

	if stream.V != "" {
		if err := DV(stream); err != nil {
			return err
		}
	}
	if err := DA(stream); err != nil {
		return err
	}
	log.Println(stream.Title, "下载完成")
	return nil
}

// videoPath / audioPath / mergedPath 统一由 relBase 生成，保证 DV/DA/Merge 落点一致。
func (s *Stream) videoPath() string {
	return filepath.Join(c.O, s.relBase+"."+codecExt(s.VCodec))
}

func (s *Stream) audioPath() string {
	return filepath.Join(c.O, s.relBase+"."+audioExt(s.ACodec))
}

func (s *Stream) mergedPath() string {
	// 合并产物与最终视频同名；Merge 成功后删掉分离的流。
	return s.videoPath()
}

// RelBase 返回产物相对基路径（含 UP 主子目录与分 P 标签，不含扩展名）。
// 由 Dl 计算并缓存；未下载时为空。
func (s *Stream) RelBase() string { return s.relBase }

// VideoPath 返回视频流（合并后即成品）的绝对路径。
func (s *Stream) VideoPath() string { return s.videoPath() }

// AudioPath 返回音频流的绝对路径；合并且删除分离流后该文件不再存在。
func (s *Stream) AudioPath() string { return s.audioPath() }

// Outputs 返回本次下载实际落盘的产物。
//
// 以「文件是否真实存在」为准：合并后删除的分离音轨、被关闭的弹幕/字幕/封面
// 都不会出现在结果里，因此调用方不必自己猜哪一步生效了。
func (s *Stream) Outputs() []site.Output {
	if s == nil || s.relBase == "" {
		return nil
	}
	var out []site.Output
	add := func(path, kind string) {
		out = append(out, outputOf(path, kind)...)
	}
	if s.V != "" {
		add(s.videoPath(), "video")
	}
	if s.A != "" {
		add(s.audioPath(), "audio")
	}
	base := filepath.Join(c.O, s.relBase)
	add(base+".cover.jpg", "cover")
	add(base+".danmaku.ass", "danmaku")
	// 字幕语言由站点返回，可能多个，用通配收集。
	if matches, err := filepath.Glob(base + ".*.srt"); err == nil {
		sort.Strings(matches)
		for _, m := range matches {
			add(m, "subtitle")
		}
	}
	return out
}

// 下载失败时的最大尝试次数（应对 CDN 偶发断流）
const downloadAttempts = 3

// 下载进度元信息：仅在 NDJSON 模式下用于标注事件归属。
type downloadMeta struct {
	BV   string
	Page int
	Kind string // video / audio / cover / ...
}

func downloadFileWith(rawURL string, path string, meta downloadMeta) error {
	emitProgress(ProgressEvent{Event: "start", MediaID: meta.BV, BV: meta.BV, Page: meta.Page, Kind: meta.Kind, Message: filepath.Base(path)})
	var lastErr error
	for i := 1; i <= downloadAttempts; i++ {
		if i > 1 {
			// 指数退避：1s、2s；期间响应取消信号。
			wait := time.Duration(1<<uint(i-2)) * time.Second
			if c.Ctx != nil {
				select {
				case <-c.Ctx.Done():
					return c.Ctx.Err()
				case <-time.After(wait):
				}
			} else {
				time.Sleep(wait)
			}
			log.Printf("重试下载 (%d/%d): %v", i, downloadAttempts, path)
		}
		if lastErr = downloadOnceWith(rawURL, path, meta); lastErr == nil {
			emitProgress(ProgressEvent{
				Event:   "done",
				MediaID: meta.BV,
				BV:      meta.BV,
				Page:    meta.Page,
				Kind:    meta.Kind,
				Message: filepath.Base(path),
				// 显式回报产出：调用方据此入库，不必再扫目录差集。
				Outputs: outputOf(path, meta.Kind),
			})
			return nil
		}
		if c.Ctx != nil && c.Ctx.Err() != nil {
			return c.Ctx.Err()
		}
		log.Printf("下载失败 (%d/%d): %v", i, downloadAttempts, lastErr)
	}
	emitProgress(ProgressEvent{Event: "error", MediaID: meta.BV, BV: meta.BV, Page: meta.Page, Kind: meta.Kind, Message: fmt.Sprint(lastErr)})
	return lastErr
}

// outputOf 把已落盘文件转成一条 Output；文件不存在时返回 nil（不制造幻影产出）。
func outputOf(path, kind string) []site.Output {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil
	}
	out := site.Output{Path: path, Kind: normalizeKind(kind, path), Bytes: st.Size()}
	if out.Kind == "video" || out.Kind == "audio" {
		out.Container = strings.TrimPrefix(filepath.Ext(path), ".")
	}
	return []site.Output{out}
}

// normalizeKind 兜底修正 kind：旧调用方可能传 ".mp4" 这类扩展名当 kind。
func normalizeKind(kind, path string) string {
	switch kind {
	case "video", "audio", "cover", "danmaku", "subtitle", "image":
		return kind
	}
	switch strings.TrimPrefix(filepath.Ext(path), ".") {
	case "mp4", "mkv", "mov", "flv":
		return "video"
	case "m4a", "flac", "mp3":
		return "audio"
	case "jpg", "jpeg", "png", "webp":
		return "cover"
	case "ass", "xml":
		return "danmaku"
	case "srt", "vtt":
		return "subtitle"
	}
	return "video"
}

func downloadOnce(rawURL string, path string) error {
	return downloadOnceWith(rawURL, path, downloadMeta{Kind: "video"})
}

func downloadOnceWith(rawURL string, path string, meta downloadMeta) error {
	if err := ensureDir(path); err != nil {
		return err
	}
	var existing int64
	if st, err := os.Stat(path); err == nil {
		existing = st.Size()
	} else if !os.IsNotExist(err) {
		return err
	}

	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Referer", "https://www.bilibili.com")
	if existing > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existing))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 本地文件已完整时，CDN 的签名 URL 常对 Range 直接回 416（请求区间超出文件末尾）。
	// 有 Content-Range 且本地已覆盖完整长度就当作完成；否则删掉残件从头重下。
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && existing > 0 {
		if total := contentRangeTotal(resp.Header.Get("Content-Range")); total > 0 && existing >= total {
			log.Printf("本地文件已完整（%d bytes），跳过下载：%s", existing, filepath.Base(path))
			emitProgress(ProgressEvent{
				Event: "done", MediaID: meta.BV, BV: meta.BV, Page: meta.Page, Kind: meta.Kind,
				Current: existing, Total: existing, Percent: 100,
				Outputs: outputOf(path, meta.Kind),
			})
			return nil
		}
		_ = resp.Body.Close()
		_ = os.Remove(path)
		return downloadOnceWith(rawURL, path, meta)
	}

	// 206 Partial Content：从断点续写；其它状态一律从头覆盖。
	appendMode := resp.StatusCode == http.StatusPartialContent && existing > 0
	if resp.StatusCode != http.StatusOK && !appendMode {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	flags := os.O_CREATE | os.O_RDWR
	if appendMode {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(path, flags, 0o666)
	if err != nil {
		return err
	}
	defer file.Close()

	// 限速 reader + 进度 writer：两者都不改变下载语义，只观测/节流字节流。
	body := util.NewRateReader(resp.Body, c.RateBytes, ctx)
	total := resp.ContentLength
	if appendMode && total > 0 {
		total += existing
	}
	sink := io.Writer(file)
	if progressEnabled() {
		ev := ProgressEvent{BV: meta.BV, Page: meta.Page, Kind: meta.Kind}
		sink = newProgressWriter(file, ev, total)
	}
	written, err := io.Copy(sink, body)
	if err != nil {
		return err
	}
	if appendMode {
		log.Printf("续传完成：%s (+%d bytes)", filepath.Base(path), written)
	}
	return nil
}

// contentRangeTotal 从 Content-Range（"bytes START-END/TOTAL" 或 "bytes */TOTAL"）中取总长度。
func contentRangeTotal(header string) int64 {
	const prefix = "bytes "
	if !strings.HasPrefix(header, prefix) {
		return 0
	}
	slash := strings.LastIndexByte(header, '/')
	if slash < 0 {
		return 0
	}
	total, err := strconv.ParseInt(strings.TrimSpace(header[slash+1:]), 10, 64)
	if err != nil || total <= 0 {
		return 0
	}
	return total
}

func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func DV(stream *Stream) error {
	return downloadFileWith(stream.V, stream.videoPath(), downloadMeta{
		BV: stream.BV, Page: stream.ActivePage, Kind: "video",
	})
}

func DA(stream *Stream) error {
	return downloadFileWith(stream.A, stream.audioPath(), downloadMeta{
		BV: stream.BV, Page: stream.ActivePage, Kind: "audio",
	})
}

func VideoFromBV(bv string) (*Video, error) {
	info, err := videoInfo(bv)
	if err != nil {
		return nil, err
	}
	var resp biliVideoInfoResp
	if err := json.Unmarshal(info, &resp); err != nil {
		return nil, err
	}
	video := Video{BV: bv}
	if _, err := applyVideoInfo(&video, &resp); err != nil {
		return nil, err
	}
	// 不打印完整结构体：SESSDATA 虽不在其中，但保持日志最小化。
	log.Printf("解析视频 %s: %s (%d P, owner=%s)", bv, video.Title, len(video.Pages), video.Owner)
	return &video, nil
}

func Merge(stream *Stream) error {
	if stream.relBase == "" {
		stream.relBase = strings.TrimSuffix(
			RenderPath(&stream.Video, codecExt(stream.VCodec)), "."+codecExt(stream.VCodec))
	}

	if stream.V == "" {
		// 仅音频：文件已是最终形态（flac 保留原样，AAC 为 m4a），无需重命名。
		log.Println(stream.Title, "音频处理完成")
		return nil
	}

	video := stream.videoPath()
	audio := stream.audioPath()
	output := stream.mergedPath()
	// 合并到临时文件，成功后原子替换，避免失败时留下半个最终文件。
	// 临时文件必须保留正确的容器扩展名（ffmpeg 靠它推断 muxer），
	// 所以插入 .merging 到扩展名之前：xxx.merging.mp4。
	ext := filepath.Ext(output)
	tmp := strings.TrimSuffix(output, ext) + ".merging" + ext
	_ = os.Remove(tmp)
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", video, "-i", audio, "-c", "copy", tmp)
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("ffmpeg merge failed: %w", err)
	}
	if err := os.Rename(tmp, output); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// output 与 video 同路径，Rename 已覆盖原视频；Delete 只需删掉分离音轨。
	if c.Delete && audio != output {
		_ = os.Remove(audio)
	}
	log.Println(stream.Title, "合并完成")
	return nil
}

var reg = regexp.MustCompile(`[/\\:*?"<>|]`)

// 去掉文件名中的非法字符
func fileNameFix(name string) string {
	return reg.ReplaceAllString(name, " ")
}
