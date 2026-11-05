package handler

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"daidai-panel/model"
	"daidai-panel/service"
)

const (
	autoUpdateLastCheckedAtKey    = "auto_update_last_checked_at"
	autoUpdatePendingVersionKey   = "auto_update_pending_version"
	autoUpdatePendingStartedAtKey = "auto_update_pending_started_at"

	// 下面五个是 #177 新增的巡检内部状态：只用 model.SetConfig 存，不注册、不进系统设置界面。
	// autoUpdateNextCheckAtKey：被 GitHub 限流后，下一次最早什么时候再查（RFC3339，额度恢复时间）。
	autoUpdateNextCheckAtKey = "auto_update_next_check_at"
	// autoUpdateRateLimitedSinceKey：这一轮连续限流从什么时候开始（RFC3339），成功检查一次后清空。
	autoUpdateRateLimitedSinceKey = "auto_update_rate_limited_since"
	// autoUpdateRateLimitNotifiedKey：这一轮连续限流是否已发过「超过 72 小时」的提醒，成功检查一次后清空。
	autoUpdateRateLimitNotifiedKey = "auto_update_rate_limit_notified"
	// autoUpdateLastFailureKey / autoUpdateLastFailureAtKey：上一次通知过的失败文案与通知时间，
	// 同一文案 24 小时内只通知一次（巡检每小时一次，失败后不再推迟 24 小时，不去重就会每小时通知一次）。
	autoUpdateLastFailureKey   = "auto_update_last_failure"
	autoUpdateLastFailureAtKey = "auto_update_last_failure_at"
)

// autoUpdateRateLimitNotifyAfter：连续被限流超过这么久才通知一次，提示去填 GitHub Token。
const autoUpdateRateLimitNotifyAfter = 72 * time.Hour

var (
	autoUpdateWatcherOnce sync.Once
	autoUpdateWatcherStop chan struct{}
	// sendAutoUpdateNotification 抽成变量只为单测能截下巡检发出的通知，运行时就是 service.SendNotification。
	sendAutoUpdateNotification = service.SendNotification
)

type panelUpdateExecutionOptions struct {
	AutoUpdate    bool
	TargetVersion string
}

func StartPanelAutoUpdateWatcher() {
	autoUpdateWatcherOnce.Do(func() {
		autoUpdateWatcherStop = make(chan struct{})
		go panelAutoUpdateLoop()
		log.Println("panel auto update watcher started (interval: 1h)")
	})
}

func StopPanelAutoUpdateWatcher() {
	if autoUpdateWatcherStop != nil {
		close(autoUpdateWatcherStop)
	}
}

func FinalizePendingAutoUpdateOnStartup() {
	targetVersion := strings.TrimSpace(model.GetConfig(autoUpdatePendingVersionKey, ""))
	if targetVersion == "" {
		return
	}

	startedAt := strings.TrimSpace(model.GetConfig(autoUpdatePendingStartedAtKey, ""))
	clearPendingAutoUpdateState()

	if Version == targetVersion {
		service.SendNotification(
			"静默更新成功",
			fmt.Sprintf("面板已自动更新到 v%s。\n完成时间：%s", targetVersion, time.Now().Format(time.DateTime)),
		)
		return
	}

	content := fmt.Sprintf("静默更新计划目标版本为 v%s，但当前启动版本仍为 v%s。", targetVersion, Version)
	if startedAt != "" {
		content += "\n发起时间：" + startedAt
	}
	service.SendNotification("静默更新失败", content)
}

func panelAutoUpdateLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	time.Sleep(time.Minute)
	runPanelAutoUpdateCheck()

	for {
		select {
		case <-ticker.C:
			runPanelAutoUpdateCheck()
		case <-autoUpdateWatcherStop:
			return
		}
	}
}

