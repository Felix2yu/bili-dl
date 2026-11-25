// Package engine 是站点无关的下载编排层。
//
// 职责边界：它只负责「按 URL 找适配器、解析、选流、调度」，不含任何站点专有逻辑，
// 也不 import 任何具体站点包——具体站点由调用方通过 Register 显式装配。
//
// 并发安全：Engine 不含包级可变状态，可被多 goroutine 共享；
// 这是对 liuying-engine 旧版「配置走 c 包全局变量」的结构性修正。
package engine

import (
	"context"
	"log"
	"net/http"

	"github.com/Felix2yu/liuying-engine/site"
)

// Options 是引擎级配置（与单次任务无关）。
type Options struct {
	// HTTPClient 是各适配器与传输层共用的客户端；nil 用引擎默认。
	HTTPClient *http.Client
	// Logger 是诊断日志落点；nil 表示不输出。
	Logger *log.Logger
	// FFmpegPath 是 ffmpeg 可执行文件路径；空表示从 PATH 查找。
	FFmpegPath string
}

// Engine 是下载引擎。零值不可用，必须经 New 构造。
type Engine struct {
	reg *Registry
	opt Options
	// client 是生效的 HTTP 客户端（已处理 nil）。
	client *http.Client
}

// New 构造引擎。
func New(opt Options) *Engine {
	client := opt.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Engine{
		reg:    NewRegistry(),
		opt:    opt,
		client: client,
	}
}

// Register 装配一个站点适配器，返回自身以便链式调用。
func (e *Engine) Register(a site.Adapter) *Engine {
	e.reg.Register(a)
	return e
}

// Registry 暴露注册表，供调用方查询已装配站点。
func (e *Engine) Registry() *Registry { return e.reg }

// Client 返回引擎使用的 HTTP 客户端。
func (e *Engine) Client() *http.Client { return e.client }

// Logger 返回引擎日志器；可能为 nil。
func (e *Engine) Logger() *log.Logger { return e.opt.Logger }

func (e *Engine) logf(format string, args ...any) {
	if e.opt.Logger != nil {
		e.opt.Logger.Printf(format, args...)
	}
}

// Resolve 解析 URL 为一条或多条媒体，不下载、不选流。
func (e *Engine) Resolve(ctx context.Context, rawURL string, opt site.ResolveOptions) ([]*site.Media, error) {
	a, err := e.reg.Match(rawURL)
	if err != nil {
		return nil, err
	}
	if opt.Client == nil {
		opt.Client = e.client
	}
	media, err := a.Resolve(ctx, rawURL, opt)
	if err != nil {
		return nil, decorate(err, a.Site(), StageResolve)
	}
	if len(media) == 0 {
		return nil, (&Error{
			Code:      CodeNotFound,
			Site:      a.Site(),
			Stage:     StageResolve,
			Retryable: false,
			Message:   "解析成功但没有可下载的内容",
		})
	}
	e.logf("解析 %s：%d 条媒体（站点 %s）", truncate(rawURL, 60), len(media), a.Site())
	return media, nil
}

// Streams 列出某条媒体的全部可选码流。适配器不支持直链能力时报 unsupported_url。
func (e *Engine) Streams(ctx context.Context, m *site.Media, sel site.Selection) (*site.StreamSet, error) {
	if m == nil {
		return nil, NewError(CodeUnsupportedURL, StageSelect, "媒体为空")
	}
	a, ok := e.reg.Lookup(m.Site)
	if !ok {
		return nil, NewError(CodeUnsupportedURL, StageSelect, "站点 %q 未注册适配器", m.Site)
	}
	sp, ok := a.(site.StreamProvider)
	if !ok {
		return nil, NewError(CodeUnsupportedURL, StageSelect, "站点 %q 不提供直链能力", m.Site)
	}
	set, err := sp.Streams(ctx, m, sel)
	if err != nil {
		return nil, decorate(err, m.Site, StageSelect)
	}
	if set == nil {
		return nil, NewError(CodeNotFound, StageSelect, "站点 %q 没有返回任何码流", m.Site)
	}
	return set, nil
}

// decorate 把站点适配器返回的错误统一成 *Error。
// 适配器用 site.ResolveError 表达分类（不能直接依赖 engine，否则会循环），这里做映射。
func decorate(err error, siteName string, stage Stage) error {
	if err == nil {
		return nil
	}
	// 已经是 engine.Error 的（例如内部转发的），只补站点。
	if e := CodeOf(err); e != "" {
		if ee, ok := err.(*Error); ok && ee.Site == "" {
			ee.Site = siteName
		}
		return err
	}
	if se, ok := site.AsResolveError(err); ok {
		code := Code(se.Code)
		if code == "" {
			code = CodeUpstreamChanged
		}
		return &Error{
			Code:       code,
			Site:       siteName,
			Stage:      stage,
			Retryable:  retryable[code],
			RetryAfter: se.RetryAfter,
			Message:    se.Message,
			Err:        se.Err,
		}
	}
	return &Error{
		Code:      CodeUpstreamChanged,
		Site:      siteName,
		Stage:     stage,
		Retryable: retryable[CodeUpstreamChanged],
		Message:   "站点返回了预期之外的数据",
		Err:       err,
	}
}
