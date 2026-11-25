// Package ssr 从 HTML 里抠出服务端注入的 JSON。
//
// 背景：不少站点把首屏数据塞进 <script> 里的 `window.__INITIAL_STATE__ = {...}`
// 或 `window._ROUTER_DATA = {...}`。这类数据不需要签名，是最省事的取数路径。
//
// 关键点：**不能用正则 + 非贪婪匹配**。JSON 里有嵌套对象和字符串，遇到第一个 `}`
// 就停会截断；字符串里的花括号与转义引号也会把括号计数带偏。必须做真正的扫描。
package ssr

import (
	"bytes"
	"fmt"
)

// ExtractObject 找出 marker 之后第一个 JSON 对象/数组并返回其原始字节。
//
// marker 例：`window.__INITIAL_STATE__`、`window._ROUTER_DATA`。
// 允许 marker 与值之间有空白、等号或冒号（不同站点写法不一）。
func ExtractObject(html []byte, marker string) ([]byte, error) {
	if marker == "" {
		return nil, fmt.Errorf("marker 为空")
	}
	idx := bytes.Index(html, []byte(marker))
	if idx < 0 {
		return nil, fmt.Errorf("页面里找不到 %s（站点结构可能已改版）", marker)
	}
	start := -1
	for i := idx + len(marker); i < len(html); i++ {
		switch html[i] {
		case '{', '[':
			start = i
		case ' ', '\t', '\r', '\n', '=', ':', '(', '"', '\'':
			continue
		default:
			// 遇到其他字符说明 marker 后面不是 JSON（例如 marker 出现在注释里）。
			// 继续往后找下一个，避免整页失败。
			next := bytes.Index(html[i:], []byte(marker))
			if next < 0 {
				return nil, fmt.Errorf("%s 之后没有 JSON 数据", marker)
			}
			return ExtractObject(html[i+next:], marker)
		}
		if start >= 0 {
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("%s 之后没有 JSON 数据", marker)
	}

	end, err := matchBracket(html, start)
	if err != nil {
		return nil, err
	}
	return html[start:end], nil
}

// matchBracket 从 start 处的 { 或 [ 开始，返回与之配对的右括号之后的位置。
// 正确处理字符串、转义与嵌套。
func matchBracket(b []byte, start int) (int, error) {
	open := b[start]
	var close byte
	switch open {
	case '{':
		close = '}'
	case '[':
		close = ']'
	default:
		return 0, fmt.Errorf("起始位置不是括号")
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(b); i++ {
		c := b[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1, nil
			}
		}
	}
	return 0, fmt.Errorf("JSON 括号不闭合（可能被截断）")
}

// SanitizeUndefined 把字符串外部出现的裸 `undefined` 换成 `null`。
//
// 小红书的 __INITIAL_STATE__ 会直接序列化 JS 对象，里面的 undefined 不是合法 JSON，
// 不修就没法用 encoding/json 解析。只替换完整词，避免动到 "undefined" 字符串。
func SanitizeUndefined(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	inString := false
	escaped := false
	for i := 0; i < len(raw); {
		c := raw[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			i++
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			i++
			continue
		}
		if c == 'u' && bytes.HasPrefix(raw[i:], []byte("undefined")) && wordBoundary(raw, i) {
			out = append(out, "null"...)
			i += len("undefined")
			continue
		}
		out = append(out, c)
		i++
	}
	return out
}

// wordBoundary 判断 i 处的词是否是独立单词（前后不是标识符字符）。
func wordBoundary(raw []byte, i int) bool {
	if i > 0 && isIdent(raw[i-1]) {
		return false
	}
	end := i + len("undefined")
	if end < len(raw) && isIdent(raw[end]) {
		return false
	}
	return true
}

func isIdent(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z')
}
