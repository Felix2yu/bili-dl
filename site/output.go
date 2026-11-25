package site

// Output 是一条已经落盘的产物。
//
// 显式回报产出（而不是让调用方扫描目录前后差集）是这次重构的核心目标之一：
// 差集在「并发任务」「目标目录已有同名文件」「下载后 remux 改名」三种场景都会误判。
type Output struct {
	// Path 是绝对路径。
	Path string `json:"path"`
	// Kind 取值：video / audio / cover / danmaku / subtitle / image。
	Kind string `json:"kind"`
	// Container 是容器扩展名，不含点；非媒体文件为空。
	Container string `json:"container,omitempty"`
	// Bytes 是文件大小。
	Bytes int64 `json:"bytes"`
}
