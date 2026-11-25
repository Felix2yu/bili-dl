package engine

import (
	"strings"

	"github.com/Felix2yu/bili-dl/site"
)

// Registry 保存已装配的适配器，按注册顺序匹配 URL。
//
// 刻意不做 init() 隐式注册：调用方必须显式装配自己要支持的站点，
// 这样 bili-history 只装 bilibili 时不会把其他站点的依赖拖进它的二进制。
type Registry struct {
	adapters []site.Adapter
}

// NewRegistry 返回空注册表。
func NewRegistry() *Registry { return &Registry{} }

// Register 追加适配器，返回自身以便链式调用。
func (r *Registry) Register(a site.Adapter) *Registry {
	if a != nil {
		r.adapters = append(r.adapters, a)
	}
	return r
}

// Match 返回第一个认领该 URL 的适配器。
func (r *Registry) Match(rawURL string) (site.Adapter, error) {
	u := strings.TrimSpace(rawURL)
	for _, a := range r.adapters {
		if a.Match(u) {
			return a, nil
		}
	}
	return nil, NewError(CodeUnsupportedURL, StageResolve, "没有可处理该链接的站点适配器: %s", truncate(u, 80))
}

// Sites 返回已注册的站点标识。
func (r *Registry) Sites() []string {
	out := make([]string, 0, len(r.adapters))
	for _, a := range r.adapters {
		out = append(out, a.Site())
	}
	return out
}

// Len 返回已注册适配器数量。
func (r *Registry) Len() int { return len(r.adapters) }

// Lookup 按站点标识取适配器。
func (r *Registry) Lookup(siteName string) (site.Adapter, bool) {
	for _, a := range r.adapters {
		if a.Site() == siteName {
			return a, true
		}
	}
	return nil, false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
