// Package auth 管理站点凭据。
//
// 设计约束：凭据必须能按「单次调用」传入，不能像旧版那样只存在于 c 包全局变量里。
// 因此这里以 context 为载体：引擎把凭据放进 ctx，适配器从 ctx 取，
// 同一个进程内多个任务可以用不同账号并行，互不干扰。
package auth

import (
	"context"
	"strings"

	"github.com/Felix2yu/bili-dl/site"
)

// Profile 是一个站点账号的凭据集合。
type Profile struct {
	// Site 是站点标识，必须与 site.Adapter.Site() 一致。
	Site string
	// Name 是 profile 名（default / work / 小号…）。
	Name string
	// Cookies 是该站点的 Cookie。
	Cookies []site.Cookie
	// Headers 是需要随请求发送的额外头（UA、Referer 等）。
	Headers map[string]string
}

// SiteAuth 转成适配器使用的凭据结构。
func (p *Profile) SiteAuth() *site.Auth {
	if p == nil {
		return nil
	}
	return &site.Auth{Cookies: p.Cookies, Headers: p.Headers}
}

// Cookie 按名字取 Cookie 值（名字大小写不敏感）。
func (p *Profile) Cookie(name string) (string, bool) {
	if p == nil {
		return "", false
	}
	for _, c := range p.Cookies {
		if strings.EqualFold(c.Name, name) {
			return c.Value, true
		}
	}
	return "", false
}

// Store 读取凭据。实现可以是 TOML 文件、环境变量或 Keychain。
type Store interface {
	// Get 取指定站点的 profile；不存在时返回 (nil, nil) 而不是错误。
	Get(ctx context.Context, siteName, profile string) (*Profile, error)
	// List 列出某站点的全部 profile 名。
	List(ctx context.Context, siteName string) ([]string, error)
}

// Chain 按顺序尝试多个 Store，返回第一个有内容的 profile。
//
// 顺序即优先级：显式文件 > 环境变量。空 profile 会被跳过，
// 这样「配了文件但没填 cookie」不会把环境变量挡住。
type Chain []Store

// Get 依次查询各 Store。
func (c Chain) Get(ctx context.Context, siteName, profile string) (*Profile, error) {
	for _, s := range c {
		if s == nil {
			continue
		}
		p, err := s.Get(ctx, siteName, profile)
		if err != nil {
			return nil, err
		}
		if p != nil && len(p.Cookies) > 0 {
			return p, nil
		}
	}
	return nil, nil
}

// List 合并各 Store 的 profile 名（去重，保持出现顺序）。
func (c Chain) List(ctx context.Context, siteName string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, s := range c {
		if s == nil {
			continue
		}
		names, err := s.List(ctx, siteName)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out, nil
}
