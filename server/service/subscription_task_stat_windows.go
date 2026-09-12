//go:build windows

package service

import (
	"errors"
	"syscall"
)

// Windows 上的名字类 Stat 错误码：路径文本本身不可能对应一个文件（命令参数里带 URL、盘符、? * | < >、超长段、重解析点成环），
// 删除判定按「这个前缀不是脚本」处理，与「不存在」类同路（见 subscriptionScriptNotAScript）。
//
// 必须写真实的 Windows 错误码数值：Go 为 Windows 定义的 syscall.ENAMETOOLONG / ELOOP / EIO 这类是自造值
// （APPLICATION_ERROR 起，见 syscall/zerrors_windows.go），os.Stat 永远不会返回；syscall 包也没有导出下面这几个具名常量。
const (
	subscriptionErrorInvalidName         = syscall.Errno(123)  // ERROR_INVALID_NAME：含 : ? * | < > 等非法字符，URL、盘符参数就是它
	subscriptionErrorBadPathname         = syscall.Errno(161)  // ERROR_BAD_PATHNAME
	subscriptionErrorFilenameExcedRange  = syscall.Errno(206)  // ERROR_FILENAME_EXCED_RANGE：文件名或扩展名太长
	subscriptionErrorDirectory           = syscall.Errno(267)  // ERROR_DIRECTORY：目录名无效
	subscriptionErrorCantResolveFilename = syscall.Errno(1921) // ERROR_CANT_RESOLVE_FILENAME：重解析点成环，相当于 ELOOP
)

// subscriptionScriptBadNamePlatform 报告 Windows 特有的名字类 Stat 错误。这里只放「名字本身不合法」的错误码；
// 网络盘、SMB 的 IO 类错误（21、59、64、121、1117 等）一个都不列——没列到的错误一律由删除判定保留。
func subscriptionScriptBadNamePlatform(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case subscriptionErrorInvalidName, subscriptionErrorBadPathname, subscriptionErrorFilenameExcedRange,
		subscriptionErrorDirectory, subscriptionErrorCantResolveFilename:
		return true
	}
	return false
}
