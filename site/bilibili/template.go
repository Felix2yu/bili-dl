package bilibili

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Felix2yu/bili-dl/c"
)

// 与流影 LibraryPath.splitPageLabel 的两条正则保持一致的产出形态：
//
//   - 单 P：       <owner>/<title>.<ext>
//   - 多 P（默认）：<owner>/<title> - 01 - <part>.<ext>
//     页码用两位以上，part 为空时退化为 <title>(1).<ext>
//
// -tp 模板决定“子路径 + 主标题”，占位符在当前文件上求值：
// {owner_uname} {title} {page} {part} {bv} {owner_mid}
var (
	rePlaceholder = regexp.MustCompile(`\{([a-zA-Z_]+)\}`)
	reIllegal     = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f]`)
)

// templateFields 收集占位符可用的字段。title 是主标题（多 P 时为视频总标题），
// part 是当前分 P 名，page 为 1-based 页码。
func templateFields(v *Video, ext string) map[string]string {
	page := v.ActivePage
	if page == 0 {
		page = 1
	}
	return map[string]string{
		"owner_uname": v.Owner,
		"owner_mid":   fmt.Sprintf("%d", v.OwnerM),
		"title":       v.Title,
		"part":        v.partName(page),
		"page":        fmt.Sprintf("%d", page),
		"page02":      fmt.Sprintf("%02d", page),
		"bv":          v.BV,
		"ext":         ext,
	}
}

func (v *Video) partName(page int) string {
	if page > 0 && page <= len(v.Pages) {
		return strings.TrimSpace(v.Pages[page-1].Part)
	}
	return ""
}

// sanitizeSegment 清洗单个路径段：非法字符替换为空格、折叠空白、去首尾点与空格。
func sanitizeSegment(s string) string {
	s = reIllegal.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, ". ")
	if s == "" {
		return "media"
	}
	return s
}

// RenderPath 根据 -tp 模板与分 P 信息，返回相对下载目录的路径（含扩展名）。
// 保证所有子目录都在返回值内，调用方 filepath.Join(c.O, rel) 即为最终落点。
func RenderPath(v *Video, ext string) string {
	tmpl := strings.TrimSpace(c.SubpathTemplate)
	fields := templateFields(v, ext)
	if tmpl == "" {
		// 默认：owner/title，与 yutto 的 -tp "{owner_uname}/{title}" 一致。
		tmpl = "{owner_uname}/{title}"
	}

	rendered := rePlaceholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		key := strings.Trim(m, "{}")
		if val, ok := fields[key]; ok {
			return sanitizeSegment(val)
		}
		return ""
	})

	// 逐段清洗，避免 owner 为空时留下 "//" 或前导斜杠。
	parts := strings.Split(rendered, "/")
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		p = sanitizeSegment(p)
		if p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		clean = []string{sanitizeSegment(v.Title)}
	}

	base := clean[len(clean)-1]
	// 多 P 时把页码与分 P 名编进文件名，匹配流影的分 P 识别正则。
	if len(v.Pages) > 1 {
		page := v.ActivePage
		if page == 0 {
			page = 1
		}
		part := strings.TrimSpace(v.partName(page))
		if part != "" && !strings.EqualFold(part, v.Title) {
			base = fmt.Sprintf("%s - %02d - %s", base, page, sanitizeSegment(part))
		} else {
			base = fmt.Sprintf("%s(%d)", base, page)
		}
		clean[len(clean)-1] = sanitizeSegment(base) // 注意这里会保留括号与连字符
	}

	// sanitizeSegment 会去掉尾点但保留 (n) 与 " - "，符合预期。
	return filepath.Join(clean...) + "." + ext
}
