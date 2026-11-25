package main

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Felix2yu/liuying-engine/api"
	"github.com/Felix2yu/liuying-engine/c"
	"github.com/Felix2yu/liuying-engine/util"
)

// 退出码：0 全部成功；1 有失败；2 参数/环境错误；130 被信号取消。
const (
	exitOK        = 0
	exitFailed    = 1
	exitUsage     = 2
	exitCancelled = 130
)

var failed atomic.Int32

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	log.SetFlags(0)
	log.SetOutput(os.Stderr) // 日志一律走 stderr；stdout 留给机器可读输出。

	c.Parse(args)

	// context：响应 SIGTERM/SIGINT，供下载、ffmpeg、请求共用。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c.Ctx = ctx

	// 认证：--auth-file 优先，其次 -c 明文。两者都不打印。
	if c.Cookie == "" && c.AuthFile != "" {
		sess, err := api.LoadSessdata(c.AuthFile, c.AuthProfile)
		if err != nil {
			log.Printf("读取认证文件失败: %v（按未登录处理）", err)
		} else if sess != "" {
			c.Cookie = sess
		}
	}
	if c.Debug {
		log.Printf("运行参数: %s", c.Describe())
	}

	// 下载目录：绝对化 + 创建。相对路径基于启动时的 CWD（流影传的是绝对路径）。
	c.WD, _ = os.Getwd()
	if !filepath.IsAbs(c.O) {
		c.O = filepath.Join(c.WD, c.O)
	}
	if err := os.MkdirAll(c.O, 0o755); err != nil {
		log.Printf("创建下载目录失败: %v", err)
		return exitUsage
	}
	log.Printf("下载路径: %s", c.O)

	// 非哔哩哔哩链接走通用引擎（X 等）。全是 B 站时继续既有流程：
	// 那套实现包含 WBI、分 P、番剧、弹幕字幕与容器策略，迁移是独立工程。
	if engineHandles(c.URLs) {
		if code, handled := runEngine(ctx, c.URLs); handled {
			return code
		}
	}

	// ffmpeg 探测：缺失时只下不到合并，作为降级而不是致命错误。
	probeCtx, cancelProbe := context.WithTimeout(ctx, 5*time.Second)
	if err := exec.CommandContext(probeCtx, "ffmpeg", "-version").Run(); err != nil {
		log.Printf("ffmpeg 不可用，将跳过音视频合并: %v", err)
	} else {
		c.FFMPEG = true
	}
	cancelProbe()

	// 构造任务列表：URL/BV 位置参数 + -bv 逗号列表 + -up 投稿列表。
	targets, code := collectTargets()
	if code != exitOK {
		return code
	}

	// no-overwrite：仅在“未使用模板”时启用（模板模式下产物路径由模板决定，
	// 流影靠目录差集判定产出，跳过已存在会导致零产出误判）。
	if c.DisableOverwrite && c.SubpathTemplate == "" {
		targets = skipExisting(targets)
	}

	outcomes := newOutcomeCollector()
	// flushReport 在每条「会产出文件」的退出路径上写结构化结果。
	// 解析阶段全失败也要写：调用方需要区分「一个都没解析出来」与「下载失败」。
	flushReport := func(canceled bool) {
		if c.InfoOnly || c.ResultJSON == "" {
			return
		}
		if err := writeReport(c.ResultJSON, buildReport(outcomes.snapshot(), canceled)); err != nil {
			log.Printf("写结果文件失败: %v", err)
			failed.Add(1)
		}
	}

	if len(targets) == 0 {
		// 有输入但全部解析/获取失败 → 失败；真的什么都没给 → 用法错误。
		// 番剧/剧集参数也计入"有输入"，避免 PGC 失败被误报成用法错误(exit 2)。
		hasInput := len(c.URLs) > 0 || c.BVs != "" || c.UP != "" ||
			c.SeasonID != "" || c.EpID != ""
		if hasInput {
			log.Printf("所有输入均无法解析，%d 个任务失败", failed.Load())
			flushReport(false)
			return exitFailed
		}
		fmt.Fprintln(os.Stderr, "用法: liuying-engine [--batch] <url|BV号|av号|b23短链> [-d 下载目录] [选项]")
		fmt.Fprintln(os.Stderr, "      liuying-engine -up <mid> [-d 下载目录]  # 下载 UP 主全部投稿")
		return exitUsage
	}

	if c.InfoOnly {
		for _, v := range targets {
			if err := api.PrintDASHInfo(v); err != nil {
				log.Printf("查看 DASH 信息失败: %v", err)
				failed.Add(1)
			}
		}
	} else {
		downloadAll(targets, outcomes)
	}
	// 无论成功、部分失败还是被取消都要落盘：调用方需要知道已经下成了哪些文件，
	// 取消场景尤其如此。-i 只查看码流、不产出文件，故不写结果。
	flushReport(ctx.Err() != nil)

	if ctx.Err() != nil {
		log.Println("已取消")
		return exitCancelled
	}
	if failed.Load() > 0 {
		log.Printf("完成，%d 个任务失败", failed.Load())
		return exitFailed
	}
	log.Println("下载完成")
	return exitOK
}

