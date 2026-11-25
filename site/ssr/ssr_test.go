package ssr

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractObjectBasic(t *testing.T) {
	html := []byte(`<html><script>window.__INITIAL_STATE__={"note":{"id":"a"}}</script></html>`)
	raw, err := ExtractObject(html, "window.__INITIAL_STATE__")
	if err != nil {
		t.Fatalf("提取失败: %v", err)
	}
	if string(raw) != `{"note":{"id":"a"}}` {
		t.Errorf("raw = %s", raw)
	}
}

func TestExtractObjectWithSpacing(t *testing.T) {
	html := []byte(`<script>window._ROUTER_DATA = { "a" : 1 };</script>`)
	raw, err := ExtractObject(html, "window._ROUTER_DATA")
	if err != nil {
		t.Fatalf("提取失败: %v", err)
	}
	if string(raw) != `{ "a" : 1 }` {
		t.Errorf("raw = %s", raw)
	}
}

func TestExtractObjectBracesInsideStrings(t *testing.T) {
	// 这是正则方案必然踩的坑：字符串里的花括号不能被计数。
	html := []byte(`<script>window.X={"text":"a}{b","nested":{"k":"}}"}}</script>`)
	raw, err := ExtractObject(html, "window.X")
	if err != nil {
		t.Fatalf("提取失败: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("提取结果不是合法 JSON: %v (%s)", err, raw)
	}
	if got["text"] != "a}{b" {
		t.Errorf("text = %v", got["text"])
	}
}

func TestExtractObjectEscapedQuote(t *testing.T) {
	html := []byte(`<script>window.X={"text":"he said \"hi\" }"}</script>`)
	raw, err := ExtractObject(html, "window.X")
	if err != nil {
		t.Fatalf("提取失败: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("非法 JSON: %v (%s)", err, raw)
	}
	if got["text"] != `he said "hi" }` {
		t.Errorf("text = %v", got["text"])
	}
}

func TestExtractArray(t *testing.T) {
	html := []byte(`<script>window.X=[{"a":1},{"b":[2,3]}]</script>`)
	raw, err := ExtractObject(html, "window.X")
	if err != nil {
		t.Fatalf("提取失败: %v", err)
	}
	if string(raw) != `[{"a":1},{"b":[2,3]}]` {
		t.Errorf("raw = %s", raw)
	}
}

func TestExtractObjectMissingMarker(t *testing.T) {
	if _, err := ExtractObject([]byte("<html></html>"), "window.NOPE"); err == nil {
		t.Fatal("缺少 marker 应报错")
	}
}

func TestExtractObjectUnclosed(t *testing.T) {
	html := []byte(`<script>window.X={"a":{"b":1}</script>`)
	if _, err := ExtractObject(html, "window.X"); err == nil {
		t.Fatal("括号不闭合应报错")
	}
}

func TestExtractObjectEmptyMarker(t *testing.T) {
	if _, err := ExtractObject([]byte("x"), ""); err == nil {
		t.Fatal("空 marker 应报错")
	}
}

func TestSanitizeUndefined(t *testing.T) {
	raw := []byte(`{"a":undefined,"b":[1,undefined],"c":"undefined","d":undefinedValue}`)
	got := string(SanitizeUndefined(raw))
	// 只替换独立的 undefined 词：字符串内与 undefinedValue 这类标识符保持原样。
	want := `{"a":null,"b":[1,null],"c":"undefined","d":undefinedValue}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestSanitizeUndefinedMakesParseable(t *testing.T) {
	// 不含其他 JS 标识符的样本，替换后必须能被 encoding/json 解析。
	raw := []byte(`{"a":undefined,"b":[1,undefined],"c":"undefined"}`)
	var v any
	if err := json.Unmarshal(SanitizeUndefined(raw), &v); err != nil {
		t.Errorf("替换结果不可解析: %v", err)
	}
}

func TestSanitizeUndefinedKeepsStringContent(t *testing.T) {
	raw := []byte(`{"k":"a undefined b"}`)
	if got := string(SanitizeUndefined(raw)); got != string(raw) {
		t.Errorf("字符串内的 undefined 不应被替换: %s", got)
	}
}

func TestRealWorldInitialState(t *testing.T) {
	// 小红书式样本：undefined 存在、字符串里带花括号、数值为浮点。
	html := []byte(`<!doctype html><html><body><script>window.__INITIAL_STATE__={"noteDetailMap":{"abc":{"note":{"noteId":"abc","desc":"标题}{","video":undefined,"imageList":[{"url":"http://x/1.jpg"}]}}}};(function(){})()</script></body></html>`)
	raw, err := ExtractObject(html, "window.__INITIAL_STATE__")
	if err != nil {
		t.Fatalf("提取失败: %v", err)
	}
	clean := SanitizeUndefined(raw)
	var state struct {
		NoteDetailMap map[string]struct {
			Note struct {
				NoteID    string `json:"noteId"`
				Desc      string `json:"desc"`
				ImageList []struct {
					URL string `json:"url"`
				} `json:"imageList"`
			} `json:"note"`
		} `json:"noteDetailMap"`
	}
	if err := json.Unmarshal(clean, &state); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, clean)
	}
	note := state.NoteDetailMap["abc"].Note
	if note.NoteID != "abc" || note.Desc != "标题}{" {
		t.Errorf("字段解析错误: %+v", note)
	}
	if len(note.ImageList) != 1 || !strings.HasSuffix(note.ImageList[0].URL, "/1.jpg") {
		t.Errorf("图片列表解析错误: %+v", note.ImageList)
	}
}
