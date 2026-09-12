package service

import (
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"

	"daidai-panel/database"
	"daidai-panel/model"
	"daidai-panel/pkg/pathutil"
)

// 本文件实现 #124「删除任务时可选同时删除脚本」的判定与执行；handler 只负责鉴权、参数与响应
// （handler/task_script_cleanup.go）。三个删除入口原有的删除语句一字不改：删除前 Collect 拍快照，
// 删除后 Execute 基于库里剩下的任务重新判定，复核通过才删文件。
//
// 删错的脚本无法撤销，少删一个只是多一步手动操作，风险不对称，所以原则是「任何不确定都保留」。
// 下面四条最容易被「顺手优化」掉，改之前务必看完：
//
//  1. 为什么不用 extractTaskScriptPath：见 task_script_target.go 文件头。它只做文本处理、与真实执行
//     多处不一致；这里只认 ParseCommandExecutionPlan（真实执行口径）解析出的文件。
//
//  2. 为什么查「其他任务」禁止在 SQL 里用 NOT IN 排除本次范围：GORM 遇到空集合或 nil 会生成
//     NOT IN (NULL)，它对任何行都不为真，查出 0 行（实验：gorm v1.31.1 + glebarez/sqlite）。
//     结果就是「没有其他任务在用这个脚本」——共用判定静默失效，直接误删别人还在用的脚本。
//     所以一律全表加载后在 Go 里过滤（loadScriptCleanupTasks），TestCollectOtherTasksNeverUsesNotIn 钉住。
//
//  3. 为什么路径上有任何一段是软链接就一律保留：解析器给出的 FullPath 已经被 EvalSymlinks 解析过，
//     直接删它，删掉的是链接指向的目标——目标可能正被其他任务用真名引用，甚至位于订阅目录；
//     只删链接本身又未必是用户想要的。面板替用户做不了这个决定，所以保留，让用户到脚本管理里处理。
//     判定靠「字面路径（Lstat，不跟随链接）」与「真实路径」比对，删除前还会再 Lstat 复核一次。
//
//  4. 为什么「排队中」的任务也要拦：排队请求里带的是触发时读出的 Task 副本，删掉任务行之后照样会执行
//     （scheduler_v2.go 入队、task_executor.go 按副本执行）；脚本若已被删，它会走 OnTaskFailed，
//     留下一条孤儿失败日志。所以运行中（status=2）与排队中（status=0.5）一视同仁；执行器进程表里
//     还有进程的（例如禁用了一个正在跑的任务，status 已被改写）也算运行中。
//
// 绝不做：RemoveAll、删父目录、动 script_versions（版本历史是删除后唯一的恢复途径）。
// 只对普通文件调用 os.Remove——Windows 上 os.Remove 连名为 dir.py 的空目录都能删掉。

// 不可删原因（reason）。decide 大体按下面的顺序检查，第一个命中的就是最终 reason：永久性原因在前，暂时性原因在后。
// 有两处例外（apiData 的 reason 说明同口径）：
//   - 执行阶段先报 not_found / changed（字面路径与 Collect 时比对），再做结构性复核（check_failed、hidden_path…）；
//     只有脚本目录解析失败、读任务或订阅失败这类环境级错误始终最先报 check_failed。
//   - LinkCheckFailed（逐段 Lstat 出错）按 check_failed 处理，但排在 hidden_path 之后判定。
const (
	TaskScriptReasonCheckFailed         = "check_failed"
	TaskScriptReasonHiddenPath          = "hidden_path"
	TaskScriptReasonSymlink             = "symlink"
	TaskScriptReasonNotRegularFile      = "not_regular_file"
	TaskScriptReasonManagedHelper       = "managed_helper"
	TaskScriptReasonSubscriptionManaged = "subscription_managed"
	TaskScriptReasonTaskNotDeleted      = "task_not_deleted"
	TaskScriptReasonShared              = "shared"
	TaskScriptReasonReferencedInHook    = "referenced_in_hook"
	TaskScriptReasonTaskRunning         = "task_running"

	// 以下只出现在执行阶段（Execute）。
	TaskScriptReasonNotConfirmed = "not_confirmed"
	TaskScriptReasonChanged      = "changed"
	TaskScriptReasonNotFound     = "not_found"
	TaskScriptReasonRemoveFailed = "remove_failed"
)

// tasks[].script_status 的取值。
const (
	taskScriptStatusResolved     = "resolved"
	taskScriptStatusNoScript     = "no_script"
	taskScriptStatusNotFound     = "not_found"
	taskScriptStatusOutside      = "outside_scripts_dir"
	taskScriptStatusUnresolved   = "unresolved"
	taskScriptStatusTaskNotFound = "task_not_found"
)

