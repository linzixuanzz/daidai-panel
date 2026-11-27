//go:build windows

package handler

import (
	"os"
	"syscall"
)

// fileBirthTime 读取文件创建时间（Unix 秒），Windows 直接取文件属性里的 CreationTime。
func fileBirthTime(_ string, info os.FileInfo) (float64, bool) {
	if info == nil {
		return 0, false
	}
	attr, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return 0, false
	}
	return float64(attr.CreationTime.Nanoseconds() / 1e9), true
}

// scriptEntryStat 取脚本树一个条目的 mtime（Unix 秒）、大小、创建时间（Unix 秒）和是否拿到了创建时间。
// Windows 的 os.ReadDir 已经把文件属性一并读回来了，entry.Info() 不再额外发系统调用，沿用原来的取法即可（#183）。
func scriptEntryStat(path string, entry os.DirEntry) (float64, int64, float64, bool) {
	info, _ := entry.Info()
	if info == nil {
		return 0, 0, 0, false
	}
	btime, hasBtime := fileBirthTime(path, info)
	return float64(info.ModTime().Unix()), info.Size(), btime, hasBtime
}
