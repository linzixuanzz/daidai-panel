package handler

import (
	"fmt"
	"sort"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

// attentionTaskFailingThreshold：启用中的任务最近这么多条「已结束」日志全部失败，才算「连续失败」。
// 取 3 而不是 1：偶发一次失败（网络抖一下、目标站点临时 502）很常见，每次都弹出来会把真正要处理的淹没；
// 连着失败 3 次基本说明脚本或环境出了需要人处理的问题。超时在日志里也记成失败（status=1），一并计入；
// 手动停止（已终止）不算失败，但它会打断「连续」。
const attentionTaskFailingThreshold = 3

// attentionTaskFailingScan：每个候选任务最多往回看这么多条日志，用来在标题里写「连续失败 N 次」。
// 看满了还全是失败就写「N 次以上」，不再往回翻（日志行的 status 列排在大字段 content 之后，读一行并不便宜）。
const attentionTaskFailingScan = 10

// attentionTaskCandidateLimit：最多检查这么多个候选任务（按最近运行时间倒序）。清单本身最多 20 条，
// 候选再多也展示不下，这个上限只是防止「几百个任务同时挂掉」时一次轮询要逐个查几百遍日志。
const attentionTaskCandidateLimit = 50

// attentionDiskHighPercent / attentionDiskCriticalPercent：数据盘使用率达到前者出 warn，达到后者升为 err。
const (
	attentionDiskHighPercent     = 85.0
	attentionDiskCriticalPercent = 95.0
)

// attentionMaxItems：清单最多返回这么多条（排序后截断）。
const attentionMaxItems = 20

// attentionDiskUsage 取数据盘使用率（%），抽成变量只为单测能替换；运行时就是系统信息里的 disk_usage。
// 拿不到（不支持的系统、df 失败）时是 0，自然不会出 disk_high。
var attentionDiskUsage = func() float64 {
	return service.GetResourceInfo().DiskUsage
}

type attentionTarget struct {
	// Kind：task | subscription | dependency | system。system 表示跳系统设置，此时 ID 为 0。
	Kind string `json:"kind"`
	ID   uint   `json:"id"`
}

type attentionItem struct {
	Key    string          `json:"key"`
	Type   string          `json:"type"`
	Level  string          `json:"level"`
	Title  string          `json:"title"`
	Detail string          `json:"detail"`
	Target attentionTarget `json:"target"`
	At     string          `json:"at"`
	// atTime 只用于排序，不下发
	atTime time.Time
}

// attentionLevelRank：排序用，err 在最前。
var attentionLevelRank = map[string]int{"err": 0, "warn": 1, "info": 2}

// Attention 汇总「需要关注」的条目，供仪表板卡片与顶栏铃铛轮询（design §4.1）。
//
// 【按角色裁剪，与 Badges 一致】viewer 只看任务；operator 加订阅；admin 再加依赖与磁盘。
// 看不到对应菜单的角色拿到这些条目也没有落点，还会泄露无权模块的状态，所以直接不查。
//
// 【这几类不出】
//   - update_available：服务端没有「最新版本」的缓存，检查更新每次都是一发 GitHub 外网请求，
//     不能挂在 60 秒一次的轮询上（理由同 Badges 的注释）。网页端如需展示，复用前端已有的 checkUpdate 结果自行合并。
//   - backup_failed：定时备份失败目前只打面板日志（service/backup_schedule.go 的 runScheduledBackup），
//     没有持久化的「最近一次备份结果」可查，所以这一类暂不出。
func (h *SystemHandler) Attention(c *gin.Context) {
	level := badgeRoleLevel(c.GetString("role"))
	items := make([]attentionItem, 0)

	// viewer 及以上：连续失败的任务
	if level >= badgeRoleViewer {
		items = append(items, collectTaskFailingAttention()...)
	}

	// operator 及以上：拉取失败的订阅。口径与 Badges 的 subs_failed 相同：subscriptions.status = 1。
	if level >= badgeRoleOperator {
		var subs []model.Subscription
		database.DB.Select("id", "name", "last_pull_at", "updated_at").
			Where("status = ?", 1).
			Order("updated_at DESC").
			Limit(attentionMaxItems).
			Find(&subs)
		for _, sub := range subs {
			at := sub.UpdatedAt
			if sub.LastPullAt != nil {
				at = *sub.LastPullAt
			}
			// 最近一次拉取日志里的错误行。只对失败的订阅查，数量很少。
			var subLog model.SubLog
			detail := ""
			if err := database.DB.Select("id", "content").
				Where("subscription_id = ?", sub.ID).
				Order("id DESC").
				First(&subLog).Error; err == nil {
				detail = service.PickAttentionDetail(subLog.Content)
			}
			items = append(items, attentionItem{
				Key:    fmt.Sprintf("subscription_failed:%d", sub.ID),
				Type:   "subscription_failed",
				Level:  "warn",
				Title:  fmt.Sprintf("订阅「%s」拉取失败", sub.Name),
				Detail: detail,
				Target: attentionTarget{Kind: "subscription", ID: sub.ID},
				atTime: at,
			})
		}
	}

	// admin：安装失败的依赖（口径同 Badges 的 deps_failed）与数据盘使用率
	if level >= badgeRoleAdmin {
		var deps []model.Dependency
		database.DB.Select("id", "type", "name", "log", "updated_at").
			Where("status = ?", model.DepStatusFailed).
			Order("updated_at DESC").
			Limit(attentionMaxItems).
			Find(&deps)
		for _, dep := range deps {
			items = append(items, attentionItem{
				Key:    fmt.Sprintf("dependency_failed:%d", dep.ID),
				Type:   "dependency_failed",
				Level:  "warn",
				Title:  fmt.Sprintf("依赖「%s」安装失败（%s）", dep.Name, dep.Type),
				Detail: service.PickAttentionDetail(dep.Log),
				Target: attentionTarget{Kind: "dependency", ID: dep.ID},
				atTime: dep.UpdatedAt,
			})
		}

		if usage := attentionDiskUsage(); usage >= attentionDiskHighPercent {
			itemLevel := "warn"
			if usage >= attentionDiskCriticalPercent {
				itemLevel = "err"
			}
			items = append(items, attentionItem{
				Key:    "disk_high",
				Type:   "disk_high",
				Level:  itemLevel,
				Title:  fmt.Sprintf("数据盘使用率 %.1f%%", usage),
				Detail: fmt.Sprintf("已超过 %.0f%%，磁盘写满会导致日志写入、依赖安装、备份失败，建议清理日志或扩容", attentionDiskHighPercent),
				Target: attentionTarget{Kind: "system", ID: 0},
				atTime: time.Now(),
			})
		}
	}

	// 排序：err → warn → info，同级按时间倒序；同一时刻再按 key 排，保证每次轮询顺序稳定（前端靠 key 做动画）。
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := attentionLevelRank[items[i].Level], attentionLevelRank[items[j].Level]
		if ri != rj {
			return ri < rj
		}
		if !items[i].atTime.Equal(items[j].atTime) {
			return items[i].atTime.After(items[j].atTime)
		}
		return items[i].Key < items[j].Key
	})
	if len(items) > attentionMaxItems {
		items = items[:attentionMaxItems]
	}
	for i := range items {
		// Local()：SQLite 取回的时间可能是 UTC，统一转成面板时区再带偏移下发
		items[i].At = items[i].atTime.Local().Format(time.RFC3339)
	}

	response.Success(c, gin.H{"items": items})
}

