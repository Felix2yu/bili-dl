package engine

import (
	"errors"
	"fmt"
	"time"
)

// Code 是错误分类。调用方按 Code 决定是否重试、如何提示，不解析字符串。
type Code string

const (
	// CodeUnsupportedURL 表示没有适配器认领该 URL。
	CodeUnsupportedURL Code = "unsupported_url"
	// CodeNotFound 表示资源不存在或已删除。
	CodeNotFound Code = "not_found"
	// CodeAuthRequired 表示需要登录态。
	CodeAuthRequired Code = "auth_required"
	// CodeAuthExpired 表示已提供登录态但已失效。
	CodeAuthExpired Code = "auth_expired"
	// CodeVIPRequired 表示需要会员/付费权限。
	CodeVIPRequired Code = "vip_required"
	// CodeGeoBlocked 表示地区限制。
	CodeGeoBlocked Code = "geo_blocked"
	// CodeRateLimited 表示被限流或风控，可带 RetryAfter。
	CodeRateLimited Code = "rate_limited"
	// CodeUpstreamChanged 表示站点改版导致解析失败（找不到预期字段）。
	CodeUpstreamChanged Code = "upstream_changed"
	// CodeHLSUnavailable 表示 HLS 清单或分片获取失败。
	CodeHLSUnavailable Code = "hls_unavailable"
	// CodeFFmpegMissing 表示缺少 ffmpeg 且流程需要它。
	CodeFFmpegMissing Code = "ffmpeg_missing"
	// CodeNetwork 表示网络类错误（拨号失败、超时、5xx）。
	CodeNetwork Code = "network"
	// CodeCanceled 表示被 ctx 取消。
	CodeCanceled Code = "canceled"
)

// Stage 标识错误发生在流程的哪一步。
type Stage string

const (
	StageResolve  Stage = "resolve"
	StageSelect   Stage = "select"
	StageTransfer Stage = "transfer"
	StageMerge    Stage = "merge"
	StageFinalize Stage = "finalize"
)

// retryable 给出各 Code 的默认可重试性。
// 少数场景会显式覆盖（如 transfer 阶段的 network 错误一定可重试）。
var retryable = map[Code]bool{
	CodeRateLimited:     true,
	CodeUpstreamChanged: true,
	CodeHLSUnavailable:  true,
	CodeNetwork:         true,
}

// Error 是引擎统一错误。Message 是给用户看的中文，Err 保留底层原因。
type Error struct {
	Code       Code
	Site       string
	Stage      Stage
	Retryable  bool
	RetryAfter time.Duration
	Message    string
	Err        error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Message != "" && e.Err != nil:
		return e.Message + ": " + e.Err.Error()
	case e.Message != "":
		return e.Message
	case e.Err != nil:
		return e.Err.Error()
	default:
		return string(e.Code)
	}
}

func (e *Error) Unwrap() error { return e.Err }

// Is 让 errors.Is(err, &Error{Code: X}) 可按 Code 比较，忽略其余字段。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return t.Code != "" && t.Code == e.Code
}

// NewError 构造错误，Retryable 取该 Code 的默认值。
func NewError(code Code, stage Stage, format string, args ...any) *Error {
	return &Error{
		Code:      code,
		Stage:     stage,
		Retryable: retryable[code],
		Message:   fmt.Sprintf(format, args...),
	}
}

// WrapError 构造带底层原因的错误。
func WrapError(code Code, stage Stage, err error, format string, args ...any) *Error {
	return &Error{
		Code:      code,
		Stage:     stage,
		Retryable: retryable[code],
		Message:   fmt.Sprintf(format, args...),
		Err:       err,
	}
}

// WithSite 补充站点标识。
func (e *Error) WithSite(s string) *Error {
	e.Site = s
	return e
}

// WithRetryAfter 补充建议的重试等待时长，并置为可重试。
func (e *Error) WithRetryAfter(d time.Duration) *Error {
	e.RetryAfter = d
	e.Retryable = true
	return e
}

// IsCode 判断 err（含被包装层）是否属于某个 Code。
func IsCode(err error, code Code) bool {
	if err == nil {
		return false
	}
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == code
}

// CodeOf 取出错误的 Code；非 *Error 返回空串。
func CodeOf(err error) Code {
	var e *Error
	if !errors.As(err, &e) {
		return ""
	}
	return e.Code
}
