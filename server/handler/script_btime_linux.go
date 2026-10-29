//go:build linux

package handler

import (
	"os"

	"golang.org/x/sys/unix"
)

// fileBirthTime 读取文件创建时间（Unix 秒）；内核或文件系统不支持 btime 时返回 false。
func fileBirthTime(path string, _ os.FileInfo) (float64, bool) {
	var stx unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &stx); err != nil {
		return 0, false
	}
	// 必须看返回的 mask，部分文件系统（如 overlay 下层、老内核）不会填 btime
	if stx.Mask&unix.STATX_BTIME == 0 {
		return 0, false
	}
	// 秒数 <= 0 说明文件系统给的是无效值（未初始化 / 1970 之前），当作拿不到，交给前端用 mtime 兜底（issue #169）
	if stx.Btime.Sec <= 0 {
		return 0, false
	}
	return float64(stx.Btime.Sec), true
}
