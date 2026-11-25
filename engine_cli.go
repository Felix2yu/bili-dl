package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Felix2yu/bili-dl/auth"
	"github.com/Felix2yu/bili-dl/c"
	"github.com/Felix2yu/bili-dl/engine"
	"github.com/Felix2yu/bili-dl/site"
	"github.com/Felix2yu/bili-dl/site/bilibili"
	"github.com/Felix2yu/bili-dl/site/douyin"
	"github.com/Felix2yu/bili-dl/site/x"
	"github.com/Felix2yu/bili-dl/site/xiaohongshu"
)

// engineHandles 判断这批输入是否该交给通用引擎。
//
// 规则：只要有一条不是哔哩哔哩链接，就走引擎；全是 B 站时继续走既有 DASH 实现
// （它包含 WBI、分 P、番剧、弹幕字幕与容器策略，短期内不迁）。
func engineHandles(urls []string) bool {
	if len(urls) == 0 {
		return false
	}
	// -bv / -up / --ss / --ep 都是 B 站专属入口，交给既有实现。
	if c.BVs != "" || c.UP != "" || c.SeasonID != "" || c.EpID != "" {
		return false
	}
	bili := bilibili.NewAdapter()
	for _, u := range urls {
		if !bili.Match(u) {
			return true
		}
	}
	return false
}

// runEngine 用通用引擎下载，返回退出码。handled=false 表示没有适配器认领，
// 调用方应回落到既有流程（从而保留「无法解析」的原有报错）。
func runEngine(ctx context.Context, urls []string) (int, bool) {
	eng := engine.New(engine.Options{Logger: log.Default()}).
		Register(x.NewAdapter()).
		Register(xiaohongshu.NewAdapter()).
		Register(douyin.NewAdapter())

	outcomes := newOutcomeCollector()
	handled := false

	for _, raw := range urls {
		if ctx.Err() != nil {
			break
		}
		if _, err := eng.Registry().Match(raw); err != nil {
			continue // 没有适配器，留给后续流程
		}
		handled = true

		req := engine.Request{
			URL:         raw,
			OutputDir:   c.O,
			WorkDir:     c.TmpDir,
			Template:    c.SubpathTemplate,
			Selection:   selectionFromGlobals(),
			Auth:        authFor(ctx, siteOf(eng, raw)),
			RateLimit:   c.RateBytes,
			Concurrency: c.J,
		}
		res, err := eng.Download(ctx, req, ndjsonSink())
		report := TargetReport{State: "succeeded"}
		if res != nil {
			report.MediaID = res.MediaID
			report.BV = res.MediaID
			report.Outputs = res.Outputs
			report.State = string(res.State)
			if res.State != engine.StateSucceeded {
				failed.Add(1)
			}
		}
		if err != nil {
			failed.Add(1)
			report.State = "failed"
			report.Error = err.Error()
			log.Printf("下载失败 [%s]: %v", raw, err)
		}
		outcomes.add(report)
	}

	if !handled {
		return exitOK, false
	}

	if c.ResultJSON != "" && !c.InfoOnly {
		if err := writeReport(c.ResultJSON, buildReport(outcomes.snapshot(), ctx.Err() != nil)); err != nil {
			log.Printf("写结果文件失败: %v", err)
			failed.Add(1)
		}
	}

	switch {
	case ctx.Err() != nil:
		log.Println("已取消")
		return exitCancelled, true
	case failed.Load() > 0:
		log.Printf("完成，%d 个任务失败", failed.Load())
		return exitFailed, true
	default:
		log.Println("下载完成")
		return exitOK, true
	}
}

// selectionFromGlobals 把 c 包配置映射成引擎的选流偏好。
func selectionFromGlobals() site.Selection {
	return site.Selection{
		VideoQuality:   c.VideoQuality,
		AudioQuality:   c.AudioQuality,
		VCodecPriority: c.VCodecPriority,
		AudioOnly:      c.AudioOnly,
	}
}

// siteOf 取该 URL 归属的站点标识，用于挑凭据。
func siteOf(eng *engine.Engine, raw string) string {
	if a, err := eng.Registry().Match(raw); err == nil {
		return a.Site()
	}
	return ""
}

// authFor 按站点取凭据：认证文件 > 环境变量。
// 哔哩哔哩的 SESSDATA 仍由 main 的启动流程写进 c.Cookie，这里只服务其他站点。
func authFor(ctx context.Context, siteName string) *site.Auth {
	if siteName == "" || siteName == "bilibili" {
		return nil
	}
	chain := auth.Chain{
		auth.FileStore{Path: c.AuthFile},
		auth.EnvStore{},
	}
	p, err := chain.Get(ctx, siteName, c.AuthProfile)
	if err != nil || p == nil {
		return nil
	}
	return p.SiteAuth()
}

// ndjsonSink 在 --progress-json 时把事件写到 stdout；否则丢弃（日志仍走 stderr）。
func ndjsonSink() engine.EventSink {
	if !c.ProgressJSON {
		return nil
	}
	w := c.ProgressWriter
	if w == nil {
		w = os.Stdout
	}
	return func(ev engine.Event) {
		if ev.Timestamp == 0 {
			ev.Timestamp = time.Now().UnixMilli()
		}
		b, err := json.Marshal(ev)
		if err != nil {
			return
		}
		fmt.Fprintln(w, string(b))
	}
}