// 提示文案。服务端是唯一来源：前端原样展示，演示站（web/src/demo）逐字照抄，改这里要同步那边。
const (
	taskScriptDetailCheckFailed       = "检查脚本引用时出错，为安全起见保留这个文件。"
	taskScriptDetailHiddenPath        = "这个文件位于受保护的目录（如 .git、node_modules）里，不能在这里删除。"
	taskScriptDetailSymlink           = "脚本路径经过了软链接，面板无法确定该删链接还是它指向的文件，已保留；如需删除请到脚本管理处理。"
	taskScriptDetailNotRegularFmt     = "%s 不是普通文件（可能是一个文件夹），面板只删除普通文件。"
	taskScriptDetailManagedHelperFmt  = "%s 是面板内置的通知辅助脚本，其他脚本会引用它，删除后也会被自动重新生成。"
	taskScriptDetailUserHelperFmt     = "脚本目录根下的 %s 通常被其他脚本当作通知库引用，删除后面板会写入内置版本替换它，已保留。"
	taskScriptDetailGlobalHookFmt     = "脚本目录根下的 %s 是全局钩子，面板每次运行任务前或结束后都会执行它，删除后会静默失效，已保留。"
	taskScriptDetailSubGitForceFmt    = "位于订阅「%s」的仓库目录里，下次拉取会把它还原%s。如果不再需要，请在订阅设置里用白名单或黑名单把它排除。"
	taskScriptDetailSubGitPreserveFmt = "位于订阅「%s」的仓库目录里；这个订阅设为「保留本地修改」，删除会被当成本地改动，上游之后再改这个文件时拉取会冲突并持续失败。如果不再需要，请用白名单或黑名单排除它。"
	taskScriptDetailSubSingleFileFmt  = "这是订阅「%s」下载的文件，每次拉取都会重新下载%s。如果不再需要，请停用或删除这个订阅。"
	taskScriptDetailSubReaddSuffix    = "，并自动重新创建对应的任务"
	taskScriptDetailTaskNotDeletedFmt = "任务「%s」没有删除成功，脚本先保留。"
	// %s（末位）是共用方代词：共用方只有 1 个填「它」，≥2 个填「它们」（formatTaskScriptSharedDetail 决定）。
	taskScriptDetailSharedFmt          = "仍被 %d 个其他任务使用：%s，删除后%s会无法运行。"
	taskScriptDetailSharedTextMatchTag = "（按命令文本匹配）"
	taskScriptDetailTaskHookFmt        = "任务「%s」的前置/后置命令里提到了 %s，面板无法确认它是否依赖这个文件。"
	taskScriptDetailSubHookFmt         = "订阅「%s」的拉取前/后命令里提到了 %s，面板无法确认它是否依赖这个文件。"
	taskScriptDetailTaskRunningFmt     = "任务「%s」正在运行或排队中，脚本先保留；等运行结束后可以到脚本管理里删除。"
	taskScriptDetailNotConfirmed       = "这个脚本不在你确认删除的列表里（可能是确认之后任务命令被改过），已保留。"
	taskScriptDetailChanged            = "确认之后这个文件发生了变化（被替换、移动或变成了软链接），已保留。"
	taskScriptDetailNotFound           = "文件已经不存在，无需删除。"
	taskScriptDetailRemoveFailed       = "删除文件失败（可能被占用或没有权限），已保留，详情见面板日志。"

	taskScriptWarningHelperNameFmt = "文件名 %s 常见于公共库（如 sendNotify、utils、sign），可能被其他脚本引用，确认不再需要再删。"

	taskScriptNoteManagedFmt   = "命令不是直接运行脚本文件（由 %s 执行），不会一并删除任何文件。"
	taskScriptNoteModuleFmt    = "命令运行的是 Python 模块 %s，没有对应的脚本文件。"
	taskScriptNoteNotFoundFmt  = "命令里的脚本 %s 已经不存在，只删除任务。"
	taskScriptNoteNotFound     = "命令里的脚本已经不存在，只删除任务。"
	taskScriptNoteOutside      = "命令里的脚本不在脚本目录内，面板不会删除脚本目录以外的文件。"
	taskScriptNoteUnresolved   = "没能从命令里识别出脚本文件（命令格式可能有误），只删除任务。"
	taskScriptNoteTaskNotFound = "任务不存在（可能已被删除）。"
	// taskScriptNoteSnapshotError：读任务快照出错时用，不能复用 taskScriptNoteUnresolved——那句会把
	// 数据库错误说成命令格式问题，前端也会因 found=false 误报「任务不存在」（见 B1）。
	taskScriptNoteSnapshotError = "读取任务信息时出错，这次不会一并删除它的脚本文件。"

	// taskScriptHelperHeadBytes 判断根目录 notify.py / sendNotify.js 是否带托管标记时只读文件头，
	// 标记写在第一行，读 4KB 足够，也避免把大文件整个读进内存。
	taskScriptHelperHeadBytes = 4096
	// taskScriptSharedNamesLimit 共用提示里最多列出几个任务名。
	taskScriptSharedNamesLimit = 3
)

// TaskScriptCleanupEnv 是判定依赖的外部环境：handler 从配置与执行器取值，测试可以直接构造。
type TaskScriptCleanupEnv struct {
	ScriptsDir string
	// IsTaskRunning 查询执行器进程表，兜住「库里 status 不是运行中、但进程还在跑」的情况。可以为 nil。
	IsTaskRunning func(uint) bool
}

// TaskScriptRef 是仍在引用某个脚本的「本次范围外」任务（含已禁用）。
type TaskScriptRef struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
	// TextMatch 为 true 表示不是按「直接运行同一个文件」认出的，而是按命令文本匹配：托管命令参数里的脚本、
	// python -m 模块映射到的文件（a/b.py、a/b/__main__.py 及各级包的 __init__.py），或命令当前跑不起来、
	// 但命令文本里指向了这个文件。
	TextMatch bool `json:"text_match"`
}

// TaskScriptTaskInfo 是请求里每个任务 id 的解析结果，按请求顺序（去重后）排列。
type TaskScriptTaskInfo struct {
	ID      uint   `json:"id"`
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Running bool   `json:"running"`
	// ScriptStatus：resolved | no_script | not_found | outside_scripts_dir | unresolved | task_not_found。
	// resolved 的任务一定出现在 scripts[] 里。
	ScriptStatus string `json:"script_status"`
	// ScriptPath 只在 resolved（服务端解析出的路径）与 not_found（只用于展示）时有值。
	ScriptPath string `json:"script_path"`
	Note       string `json:"note"`
}

// TaskScriptItem 是按真实文件合并后的一个脚本。Deletable 当且仅当 Reason 为空。
type TaskScriptItem struct {
	Path      string          `json:"path"`
	TaskIDs   []uint          `json:"task_ids"`
	Deletable bool            `json:"deletable"`
	Reason    string          `json:"reason"`
	Detail    string          `json:"detail"`
	SharedBy  []TaskScriptRef `json:"shared_by"`
	Warnings  []string        `json:"warnings"`
	// RemoveError 只在 reason=remove_failed 时有值，给 handler 写面板日志用。
	// 刻意不进 JSON：底层错误原文带绝对路径，不能回显给前端。
	RemoveError string `json:"-"`
}

// TaskScriptPreview 是预览接口的 data。所有数组都初始化为非 nil，序列化出来是 [] 而不是 null。
type TaskScriptPreview struct {
	// Checked 恒为 true，是哨兵字段：演示站对未注册的端点兜底返回 {data:[]}，前端靠它区分「真的查过」和「没查成」。
	Checked bool                 `json:"checked"`
	Tasks   []TaskScriptTaskInfo `json:"tasks"`
	Scripts []TaskScriptItem     `json:"scripts"`
}

// TaskScriptDeleteResult 是删除入口带开关时追加在响应里的 scripts 字段。
type TaskScriptDeleteResult struct {
	Tasks   []TaskScriptTaskInfo `json:"tasks"`
	Deleted []TaskScriptItem     `json:"deleted"`
	Skipped []TaskScriptItem     `json:"skipped"`
}

