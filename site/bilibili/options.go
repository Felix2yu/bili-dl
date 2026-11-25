package bilibili

// VideoOption 是一条可选的视频码流，供调用方渲染画质选择界面。
// Ext 是该编码实际落盘的容器，与 codecExt 一致（av1→mkv，hevc→mov，其余→mp4）。
type VideoOption struct {
	ID        int
	Codec     string
	Width     int
	Height    int
	Bandwidth int64
	Ext       string
}

// AudioOption 是一条可选的音频码流，含 dolby 与 flac 无损轨。
type AudioOption struct {
	ID        int
	Codec     string
	Bandwidth int64
	Ext       string
}

// ListOptions 返回投稿/番剧的全部可选码流。与 GetStream 共用 fetchDash，
// 因此 WBI 签名、PGC 响应外层、错误码语义都跟下载路径一致。
func ListOptions(v Video) ([]VideoOption, []AudioOption, error) {
	resp, err := fetchDash(v)
	if err != nil {
		return nil, nil, err
	}
	return videoOptions(resp.Data.Dash.Video), audioOptions(resp.Data.Dash), nil
}

func videoOptions(streams []dashStream) []VideoOption {
	out := make([]VideoOption, 0, len(streams))
	for _, s := range streams {
		out = append(out, VideoOption{
			ID:        int(s.ID),
			Codec:     s.Codecs,
			Width:     int(s.Width),
			Height:    int(s.Height),
			Bandwidth: int64(s.Bandwidth),
			Ext:       codecExt(s.Codecs),
		})
	}
	return out
}

// audioOptions 收敛普通 + dolby + flac 三路候选，顺序与 GetStream 的选流候选一致。
func audioOptions(dash dashInfo) []AudioOption {
	candidates := make([]dashStream, 0, len(dash.Audio))
	candidates = append(candidates, dash.Audio...)
	candidates = append(candidates, dash.Dolby.Audio...)
	candidates = append(candidates, dash.Flac.Audio...)

	out := make([]AudioOption, 0, len(candidates))
	for _, s := range candidates {
		out = append(out, AudioOption{
			ID:        int(s.ID),
			Codec:     s.Codecs,
			Bandwidth: int64(s.Bandwidth),
			Ext:       audioExt(s.Codecs),
		})
	}
	return out
}
