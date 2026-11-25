package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Felix2yu/bili-dl/site"
)

// fakeAdapter 是最小适配器，用于验证注册表与引擎的路由行为。
type fakeAdapter struct {
	name  string
	hosts []string
	site  *site.Media
	err   error
}

func (f *fakeAdapter) Site() string { return f.name }

func (f *fakeAdapter) Match(rawURL string) bool {
	for _, h := range f.hosts {
		if len(rawURL) >= len(h) && rawURL[:len(h)] == h {
			return true
		}
	}
	return false
}

func (f *fakeAdapter) Resolve(_ context.Context, _ string, _ site.ResolveOptions) ([]*site.Media, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.site == nil {
		return nil, nil
	}
	return []*site.Media{f.site}, nil
}

func TestRegistryMatchByOrder(t *testing.T) {
	first := &fakeAdapter{name: "a", hosts: []string{"https://shared"}}
	second := &fakeAdapter{name: "b", hosts: []string{"https://shared", "https://only-b"}}

	r := NewRegistry().Register(first).Register(second)

	got, err := r.Match("https://shared/x")
	if err != nil {
		t.Fatalf("应命中: %v", err)
	}
	if got.Site() != "a" {
		t.Errorf("应按注册顺序取第一个，实际 %q", got.Site())
	}

	got, err = r.Match("https://only-b/x")
	if err != nil || got.Site() != "b" {
		t.Errorf("应命中 b，实际 %v %v", got, err)
	}
}

func TestRegistryUnsupportedURL(t *testing.T) {
	r := NewRegistry()
	_, err := r.Match("https://unknown.example/x")
	if err == nil {
		t.Fatal("未注册站点应报错")
	}
	if !IsCode(err, CodeUnsupportedURL) {
		t.Errorf("应为 unsupported_url，实际 %q", CodeOf(err))
	}
}

func TestRegistryNilAdapterIgnored(t *testing.T) {
	r := NewRegistry().Register(nil)
	if r.Len() != 0 {
		t.Errorf("nil 适配器不应被注册，实际 Len=%d", r.Len())
	}
}

func TestEngineResolve(t *testing.T) {
	m := &site.Media{Site: "a", ID: "id1", Title: "t"}
	e := New(Options{}).Register(&fakeAdapter{name: "a", hosts: []string{"https://a"}, site: m})

	got, err := e.Resolve(context.Background(), "https://a/v/1", site.ResolveOptions{})
	if err != nil {
		t.Fatalf("Resolve 失败: %v", err)
	}
	if len(got) != 1 || got[0].ID != "id1" {
		t.Fatalf("Resolve 结果错误: %+v", got)
	}
}

func TestEngineResolveEmptyIsNotFound(t *testing.T) {
	e := New(Options{}).Register(&fakeAdapter{name: "a", hosts: []string{"https://a"}})

	_, err := e.Resolve(context.Background(), "https://a/v/1", site.ResolveOptions{})
	if !IsCode(err, CodeNotFound) {
		t.Errorf("空结果应为 not_found，实际 %q", CodeOf(err))
	}
}

func TestEngineResolveWrapsBareError(t *testing.T) {
	// 适配器返回未分类错误时，引擎应包装成 upstream_changed 并补上站点。
	boom := errors.New("unexpected html")
	e := New(Options{}).Register(&fakeAdapter{name: "a", hosts: []string{"https://a"}, err: boom})

	_, err := e.Resolve(context.Background(), "https://a/v/1", site.ResolveOptions{})
	if !IsCode(err, CodeUpstreamChanged) {
		t.Errorf("裸错误应为 upstream_changed，实际 %q", CodeOf(err))
	}
	var ee *Error
	if !errors.As(err, &ee) || ee.Site != "a" {
		t.Errorf("应补上 Site=a，实际 %+v", ee)
	}
	if !errors.Is(err, boom) {
		t.Error("底层错误应可 Unwrap")
	}
}

func TestEngineStreamsRequiresProvider(t *testing.T) {
	// fakeAdapter 只实现 Adapter，不实现 StreamProvider。
	e := New(Options{}).Register(&fakeAdapter{name: "a", hosts: []string{"https://a"}})
	_, err := e.Streams(context.Background(), &site.Media{Site: "a"}, site.Selection{})
	if !IsCode(err, CodeUnsupportedURL) {
		t.Errorf("缺少直链能力应为 unsupported_url，实际 %q", CodeOf(err))
	}
}

func TestErrorRetryableDefaults(t *testing.T) {
	cases := map[Code]bool{
		CodeRateLimited:     true,
		CodeUpstreamChanged: true,
		CodeNetwork:         true,
		CodeNotFound:        false,
		CodeAuthRequired:    false,
		CodeCanceled:        false,
	}
	for code, want := range cases {
		if got := NewError(code, StageResolve, "x").Retryable; got != want {
			t.Errorf("%s: Retryable want %v, got %v", code, want, got)
		}
	}
}

func TestErrorWithRetryAfterForcesRetryable(t *testing.T) {
	e := NewError(CodeNotFound, StageTransfer, "x").WithRetryAfter(90 * time.Second)
	if !e.Retryable {
		t.Error("WithRetryAfter 应置 Retryable")
	}
	if e.RetryAfter != 90*time.Second {
		t.Errorf("RetryAfter 未写入: %v", e.RetryAfter)
	}
}

func TestErrorIsMatchesByCode(t *testing.T) {
	err := WrapError(CodeNetwork, StageTransfer, errors.New("dial tcp: timeout"), "下载失败")
	if !errors.Is(err, &Error{Code: CodeNetwork}) {
		t.Error("errors.Is 应按 Code 命中")
	}
	if errors.Is(err, &Error{Code: CodeNotFound}) {
		t.Error("不同 Code 不应命中")
	}
	if err.Error() == "" {
		t.Error("Error() 不应为空")
	}
}
