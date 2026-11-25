//go:build integration

// Package smoke 是站点可用性的探针：只做解析与取流，不落盘。
//
// 它与普通单元测试分开（build tag = integration），因为它打真实网络：
// 普通 CI 必须保持离线可重复，而这里要的恰恰是「站点有没有改版」。
// 由 .github/workflows/smoke.yml 每日跑一次，失败即开 issue。
package smoke

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/bili-dl/api"
	"github.com/Felix2yu/bili-dl/c"
	"github.com/Felix2yu/bili-dl/engine"
	"github.com/Felix2yu/bili-dl/site"
	"github.com/Felix2yu/bili-dl/site/douyin"
	"github.com/Felix2yu/bili-dl/site/x"
	"github.com/Felix2yu/bili-dl/site/xiaohongshu"
)

// 默认用长期稳定的公开内容；可用环境变量覆盖（例如换成自己的测试稿）。
const (
	defaultBilibili = "https://www.bilibili.com/video/BV1GJ411x7h7"
	// jack 的第一条推文：纯文字、永远存在，用来探「syndication 端点是否还活着」。
	defaultX = "https://x.com/jack/status/20"
	// 用户验证过的正文页直链（短链路径由离线单测覆盖）；token 过期时用 SMOKE_XHS_URL 覆盖。
	defaultXHS = "https://www.xiaohongshu.com/explore/6b06d8f2000000002b006721" +
		"?xsec_token=CBt9QfeBEeel3ucFVeAuQAAvyXXhYBdVNOgscQ3BNbreI=&xsec_source=app_share"
	// 抖音样本只探「壳是否仍然没有直链」；过期时用 SMOKE_DOUYIN_URL 覆盖。
	defaultDouyin = "6.64 复制打开抖音 https://v.douyin.com/fy9rPadJ1SU/ :9pm"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestMain(m *testing.M) {
	// 让遗留的 bilibili 路径拿到一份确定的默认配置。
	c.Parse(nil)
	c.O = os.TempDir()
	os.Exit(m.Run())
}

// TestBilibiliResolveAndStream 验证：能解析视频、能选到码流。
// 这一步覆盖 WBI 签名、view 接口与 DASH 选流三处最容易因改版失效的地方。
func TestBilibiliResolveAndStream(t *testing.T) {
	url := envOr("SMOKE_BILIBILI_URL", defaultBilibili)

	bv, page, err := api.ParseInput(url)
	if err != nil {
		t.Fatalf("解析输入失败: %v", err)
	}
	video, err := api.VideoFromBV(bv)
	if err != nil {
		t.Fatalf("取视频详情失败（view 接口或 WBI 可能已变）: %v", err)
	}
	if video.Title == "" {
		t.Error("详情里没有标题")
	}
	target, err := api.VideoForPage(video, page)
	if err != nil {
		t.Fatalf("定位分 P 失败: %v", err)
	}
	stream, err := api.GetStream(target)
	if err != nil {
		t.Fatalf("选流失败（DASH 结构可能已变）: %v", err)
	}
	if stream.V == "" && stream.A == "" {
		t.Fatal("选流结果里没有可用地址")
	}
	t.Logf("%s | 视频流=%v 音频流=%v", video.Title, stream.V != "", stream.A != "")
}

// TestXEndpointAlive 验证 syndication 端点仍可访问且信封结构未变。
//
// 这里**不要求**必须有视频：默认样本是纯文字推文，能取到 id_str/text 就说明
// 端点与信封可用；如果连这个都挂了，说明 X 改了取数方式（需要改用 GraphQL）。
func TestXEndpointAlive(t *testing.T) {
	url := envOr("SMOKE_X_URL", defaultX)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	eng := engine.New(engine.Options{}).Register(x.NewAdapter())
	media, err := eng.Resolve(ctx, url, site.ResolveOptions{})
	if err != nil {
		code := engine.CodeOf(err)
		switch code {
		case engine.CodeNotFound:
			// 纯文字/无视频，端点本身是好的。
			t.Logf("端点可用，该内容没有视频：%v", err)
			return
		case engine.CodeUpstreamChanged:
			t.Fatalf("端点或信封结构已变（%s）：%v", code, err)
		case engine.CodeNetwork:
			// 网络不可达无法区分「站点改版」与「这台机器到不了 X」，
			// 直接失败会把每日探针变成噪声；记为跳过并保留日志。
			t.Skipf("网络不可达，无法据此判断站点改版：%v", err)
		default:
			t.Fatalf("解析失败（%s）：%v", code, err)
		}
	}
	if len(media) == 0 {
		t.Fatal("解析成功但没有媒体")
	}

	set, err := eng.Streams(ctx, media[0], site.Selection{})
	if err != nil {
		t.Fatalf("取流失败（variants 结构可能已变）: %v", err)
	}
	if len(set.Videos) == 0 {
		t.Fatal("没有可用视频流")
	}
	t.Logf("%s | 码流 %d 条，首条 HLS=%v", media[0].Title, len(set.Videos), set.Videos[0].HLS)
}

// TestXiaohongshuResolveAndStream 验证短链展开、__INITIAL_STATE__ 提取和 h264 直链。
func TestXiaohongshuResolveAndStream(t *testing.T) {
	raw := envOr("SMOKE_XHS_URL", defaultXHS)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	eng := engine.New(engine.Options{}).Register(xiaohongshu.NewAdapter())
	media, err := eng.Resolve(ctx, raw, site.ResolveOptions{})
	if err != nil {
		switch engine.CodeOf(err) {
		case engine.CodeNetwork:
			t.Skipf("网络不可达：%v", err)
		case engine.CodeAuthRequired:
			t.Skipf("样本 token 过期或被风控要求登录，换一个 SMOKE_XHS_URL：%v", err)
		case engine.CodeUpstreamChanged:
			t.Fatalf("小红书首屏结构或短链已变：%v", err)
		default:
			t.Fatalf("解析失败（%s）：%v", engine.CodeOf(err), err)
		}
	}
	if len(media) == 0 || media[0].ID == "" {
		t.Fatal("解析结果里没有笔记")
	}
	set, err := eng.Streams(ctx, media[0], site.Selection{})
	if err != nil {
		t.Fatalf("取流失败（h264 结构可能已变）：%v", err)
	}
	if len(set.Videos) == 0 {
		t.Fatal("没有可用视频流")
	}
	t.Logf("%s | %s | 码流 %d 条，首条=%s", media[0].ID, media[0].Title, len(set.Videos), set.Videos[0].URL[:min(len(set.Videos[0].URL), 80)])
}

// TestDouyinShellProbe 不把「拿不到直链」当成失败：当前抖音分享页本来就只有风控壳。
// 真正的信号是壳结构改变或意外拿到了直链——后者意味着可以放开完整下载。
func TestDouyinShellProbe(t *testing.T) {
	raw := envOr("SMOKE_DOUYIN_URL", defaultDouyin)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	eng := engine.New(engine.Options{}).Register(douyin.NewAdapter())
	media, err := eng.Resolve(ctx, raw, site.ResolveOptions{})
	if err == nil {
		t.Logf("抖音首屏已暴露直链（%d 条媒体），可以考虑启用完整下载", len(media))
		return
	}
	switch engine.CodeOf(err) {
	case engine.CodeNetwork:
		t.Skipf("网络不可达：%v", err)
	case engine.CodeUpstreamChanged:
		if strings.Contains(err.Error(), "风控壳") {
			t.Logf("仍是风控壳，符合当前预期：%v", err)
			return
		}
		t.Fatalf("抖音首屏结构已变，需要重新评估：%v", err)
	case engine.CodeAuthRequired:
		t.Logf("需要登录态，符合受限内容预期：%v", err)
	default:
		t.Fatalf("解析失败（%s）：%v", engine.CodeOf(err), err)
	}
}
