package auth

import (
	"context"

	"github.com/Felix2yu/bili-dl/site"
)

// ctxKey 是私有的 context key 类型，避免与其他包的 key 冲突。
type ctxKey struct{}

// WithAuth 把凭据放进 context。传 nil 等价于「显式未登录」，
// 会覆盖上层可能存在的凭据（用于「本次任务不要用登录态」这类场景）。
func WithAuth(ctx context.Context, a *site.Auth) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, a)
}

// FromContext 取 context 里的凭据；没有则返回 nil。
func FromContext(ctx context.Context) *site.Auth {
	if ctx == nil {
		return nil
	}
	a, _ := ctx.Value(ctxKey{}).(*site.Auth)
	return a
}

// CookieValue 从 context 里取某个 Cookie 值。名字大小写不敏感。
func CookieValue(ctx context.Context, name string) (string, bool) {
	a := FromContext(ctx)
	if a == nil {
		return "", false
	}
	for _, c := range a.Cookies {
		if equalFold(c.Name, name) {
			return c.Value, true
		}
	}
	return "", false
}

// HeaderValue 从 context 里取额外的请求头。
func HeaderValue(ctx context.Context, name string) (string, bool) {
	a := FromContext(ctx)
	if a == nil || a.Headers == nil {
		return "", false
	}
	for k, v := range a.Headers {
		if equalFold(k, name) {
			return v, true
		}
	}
	return "", false
}

// equalFold 是 strings.EqualFold 的薄封装，避免调用方到处 import strings。
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		// ASCII 折写在等长时成立；长度不同直接判否即可覆盖 Cookie 名的实际用法。
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
