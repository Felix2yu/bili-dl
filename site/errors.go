package site

import (
	"errors"
	"fmt"
	"time"
)

// ErrorCode 是适配器向引擎汇报的错误分类。
//
// 站点适配器不直接依赖 engine 包（避免 engine -> site -> engine 的循环），
// 因此这里定义一份与 engine.Code 一一对应的枚举，由 engine 负责映射。
type ErrorCode string

const (
	ErrUnsupportedURL  ErrorCode = "unsupported_url"
	ErrNotFound        ErrorCode = "not_found"
	ErrAuthRequired    ErrorCode = "auth_required"
	ErrAuthExpired     ErrorCode = "auth_expired"
	ErrVIPRequired     ErrorCode = "vip_required"
	ErrGeoBlocked      ErrorCode = "geo_blocked"
	ErrRateLimited     ErrorCode = "rate_limited"
	ErrUpstreamChanged ErrorCode = "upstream_changed"
	ErrHLSUnavailable  ErrorCode = "hls_unavailable"
	ErrNetwork         ErrorCode = "network"
)

// ResolveError 是适配器返回的结构化错误。
// Message 直接给用户看，Err 保留底层原因便于日志排查。
type ResolveError struct {
	Code       ErrorCode
	Message    string
	RetryAfter time.Duration
	Err        error
}

func (e *ResolveError) Error() string {
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

func (e *ResolveError) Unwrap() error { return e.Err }

// Is 允许按 Code 比较。
func (e *ResolveError) Is(target error) bool {
	t, ok := target.(*ResolveError)
	return ok && t.Code != "" && t.Code == e.Code
}

// NewError 构造适配器错误。
func NewError(code ErrorCode, format string, args ...any) *ResolveError {
	return &ResolveError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WrapError 构造带底层原因的适配器错误。
func WrapError(code ErrorCode, err error, format string, args ...any) *ResolveError {
	return &ResolveError{Code: code, Message: fmt.Sprintf(format, args...), Err: err}
}

// AsResolveError 取出适配器错误。
func AsResolveError(err error) (*ResolveError, bool) {
	var e *ResolveError
	ok := errors.As(err, &e)
	return e, ok
}
