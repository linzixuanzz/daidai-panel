//go:build !linux && !windows

package handler

import "os"

// fileBirthTime 其它平台不提供创建时间，脚本树里就不带 btime 键。
func fileBirthTime(_ string, _ os.FileInfo) (float64, bool) {
	return 0, false
}

// scriptEntryStat 取脚本树一个条目的 mtime（Unix 秒）、大小、创建时间（Unix 秒）和是否拿到了创建时间。
// 其它平台沿用 entry.Info()，创建时间一律拿不到（#183 只给 Linux 合并了系统调用）。
func scriptEntryStat(path string, entry os.DirEntry) (float64, int64, float64, bool) {
	info, _ := entry.Info()
	if info == nil {
		return 0, 0, 0, false
	}
	btime, hasBtime := fileBirthTime(path, info)
	return float64(info.ModTime().Unix()), info.Size(), btime, hasBtime
}