func runPanelAutoUpdateCheck() {
	if !model.GetRegisteredConfigBool("auto_update_enabled") {
		return
	}

	lastCheckedAt := parseAutoUpdateTime(model.GetConfig(autoUpdateLastCheckedAtKey, ""))
	if !lastCheckedAt.IsZero() && time.Since(lastCheckedAt) < 24*time.Hour {
		return
	}
	// 上次被限流时记下了额度恢复时间，没到就不查（再查也只会再被拒一次）
	nextCheckAt := parseAutoUpdateTime(model.GetConfig(autoUpdateNextCheckAtKey, ""))
	if !nextCheckAt.IsZero() && time.Now().Before(nextCheckAt) {
		return
	}

	release, err := fetchLatestPanelRelease()
	if err != nil {
		now := time.Now()
		var apiErr *githubAPIError
		if errors.As(err, &apiErr) && apiErr.RateLimited {
			// 限流（#177）：不通知、不写「上次检查时间」，额度恢复后的第一个整点巡检再查。
			// 没有恢复时间头时不记 next，下一次整点巡检（1 小时后）自然会再查。
			nextValue := ""
			if !apiErr.ResetAt.IsZero() {
				nextValue = apiErr.ResetAt.Format(time.RFC3339)
			}
			_ = model.SetConfig(autoUpdateNextCheckAtKey, nextValue)

			since := parseAutoUpdateTime(model.GetConfig(autoUpdateRateLimitedSinceKey, ""))
			if since.IsZero() {
				_ = model.SetConfig(autoUpdateRateLimitedSinceKey, now.Format(time.RFC3339))
			} else if now.Sub(since) >= autoUpdateRateLimitNotifyAfter && model.GetConfig(autoUpdateRateLimitNotifiedKey, "") == "" {
				// 连续限流超过 72 小时只提醒一次；成功检查一次后计数清零，下一轮再满 72 小时才会再提醒
				content := "GitHub API 已连续超过 72 小时拒绝面板检查更新（访问次数已达上限，常见于多人共用同一个代理出口），静默更新一直没能检查新版本。"
				if apiErr.TokenUsed {
					content += "已填写的 GitHub Token 额度也被用完，请确认它没有被其它程序共用。"
				} else {
					content += "可在「系统设置 → 网络」填写 GitHub Token 提高额度。"
				}
				sendAutoUpdateNotification("静默更新检查受阻", content)
				_ = model.SetConfig(autoUpdateRateLimitNotifiedKey, "true")
			}
			return
		}

		// 其它失败：照旧通知，但同一文案 24 小时内只通知一次；下一次整点巡检照常重试，不推迟 24 小时
		_ = model.SetConfig(autoUpdateNextCheckAtKey, "")
		content := "自动检查更新失败：" + err.Error()
		// 去重按「错误类别」比：GitHub 返回的错误按文案区分；网络层错误（超时、连接被重置等）的文案里
		// 带对端 IP、本地端口，每次都不一样，按文案比会退化成每小时通知一次，所以统一算作一类
		failureKey := content
		if apiErr == nil {
			failureKey = "network"
		}
		lastFailure := model.GetConfig(autoUpdateLastFailureKey, "")
		lastFailureAt := parseAutoUpdateTime(model.GetConfig(autoUpdateLastFailureAtKey, ""))
		if failureKey != lastFailure || lastFailureAt.IsZero() || now.Sub(lastFailureAt) >= 24*time.Hour {
			sendAutoUpdateNotification("静默更新失败", content)
			_ = model.SetConfig(autoUpdateLastFailureKey, failureKey)
			_ = model.SetConfig(autoUpdateLastFailureAtKey, now.Format(time.RFC3339))
		}
		return
	}

	// 只有真正拿到 release 才算「检查过」：写上次检查时间，并清掉限流 / 失败去重的状态
	_ = model.SetConfig(autoUpdateLastCheckedAtKey, time.Now().Format(time.RFC3339))
	for _, key := range []string{
		autoUpdateNextCheckAtKey,
		autoUpdateRateLimitedSinceKey,
		autoUpdateRateLimitNotifiedKey,
		autoUpdateLastFailureKey,
		autoUpdateLastFailureAtKey,
	} {
		_ = model.SetConfig(key, "")
	}

	latestVersion := release.version()
	if !compareVersions(Version, latestVersion) {
		return
	}

	plan, err := buildPanelUpdatePlanForRelease(release)
	if err != nil {
		service.SendNotification("静默更新失败", "自动更新准备失败："+err.Error())
		return
	}

	if err := panelUpdater.begin(plan); err != nil {
		return
	}

	recordPendingAutoUpdate(latestVersion)
	go executePanelUpdateWithOptions(plan, panelUpdateExecutionOptions{
		AutoUpdate:    true,
		TargetVersion: latestVersion,
	})
}

func recordPendingAutoUpdate(version string) {
	if strings.TrimSpace(version) == "" {
		return
	}
	_ = model.SetConfig(autoUpdatePendingVersionKey, strings.TrimSpace(version))
	_ = model.SetConfig(autoUpdatePendingStartedAtKey, time.Now().Format(time.RFC3339))
}

func clearPendingAutoUpdateState() {
	_ = model.SetConfig(autoUpdatePendingVersionKey, "")
	_ = model.SetConfig(autoUpdatePendingStartedAtKey, "")
}

func parseAutoUpdateTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func notifyAutoUpdateFailure(targetVersion string, err error) {
	clearPendingAutoUpdateState()
	message := "静默更新执行失败"
	if strings.TrimSpace(targetVersion) != "" {
		message = fmt.Sprintf("静默更新到 v%s 失败", targetVersion)
	}
	if err != nil {
		message += "：" + err.Error()
	}
	service.SendNotification("静默更新失败", message)
}
