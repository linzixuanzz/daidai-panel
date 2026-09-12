//go:build !windows

package service

// subscriptionScriptBadNamePlatform：非 Windows 平台没有额外的名字类错误——ENAMETOOLONG / ELOOP / EINVAL 已在共享判定
// subscriptionScriptBadName 里覆盖。IO / 网络类错误（EIO、ESTALE、EHOSTDOWN、ECONNRESET 等）一个都不列：
// 没列到的错误一律由删除判定保留。
func subscriptionScriptBadNamePlatform(err error) bool {
	return false
}