// TaskScriptCleanup 是删除前拍下的快照。必须在删任务之前 Collect（删完就读不到命令了），
// 删任务之后再 Execute。
type TaskScriptCleanup struct {
	ids       []uint
	env       TaskScriptCleanupEnv
	base      scriptsBase
	baseErr   error
	snapErr   error
	snapshots map[uint]model.Task
	tasks     []TaskScriptTaskInfo
	groups    []*taskScriptGroup
}

// taskScriptGroup 是指向同一个真实文件的一组请求任务（按 os.SameFile 合并）。
type taskScriptGroup struct {
	path     string      // 对外展示与确认用的相对路径（真实路径相对 Real）
	realPath string      // 已解析软链接的绝对路径，也是最终 os.Remove 的对象
	info     os.FileInfo // Collect 时 os.Stat(realPath)：用于合并与执行前复核；取不到为 nil
	members  []taskScriptMember
	taskIDs  []uint
}

type taskScriptMember struct {
	taskID uint
	target TaskScriptTarget
	lstat  os.FileInfo // Collect 时对字面路径做的 Lstat（不跟随软链接）；失败为 nil
}

// taskScriptRefTask 是可能引用某个脚本的一条任务（范围外的其他任务，或执行阶段没删掉的范围内任务）。
type taskScriptRefTask struct {
	task   model.Task
	target TaskScriptTarget
	info   os.FileInfo // Kind=script 时 os.Stat(RealPath)
}

// taskScriptClassifyInput 是一次判定共用的上下文：同一次 classify 里所有脚本组看到的是同一份任务与订阅快照。
type taskScriptClassifyInput struct {
	execute   bool
	loadErr   error
	subs      []model.Subscription
	others    []taskScriptRefTask // 本次范围外的任务（含已禁用）
	remaining []taskScriptRefTask // 只在执行阶段：本该删掉却仍留在库里的范围内任务
}

// taskScriptCleanupMu 让执行阶段整体串行：两个带开关的请求并发删除共用同一脚本的两个任务时，
// 后执行的一方基于前者的结果重新判定，只会得到 not_found 或 shared，不会误删。
// Create / Copy / Import / 订阅同步都不拿这把锁，所以从重新判定到 os.Remove 之间仍有毫秒级窗口，
// 无法完全消除（已记入设计的风险一节）。
var taskScriptCleanupMu sync.Mutex

// PreviewTaskScriptDeletion 只读地给出预览结果，不加锁。
func PreviewTaskScriptDeletion(ids []uint, env TaskScriptCleanupEnv) TaskScriptPreview {
	cleanup := CollectTaskScriptTargets(ids, env)
	return TaskScriptPreview{
		Checked: true,
		Tasks:   cleanup.tasks,
		Scripts: cleanup.classify(false),
	}
}

// CollectTaskScriptTargets 给请求里的任务拍快照并解析脚本。必须在删任务之前调用。
func CollectTaskScriptTargets(ids []uint, env TaskScriptCleanupEnv) *TaskScriptCleanup {
	cleanup := &TaskScriptCleanup{
		ids:       uniqueTaskScriptIDs(ids),
		env:       env,
		snapshots: make(map[uint]model.Task),
		tasks:     []TaskScriptTaskInfo{},
		groups:    []*taskScriptGroup{},
	}
	cleanup.base, cleanup.baseErr = resolveScriptsBase(env.ScriptsDir)
	if cleanup.baseErr != nil {
		log.Printf("[任务删除] 解析脚本目录失败，本次不删除任何脚本: %v", cleanup.baseErr)
	}

	if len(cleanup.ids) > 0 {
		var snaps []model.Task
		// IN 遇到空集合会生成 IN (NULL) 查出 0 行——那是安全方向（什么都不删）；这里又先判了非空。
		if err := database.DB.Select("id", "name", "command", "status").Where("id IN ?", cleanup.ids).Find(&snaps).Error; err != nil {
			cleanup.snapErr = err
			log.Printf("[任务删除] 读取任务快照失败，本次不删除任何脚本: %v", err)
		}
		for _, snap := range snaps {
			cleanup.snapshots[snap.ID] = snap
		}
	}

	for _, id := range cleanup.ids {
		cleanup.tasks = append(cleanup.tasks, cleanup.collectTask(id))
	}
	sort.SliceStable(cleanup.groups, func(i, j int) bool {
		return cleanup.groups[i].path < cleanup.groups[j].path
	})
	return cleanup
}

func (c *TaskScriptCleanup) collectTask(id uint) TaskScriptTaskInfo {
	snap, ok := c.snapshots[id]
	if !ok {
		if c.snapErr != nil {
			// 读快照出错时不知道任务在不在，不能说「任务不存在」；什么脚本都不会删。
			// 用单独的 note（而非 taskScriptNoteUnresolved）说清是查库出错，见 B1。
			return TaskScriptTaskInfo{ID: id, ScriptStatus: taskScriptStatusUnresolved, Note: taskScriptNoteSnapshotError}
		}
		return TaskScriptTaskInfo{ID: id, ScriptStatus: taskScriptStatusTaskNotFound, Note: taskScriptNoteTaskNotFound}
	}

	info := TaskScriptTaskInfo{ID: id, Name: snap.Name, Found: true, Running: c.isTaskRunning(snap)}
	target := ResolveTaskScriptTarget(snap.Command, c.base)
	switch target.Kind {
	case taskScriptKindScript:
		group := c.addMember(id, target)
		info.ScriptStatus = taskScriptStatusResolved
		info.ScriptPath = group.path
	case taskScriptKindManaged:
		info.ScriptStatus = taskScriptStatusNoScript
		info.Note = fmt.Sprintf(taskScriptNoteManagedFmt, target.ManagedCommand)
	case taskScriptKindModule:
		info.ScriptStatus = taskScriptStatusNoScript
		info.Note = fmt.Sprintf(taskScriptNoteModuleFmt, target.PythonModule)
	case taskScriptKindNotFound:
		info.ScriptStatus = taskScriptStatusNotFound
		info.ScriptPath = target.HintPath
		if target.HintPath != "" {
			info.Note = fmt.Sprintf(taskScriptNoteNotFoundFmt, target.HintPath)
		} else {
			info.Note = taskScriptNoteNotFound
		}
	case taskScriptKindOutside:
		info.ScriptStatus = taskScriptStatusOutside
		info.Note = taskScriptNoteOutside
	default:
		info.ScriptStatus = taskScriptStatusUnresolved
		info.Note = taskScriptNoteUnresolved
	}
	return info
}

