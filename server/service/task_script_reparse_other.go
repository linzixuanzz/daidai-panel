//go:build !windows

package service

import "os"

// isReparsePoint 在非 Windows 平台恒为 false：软链接由 os.ModeSymlink 判定，
// 不存在 Windows 那种「联接不报 ModeSymlink」的问题（见 A1 与 task_script_reparse_windows.go）。
// 调用点经 isReparsePointFn，测试可以注入假函数，在 Linux 上也钉住这条分支（见 R2-05）。
func isReparsePoint(os.FileInfo) bool {
	return false
}
