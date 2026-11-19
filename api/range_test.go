package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Felix2yu/bili-dl/c"
)

func TestDownloadOnceCompleteFile416(t *testing.T) {
	c.Defaults()
	c.Ctx = nil
	c.ProgressJSON = false

	payload := []byte("complete-file-content")
	dir := t.TempDir()
	path := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(payload)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		t.Errorf("完整文件不应重新下载")
	}))
	defer srv.Close()

	if err := downloadOnce(srv.URL, path); err != nil {
		t.Fatalf("本地已完整时应视为成功，实际: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("文件被改动: %q", got)
	}
}

func TestDownloadOnce416WithPartialFileRedownloads(t *testing.T) {
	c.Defaults()
	c.Ctx = nil
	c.ProgressJSON = false

	payload := []byte("complete-file")
	dir := t.TempDir()
	path := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(path, []byte("par"), 0o644); err != nil {
		t.Fatal(err)
	}
	ranged := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranged++
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(payload)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	if err := downloadOnce(srv.URL, path); err != nil {
		t.Fatalf("416 后应从重下载，实际: %v", err)
	}
	if ranged != 1 {
		t.Fatalf("应发出 1 次 Range 请求，实际 %d", ranged)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("重下内容不对: %q", got)
	}
}

func TestContentRangeTotal(t *testing.T) {
	cases := map[string]int64{
		"bytes 100-199/200": 200,
		"bytes */37123456":  37123456,
		"bytes */0":         0,
		"none":              0,
		"":                  0,
	}
	for header, want := range cases {
		if got := contentRangeTotal(header); got != want {
			t.Errorf("%q: got %d, want %d", header, got, want)
		}
	}
}