// addMember 把一个解析出脚本的请求任务并入对应的文件组：按 os.SameFile 识别同一个文件
// （./a.py、目录内的绝对路径、python3 与 task 两种前缀、Windows 下的 A.PY 都是同一个文件）。
func (c *TaskScriptCleanup) addMember(taskID uint, target TaskScriptTarget) *taskScriptGroup {
	if target.LinkCheckFailed {
		// 这个文件会按 check_failed 保留，前端只看到「检查脚本引用时出错」；原因只能到面板日志里查，所以这里必须留痕。
		// 口径与「解析脚本目录失败」「读取任务或订阅失败」两行一致：相对路径 + 错误原文。
		// 行里固定含「失败，已保留」，detectPanelLogLevel 会判 ERROR，不受路径里用户可控的文字影响。
		log.Printf("[任务删除] 逐段检查任务 [%d] 的脚本路径 %s 失败，已保留这个文件: %v", taskID, taskScriptDisplayPath(target), target.linkCheckErr)
	}
	member := taskScriptMember{taskID: taskID, target: target}
	if target.LiteralAbs != "" {
		if lstat, err := os.Lstat(target.LiteralAbs); err == nil {
			member.lstat = lstat
			pinFileIdentity(member.lstat)
		}
	}
	var info os.FileInfo
	if stat, err := os.Stat(target.RealPath); err == nil {
		info = stat
		pinFileIdentity(info)
	}

	for _, group := range c.groups {
		sameFile := info != nil && group.info != nil && os.SameFile(group.info, info)
		// 取不到文件信息的目标没法按文件合并，只能按真实路径文本合并；这类组在判定时一律 check_failed。
		samePath := info == nil && group.info == nil && group.realPath == target.RealPath
		if sameFile || samePath {
			group.members = append(group.members, member)
			group.taskIDs = append(group.taskIDs, taskID)
			return group
		}
	}

	group := &taskScriptGroup{
		path:     taskScriptDisplayPath(target),
		realPath: target.RealPath,
		info:     info,
		members:  []taskScriptMember{member},
		taskIDs:  []uint{taskID},
	}
	c.groups = append(c.groups, group)
	return group
}

// Execute 在删任务之后调用：持包级锁，基于库里剩下的任务重新判定，按 confirm 收窄，复核通过后删除文件。
//
// confirm 的语义：nil 表示调用方没传（不先调预览的 APP 与 OpenAPI），服务端判定可删的全部删除；
// 传了则只删 path 与列表某一项完全相等（大小写敏感）的，其余记 not_confirmed；传了空列表就一个都不删。
// confirm 只能收窄、不能扩大：请求里永远不接受「要删哪个文件」，路径只从 tasks.command 解析。
func (c *TaskScriptCleanup) Execute(confirm *[]string) TaskScriptDeleteResult {
	taskScriptCleanupMu.Lock()
	defer taskScriptCleanupMu.Unlock()

	result := TaskScriptDeleteResult{
		Tasks:   append([]TaskScriptTaskInfo{}, c.tasks...),
		Deleted: []TaskScriptItem{},
		Skipped: []TaskScriptItem{},
	}

	var confirmed map[string]bool
	if confirm != nil {
		confirmed = make(map[string]bool, len(*confirm))
		for _, confirmedPath := range *confirm {
			confirmed[confirmedPath] = true
		}
	}

	items := c.classify(true)
	for i, item := range items {
		group := c.groups[i]
		if !item.Deletable {
			result.Skipped = append(result.Skipped, item)
			continue
		}
		if confirmed != nil && !confirmed[item.Path] {
			result.Skipped = append(result.Skipped, keepTaskScriptItem(item, TaskScriptReasonNotConfirmed, taskScriptDetailNotConfirmed))
			continue
		}
		if reason, detail := c.recheckBeforeRemove(group); reason != "" {
			result.Skipped = append(result.Skipped, keepTaskScriptItem(item, reason, detail))
			continue
		}
		// 只删这一个普通文件：绝不 RemoveAll，不删父目录，不动 script_versions。
		if err := os.Remove(group.realPath); err != nil {
			if os.IsNotExist(err) {
				result.Skipped = append(result.Skipped, keepTaskScriptItem(item, TaskScriptReasonNotFound, taskScriptDetailNotFound))
				continue
			}
			kept := keepTaskScriptItem(item, TaskScriptReasonRemoveFailed, taskScriptDetailRemoveFailed)
			kept.RemoveError = err.Error()
			result.Skipped = append(result.Skipped, kept)
			continue
		}
		result.Deleted = append(result.Deleted, item)
	}
	return result
}

// classify 是预览与执行共用的判定。execute=true 时范围内的任务应当已经被删掉：
// 仍留在库里的会参与 task_not_deleted 判定，并且会先比对 Collect 时的文件状态。
func (c *TaskScriptCleanup) classify(execute bool) []TaskScriptItem {
	items := make([]TaskScriptItem, len(c.groups))
	for i, group := range c.groups {
		items[i] = TaskScriptItem{
			Path:     group.path,
			TaskIDs:  append([]uint{}, group.taskIDs...),
			SharedBy: []TaskScriptRef{},
			Warnings: taskScriptWarnings(group),
		}
	}
	if len(c.groups) == 0 {
		return items
	}

	scope := make(map[uint]bool, len(c.ids))
	for _, id := range c.ids {
		scope[id] = true
	}
	input := taskScriptClassifyInput{execute: execute}
	others, remaining, err := loadScriptCleanupTasks(scope)
	if err != nil {
		input.loadErr = err
	}
	subs, err := loadScriptCleanupSubscriptions()
	if err != nil && input.loadErr == nil {
		input.loadErr = err
	}
	if input.loadErr != nil {
		log.Printf("[任务删除] 读取任务或订阅失败，本次不删除任何脚本: %v", input.loadErr)
	}
	input.subs = subs
	input.others = c.resolveRefTasks(others)
	if execute {
		input.remaining = c.resolveRefTasks(remaining)
	}

	for i, group := range c.groups {
		item := &items[i]
		// shared_by 不论最终 reason 是什么都填写，让用户知道还有谁在用它。
		for _, ref := range input.others {
			if hit, textMatch := group.referencedBy(ref); hit {
				item.SharedBy = append(item.SharedBy, TaskScriptRef{ID: ref.task.ID, Name: ref.task.Name, TextMatch: textMatch})
			}
		}
		item.Reason, item.Detail = c.decide(group, item.SharedBy, input)
		item.Deletable = item.Reason == ""
	}
	return items
}

