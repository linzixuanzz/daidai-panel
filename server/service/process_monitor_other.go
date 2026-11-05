//go:build !linux

package service

// 非 Linux（Windows 开发机、macOS）没有 /proc：「后台进程」页显示「仅 Linux 部署支持」。
const processMonitorSupported = false

// ListPanelProcesses 在非 Linux 上恒返回空列表；handler 另外带上 supported=false。
func ListPanelProcesses(includeAll bool) ([]PanelProcess, error) {
	return []PanelProcess{}, nil
}

// KillPanelProcess 在非 Linux 上不支持。
func KillPanelProcess(pid int, startTicks uint64) (*PanelProcess, bool, error) {
	return nil, false, ErrProcessMonitorUnsupported
}