// collectTaskFailingAttention 找出「启用中、且最近 attentionTaskFailingThreshold 条已结束日志全部失败」的任务。
//
// 先用 tasks.last_run_status = 失败 粗筛：最近一次都不是失败的任务不可能连续失败，
// 这样只需要对少数候选任务回看日志，而不是扫整张 task_logs（日志行读 status 要穿过大字段 content，见 model.TaskLog 的注释）。
// 回看走 idx_task_logs_task_id_started_at 倒序取前几条，每个候选只读个位数的行。
func collectTaskFailingAttention() []attentionItem {
	var candidates []model.Task
	database.DB.Select("id", "name", "last_run_at").
		Where("status <> ? AND last_run_status = ?", model.TaskStatusDisabled, model.RunFailed).
		Order("last_run_at DESC").
		Limit(attentionTaskCandidateLimit).
		Find(&candidates)

	items := make([]attentionItem, 0)
	for _, task := range candidates {
		var logs []model.TaskLog
		// 只看已结束的日志：运行中的那条既不算失败也不打断「连续」
		database.DB.Select("id", "status", "started_at", "ended_at").
			Where("task_id = ? AND status IN ?", task.ID, []int{model.LogStatusSuccess, model.LogStatusFailed, model.LogStatusAborted}).
			Order("started_at DESC, id DESC").
			Limit(attentionTaskFailingScan).
			Find(&logs)

		consecutive := 0
		for _, taskLog := range logs {
			if taskLog.Status == nil || *taskLog.Status != model.LogStatusFailed {
				break
			}
			consecutive++
		}
		if consecutive < attentionTaskFailingThreshold {
			continue
		}

		title := fmt.Sprintf("%s · 连续失败 %d 次", task.Name, consecutive)
		if consecutive >= attentionTaskFailingScan {
			title = fmt.Sprintf("%s · 连续失败 %d 次以上", task.Name, attentionTaskFailingScan)
		}
		latest := logs[0]
		at := latest.StartedAt
		if latest.EndedAt != nil {
			at = *latest.EndedAt
		}
		detail := ""
		if config.C != nil {
			detail = service.TaskLogAttentionDetail(latest.ID, config.C.Data.LogDir)
		}
		if detail != "" {
			detail = "最近一次：" + detail
			// 加了前缀后再保证一次 ≤120 字
			if runes := []rune(detail); len(runes) > service.AttentionDetailMaxRunes {
				detail = string(runes[:service.AttentionDetailMaxRunes-1]) + "…"
			}
		}
		items = append(items, attentionItem{
			Key:    fmt.Sprintf("task_failing:%d", task.ID),
			Type:   "task_failing",
			Level:  "err",
			Title:  title,
			Detail: detail,
			Target: attentionTarget{Kind: "task", ID: task.ID},
			atTime: at,
		})
	}
	return items
}