// decide 按 api_contract 第 3 节的顺序给出第一个命中的保留原因；返回空串表示可以删除。
// 两处例外见 reason 常量上方的注释：执行阶段先报 not_found / changed；LinkCheckFailed 排在 hidden_path 之后。
func (c *TaskScriptCleanup) decide(group *taskScriptGroup, sharedBy []TaskScriptRef, in taskScriptClassifyInput) (string, string) {
	// 环境级错误最先拦：无从判定，一律 check_failed。
	if c.baseErr != nil || in.loadErr != nil {
		return TaskScriptReasonCheckFailed, taskScriptDetailCheckFailed
	}

	// 预览阶段：先做结构性复核（groupConsistent → check_failed、hidden → hidden_path），与改动前顺序一致。
	if !in.execute {
		if !c.groupConsistent(group) {
			return TaskScriptReasonCheckFailed, taskScriptDetailCheckFailed
		}
		if group.hidden() {
			return TaskScriptReasonHiddenPath, taskScriptDetailHiddenPath
		}
	}

	// 字面路径的当前状态（Lstat，不跟随软链接）。执行阶段先与 Collect 时比对：确认之后文件被删、
	// 或被替换成同名目录 / 指向外部的软链接 / 另一个新文件，统一报 not_found / changed，
	// 比「不是普通文件」「经过软链接」更贴近真相。这一步排在 groupConsistent 之前（仅执行阶段），
	// 是为了让「换成指向目录外的软链接」报 changed，而不是 groupConsistent 的 check_failed（见 B4）。
	current := make([]os.FileInfo, len(group.members))
	for i, member := range group.members {
		info, err := os.Lstat(member.target.LiteralAbs)
		if err != nil {
			if !in.execute {
				return TaskScriptReasonCheckFailed, taskScriptDetailCheckFailed
			}
			if os.IsNotExist(err) {
				return TaskScriptReasonNotFound, taskScriptDetailNotFound
			}
			return TaskScriptReasonChanged, taskScriptDetailChanged
		}
		if in.execute && !sameLstatIdentity(info, member.lstat) {
			return TaskScriptReasonChanged, taskScriptDetailChanged
		}
		current[i] = info
	}

	// 执行阶段：Lstat 身份比对通过后再做结构性复核；通过的组仍会走后面的 symlink / recheckBeforeRemove，防护不削弱。
	if in.execute {
		if !c.groupConsistent(group) {
			return TaskScriptReasonCheckFailed, taskScriptDetailCheckFailed
		}
		if group.hidden() {
			return TaskScriptReasonHiddenPath, taskScriptDetailHiddenPath
		}
	}

	// 末级是软链接，或字面相对路径与真实相对路径不同（中间某一段目录是软链接、或用脚本目录别名写的绝对路径），
	// 或 Windows 目录联接：文本上 Direct 为真时 pathHasLinkSegment 才逐段检查，命中会令 Direct=false（→ symlink）；
	// 逐段 Lstat 出错则标 LinkCheckFailed（→ check_failed，排在上面的 hidden_path 之后）。见 A1、R2-1。
	for i, member := range group.members {
		if member.target.LinkCheckFailed {
			return TaskScriptReasonCheckFailed, taskScriptDetailCheckFailed
		}
		if !member.target.Direct || current[i].Mode()&os.ModeSymlink != 0 {
			return TaskScriptReasonSymlink, taskScriptDetailSymlink
		}
	}
	// 解析器会接受名字像脚本的目录（dir.py），这里必须拦住。
	for _, info := range current {
		if !info.Mode().IsRegular() {
			return TaskScriptReasonNotRegularFile, fmt.Sprintf(taskScriptDetailNotRegularFmt, group.path)
		}
	}
	if detail := c.managedHelperDetail(group); detail != "" {
		return TaskScriptReasonManagedHelper, detail
	}
	if detail := c.subscriptionDetail(group, in.subs); detail != "" {
		return TaskScriptReasonSubscriptionManaged, detail
	}
	if in.execute {
		for _, ref := range in.remaining {
			if hit, _ := group.referencedBy(ref); hit {
				return TaskScriptReasonTaskNotDeleted, fmt.Sprintf(taskScriptDetailTaskNotDeletedFmt, ref.task.Name)
			}
		}
	}
	if len(sharedBy) > 0 {
		return TaskScriptReasonShared, formatTaskScriptSharedDetail(sharedBy)
	}
	if detail := c.hookDetail(group, in); detail != "" {
		return TaskScriptReasonReferencedInHook, detail
	}
	for _, id := range group.taskIDs {
		if snap, ok := c.snapshots[id]; ok && c.isTaskRunning(snap) {
			return TaskScriptReasonTaskRunning, fmt.Sprintf(taskScriptDetailTaskRunningFmt, snap.Name)
		}
	}
	return "", ""
}

// recheckBeforeRemove 是 os.Remove 之前的最后一次复核，任何一项对不上都不删（记 changed / not_found）。
// 从 classify 到这里仍有极短的窗口，而删除不可撤销，所以宁可重复检查。
func (c *TaskScriptCleanup) recheckBeforeRemove(group *taskScriptGroup) (string, string) {
	current, err := os.Stat(group.realPath)
	if err != nil {
		if os.IsNotExist(err) {
			return TaskScriptReasonNotFound, taskScriptDetailNotFound
		}
		return TaskScriptReasonChanged, taskScriptDetailChanged
	}
	if !sameFileIdentity(current, group.info) {
		return TaskScriptReasonChanged, taskScriptDetailChanged
	}
	for _, member := range group.members {
		literal, err := os.Lstat(member.target.LiteralAbs)
		if err != nil {
			if os.IsNotExist(err) {
				return TaskScriptReasonNotFound, taskScriptDetailNotFound
			}
			return TaskScriptReasonChanged, taskScriptDetailChanged
		}
		// Mode().IsRegular() 对软链接、目录、设备文件都为 false。
		if !literal.Mode().IsRegular() || !sameFileIdentity(literal, member.lstat) || !sameFileIdentity(literal, current) {
			return TaskScriptReasonChanged, taskScriptDetailChanged
		}
		// 逐段再查一次路径上是否新出现软链接 / Windows 目录联接：命中或出错都记 changed（不可撤销，宁可保留）。见 A1。
		if hit, err := pathHasLinkSegment(c.base, member.target.LiteralAbs); err != nil || hit {
			return TaskScriptReasonChanged, taskScriptDetailChanged
		}
	}
	if !pathutil.IsWithinBase(c.base.Real, group.realPath) || ShouldHideScriptTreeRelativePath(group.path) {
		return TaskScriptReasonChanged, taskScriptDetailChanged
	}
	return "", ""
}