// collectTargets 构造待下载视频列表。每个 target 的 ActivePage 表示起始分 P。
func collectTargets() ([]api.Video, int) {
	var out []api.Video

	// 位置参数：完整 URL / BV / av / 分享口令 / b23 短链。
	for _, raw := range c.URLs {
		bv, page, err := api.ParseInput(raw)
		if err != nil {
			log.Printf("解析输入失败: %v", err)
			failed.Add(1)
			continue
		}
		v, err := api.VideoFromBV(bv)
		if err != nil {
			log.Printf("获取视频信息失败: %v", err)
			failed.Add(1)
			continue
		}
		out = append(out, expandVideo(v, page)...)
	}

	// -bv 逗号列表（旧接口）：只接受 BV/av，走同一解析器。
	if c.BVs != "" {
		for _, raw := range strings.Split(c.BVs, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			bv, page, err := api.ParseInput(raw)
			if err != nil {
				log.Printf("解析 -bv 失败: %v", err)
				failed.Add(1)
				continue
			}
			v, err := api.VideoFromBV(bv)
			if err != nil {
				log.Printf("获取视频信息失败: %v", err)
				failed.Add(1)
				continue
			}
			out = append(out, expandVideo(v, page)...)
		}
	}

	// -up：UP 主全部投稿。
	if c.UP != "" {
		videos, err := api.AllVideo(c.UP)
		if err != nil {
			log.Printf("获取 UP 主投稿失败: %v", err)
			failed.Add(1)
		}
		for _, v := range videos {
			full, err := api.VideoFromBV(v.BV)
			if err != nil {
				log.Printf("获取视频信息失败: %v", err)
				failed.Add(1)
				continue
			}
			out = append(out, expandPages(full, 1)...)
		}
	}

	// --ss/--ep：番剧/剧集，直接展开为 PGC 剧集目标（不再走 BV 解析）。
	if seasons, err := api.BuildSeasonTargets(); err != nil {
		log.Printf("获取番剧失败: %v", err)
		failed.Add(1)
	} else {
		for _, v := range seasons {
			out = append(out, expandPages(&v, 1)...)
		}
	}
	return out, exitOK
}

// expandVideo 先处理合集展开，再按分 P 展开。
// --collection 开启且视频属于合集时，下载整个合集（每个条目再独立展开分 P）。
func expandVideo(v *api.Video, startPage int) []api.Video {
	if c.Collection && v.UGCSeason != nil {
		parts := api.ExpandUGCSeason(v)
		if len(parts) > 0 {
			log.Printf("合集 %q 展开为 %d 个视频", v.UGCSeason.Title, len(parts))
			var out []api.Video
			for i := range parts {
				out = append(out, expandPages(&parts[i], 1)...)
			}
			return out
		}
	}
	return expandPages(v, startPage)
}

// expandPages 展开分 P，页码与 --batch 规则由 api.ExpandPages 统一实现。
func expandPages(v *api.Video, startPage int) []api.Video {
	out := api.ExpandPages(v, startPage, c.Batch)
	if len(out) == 0 {
		failed.Add(1)
	}
	return out
}

func downloadAll(targets []api.Video, outcomes *outcomeCollector) {
	limit := util.NewGoLimit(c.J)
	for _, v := range targets {
		if c.Ctx.Err() != nil {
			break
		}
		limit.Add()
		v := v
		go func() {
			defer limit.Done()
			select {
			case <-c.Ctx.Done():
				return
			default:
			}

			report := TargetReport{
				MediaID: v.BV,
				BV:      v.BV,
				Page:    v.ActivePage,
				Title:   v.Title,
			}

			stream, err := api.GetStream(v)
			if err != nil {
				log.Printf("获取流失败 [%s]: %v", v.BV, err)
				failed.Add(1)
				report.State = "failed"
				report.Error = err.Error()
				outcomes.add(report)
				return
			}

			var failure string
			if err := api.Dl(stream); err != nil {
				log.Printf("下载失败 [%s]: %v", v.BV, err)
				failed.Add(1)
				failure = err.Error()
			} else if c.FFMPEG && c.Merge {
				// -m 对所有分支统一生效（旧版 BV 分支无条件合并的 bug 已修）。
				if err := api.Merge(stream); err != nil {
					// 合并失败仍保留分离流，产物照实上报，便于调用方判断「文件在但不完整」。
					log.Printf("合并失败 [%s]: %v", v.BV, err)
					failed.Add(1)
					failure = err.Error()
				}
			}
			if failure == "" {
				// 弹幕/字幕/封面：best-effort，失败不增加 failed 计数。
				api.DownloadExtras(stream)
			}

			if failure == "" {
				report.State = "succeeded"
			} else {
				report.State = "failed"
				report.Error = failure
			}
			report.Outputs = stream.Outputs()
			outcomes.add(report)
		}()
	}
	limit.Wait()
}

// skipExisting 扫描下载目录里已有的 BV 文件，跳过已下载项（仅无模板模式）。
var (
	reExisting = regexp.MustCompile(`_BV[a-zA-Z0-9]+(-merged)?\.(mp4|mkv|mov|m4a|flac)`)
	reBV       = regexp.MustCompile(`BV[a-zA-Z0-9]+`)
)

func skipExisting(targets []api.Video) []api.Video {
	exists := make(map[string]struct{})
	_ = filepath.WalkDir(c.O, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if reExisting.MatchString(d.Name()) {
			if bv := reBV.FindString(d.Name()); bv != "" {
				exists[bv] = struct{}{}
			}
		}
		return nil
	})
	var out []api.Video
	for _, v := range targets {
		if _, ok := exists[v.BV]; ok {
			log.Printf("%s 已存在，跳过", v.BV)
			continue
		}
		out = append(out, v)
	}
	return out
}
