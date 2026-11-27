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
	"gorm.io/gorm/clause"
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

// attentionMaxItems：清单里未读、已读各最多返回这么多条（排序后截断，合计最多两倍）。
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
	// Read：当前用户是否已读（#186，v4.0.3）。老面板没有这个字段，客户端按未读处理。
	// 已读条目照样下发、按原顺序排：APP 也调这个接口（取第一条 task_failing 做「最常失败」统计卡），
	// 它不认 read，服务端不能把已读的过滤掉。
	Read bool `json:"read"`
	// atTime 只用于排序，不下发
	atTime time.Time
	// version 判断已读用的版本，不下发：一般是 at 的 UTC RFC3339；disk_high 的 at 每次都是 now，改用 level（warn → err 会重新未读）
	version string
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
//
// 【已读（#186，v4.0.3）】按当前用户的 attention_reads 打 read 标记：key 与版本都对得上才算已读，
// 问题再次发生（at 变了）就回到未读。
// 顺手清理过期记录：已读记录里「key 已不在当前清单里」的，说明问题已经消失，删掉；
// 这样磁盘降下去再涨回来（disk_high 的版本是 level，可能和旧记录一样）也会重新未读。没有过期记录就不写库。
//
// 【截断】排序与旧版一致（level → 时间倒序 → key，不把未读提前），按这个顺序挑出前 20 条未读和前 20 条已读，
// 保持原顺序合并下发（最多 40 条）：老 APP 不认 read、取第一条 task_failing 做「最常失败」卡，
// 已读的 err 不能被一堆未读 warn 挤掉；网页端只显示未读，未读也不会被已读挤出 20 条。
func (h *SystemHandler) Attention(c *gin.Context) {
	items := collectAttentionItems(badgeRoleLevel(c.GetString("role")))

	// 查不到当前用户（理论上不会）就全部按未读下发，不影响清单本身
	var user model.User
	if err := database.DB.Select("id").Where("username = ?", c.GetString("username")).First(&user).Error; err == nil {
		var reads []model.AttentionRead
		database.DB.Where("user_id = ?", user.ID).Find(&reads)
		currentVersions := make(map[string]string, len(items))
		for _, item := range items {
			currentVersions[item.Key] = item.version
		}
		readVersions := make(map[string]string, len(reads))
		staleIDs := make([]uint, 0)
		for _, read := range reads {
			if _, ok := currentVersions[read.Key]; !ok {
				staleIDs = append(staleIDs, read.ID)
				continue
			}
			readVersions[read.Key] = read.Version
		}
		for i := range items {
			if version, ok := readVersions[items[i].Key]; ok && version == items[i].version {
				items[i].Read = true
			}
		}
		// 有过期记录才发一条 DELETE；删失败只是下次轮询再删，不影响本次返回
		if len(staleIDs) > 0 {
			database.DB.Where("id IN ?", staleIDs).Delete(&model.AttentionRead{})
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
	// 未读、已读各留排在最前的 attentionMaxItems 条，保持原顺序
	kept := make([]attentionItem, 0, len(items))
	unreadCount, readCount := 0, 0
	for _, item := range items {
		if item.Read {
			if readCount >= attentionMaxItems {
				continue
			}
			readCount++
		} else {
			if unreadCount >= attentionMaxItems {
				continue
			}
			unreadCount++
		}
		kept = append(kept, item)
	}

	response.Success(c, gin.H{"items": kept})
}

// attentionReadRequest：回传客户端看到的条目（key + at + level），服务端只认「客户端看到的版本」等于当前版本的条目。
type attentionReadRequest struct {
	Items []struct {
		Key   string `json:"key"`
		At    string `json:"at"`
		Level string `json:"level"`
	} `json:"items"`
}

// AttentionRead 把「需要关注」的条目标记为当前用户已读（#186，v4.0.3）。
//
// 按当前角色重算一遍当前条目（与 GET 共用 collectAttentionItems），对每个回传条目：
// 当前清单里有同 key 的条目、且客户端看到的版本等于当前版本，才 upsert；否则跳过，那条保持未读。
// 不按「点击那一刻的服务端版本」记：两次轮询之间新发生、用户还没见过的失败不能被静默标为已读。
// 过期记录的清理在 GET 里做。
func (h *SystemHandler) AttentionRead(c *gin.Context) {
	var req attentionReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求格式错误")
		return
	}
	if len(req.Items) == 0 {
		response.BadRequest(c, "请指定要标记已读的条目")
		return
	}

	var user model.User
	if err := database.DB.Select("id").Where("username = ?", c.GetString("username")).First(&user).Error; err != nil {
		response.NotFound(c, "用户不存在")
		return
	}

	currentVersions := make(map[string]string)
	for _, item := range collectAttentionItems(badgeRoleLevel(c.GetString("role"))) {
		currentVersions[item.Key] = item.version
	}

	marked := 0
	for _, seen := range req.Items {
		current, ok := currentVersions[seen.Key]
		if !ok {
			continue
		}
		// 客户端看到的版本：disk_high 用 level；其余把 at 解析后转 UTC，与 collectAttentionItems 的口径一致（解析失败视为对不上）
		seenVersion := seen.Level
		if seen.Key != "disk_high" {
			seenAt, err := time.Parse(time.RFC3339, seen.At)
			if err != nil {
				continue
			}
			seenVersion = seenAt.UTC().Format(time.RFC3339)
		}
		if seenVersion != current {
			continue
		}
		// 同一个 key 回传多次只记一次
		delete(currentVersions, seen.Key)
		// (user_id, key) 联合唯一：已有记录就只改版本
		record := model.AttentionRead{UserID: user.ID, Key: seen.Key, Version: current}
		if err := database.DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}, {Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"version", "updated_at"}),
		}).Create(&record).Error; err != nil {
			response.InternalError(c, "标记已读失败")
			return
		}
		marked++
	}

	response.Success(c, gin.H{"marked": marked})
}

// collectAttentionItems 按角色收集当前全部「需要关注」条目（未排序、未截断、未打已读标记），GET 与 POST 已读共用。
// 每条的 At 与 version 在这里填好。
func collectAttentionItems(level int) []attentionItem {
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

	for i := range items {
		// Local()：SQLite 取回的时间可能是 UTC，统一转成面板时区再带偏移下发
		items[i].At = items[i].atTime.Local().Format(time.RFC3339)
		// 版本统一用 UTC：改面板时区不会让已读全部失效；客户端回传的 at 也转 UTC 再比
		items[i].version = items[i].atTime.UTC().Format(time.RFC3339)
		// disk_high 的 at 每次都是 now，拿它当版本等于永远未读；改用级别，升到 err 时重新未读
		if items[i].Type == "disk_high" {
			items[i].version = items[i].Level
		}
	}
	return items
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
