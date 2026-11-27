//go:build linux

package handler

import (
	"errors"
	"os"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// statxUnsupported 记住「本进程用不了 statx」（老内核返回 ENOSYS，群晖 DSM 的 3.10 内核就没有；
// 老版 Docker 的 seccomp 规则拦 statx 返回 EPERM）。
// 一旦置上，后面每个文件都直接走 entry.Info()，不再先白打一次注定失败的 statx。
var statxUnsupported atomic.Bool

// scriptEntryStat 取脚本树一个条目的 mtime（Unix 秒）、大小、创建时间（Unix 秒）和是否拿到了创建时间。
//
// #183：原来每个条目要两次系统调用（entry.Info() 的 lstat + 取 btime 的 statx），NAS、Docker 绑定挂载这类
// 慢文件系统上脚本树要等好几秒。现在用一次 statx 同时拿 mtime / size / btime。
// AT_SYMLINK_NOFOLLOW 与 entry.Info() 的 lstat 口径一致：软链接取的是链接本身。
func scriptEntryStat(path string, entry os.DirEntry) (float64, int64, float64, bool) {
	if !statxUnsupported.Load() {
		var stx unix.Statx_t
		err := unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BASIC_STATS|unix.STATX_BTIME, &stx)
		if err == nil {
			btime := float64(0)
			hasBtime := false
			// btime 判定与原来的 fileBirthTime 一致：
			// 必须看返回的 mask，部分文件系统（如 overlay 下层、老内核）不会填 btime；
			// 秒数 <= 0 说明文件系统给的是无效值（未初始化 / 1970 之前），当作拿不到，交给前端用 mtime 兜底（issue #169）
			if stx.Mask&unix.STATX_BTIME != 0 && stx.Btime.Sec > 0 {
				btime = float64(stx.Btime.Sec)
				hasBtime = true
			}
			// mtime / size 正常都会填；万一文件系统没给，退回 entry.Info() 取，btime 仍用这次拿到的
			if stx.Mask&unix.STATX_MTIME != 0 && stx.Mask&unix.STATX_SIZE != 0 {
				return float64(stx.Mtime.Sec), int64(stx.Size), btime, hasBtime
			}
			info, _ := entry.Info()
			if info == nil {
				return 0, 0, btime, hasBtime
			}
			return float64(info.ModTime().Unix()), info.Size(), btime, hasBtime
		}
		// ENOSYS：内核没有 statx；EPERM：老版 Docker 的 seccomp 拦了 statx。
		// stat 类调用对单个文件不会回 EPERM（权限问题回的是 EACCES），所以 EPERM 也当作整个进程不支持。
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) {
			statxUnsupported.Store(true)
		}
		// 其它错误（文件刚被删掉等）也落到下面的老路径，结果与改动前一致
	}

	// 没有 statx（或这次 statx 失败）：只能 lstat，拿不到创建时间
	info, _ := entry.Info()
	if info == nil {
		return 0, 0, 0, false
	}
	return float64(info.ModTime().Unix()), info.Size(), 0, false
}