// groupConsistent 是防御性复核：能取到文件信息、确实位于真实脚本目录内、组内成员指向同一个相对路径。
// 硬链接会让不同路径 SameFile，这种组删掉一个路径另一个仍在，报告会失真，干脆保留。
func (c *TaskScriptCleanup) groupConsistent(group *taskScriptGroup) bool {
	if group.info == nil || !pathutil.IsWithinBase(c.base.Real, group.realPath) {
		return false
	}
	for _, member := range group.members {
		if isOutsideRel(member.target.RelPath) || !samePathText(member.target.RelPath, group.path) {
			return false
		}
	}
	return true
}

// managedHelperDetail：脚本目录根下的 notify.py / sendNotify.js 不论有无托管标记一律保留。
// 不带标记的，面板不会覆盖；但文件缺失时每次运行都会写入内置版本（ensureManagedHelperFile），
// 而且环境变量写死指向根目录下这两个文件——删掉用户自己的版本，面板会悄悄换成内置版本。
// 读文件头只是为了选文案。
func (c *TaskScriptCleanup) managedHelperDetail(group *taskScriptGroup) string {
	if !samePathText(filepath.Dir(group.realPath), c.base.Real) {
		return ""
	}
	name := filepath.Base(group.realPath)
	// 脚本目录根下的全局钩子 task_before.sh / task_after.sh / extra.sh：执行器每次跑任务前（task_before.sh）
	// 或结束后（task_after.sh、extra.sh）都会 RunHookScript 它们，文件缺失时静默返回、任务日志里什么都不写。
	// 删掉「直接运行钩子文件」的那个任务并连脚本一起删，会让全局钩子静默失效，所以一律保留。
	// reason 仍用 managed_helper（不新增 reason 值），只多一条 detail 文案。子目录下的同名文件不受影响。
	if isGlobalHookScriptName(name) {
		return fmt.Sprintf(taskScriptDetailGlobalHookFmt, name)
	}
	if !samePathText(name, notifyPyFilename) && !samePathText(name, sendNotifyJSFilename) {
		return ""
	}
	if fileHeadContains(group.realPath, managedNotifyHelperToken, taskScriptHelperHeadBytes) {
		return fmt.Sprintf(taskScriptDetailManagedHelperFmt, name)
	}
	return fmt.Sprintf(taskScriptDetailUserHelperFmt, name)
}

// isGlobalHookScriptName 判断文件名是否是脚本目录根下的全局钩子（Windows 下不分大小写）。
// 名单与 task_executor.go / scheduler.go 里 RunHookScript 的调用一致。
func isGlobalHookScriptName(name string) bool {
	return samePathText(name, "task_before.sh") ||
		samePathText(name, "task_after.sh") ||
		samePathText(name, "extra.sh")
}

// subscriptionDetail：订阅管理的文件一律保留（订阅已停用也算）。以路径为准，不看 subscription: 标签——
// 任务 Create 直接落库 labels，标签可以伪造。
func (c *TaskScriptCleanup) subscriptionDetail(group *taskScriptGroup, subs []model.Subscription) string {
	for i := range subs {
		sub := &subs[i]
		readd := ""
		if resolveSubscriptionAutoAddTask(sub) {
			readd = taskScriptDetailSubReaddSuffix
		}

		if sub.Type == model.SubTypeSingleFile {
			// 单文件订阅只保护精确的下载目标（每次拉取都会无条件重新下载），同目录下的其他文件照常判定。
			dest := singleFileSubscriptionDestPath(c.base.Abs, sub)
			if info, err := os.Stat(dest); err == nil && group.info != nil && os.SameFile(info, group.info) {
				return fmt.Sprintf(taskScriptDetailSubSingleFileFmt, sub.Name, readd)
			}
			continue
		}

		// 拉取时 Type 不是 single-file 的一律按 git 仓库处理，这里同口径。
		if !c.gitSubscriptionOwns(sub, group) {
			continue
		}
		if resolveSubscriptionForceOverwrite(sub) {
			return fmt.Sprintf(taskScriptDetailSubGitForceFmt, sub.Name, readd)
		}
		return fmt.Sprintf(taskScriptDetailSubGitPreserveFmt, sub.Name)
	}
	return ""
}

// gitSubscriptionOwns 判断文件是否位于 git 订阅的根目录内。根目录公式与拉取时一致
// （pullGitRepoWithCallback：ScriptsDir/<SaveDir，空则 Alias，再空则 URL 末段去掉 .git>）。
//
// 不要求根目录已经是 git 仓库：已存在的非空目录在下次拉取时会被 git init 加 reset --hard 原地覆盖。
// 但根目录等于脚本目录或在它上层时，只有那里真的是 git 仓库才算——handler 不校验 SaveDir，
// 一条配错的订阅否则会把全部脚本锁死、一个都删不掉。
func (c *TaskScriptCleanup) gitSubscriptionOwns(sub *model.Subscription, group *taskScriptGroup) bool {
	root := filepath.Join(c.base.Abs, subscriptionSaveDir(sub))
	if pathutil.IsWithinBase(root, c.base.Abs) {
		return IsGitRepo(root)
	}
	for _, member := range group.members {
		if pathutil.IsWithinBase(root, group.realPath) || (member.target.LiteralAbs != "" && pathutil.IsWithinBase(root, member.target.LiteralAbs)) {
			return true
		}
	}
	return false
}

// hookDetail：范围外任务的前置/后置命令，或订阅的拉取前/后命令里提到了这个文件，面板无法确认是否依赖，保留。
func (c *TaskScriptCleanup) hookDetail(group *taskScriptGroup, in taskScriptClassifyInput) string {
	names := group.names()
	fileName := path.Base(group.path)
	for _, ref := range in.others {
		for _, text := range []*string{ref.task.TaskBefore, ref.task.TaskAfter} {
			if text != nil && hookTextMentionsScript(*text, names, c.base) {
				return fmt.Sprintf(taskScriptDetailTaskHookFmt, ref.task.Name, fileName)
			}
		}
	}
	for _, sub := range in.subs {
		if hookTextMentionsScript(sub.PreScript, names, c.base) || hookTextMentionsScript(sub.HookScript, names, c.base) {
			return fmt.Sprintf(taskScriptDetailSubHookFmt, sub.Name, fileName)
		}
	}
	return ""
}

