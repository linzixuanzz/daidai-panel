//go:build windows

package service

import (
	"os"
	"syscall"
)

// isReparsePoint 判断一个 Lstat 得到的 FileInfo 是否带 FILE_ATTRIBUTE_REPARSE_POINT。
// pathHasLinkSegment 只对中间目录段调用它（经 isReparsePointFn），末级文件不调用：
//   - 默认 winsymlink=1 下，目录联接（junction）已报 os.ModeIrregular，由 mode 检查拦住；这道检查兜的是
//     GODEBUG=winsymlink=0 回退旧语义时不报任何特殊 mode 的非名称代理重解析目录（见 A1）。
//   - 不能用在末级：Windows Server 重复数据删除（DEDUP）会把普通文件转成重解析点，Go 刻意把它当普通文件，
//     这里却会返回 true，脚本就会被误判成「经过软链接」、永远删不掉（见 R2-4）。
func isReparsePoint(info os.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	return data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