// hookTextMentionsScript 判断一段内联 shell 有没有提到这个文件。这类片段无法可靠解析，只能按 token 匹配：
// 切词后逐个归一化，等于相对路径、或文件名相同即命中。按文件名匹配能认出 cd jd && node sign.js；
// 整 token 比较则不会让 data.py 误伤 a.py。
func hookTextMentionsScript(text string, names []string, base scriptsBase) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("\"'`;&|()<>=", r)
	})
	for i, field := range fields {
		// python -m 的模块名（cd /tmp && python3 -m jd.sign → 实际加载 jd/sign.py）：模块名既不等于相对路径，
		// 文件名也对不上，下面的比较认不出来，删掉后这个钩子会报 ModuleNotFoundError。按任务命令的 python -m 同一口径
		// （pythonModuleTextCandidates）映射成它可能加载的文件再比。见 R2-3、A3。
		// 候选是从脚本目录根算起的，而 python -m 按当前目录找模块：钩子先 cd 进子目录再 -m（cd jd && python3 -m sign
		// → 实际加载 jd/sign.py）时整路径对不上，所以再按「以 /候选 结尾」比一次（R3-BE-1）。用后缀而不是像下面那样
		// 只比文件名：后缀同样认得出点号模块和沿途父包的 __init__.py（cd jd && python3 -m pkg.mod → jd/pkg/mod.py、
		// jd/pkg/__init__.py），而且更收敛——python3 -m json.tool 只会保住 */json/tool.py，不会让任意一个 tool.py
		// 都删不掉。代价是 cd 到别处再 -m sign 也会保住 jd/sign.py，偏差只朝多保留走。
		if hookPythonModuleReference(fields, i) {
			for _, candidate := range pythonModuleTextCandidates(field, base) {
				for _, name := range names {
					if samePathText(candidate, name) || pathTextHasSuffix(name, "/"+candidate) {
						return true
					}
				}
			}
		}
		normalized := normalizeScriptReference(field, base)
		if normalized == "" {
			continue
		}
		fieldBase := path.Base(normalized)
		for _, name := range names {
			if samePathText(normalized, name) || samePathText(fieldBase, path.Base(name)) {
				return true
			}
		}
		// 省略扩展名的 node 引用（cd jd && node sign → 实际跑 sign.js）：field 没有扩展名，
		// 且前一个 token 是 node/tsx/ts-node/bun 或 field 以 ./ 开头时，去掉候选文件的扩展名再比一次。
		// 限定这两个条件是为了避免 echo sign 之类的普通文本造成过度保留。见 A5。
		if path.Ext(fieldBase) == "" && hookNodeStyleReference(field, fields, i) {
			for _, name := range names {
				switch strings.ToLower(path.Ext(name)) {
				case ".js", ".mjs", ".ts":
					if samePathText(fieldBase, strings.TrimSuffix(path.Base(name), path.Ext(name))) {
						return true
					}
				}
			}
		}
	}
	return false
}

// hookPythonModuleReference 判断 fields[idx] 是否是 python -m 的模块名：前一个 token 是 -m，并且再往前找到的
// 第一个不以 - 开头的 token 是 python 解释器（跳过 -u 这类选项，python3 -u -m a 也能认出）。
// 解释器名单复用 IsPythonInterpreter，与 ParseCommandExecutionPlan 认 python -m 的口径一致。
// 限定解释器，是为了不让 git commit -m a、task -m 5 x.py 这类写法把 a、5 当成模块而过度保留。
func hookPythonModuleReference(fields []string, idx int) bool {
	if idx < 2 || fields[idx-1] != "-m" {
		return false
	}
	for j := idx - 2; j >= 0; j-- {
		if strings.HasPrefix(fields[j], "-") {
			continue
		}
		return IsPythonInterpreter(fields[j])
	}
	return false
}

// pathTextHasSuffix 判断路径文本 s 是否以 suffix 结尾，大小写口径与 samePathText 一致：Windows 文件系统
// 不区分大小写（JD/Sign.py 与 jd/sign.py 是同一个文件），两边先转小写再比；其余平台严格比较。
// 调用方负责让 suffix 以 / 开头，保证按整段路径比，不让 xsign.py 因为以 sign.py 结尾而命中。
func pathTextHasSuffix(s, suffix string) bool {
	if runtime.GOOS == "windows" {
		return strings.HasSuffix(strings.ToLower(s), strings.ToLower(suffix))
	}
	return strings.HasSuffix(s, suffix)
}

// hookNodeStyleReference 判断 fields[idx] 是否是「node 风格、省略扩展名」的脚本引用：
// 以 ./ 开头，或前一个 token 是 node/tsx/ts-node/bun。
func hookNodeStyleReference(field string, fields []string, idx int) bool {
	if strings.HasPrefix(field, "./") {
		return true
	}
	if idx <= 0 {
		return false
	}
	switch fields[idx-1] {
	case "node", "tsx", "ts-node", "bun":
		return true
	}
	return false
}

// referencedBy 判断一条任务是否引用这个文件：直接运行它（Kind=script，按 SameFile），
// 或所有非 script 的 Kind（managed / module / not_found / outside / unresolved）按命令文本 / 模块名匹配。
// 后者只用来扩大共用判定：宁可多认一个共用方而多保留，也不误删别人还在用的脚本（见 A3、A4）。
func (g *taskScriptGroup) referencedBy(ref taskScriptRefTask) (hit bool, textMatch bool) {
	if ref.target.Kind == taskScriptKindScript {
		return ref.info != nil && g.info != nil && os.SameFile(ref.info, g.info), false
	}
	names := g.names()
	for _, candidate := range ref.target.TextCandidates {
		for _, name := range names {
			if samePathText(candidate, name) {
				return true, true
			}
		}
	}
	return false, false
}

// names 是这个文件在命令里可能的写法（真实相对路径与各成员的字面相对路径），用于文本匹配与隐藏目录判定。
func (g *taskScriptGroup) names() []string {
	names := []string{g.path}
	for _, member := range g.members {
		for _, rel := range []string{member.target.RelPath, member.target.LiteralRel} {
			if !isOutsideRel(rel) {
				names = append(names, rel)
			}
		}
	}
	return names
}

// hidden：执行解析器不过滤 .git / node_modules 等目录，这里对真实路径和字面路径各查一次。
func (g *taskScriptGroup) hidden() bool {
	for _, name := range g.names() {
		if ShouldHideScriptTreeRelativePath(name) {
			return true
		}
	}
	return false
}

func (c *TaskScriptCleanup) isTaskRunning(task model.Task) bool {
	if task.Status == model.TaskStatusRunning || task.Status == model.TaskStatusQueued {
		return true
	}
	return c.env.IsTaskRunning != nil && c.env.IsTaskRunning(task.ID)
}

func (c *TaskScriptCleanup) resolveRefTasks(tasks []model.Task) []taskScriptRefTask {
	refs := make([]taskScriptRefTask, 0, len(tasks))
	for _, task := range tasks {
		ref := taskScriptRefTask{task: task, target: ResolveTaskScriptTarget(task.Command, c.base)}
		if ref.target.Kind == taskScriptKindScript {
			if info, err := os.Stat(ref.target.RealPath); err == nil {
				ref.info = info
			}
		}
		refs = append(refs, ref)
	}
	return refs
}

// loadScriptCleanupTasks 全表加载任务（含已禁用），在 Go 里按 scope 分成「范围外的其他任务」与「仍在库里的范围内任务」。
//
// ⚠️ 禁止改成在 SQL 里排除 scope：GORM 对空集合或 nil 生成 NOT IN (NULL)，查出 0 行，
// 共用判定会静默失效、直接误删。scope 为 nil 或空时，others 必须等于全表（TestCollectOtherTasksNeverUsesNotIn）。
func loadScriptCleanupTasks(scope map[uint]bool) ([]model.Task, []model.Task, error) {
	var all []model.Task
	if err := database.DB.Select("id", "name", "command", "status", "task_before", "task_after").Order("id ASC").Find(&all).Error; err != nil {
		return nil, nil, err
	}
	others := make([]model.Task, 0, len(all))
	remaining := make([]model.Task, 0)
	for _, task := range all {
		if scope[task.ID] {
			remaining = append(remaining, task)
			continue
		}
		others = append(others, task)
	}
	return others, remaining, nil
}

// loadScriptCleanupSubscriptions 全部加载，包括已停用的订阅（停用的订阅重新启用后照样会拉取）。
func loadScriptCleanupSubscriptions() ([]model.Subscription, error) {
	var subs []model.Subscription
	err := database.DB.Select("id", "name", "type", "url", "save_dir", "alias", "overwrite_mode", "auto_add_task_mode", "pre_script", "hook_script").
		Order("id ASC").Find(&subs).Error
	return subs, err
}

// taskScriptWarnings：文件名命中公共库名单时只提示、不阻断——sign.js 是最常见的签到脚本名，拦下来会让常见任务删不掉脚本。
func taskScriptWarnings(group *taskScriptGroup) []string {
	warnings := []string{}
	name := path.Base(group.path)
	if isSubscriptionHelperScript(name) {
		warnings = append(warnings, fmt.Sprintf(taskScriptWarningHelperNameFmt, strings.TrimSuffix(name, path.Ext(name))))
	}
	return warnings
}

func formatTaskScriptSharedDetail(refs []TaskScriptRef) string {
	names := make([]string, 0, taskScriptSharedNamesLimit)
	for i, ref := range refs {
		if i >= taskScriptSharedNamesLimit {
			break
		}
		name := ref.Name
		if ref.TextMatch {
			name += taskScriptDetailSharedTextMatchTag
		}
		names = append(names, name)
	}
	list := strings.Join(names, "、")
	if len(refs) > taskScriptSharedNamesLimit {
		list += " 等"
	}
	// 共用方只有 1 个用「它」，多个用「它们」。
	pronoun := "它们"
	if len(refs) == 1 {
		pronoun = "它"
	}
	return fmt.Sprintf(taskScriptDetailSharedFmt, len(refs), list, pronoun)
}

func keepTaskScriptItem(item TaskScriptItem, reason, detail string) TaskScriptItem {
	item.Deletable = false
	item.Reason = reason
	item.Detail = detail
	return item
}

func sameLstatIdentity(current, collected os.FileInfo) bool {
	return collected != nil && current.Mode().Type() == collected.Mode().Type() && sameFileIdentity(current, collected)
}

// pinFileIdentity 强制加载并固化一个 FileInfo 的文件身份。
// Windows 上 os.Stat/Lstat 得到的 FileInfo 要到首次 os.SameFile 时才按路径读取卷序号与文件索引，
// 之后再对同一路径 Stat 会读到替换后的新文件。Collect 时立刻调一次 SameFile(x,x) 把当时的身份缓存下来，
// 之后 sameFileIdentity、recheckBeforeRemove 比较的就是 Collect 时的身份（见 A6）。
// 非 Windows 上 SameFile 直接比 Stat_t，无副作用，也无害。
func pinFileIdentity(info os.FileInfo) {
	if info == nil {
		return
	}
	_ = os.SameFile(info, info)
}

// sameFileIdentity 判断两个 FileInfo 是否是「同一个、且没变过」的文件。
// 除 os.SameFile 外还比 size、modtime，以及（Linux/Android）inode 变更时间 ctime：
// Windows 上对同一路径的两次 os.Stat，os.SameFile 可能因文件 id 是按路径惰性读取、
// 或 NTFS 复用 MFT 记录而恒为真，单靠它认不出「确认之后文件被同名替换」；size / modtime 让大多数替换
// 在各平台都能被察觉；ctime 再兜住「同尺寸 + 用 os.Chtimes 还原 mtime」的替换（ctime 无法被还原，见 A6）。
// 方向一致——宁可多判成 changed 而保留，绝不误删。
func sameFileIdentity(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return false
	}
	if !(os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())) {
		return false
	}
	// 两边都能取到 ctime 时再比一道；取不到（如 Windows）就退回上面的 size+mtime 判据。
	ctimeA, okA := fileChangeTime(a)
	ctimeB, okB := fileChangeTime(b)
	if okA && okB && !ctimeA.Equal(ctimeB) {
		return false
	}
	return true
}

// taskScriptDisplayPath 选对外展示的相对路径；正常情况下就是 RelPath。
// 两个兜底分支只在防御性场景出现（这类组会被判 check_failed），保证绝不回显绝对路径。
func taskScriptDisplayPath(target TaskScriptTarget) string {
	if !isOutsideRel(target.RelPath) {
		return target.RelPath
	}
	if !isOutsideRel(target.LiteralRel) {
		return target.LiteralRel
	}
	return filepath.Base(target.RealPath)
}

func fileHeadContains(filePath, needle string, limit int64) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()
	head, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return false
	}
	return strings.Contains(string(head), needle)
}

func uniqueTaskScriptIDs(ids []uint) []uint {
	result := make([]uint, 0, len(ids))
	seen := make(map[uint]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	return result
}
