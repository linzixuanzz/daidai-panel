# 后端质量规范

> 目标是让后端改动稳定、兼容、容易回溯，而不是为了形式统一牺牲可维护性。

---

## 禁止项

- 禁止只改一层，不检查对应 handler/service/model/database 联动。
- 禁止为了抽象强拆很多小函数，让主流程反而难读。
- 禁止绕开 `pkg/response` 随意发散响应格式。
- 禁止忽视本地 SQLite 老数据兼容问题。
- 禁止在没有验证的情况下声称后端改动完成。

---

## 必做项

- 先搜索现有实现，优先复用已有分层和已有模式。
- 复杂边界和兼容逻辑补中文注释。
- 数据库字段/索引/迁移相关改动必须检查 `database/database.go`。
- 安全相关逻辑要检查成功、失败、限流、鉴权分支。
- 改后端逻辑后默认跑测试。

---

## 测试要求

后端改动后默认执行：

```bash
cd server
go test ./...
```

如果本次修改没有对应测试覆盖点，也要在结果里明确说明还有哪些残余风险。

- 用例修改任何包级全局，必须用 `t.Cleanup` 还原；能在 `testutil.SetupTestEnv` 里统一重置的优先放那里。
- `testutil.SetupTestEnv` 在进入与退出时都会把 middleware 可信代理重置回默认私网段；想验证「可信代理改窄后的行为」的用例，必须在 `SetupTestEnv` **之后**自己配置。

---

## 场景：定时任务默认列表排序与置顶优先级

### 1. Scope / Trigger

- 触发：修改 `server/handler/task_query.go` 中任务列表默认排序、`is_pinned`、`status`、`list_order`、`sort_order` 相关逻辑，或改动 `PUT /api/tasks/sort` 拖拽排序时必须看本节。
- 原因：置顶是用户主动设置的展示优先级。如果默认排序先按启用 / 禁用状态分组，再按 `is_pinned` 排序，禁用后的置顶任务会被普通启用任务挤到后面，表现为“禁用任务不能保持置顶”。

### 2. Contracts

- 默认任务列表排序必须先尊重 `is_pinned DESC`，再按任务状态分组。
- 已置顶任务即使状态变为禁用，也必须继续保留在置顶区域。
- 置顶区内部再按状态分组、`list_order`、`sort_order`、创建时间和 ID 保持稳定顺序。
- 自定义视图排序没有命中差异时，最终兜底排序也必须使用同一套默认规则，避免默认列表和视图列表表现不一致。
- **`list_order` 和 `sort_order` 是两个互不影响的字段，禁止合并成一个：**
  - `tasks.list_order` 管**列表展示顺序**，任务页的拖拽排序写的就是它；
  - `tasks.sort_order` 继续管**开机任务的执行顺序**（见「场景：任务并发闸门与执行槽位」），拖拽绝不能碰它。

  拿 `sort_order` 去接列表拖拽会静默改写用户的开机编排，而守着开机顺序的那条用例因为自己显式设值**抓不到**这种回归 —— 这正是必须新开一列的原因。
- 默认排序里 `list_order` 排在 `sort_order` **之前**，完整顺序是：`is_pinned` → 状态分组 → **`list_order`** → `sort_order` → `created_at DESC` → `id DESC`。
- 存量行的 `list_order` 一律补 0（`EnsureColumns` 里 `INTEGER NOT NULL DEFAULT 0`），所以升级后默认列表顺序与升级前**逐字节一致**，不会因为加了一列就把用户看惯的顺序打乱。
- **SQL 下推路径与内存排序路径必须用同一套口径**：任何一处漏加 `list_order`，分页结果与整表顺序就会对不上，表现为「翻页时任务重复出现或漏掉」。

### 3. Tests Required

- 禁用但已置顶任务应排在普通启用任务前面。
- 运行中 / 排队中 / 启用状态变化不能打乱同组内 `sort_order` 的稳定顺序。
- 存量数据（`list_order` 全为 0）时默认列表顺序与加列之前完全一致。
- 拖拽写入 `list_order` 后，同桶内顺序按 `list_order` 生效，且 `sort_order` 一个字节都没被改动。
- 修改排序时至少运行：

```bash
cd server
go test ./handler -run "TestTaskListKeepsPinnedDisabledTasksInPinnedArea|TestTaskListKeepsStableOrderWhenTaskStatusChangesToRunning"
go test ./...
```

---

## 场景：开机运行任务每天自动触发一次

### 1. Scope / Trigger

- 触发：修改 `server/service/scheduler_v2.go` 的 `EnqueueStartupTasks()`、`RunNow()`，或修改 `model.Task` 的任务类型 / 启动触发状态字段时必须看本节。
- 原因：「开机运行」是面板启动流程的自动触发能力，不等同于“每次服务进程启动都重复执行”。面板更新、容器重建、电脑重启都会导致服务再次启动，如果不持久化当天自动触发状态，同一天可能重复跑用户原本只想每天启动自动跑一次的任务。

### 2. Signatures

- 自动触发入口：`func (s *SchedulerV2) EnqueueStartupTasks() int`
- 手动触发入口：`func (s *SchedulerV2) RunNow(taskID uint) error`
- 任务字段：`Task.LastStartupAutoRunDate string`
- 数据库字段：`tasks.last_startup_auto_run_date VARCHAR(10) DEFAULT ''`

### 3. Contracts

- 开机运行任务的自动触发按面板本地日期限流，同一个任务同一天只能由 `EnqueueStartupTasks()` 自动入队一次。
- 自动触发成功入队后，必须立即写入 `last_startup_auto_run_date=当天日期`，避免任务执行结束回到「启用」后，当天再次重启又被自动入队。
- 手动运行必须继续走 `RunNow()`，不得读取或修改 `last_startup_auto_run_date`，确保用户当天仍可手动运行多次。
- 旧日期、空字符串、`NULL` 都表示当天尚未自动触发，可以在当天首次启动时自动入队。
- 新字段必须在 `database.EnsureColumns()` 中补列，保证已有 SQLite 用户升级后无需手动迁移。

### 4. Validation & Error Matrix

- `last_startup_auto_run_date == today` -> `EnqueueStartupTasks()` 跳过该任务。
- `last_startup_auto_run_date == ''` 或旧日期 -> `EnqueueStartupTasks()` 正常入队，并写入今天日期。
- 自动入队失败（队列满 / scheduler stopped）-> 不写入今天日期，允许后续启动再尝试。
- 手动 `RunNow()` -> 不检查今天日期，正常入队或返回原有队列错误。
- 老库缺字段 -> 启动时通过 `EnsureColumns()` 自动补列，不能要求用户手工改库。

### 5. Good/Base/Bad Cases

- Good：早上第一次启动面板，开机运行任务自动执行；上午面板更新重启，任务不再自动重复执行；用户手动点「运行」仍可执行。
- Base：昨天自动执行过，今天首次启动面板时再次自动执行。
- Bad：直接用 `last_run_at` 判断是否今天跑过，因为手动运行也会更新 `last_run_at`，会误伤「手动可以再次执行」的需求。

### 6. Tests Required

- 同一天第一次 `EnqueueStartupTasks()` 返回 1，写入 `LastStartupAutoRunDate=today`。
- 模拟任务完成后状态回到启用，同一天第二次 `EnqueueStartupTasks()` 返回 0。
- `RunNow()` 在 `LastStartupAutoRunDate=today` 时仍可多次入队。
- 旧日期任务在新的一天仍可自动入队。
- 修改后至少运行：

```bash
cd server
go test ./service -run "TestSchedulerV2" -count=1
go test ./...
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：每次面板服务启动都重新入队，更新/重启会导致同一天重复自动跑。
database.DB.Where("status = ? AND task_type = ?", model.TaskStatusEnabled, model.TaskTypeStartup).Find(&tasks)
```

```go
// 错误：用 last_run_at 限制会把用户手动运行也算进去，破坏“手动可多次执行”。
database.DB.Where("DATE(last_run_at) <> ?", today).Find(&tasks)
```

#### Correct

```go
// 正确：只限制开机运行的自动触发日期，手动 RunNow 不看这个字段。
database.DB.
  Where("status = ? AND task_type = ?", model.TaskStatusEnabled, model.TaskTypeStartup).
  Where("last_startup_auto_run_date IS NULL OR last_startup_auto_run_date <> ?", today).
  Find(&tasks)
```

---

## 场景：任务并发闸门与执行槽位

### 1. Scope / Trigger

- 触发：修改 `server/service/scheduler_v2.go` 的 worker 循环 / `executeTask()` / 槽位管理、`server/service/task_executor.go` 的 `OnTaskExecuting()` / `RunTask()`、`server/service/scheduler_manager.go` 的调度器初始化与关停，或改动随机延迟链路时必须看本节。
- 原因：`max_concurrent_tasks`（UI 文案「定时任务最大并发数」）与「不允许多实例」曾长期**完全失效**——worker 在 `OnTaskExecuting` 里 `go runTask(...)` 后立即返回，`defer removeRunningTask` 随即触发，闸门只覆盖了几乎瞬时的「派发」窗口。用户把并发数设为 1 也不起作用，且这个失效是静默的：没有任何日志或报错，只能靠读代码发现。

### 2. Signatures

- 并发闸门：`func (s *SchedulerV2) executeTask(req *ExecutionRequest)`（**必须阻塞到任务真正结束**）
- 槽位获取：`func (s *SchedulerV2) acquireRunningSlot(req *ExecutionRequest) (int64, bool)`
- 槽位释放：`func (s *SchedulerV2) removeRunningTask(taskID uint, goid int64)`
- 执行入口：`func (e *TaskExecutor) RunTask(req *ExecutionRequest)`（同步，含全部重试）
- 准备入口：`func (e *TaskExecutor) OnTaskExecuting(req *ExecutionRequest) error`（只准备，**不得阻塞到任务结束**）
- 延迟计算：`func (e *TaskExecutor) ResolveExecutionDelay(req *ExecutionRequest) time.Duration`（只算时长，**不得 sleep**）
- 延迟等待：`func (s *SchedulerV2) EnqueueDelayed(delay time.Duration, reqFunc func() *ExecutionRequest)`
- 关停两段：`func (s *SchedulerV2) SignalStop()` / `func (s *SchedulerV2) WaitWorkers(timeout time.Duration) bool`
- 并发数热生效：`func (s *SchedulerV2) SetWorkerCount(n int) (previous int, applied int)` / `func (s *SchedulerV2) GetWorkerCount() int` / `func ApplySchedulerWorkerCount()`
- 配置键：`max_concurrent_tasks` -> `SchedulerConfig.WorkerCount`（启动时）-> `SetWorkerCount()`（保存后热生效）
- 请求字段：`ExecutionRequest.DelayResolved bool`、包内 `taskLog *model.TaskLog` / `tinyLog *TinyLog`

### 3. Contracts

- **worker 即并发名额**：`WorkerCount = N` 必须等价于「任意时刻最多 N 个任务处于执行中」。`executeTask` 从取得槽位到返回，必须完整覆盖任务执行全过程（含 `MaxRetries` 重试与 `RetryInterval` 等待）。
- `SchedulerEventHandler` 的职责分离不可合并：`OnTaskExecuting` 只做依赖检查、解析命令、建立日志记录；真正执行必须在 `RunTask` 中同步完成。合并会让 `OnTaskStarted` 在任务结束后才被调用，语义倒置。
- **随机延迟不得占用槽位**：延迟必须在 worker 取得槽位**之前**完成，实现方式是 `EnqueueDelayed` 重新入队。若在槽位内 sleep，`max_concurrent_tasks=1` + 全局延迟会让串行总耗时被延迟放大数倍。
- `DelayResolved` 必须在调用 `ResolveExecutionDelay` **之前**无条件置为 true，保证一次请求只判定一次延迟，否则重新入队会无限循环。
- 「多实例检查 + 登记运行中」必须在**同一把写锁**内完成。分成 RLock 检查 + Lock 登记两段会产生 TOCTOU：两个 worker 可同时通过检查，把 `AllowMultipleInstances=false` 的任务跑成两份。
- **准备阶段必须有 `recover`**：`executeTask` 内 `OnTaskScheduled` / `OnTaskExecuting` / `OnTaskStarted` 的 panic 会打穿 worker goroutine，那个并发名额将永久消失。`runTask` 内部自带 recover，但覆盖不到它之外的阶段。
- **关停顺序固定**：`SignalStop()` → `StopAllRunningTasks()` → `WaitWorkers()` → `executor.Wait()`。先等 worker 再杀进程会让每次关机都白等满超时。
- 队列容量与并发数解耦。`Enqueue` 保持非阻塞 + 满时返回错误的语义，**不得**改成阻塞入队（会卡死 cron 线程）。
- 任何入队失败路径都必须保证任务状态不停留在 `queued` 假象上——包括 `EnqueueDelayed` 到期后重新入队失败这条新路径。
- **并发数改动必须热生效**：保存 `max_concurrent_tasks` 后立刻走 `reloadRuntimeConfigKeys` -> `ApplySchedulerWorkerCount()`，不得要求用户重启面板（重启会中断所有正在运行的任务）。
- **调小只能在两次任务之间收 worker**：退休判断必须放在 worker 取下一个请求**之前**，绝不能打断正在执行的任务。因此调小是最终一致的，测试必须轮询断言，不能用固定 sleep。
- **「超编就退休」必须在同一把锁内判断 + 减计数**：`desiredWorkers` / `liveWorkers` 刻意用普通 int + `workerLock`，不用 atomic。atomic 会让这个 check-then-act 出现多个 worker 同时判定自己该退出，最终退得比该退的多。
- **调小必须唤醒空闲 worker**：空闲 worker 阻塞在 `taskQueue` 上，不通过 `resizeCh` 叫醒就发现不了自己已经超编；队列长期空闲时调小会完全不生效。唤醒信号必须非阻塞发送，且 worker 回到循环顶部要重新判断（不能信任信号本身），这样并发数被连续改动时也能自愈。
- **开机任务的执行顺序只认 `sort_order`，与列表展示顺序完全解耦**：任务列表页的拖拽排序写的是另一列 `tasks.list_order`（见「场景：定时任务默认列表排序与置顶优先级」）。这是刻意的双向隔离 —— 用户拖列表不会静默打乱开机编排，反过来改 `sort_order` 也不会挪动列表位置。要改开机编排必须改 `sort_order`（任务表单 / `PUT /tasks/:id`），拖列表没有任何效果。

### 4. Validation & Error Matrix

- 同一任务已在执行 + `AllowMultipleInstances == false` -> `acquireRunningSlot` 返回 false，本次触发被丢弃，日志必须说明是**单实例规则**而非并发上限（并发上限只排队不丢弃，写错会误导用户排查方向）。
- `AllowMultipleInstances == true` -> 不受单实例限制，但仍受 `WorkerCount` 约束。
- `ResolveExecutionDelay > 0` 且 `DelayResolved == false` -> 交给 `EnqueueDelayed`，worker `continue` 取下一个请求，**不占槽位**。
- `DelayResolved == true` -> 直接执行，不再二次延迟。
- 延迟等待期间调度器关停 -> `EnqueueDelayed` 走 `stopCh` 分支直接放弃，由 `MarkActiveTasksInterrupted` 兜底结算。
- 延迟到期重新入队失败（队列满）-> 打日志 + 把仍停在 `queued` 的任务状态放回 `ResolveTaskInactiveStatus(task)`。
- 准备阶段 panic -> `recover` 后按 `OnTaskFailed` 结算，槽位由 `defer removeRunningTask` 正常归还。
- `handler == nil`（测试场景）-> `deferForExecutionDelay` 与 `executeTask` 都必须有判空保护，不得空指针。

### 5. Good/Base/Bad Cases

- Good：并发数设为 1 时开机任务严格串行，用户把前置任务排到 `sort_order` 第一位即可完成编排，不必把前置脚本复制进每个任务。
- Base：并发数设为 5（默认），6 个任务同时触发时 5 个执行、1 个排队，无任务丢失。
- Bad：`OnTaskExecuting` 里 `go runTask(...)` 后立即返回——闸门只覆盖派发窗口，`max_concurrent_tasks` 与「不允许多实例」双双失效，且**静默无报错**。
- Bad：把随机延迟的 `time.Sleep` 留在 worker 线程上——延迟直接吃掉并发名额。
- Bad：`WorkerCount` 只在 `InitSchedulerV2()` 读一次，改配置后必须重启面板才生效。用户升级后把并发数改成 1、保存成功却毫无效果，只会得出「还是没修好」的结论，而重启面板又会中断所有正在运行的任务。
- Bad：调小并发数时直接 kill 掉多余 worker，或让 worker 在执行任务途中退出——正在跑的任务会被无声打断。

### 6. Tests Required

见 `server/service/scheduler_v2_concurrency_test.go`，用假 handler 实现完整接口，不落库、不跑真实脚本：

- 并发数 1 -> 后一个任务的 `Start` 不早于前一个任务的 `End`，且峰值并发 == 1。
- 并发数 2 + 4 个任务 -> 峰值并发 `<= 2`，同时断言峰值**确实达到** 2（否则用例可能在空转）。
- `AllowMultipleInstances=false` 执行中重复触发 -> 第二次被拒绝，`startedCount == 1`。
- `AllowMultipleInstances=true` -> 可并行，峰值 == 2。
- 开机任务在并发数 1 下按 `sort_order` 升序执行，且区间不重叠。
  ⚠️ 这条用例自己会显式给每个任务设 `sort_order`，所以它**抓不到**「有人把列表拖拽接到 `sort_order` 上」这类回归 —— 那条防线在 `list_order` 那一节，不要以为这里绿了就安全。
- 执行中 `GetRunningCount() == 1`，结束后 == 0。
- 有延迟时 worker 不占槽位（延迟期间其他任务可正常执行）。
- 准备失败 / 准备阶段 panic -> 槽位都必须归还，且后续任务仍能被同一个 worker 执行。
- 延迟重新入队失败 -> 任务状态从 `queued` 回落。
- `ShutdownSchedulerV2` 在有任务运行时耗时远小于等待超时，且中断发生在等待 worker 之前。
- `SetWorkerCount` 调大 -> 排队中的任务立刻被新 worker 接走，峰值达到新上限。
- `SetWorkerCount` 调小 -> 收缩完成后峰值不超过新上限。
- `SetWorkerCount` 调小时队列全程空闲 -> `GetWorkerCount()` 仍必须降到目标值（这条专门防「空闲 worker 卡在 `<-taskQueue` 上永远发现不了自己该退休」）。
- `SetWorkerCount` 调小时有任务在执行 -> 该任务必须正常跑完。
- `SetWorkerCount(0)` / 负数 -> 钳到 1，调度器仍能执行任务。
- 修改后至少运行：

```bash
cd server
go test ./service -run "TestSchedulerV2|TestShutdownSchedulerV2|TestTaskExecutorResolveExecutionDelay|TestShouldApplyRandomDelayForTrigger" -count=3
go test ./...
```

> **突变验证**：这类闸门用例极易写成「永远为真」。把 `s.handler.RunTask(req)` 临时改成 `go s.handler.RunTask(req)`（等价于回到 bug 前的非阻塞行为），串行、并发上限、单实例、开机顺序、运行计数五条用例必须**确定性变红**；若不红，说明用例没有真正在检测闸门。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：派发完就返回，defer 立刻释放槽位。
// max_concurrent_tasks 和 AllowMultipleInstances 双双失效，且没有任何报错。
func (s *SchedulerV2) executeTask(req *ExecutionRequest) {
	s.addRunningTask(req.TaskID, goid)
	defer s.removeRunningTask(req.TaskID, goid)   // ← 任务还在跑就被摘掉
	s.handler.OnTaskExecuting(req)                 // ← 内部 go runTask(...) 后立即返回
}
```

```go
// 错误：随机延迟在 worker 线程上 sleep，直接空占一个并发名额。
if shouldApplyRandomDelayForTrigger(req.TriggerType) {
	time.Sleep(time.Duration(rand.Intn(randomDelay)+1) * time.Second)
}
```

```go
// 错误：两段锁之间存在 TOCTOU，两个 worker 可同时通过单实例检查。
if !s.checkConcurrency(req) { return }   // RLock
s.addRunningTask(req.TaskID, goid)       // Lock
```

#### Correct

```go
// 正确：槽位持有到任务真正结束；准备与执行分离；准备阶段 panic 不吃掉名额。
func (s *SchedulerV2) executeTask(req *ExecutionRequest) {
	goid, ok := s.acquireRunningSlot(req)   // 检查 + 登记在同一把写锁内
	if !ok {
		return
	}
	defer s.removeRunningTask(req.TaskID, goid)
	defer func() {
		if r := recover(); r != nil {
			s.handler.OnTaskFailed(req, fmt.Errorf("任务调度阶段异常: %v", r))
		}
	}()

	if err := s.handler.OnTaskExecuting(req); err != nil {   // 只准备
		s.handler.OnTaskFailed(req, err)
		return
	}
	s.handler.OnTaskStarted(req)
	s.handler.RunTask(req)                                    // 阻塞到结束
}
```

```go
// 正确：延迟在取槽位之前完成，worker 立刻去处理下一个请求。
if s.deferForExecutionDelay(req) {
	continue
}
s.executeTask(req)
```

---

## 场景：主动停止任务的 Aborted 独立状态

### 1. Scope / Trigger

- 触发：修改任务手动停止、批量停止、定时停止、CLI stop、任务执行完成结算、通知发送、`notify_on_abort` 字段、统计接口或前端终止状态展示时必须看本节。
- 原因：手动停止和定时停止通常是用户主动规划的终止，不应被当成脚本异常失败，也不应伪装成自然成功。必须用独立 `Aborted` 状态表达“任务被用户或计划主动终止”。

### 2. Signatures

- 停止标记：`func markManualStop(taskID uint)`
- 跨包停止标记：`func MarkManualStop(taskID uint)`
- 完成结算覆盖：`func applyManualStopOverride(taskID uint, runStatus, logStatus int) (finalRun int, finalLog int, aborted bool)`
- 单任务停止入口：`PUT /api/v1/tasks/:id/stop`
- 批量停止入口：`PUT /api/v1/tasks/batch` with `action="stop"`
- 定时停止入口：`func (s *SchedulerV2) stopTaskBySchedule(taskID uint)`
- CLI 停止入口：`ddp stop`
- 任务运行状态：`model.RunAborted`
- 日志状态：`model.LogStatusAborted`
- 任务字段：`Task.NotifyOnAbort bool`
- 数据库字段：`tasks.notify_on_abort BOOLEAN DEFAULT 0`
- 前端字段：`notify_on_abort`

### 3. Contracts

- 手动停止、批量停止、定时停止和 CLI stop 必须统一写入 `RunAborted` / `LogStatusAborted`。
- 主动停止命中停止标记后，任务完成结算必须覆盖为 Aborted，不能按退出码写失败。
- Aborted 不触发成功通知，也不触发失败通知；仅当 `notify_on_abort=true` 时发送终止通知。
- Aborted 必须单独统计，不能增加成功数或失败数；成功率只使用 `success / (success + failed)`。
- 停止标记必须在杀进程之前写入，避免任务完成 `defer` 先执行导致仍被结算成失败。
- 定时停止使用 PID 兜底杀进程时，也必须打停止标记，不能只 `KillProcessByPid`。
- 自然失败、依赖失败、脚本超时、面板异常退出导致的中断仍按失败处理，不得被误改成 Aborted。
- 新字段必须在 `database.EnsureColumns()` 中补列，保证老 SQLite 数据库升级后默认不发送终止通知。

### 4. Validation & Error Matrix

- 主动停止 / 批量停止 / 定时停止 / CLI stop -> 运行状态 Aborted、日志状态 Aborted。
- `notify_on_abort=false` -> 不发送成功 / 失败 / 终止通知。
- `notify_on_abort=true` -> 只发送「任务已终止」通知。
- 自然成功 + 未停止 -> 成功状态和成功通知保持原逻辑。
- 自然失败 + 未停止 -> 失败状态和失败通知保持原逻辑。
- 老库缺 `notify_on_abort` -> 启动时自动补列，默认 `0`。
- 测试或异常启动阶段 `GetTaskExecutor()==nil` -> 停止接口不得 panic，应继续走状态 / 日志兜底更新。

### 5. Good/Base/Bad Cases

- Good：长驻任务配置了定时停止，晚上到点被停止后显示「已终止」，不发送失败通知，不增加失败统计，仪表盘终止统计 +1。
- Base：用户手动点击停止，任务列表和日志列表显示「已终止」，成功率不受影响。
- Bad：定时停止只杀 PID 不打停止标记，任务执行完成时收到非 0 退出码，被误判为失败。
- Bad：把 Aborted 当成功写入统计，导致用户分不清自然完成和计划终止。

### 6. Tests Required

- `applyManualStopOverride`：
  - 命中主动停止标记时应强制返回 `RunAborted` / `LogStatusAborted`。
  - 标记读即清，重复调用不得继续覆盖状态。
  - 未打停止标记的自然失败不能被改成 Aborted。
- handler：
  - 创建任务时 `notify_on_abort` 能保存并回传。
  - `PUT /tasks/:id/stop` 把运行中日志改成 `LogStatusAborted`，任务 `last_run_status` 改成 `RunAborted`。
- scheduler：
  - 定时停止必须打停止标记，并把运行中日志兜底改成 `LogStatusAborted`。
- notification / stats：
  - Aborted 通知标题、正文、context 与成功 / 失败通知区分开。
  - Dashboard / stats 必须返回 aborted 独立统计，成功率不被 Aborted 拉低。
- 修改后至少运行：

```bash
cd server
go test ./service -run "TestApplyManualStopOverride|TestConsumeManualStop|TestManualStop|TestSchedulerV2|TestBuildTaskExecutionNotification" -count=1
go test ./handler -run "TestStopTaskMarksRunningLogAborted|TestCreateTaskPersistsNotifyOnAbortSwitch|TestSystemDashboardAndStatsReportAbortedSeparately" -count=1
go test ./...
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：定时停止只杀进程，不打停止标记，完成结算会把退出码当普通失败。
if task.PID != nil {
    KillProcessByPid(*task.PID)
}
```

```go
// 错误：命中停止标记后伪装成自然成功，统计上无法区分主动终止和真实成功。
if consumeManualStop(taskID) {
    return model.RunSuccess, model.LogStatusSuccess, true
}
```

#### Correct

```go
// 正确：杀进程前先打停止标记，完成结算时统一写入 Aborted。
markManualStop(taskID)
KillProcessByPid(*task.PID)
```

```go
// 正确：主动停止使用独立 Aborted 状态，通知和统计都走单独口径。
if !consumeManualStop(taskID) {
    return runStatus, logStatus, false
}
return model.RunAborted, model.LogStatusAborted, true
```

---

## 场景：任务自定义成功退出码

### 1. Scope / Trigger

- 触发：修改 `model.Task` 的退出码字段、`task_executor.go` / `scheduler.go` 的成功判断、任务日志结算、重试、通知，或任务创建/编辑/复制/导入导出时必须看本节。
- 原因：少量历史脚本会在业务完成后返回 `1`。面板需要允许任务显式兼容，但不能通过“结束”“完成”等日志文本猜测成功，更不能把所有退出码 `1` 全局放行。

### 2. Signatures

- 任务字段：`Task.SuccessExitCodes string`
- API / 导入导出字段：`success_exit_codes`，字符串，例如 `"0,1"`
- 数据库字段：`tasks.success_exit_codes VARCHAR(128) NOT NULL DEFAULT '0'`
- 规范化：`func NormalizeSuccessExitCodes(raw string) (string, error)`
- 运行判断：`func (t *Task) IsSuccessExitCode(exitCode int) bool`
- 前端入口：任务表单 -> 高级设置 -> 成功退出码

### 3. Contracts

- 默认值、空字符串、`null` 和旧数据都按 `0` 处理，现有任务升级后行为不能改变。
- 只接受 `0-255` 的整数；允许英文逗号、中文逗号或空白分隔，保存前统一为英文逗号并去重。
- 只有 `RunCommandWithPlan` 正常返回进程结果后，才允许调用 `IsSuccessExitCode`。
- 启动错误、执行器 panic、超时/信号负退出码不能被配置覆盖；主动停止继续由 `applyManualStopOverride` 结算为 Aborted。
- `TaskExecutor` 和旧 `Scheduler` 必须使用同一规则；重试、任务状态、日志状态、依赖任务、统计和通知不能分叉。
- `TaskExecutor` 的任务状态和日志状态都必须使用同一个 `success` 结果，禁止日志状态再次直接判断 `exitCode != 0`。
- 非零成功码仍保留真实退出码，日志尾部追加“已按任务配置判定成功”，便于用户区分标准成功和兼容成功。
- 新字段必须同步：model、`EnsureColumns()`、创建/更新 handler、`ToDict()`、复制、导入导出和任务表单。

### 4. Validation & Error Matrix

- 默认/空配置 + 退出码 `0` -> Success。
- 默认/空配置 + 退出码 `1` -> Failed，按原规则重试和通知。
- 配置 `0,1` + 退出码 `1` -> Success，不发送失败通知，日志保留退出码 `1` 和兼容说明。
- 配置 `0,1` + 退出码 `2` -> Failed。
- 任意配置 + 退出码 `-1`（超时或信号）-> Failed。
- 任意配置 + 进程启动错误 / 执行器 panic -> Failed，不能因为错误路径使用了内部值 `1` 而成功。
- 任意配置 + 手动停止 / 定时停止 -> Aborted，不进入成功或失败通知。
- 配置含文本、负数或大于 `255` -> API 返回参数错误，导入时跳过该任务并记录错误。

### 5. Good/Base/Bad Cases

- Good：确认某历史脚本业务完成后固定 `process.exit(1)`，仅给该任务配置 `0,1`；任务、日志和通知都显示成功，日志仍能看到真实退出码。
- Base：普通脚本不改设置，退出 `0` 成功、退出非零失败。
- Bad：看到日志最后有“结束”就全局把退出码 `1` 改成成功，真实异常也会被隐藏。
- Bad：任务状态按 `success` 写成功，但日志状态继续按 `exitCode != 0` 写失败，造成列表、统计和通知互相矛盾。

### 6. Tests Required

- model：空值默认 `0`；`0,1` 接受 `1`、拒绝 `2`；负数永不成功；非法文本和范围返回错误。
- database：模拟旧库缺列，`EnsureColumns()` 必须补出 `success_exit_codes`，已有任务默认回填 `0`。
- handler：创建、更新、复制、导出和导入能保存规范化字段；非法配置返回 `400` 或导入错误。
- executor：同一个真实退出码 `1` 在默认配置下任务/日志均失败，在 `0,1` 下任务/日志均成功，并保留退出码和兼容说明。
- notification：非零成功码必须生成 Success 标题/context，保留真实 `exit_code`，且失败原因字段为空。
- 修改后至少运行：

```bash
cd server
go test ./model ./handler ./service -run "TestNormalizeSuccessExitCodes|TestTaskIsSuccessExitCode|TestTaskSuccessExitCodes|TestTaskExecutorAppliesConfiguredSuccessExitCodes" -count=1
go test ./... -count=1
go vet ./...
cd ../web && npm run build
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：全局放行 1 会隐藏真正的脚本失败。
if result.ReturnCode == 0 || result.ReturnCode == 1 {
    success = true
}

// 错误：任务成功但日志仍按非零退出码写失败。
if exitCode != 0 {
    logStatus = model.LogStatusFailed
}
```

#### Correct

```go
// 正确：只有正常拿到进程结果后，才按当前任务显式配置判断。
if task.IsSuccessExitCode(result.ReturnCode) {
    success = true
}

// 正确：任务、日志、通知统一使用同一份 success 结果。
if !success {
    logStatus = model.LogStatusFailed
}
```

## 场景：反代 CORS 同源判断与外部端口

### 1. Scope / Trigger

- 触发：修改 `server/middleware/cors.go`、反代头解析、登录 403 / CORS 拦截相关逻辑时必须看本节。
- 原因：群晖、飞牛、Nginx Proxy Manager 等多层反代可能只把公网域名写入 `Host` / `X-Forwarded-Host`，却丢掉浏览器实际访问的外部端口。例如浏览器 `Origin=https://dd.example.com:5888`，后端只看到 `Host=dd.example.com`，如果直接比较完整 `host:port` 会误判跨域。

### 2. Contracts

- 公网域名不能默认全放开，仍必须满足以下任一条件：
  - 命中 `config.yaml` 的 `cors.origins`
  - `Origin` 域名与 `Host` / `X-Forwarded-Host` / `X-Original-Host` / RFC 7239 `Forwarded host=` 一致
  - 私有/Loopback IP 来源命中已有局域网放行逻辑
- 如果 `X-Forwarded-Port` 明确存在，必须与 `Origin` 端口一致；端口冲突时必须拒绝。
- 如果反代没有传 `X-Forwarded-Port`，但域名一致且候选 host 没有端口，可以按“反代丢失外部端口”兼容放行。
- 不允许为了修复 NAS 反代问题把 `Allow-Origin` 改成 `*`，因为登录接口携带认证能力，公网开放会扩大攻击面。

### 3. Tests Required

- `Origin=https://域名:端口` + `X-Forwarded-Host=同域名` + 无 `X-Forwarded-Port` -> 放行
- `Origin=https://域名:端口` + `X-Forwarded-Host=同域名` + `X-Forwarded-Port=同端口` -> 放行
- `Origin=https://域名:端口` + `X-Forwarded-Host=同域名` + `X-Forwarded-Port=不同端口` -> 拒绝
- `Origin=https://恶意域名:端口` + `X-Forwarded-Host=面板域名` -> 拒绝

---

## 评审检查清单

- 分层是否清晰，职责是否仍然合理？
- 是否引入了不必要的新抽象？
- 响应结构是否与现有接口风格一致？
- 数据库兼容和迁移是否考虑到了？
- 是否执行了 `go test ./...`？

---

## 场景：订阅 Git 仓库路径过滤

### 1. Scope / Trigger

- 触发：修改 `server/service/subscription.go` 里 Git 订阅拉取、`sub_path`、`whitelist`、`blacklist`、sparse checkout 相关逻辑时必须看本节。
- 原因：Git sparse-checkout 的 cone 模式会默认保留仓库根目录文件，不能满足“只拉指定子目录 / 白名单文件”的产品语义。

### 2. Signatures

- 入口：`pullGitRepoWithCallback(ctx context.Context, sub *model.Subscription, authCfg gitAuthConfig, emit PullCallback) (string, error)`
- 路径过滤构造：`buildSubscriptionSparseCheckoutPatterns(sub *model.Subscription) []string`
- sparse 应用：`applySparseCheckout(ctx context.Context, repoDir string, sub *model.Subscription, env []string, emit PullCallback) error`

### 3. Contracts

- `sub.SubPath`：逗号分隔，优先级最高，表示真实工作区只检出这些仓库路径。
- `sub.Whitelist`：未设置 `SubPath` 时参与真实检出范围；历史语义是“路径包含匹配”，实现时要尽量保持这个直觉。
- `sub.Blacklist`：参与 sparse 排除规则；只有黑名单时先包含全部，再通过 `!pattern` 排除。
- 首次 clone 有路径过滤时必须使用 `--no-checkout`，先设置 sparse 规则，再 `git checkout HEAD`。
- GitHub 等支持 partial clone 的远端可以加 `--filter=blob:none`，但不能依赖所有 Git 服务都支持；不支持时应退化为普通浅克隆。

### 4. Validation & Error Matrix

- `sparse-checkout init` 失败 → 返回 `sparse-checkout init 失败: %w`
- `sparse-checkout set` 失败 → 返回 `sparse-checkout set 失败: %w`
- 清空过滤后关闭 sparse 失败 → 返回 `关闭 sparse-checkout 失败: %w`
- `ctx` 取消 → 沿用拉库链路的 `拉取已停止`

### 5. Good/Base/Bad Cases

- Good：`SubPath="scripts/daily"` 后，`scripts/daily/keep.js` 存在，`root.js` 和 `scripts/other/skip.js` 不落盘。
- Base：未设置子目录 / 白名单 / 黑名单时，保持完整仓库检出。
- Bad：使用 `git sparse-checkout init --cone` 后只设置子目录，因为 cone 模式仍会保留根目录文件。

### 6. Tests Required

- 子目录回归：断言指定子目录文件存在，仓库根文件和其它目录文件不存在。
- 白名单回归：断言白名单命中文件存在，非白名单文件不存在。
- 清空过滤回归：已有 sparse 仓库清空配置后，应能恢复完整检出。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：先 clone 全部，再用 cone sparse，会短暂全量落盘且根目录文件仍会保留。
args := []string{"clone", "--depth", "1", remoteURL, destDir}
_ = exec.CommandContext(ctx, "git", args...)
_ = exec.CommandContext(ctx, "git", "sparse-checkout", "init", "--cone")
```

#### Correct

```go
// 正确：先不检出工作区，设置 no-cone sparse 规则后再 checkout。
args := []string{"clone", "--depth", "1", "--filter=blob:none", "--no-checkout", remoteURL, destDir}
_ = exec.CommandContext(ctx, "git", args...)
_ = exec.CommandContext(ctx, "git", "sparse-checkout", "init", "--no-cone")
_ = exec.CommandContext(ctx, "git", "sparse-checkout", "set", "--no-cone", "scripts/daily")
_ = exec.CommandContext(ctx, "git", "checkout", "HEAD")
```

---

## 场景：Node.js 依赖安装清单修复

### 1. Scope / Trigger

- 触发：修改 `server/handler/deps.go`、`server/service/dependency_auto_install.go`、`server/service/backup_runtime.go` 里 npm install / uninstall / reinstall / auto-install 相关逻辑时必须看本节。
- 原因：所有 Node.js 依赖共用 `data/deps/nodejs/package.json` 和 `package-lock.json`；多个 npm 进程并发写同一文件，或历史坏文件残留，都会导致 `npm ERR! code EJSONPARSE`。

### 2. Signatures

- 加锁：`LockNodePackageOperation() func()`
- 安装命令：`NewNpmInstallCommand(packageName string) (*exec.Cmd, error)`
- 卸载命令：`NewNpmUninstallCommand(packageName string, force bool) (*exec.Cmd, error)`
- 清单校验：`ensureNodePackageManifest(nodeDir string) error`

### 3. Contracts

- 所有 npm install / uninstall / force uninstall / backup restore reinstall / auto-install 都必须持有 `LockNodePackageOperation()` 返回的锁，直到 npm 进程结束。
- 执行 npm 前必须先校验 `data/deps/nodejs/package.json`。
- `package.json` 不存在时，写入最小合法清单：`private: true` 和 `dependencies: {}`。
- `package.json` 非法或 `dependencies` 不是对象时，先备份为 `package.json.broken-*`，再根据现有 `node_modules/*/package.json` 重建依赖清单。
- npm 环境必须保留代理和 npm 镜像：`NpmInstallEnv(AppendProxyEnv(...), CurrentNpmMirror())`。

### 4. Validation & Error Matrix

- 创建 Node.js 依赖目录失败 → `创建 Node.js 依赖目录失败: %w`
- 读取 package.json 失败 → `读取 Node.js package.json 失败: %w`
- 备份坏 package.json 失败 → `备份损坏的 Node.js package.json 失败: %w`
- 写入新 package.json 失败 → `写入 Node.js package.json 失败: %w`

### 5. Good/Base/Bad Cases

- Good：坏 `package.json` 末尾多 `}`，安装前自动备份并重建，之后 npm 可以继续安装。
- Base：无 `package.json`，自动创建合法最小清单。
- Bad：直接并发执行多个 `exec.Command("npm", "install", "--prefix", nodeDir, name)`，容易并发写坏 JSON。

### 6. Tests Required

- 损坏清单回归：写入非法 `package.json`，断言修复后 JSON 可解析、原文件被备份为 `package.json.broken-*`。
- 依赖保留回归：已有 `node_modules/axios/package.json` 时，重建后的 `dependencies` 包含 `axios` 版本。
- 命令链路回归：handler/service 中所有 Node.js npm 调用都必须通过 `NewNpmInstallCommand` / `NewNpmUninstallCommand`。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：多个 goroutine 可能同时写同一个 package.json，且坏 JSON 不会被修复。
cmd := exec.Command("npm", "install", "--prefix", filepath.Join(depsDir, "nodejs"), name)
out, err := cmd.CombinedOutput()
```

#### Correct

```go
// 正确：持锁到 npm 进程结束，并在命令创建前修复 package.json。
unlock := service.LockNodePackageOperation()
defer unlock()

cmd, err := service.NewNpmInstallCommand(name)
if err != nil {
    return err
}
out, err := cmd.CombinedOutput()
```

### 8. CommonJS 兼容版本映射

- `NewNpmInstallCommand(packageName)` 内部必须先走 `ResolveNodeInstallPackageSpec(packageName)`，不要直接把裸包名交给 `npm install`。
- 只有裸包名允许命中 CommonJS 兼容映射；用户显式写 `uuid@9.0.0`、`uuid@latest`、Git URL、本地路径、`file:` 等来源时必须保持原样。
- 安装前日志必须调用 `NodeInstallCompatibilityNotice(packageName)`：
  - 命中映射 -> 说明将安装的兼容版本，例如 `uuid@8.3.2`
  - 未命中映射 -> 明确提示“该包未在兼容映射中，将按 npm 默认版本安装。”
- 脚本运行日志命中 `ERR_REQUIRE_ESM` 时，`BuildModuleCompatibilityHint(output)` 应尝试从 `node_modules/<pkg>/...` 或 `require('<pkg>')` 解析包名；命中映射时给出重装旧版建议，未命中映射时提示手动指定兼容 `require()` 的旧版本。

---

## 场景：任务命令支持依赖可执行命令

### 1. Scope / Trigger

- 触发：修改 `server/service/script_runner.go`、`server/service/runtime_exec.go`、任务命令解析、`RunCommand()`、`ParseCommandExecutionPlan()` 或依赖命令执行相关逻辑时必须看本节。
- 原因：部分青龙生态工具（例如 `dailycheckin`）安装后暴露的是 Python/Node 依赖目录里的可执行命令，不一定是 `scripts/` 目录中的 `.py/.js/.sh` 文件。如果只按脚本路径校验，会误报“脚本不存在或命令格式无效”。

### 2. Signatures

- 命令解析入口：`ParseCommandExecutionPlan(command, scriptsDir string) (*CommandExecutionPlan, error)`
- 命令执行入口：`RunCommand(command, scriptsDir string, timeout int, envVars map[string]string, maxOutputBytes int, onOutput OnOutputFunc) (...)`
- 计划字段：
  - `CommandExecutionPlan.ManagedCommand string`
  - `CommandExecutionPlan.PythonModule string`
  - `CommandExecutionPlan.WorkDir string`
- 执行构造：
  - `createManagedExecutableCommand(commandName string, commandArgs []string, workDir string, envVars map[string]string)`
  - `createManagedPythonModuleCommand(interpreter string, moduleName string, moduleArgs []string, workDir string, envVars map[string]string)`

### 3. Contracts

- `dailycheckin --help` 这类裸命令允许作为托管依赖命令执行，但命令名只能包含字母、数字、`_`、`-`、`.`，不能包含路径分隔符、shell 元字符或以 `-` 开头。
- `task dailycheckin now`、`task dailycheckin -- --config config.json` 必须保留 `task` 模式语义和透传参数。
- `python3 -m dailycheckin --help` 必须作为 Python 模块命令执行，模块名只允许字母、数字、`_`、`.`，不能以 `.` 开头/结尾，也不能包含 `..`。
- 托管依赖命令的工作目录默认使用 `scriptsDir`；脚本文件任务继续使用脚本所在目录。
- 托管依赖命令仍要注入任务环境变量，并优先从面板托管 Python/Node bin 目录解析可执行文件，不应直接依赖系统 PATH。

### 4. Validation & Error Matrix

- 命令为空 -> `命令格式无效`
- `task` 后缺少脚本路径或依赖命令名 -> `命令格式无效，缺少脚本路径或依赖命令名`
- 依赖命令名包含非法字符或像文件路径 -> `脚本不存在或命令格式无效`
- `python -m` 缺少模块名 -> `python -m 命令缺少模块名`
- Python 模块名非法 -> `python 模块名无效: <name>`
- 托管 bin 目录找不到命令 -> 提示先安装对应 Python/Node 依赖，或改用脚本文件命令

### 5. Good/Base/Bad Cases

- Good：用户在依赖页安装 `dailycheckin` 后，任务命令填写 `dailycheckin --help` 可以直接运行。
- Base：传统脚本路径命令 `task demo.py now`、`node demo.js`、`bash demo.sh` 行为不变。
- Bad：把任意包含 `/`、`;`、`&`、`|` 的字符串当依赖命令执行，会绕过脚本路径安全校验并扩大命令注入风险。

### 6. Tests Required

- `TestParseCommandExecutionPlanSupportsManagedDependencyCommands`
  - 断言裸依赖命令、`task` 模式、透传参数、`python -m` 都能解析到正确字段。
- `TestRunCommandSupportsManagedDependencyCommand`
  - 断言依赖命令可以从托管 bin 目录运行，并能读取任务环境变量和参数。
- 回归验证：
  - `cd server && go test ./service -run "TestParseCommandExecutionPlanSupportsManagedDependencyCommands|TestRunCommandSupportsManagedDependencyCommand" -count=1`
  - `cd server && go test ./...`

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：找不到 scriptsDir 下的文件就直接失败，导致 dailycheckin 这类依赖命令无法运行。
fullPath, _, err := findTaskScriptTarget(tokens, scriptsDir, forcedMode)
if err != nil {
    return nil, err
}
```

#### Correct

```go
// 正确：脚本路径查找失败后，再判断是否是安全的托管依赖命令。
fullPath, _, err := findTaskScriptTarget(tokens, scriptsDir, forcedMode)
if err != nil {
    managedCommand, _, managedErr := findTaskManagedCommandTarget(tokens, forcedMode)
    if managedErr != nil {
        return nil, err
    }
    plan.ManagedCommand = managedCommand
    plan.WorkDir = scriptsDir
    return plan, nil
}
```

---

## 场景：脚本目录污染隔离与 Windows 资源监控

### 1. Scope / Trigger

- 触发：修改 `server/service/resource_monitor*.go`、`server/handler/script_file_ops.go`、`server/service/backup*.go`、`server/main.go` 里脚本目录扫描、备份恢复、资源监控或启动期清理逻辑时必须看本节。
- 原因：Windows 运行态如果只实现 Linux 资源采集，仪表板会长期显示 `0 B / 0 B`；脚本目录如果混入 `%SystemDrive%` 这类异常目录，会污染脚本管理、统计和备份恢复链路。

### 2. Signatures

- Windows 资源补齐：`fillWindowsResourceInfo(info *ResourceInfo)`
- 异常脚本判断：`ShouldIgnoreScriptEntryName(name string) bool`
- 绝对路径判断：`ShouldIgnoreScriptPath(scriptsDir, targetPath string) bool`
- 相对路径判断：`ShouldIgnoreScriptRelativePath(relPath string) bool`
- 启动期隔离：`QuarantineUnexpectedScriptEntriesOnStartup()`

### 3. Contracts

- `GetResourceInfo()` 在 Windows 下必须返回可用的 `memory_total`、`memory_used`、`disk_total`、`disk_used`，不能继续全量为 `0`。
- 启动时如果脚本目录顶层命中 `%SystemDrive%` 等异常目录，必须自动移动到 `data/quarantine/scripts/`，而不是继续暴露给脚本管理页。
- 脚本文件树、脚本统计、备份打包、备份恢复复制链路都必须复用同一套 `ShouldIgnoreScript*` 判断，避免有的地方隐藏、有的地方继续打包。
- 备份恢复遇到命中异常规则的脚本相对路径时必须跳过，不能把污染目录重新写回脚本根目录。
- 备份恢复写回脚本目录、日志目录、`panel.log` 这类 live 资源时，禁止“先清空 live，再逐步复制”；必须先把新内容完整写入同目录 staging 位置，确认成功后再原子切换到 live 目录/文件。

### 4. Validation & Error Matrix

- Windows 资源采集 API 调用失败 → 返回 0，但不能影响服务启动。
- 脚本目录扫描遇到异常目录 → 展示层/统计层跳过；启动期尝试隔离到 quarantine。
- quarantine 目标重名 → 追加 `.duplicate-N` 后缀，不能覆盖旧证据目录。
- 备份恢复中遇到 `%SystemDrive%/...` 相对路径 → 直接跳过，不报错中断整个恢复流程。
- 备份恢复 staging 构建失败 → 直接返回错误，live 目录/文件必须保持恢复前原样，不能出现“旧数据已删，新数据没写完”的半恢复状态。

### 5. Good/Base/Bad Cases

- Good：Windows 仪表板显示真实内存/磁盘占用；脚本页只显示正常脚本文件；异常 `%SystemDrive%` 目录被移到 `data/quarantine/scripts/%SystemDrive%`。
- Base：Linux 继续沿用 `/proc` 和 `df` 采集逻辑，不受 Windows 分支影响。
- Bad：只在前端隐藏 `%SystemDrive%`，但备份仍把异常目录继续打包；或只修仪表板展示，不修 `/api/system/info` 的 0 值来源；或恢复时先删 live 目录，复制中途失败后留下空目录/半目录。

### 6. Tests Required

- `TestShouldIgnoreScriptEntryName`
- `TestShouldIgnoreScriptPath`
- `TestShouldIgnoreScriptRelativePath`
- `TestQuarantineUnexpectedScriptEntriesOnStartup`
- `TestCreateBackupSkipsQuarantinedScriptEntriesInArchive`
- `TestRestoreScriptFilesKeepsLiveDataWhenStageCopyFails`
- `TestRestoreLogFilesKeepsLivePanelLogWhenStageCopyFails`
- `TestRestoreQingLongScriptsKeepsLiveDataWhenStageCopyFails`
- 回归验证：
  - `go test ./...`
  - Windows 运行态下 `/api/system/info` 不再返回 `memory_total=0 && disk_total=0`
  - 脚本管理页不再显示 `%SystemDrive%`

### 7. Wrong vs Correct

#### Wrong

```go
if runtime.GOOS == "linux" {
    info.MemoryTotal, info.MemoryUsed, info.MemoryFree = getLinuxMemory()
}
// Windows 下什么都不做，最终资源信息全是 0
```

```go
filepath.Walk(scriptsDir, func(path string, info os.FileInfo, err error) error {
    if err != nil || info.IsDir() {
        return nil
    }
    count++
    return nil
})
```

```go
// 错误：先清空 live 目录，再边拷贝边恢复；复制中途失败会把旧数据一起打掉。
_ = clearDirectoryContents(config.C.Data.ScriptsDir)
_ = copyDirectoryContents(sourceDir, config.C.Data.ScriptsDir)
```

#### Correct

```go
if runtime.GOOS == "windows" {
    fillWindowsResourceInfo(&info)
}
```

```go
filepath.Walk(scriptsDir, func(path string, info os.FileInfo, err error) error {
    if err != nil || info == nil {
        return nil
    }
    if info.IsDir() && service.ShouldIgnoreScriptPath(scriptsDir, path) {
        return filepath.SkipDir
    }
    if !info.IsDir() && service.ShouldIgnoreScriptPath(scriptsDir, path) {
        return nil
    }
    count++
    return nil
})
```

```go
// 正确：先把恢复结果写到 staging，成功后再切换 live 目录。
_ = restoreDirectoryWithStage(config.C.Data.ScriptsDir, func(stageDir string) error {
    return copyDirectoryContents(sourceDir, stageDir)
})
```

---

## 场景：备份恢复环境变量启用状态

### 1. Scope / Trigger

- 触发：修改 `server/service/backup_runtime.go`、`server/service/backup_types.go`、青龙备份转换或环境变量备份恢复逻辑时必须看本节。
- 原因：`model.EnvVar.Enabled` 是 `bool`，并带有 `gorm:"default:true"`。如果恢复时直接 `Create(&model.EnvVar{Enabled:false})`，GORM 会把 `false` 当成零值交给 SQLite 默认值，最终恢复成 `true`。

### 2. Signatures

- 备份字段：`BackupEnvVar.Enabled *bool json:"enabled,omitempty"`
- 导出转换：`backupEnvVarFromModel(item model.EnvVar) BackupEnvVar`
- 恢复转换：`modelEnvVarFromBackup(item BackupEnvVar) model.EnvVar`
- 恢复入口：`restoreEnvVars(tx *gorm.DB, envVars []BackupEnvVar) error`

### 3. Contracts

- 新备份必须明确写出环境变量 `enabled=true/false`，不能因为 `false` 是零值而丢字段。
- 恢复时如果 `enabled=false` 明确存在，最终数据库里的 `env_vars.enabled` 必须是 `false`。
- 老备份如果缺少 `enabled` 字段，必须按历史行为默认恢复为启用，避免把旧备份环境变量批量恢复成禁用。
- 青龙备份转换得到的环境变量也必须走同一套 `BackupEnvVar` 转换，不能直接把 `model.EnvVar` 塞进备份清单。

### 4. Validation & Error Matrix

- `enabled=false` 明确存在 -> 恢复后 `env_vars.enabled=false`
- `enabled=true` 明确存在 -> 恢复后 `env_vars.enabled=true`
- `enabled` 字段缺失 -> 恢复后 `env_vars.enabled=true`
- 恢复写入失败 -> 回滚本次备份恢复事务

### 5. Good/Base/Bad Cases

- Good：备份里一启用一禁用两个环境变量，恢复后状态完全一致。
- Base：旧备份没有 `enabled` 字段，恢复后变量保持启用，兼容老用户数据。
- Bad：恢复时直接 `tx.Create(&model.EnvVar{Enabled:false})`，结果被 SQLite 默认值覆盖成启用。

### 6. Tests Required

- `TestRestoreBackupManifestReplacesCoreBusinessData`：断言启用和禁用环境变量都按备份状态恢复。
- `TestRestoreBackupManifestDefaultsLegacyEnvEnabledWhenMissing`：断言老备份缺少 `enabled` 字段时默认启用。
- `TestCreateBackupIncludesSelectedContentInArchive`：断言导出的备份清单包含 `enabled=false`。
- 修改后至少运行：

```bash
cd server
go test ./service -run "TestRestoreBackupManifest|TestCreateBackup|TestBuildQingLongManifest" -count=1
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：Enabled=false 会被 GORM 当成零值，配合 default:true 后容易恢复成 true。
env := model.EnvVar{Name: item.Name, Value: item.Value, Enabled: false}
_ = tx.Create(&env).Error
```

#### Correct

```go
// 正确：用 *bool 区分字段缺失和明确 false，创建后对禁用状态做兜底写回。
env := modelEnvVarFromBackup(item)
shouldRestoreDisabled := !env.Enabled
if err := tx.Create(&env).Error; err != nil {
    return err
}
if shouldRestoreDisabled {
    return tx.Model(&model.EnvVar{}).Where("id = ?", env.ID).Update("enabled", false).Error
}
```

---

## 场景：备份恢复任务标签

### 1. Scope / Trigger

- 触发：修改 `server/service/backup_types.go`、`server/service/backup_runtime.go`、`server/service/backup_qinglong.go`，或往 `BackupPayload` / `BackupConfigBundle` 里**新增 / 调整任何直连或嵌入 `model.X` 的字段**时必须看本节。
- 原因：`model.Task.Labels` 打的是 `json:"-"`（它在数据库里存成逗号串，对外一律以数组形态下发）。备份清单以前直接 `Find(&manifest.Data.Tasks)` 拿 `[]model.Task` 序列化，标签整列被静默丢掉 —— 这就是 issue #112。同一个机制还丢了 `model.Subscription.AuthToken`。这类丢失**没有任何报错**：导出成功、恢复成功，只是数据少了一块，用户往往在恢复很久之后才发现。

### 2. Signatures

- 备份任务：`BackupTask` = 嵌入 `model.Task` + 备份专用的 `Labels []string`（json tag `labels`）
- 备份订阅：`BackupSubscription` = 嵌入 `model.Subscription` + 备份专用的 `AuthToken string`（json tag `auth_token`）
- 清单字段：`BackupPayload.Tasks []BackupTask` / `BackupPayload.Subscriptions []BackupSubscription`
- 订阅恢复：`restoreSubscriptions(...) (map[uint]uint, error)`（返回 **旧 ID -> 新 ID** 映射）
- 标签重映射：`remapSubscriptionLabels(...)`
- 护栏测试：`TestBackupPayloadModelsHaveNoJSONHiddenFields`

### 3. Contracts

- **核心不变量**：`BackupPayload` / `BackupConfigBundle` 里凡是直连或嵌入 `model.X` 的字段，`model.X` **不得存在未被外层覆盖的 `json:"-"` 字段**。一旦存在，就必须建一个 `Backup*` 包装结构，把该字段以显式 json tag 提出来。
- 包装结构建好后，**导出、恢复、青龙导入三处必须共用同一套转换**。只改其中一处是最常见的漏网：漏了 `backup_qinglong.go` 的 `manifest.Data.Tasks = tasks`，走青龙路径进来的任务照样静默丢标签。
- 外层字段层级更浅，会遮蔽被嵌入提升的同名字段；落进清单的必须是与 `/api/tasks/export` 和 `ToDict()` **一致的数组形态**，不能一个接口给逗号串、另一个给数组。
- 用**嵌入**而不是平铺列字段：平铺意味着 `model.Task` 每加一个字段都要手工跟一次，漏跟同样是静默丢数据。嵌入的已知代价（以后再加 `json:"-"` 字段又会静默丢）由上面那条护栏测试兜住。
- **不得为了绕开这条不变量去改 `model` 层的 tag**（例如把 `model.Task.Labels` 从 `json:"-"` 改成 `json:"labels"`）：同一个 key 会同时出现「逗号串」和「数组」两种形态，老 manifest 反序列化直接报错，而恢复是全事务的，一条读不出来整次恢复回滚。
- 老备份缺这些新键 -> 反序列化为零值 -> 必须等价于升级前的行为（`labels` 缺失 = 空标签，`auth_token` 缺失 = 空令牌），**不得报错**。
- `BackupManifest.Version` 全仓只写不读，**不得**引入按版本分叉的恢复逻辑。
- **恢复时 `subscription:<id>` 标签必须按 旧 ID -> 新 ID 重映射。** 恢复流程会给订阅重新分配主键（`item.ID = 0`），而 `deleteAll` 只 `DELETE FROM`、不重置 `sqlite_sequence`，驱动建的是 `integer PRIMARY KEY AUTOINCREMENT`，语义就是**不复用已删除的 ROWID**。把旧 ID 原样写回，标签就可能挂到**另一个不相干的订阅**头上。
- 重映射规则三条，缺一不可：
  1. 命中 old->new 映射 -> 改写成 `subscription:<新 ID>`；
  2. 没命中（订阅没被一起恢复 / 备份里就没有那个订阅）-> **丢弃该条 `subscription:` 标签**，绝不保留旧 ID；
  3. 非 `subscription:` 前缀的标签（含 `分组:` 与用户自定义）**一律原样保留**。
- 重映射**不得复用** `handler/task_labels.go` 的 `sanitizeIncomingLabels`：那是给用户输入用的，会把 `subscription:` 前缀整条洗掉。
- 订阅恢复排在任务恢复之后时，采用**后置 pass**（订阅恢复完再回头改任务标签，照同文件 `pendingDepends` 的写法），不要为此调整既有恢复顺序。

### 4. Validation & Error Matrix

- 任务有标签 -> 导出的 manifest 里 `tasks[].labels` 是数组，恢复后 `tasks.labels` 逐字一致
- 老备份没有 `labels` 键 -> 恢复后标签为空，不报错（等价于升级前行为）
- 订阅有 `AuthToken` -> 导出带上，恢复后可继续拉私有仓库
- 老备份没有 `auth_token` 键 -> 恢复后为空串，不报错
- 标签 `subscription:7`，7 号订阅本次一起恢复并拿到新 ID 12 -> 标签改写成 `subscription:12`
- 标签 `subscription:7`，但本次只勾了任务没勾订阅（映射为空）-> 该标签被丢弃，同一任务上的其它标签原样保留
- 往 `BackupPayload` / `BackupConfigBundle` 新加一个含未覆盖 `json:"-"` 字段的 `model.X` -> `TestBackupPayloadModelsHaveNoJSONHiddenFields` 必须变红
- 恢复过程中任一步失败 -> 整个事务回滚，不留半套数据

### 5. Good/Base/Bad Cases

- Good：备份 -> 清库 -> 恢复，任务的分组标签、用户自定义标签、订阅归属标签全部还原，订阅也能继续拉取。
- Base：加这两个字段之前导出的老备份包，恢复后任务无标签、订阅无令牌，与升级前行为逐字一致，不报错。
- Bad：`Find(&manifest.Data.Tasks)` 直接把 `[]model.Task` 序列化进清单 —— 导出、恢复都「成功」，标签静默消失，备份页文案还写着「包含标签」。
- Bad：恢复时把 `subscription:<旧 ID>` 原样写回。旧 ID 很可能已经属于另一个订阅，而订阅同步的 autoDelete 分支会对「认领到但不在候选集里」的任务执行 `RemoveJob` + 删 task_logs + `Delete(&task)` —— **物理删除**。修 bug 反而比原 bug 严重得多。
- Bad：只改导出侧，忘了青龙导入那条路径，一半用户的标签照丢。
- Bad：把 `AuthToken` 带进备份却不提醒用户 —— PAT 会以**明文**进入备份包，发布说明与 issue 回复必须写明「别把备份文件外发」。

### 6. Tests Required

- `TestBackupPayloadModelsHaveNoJSONHiddenFields`：用 reflect 遍历 `BackupPayload` / `BackupConfigBundle` 里所有直连或嵌入的 `model.X`，发现 `json:"-"` 字段而外层没有同名覆盖字段就 fail。这是**唯一**能挡住「以后再加字段又静默丢」的护栏，不得删、不得改成只检查已知字段的白名单。
- 导出用例：断言 manifest 里 `tasks[].labels`、`subscriptions[].auth_token` 存在且正确。
- 恢复用例：断言标签与令牌逐字还原；断言老格式（无这两个键）恢复后不报错且落到零值。
- 重映射用例：订阅 ID 变化后标签变成 `subscription:<新 ID>`；映射未命中时该标签被丢弃、同任务其它标签保留。
- 青龙导入用例：断言走 `backup_qinglong.go` 进来的任务同样带上标签。
- 修改后至少运行：

```bash
cd server
go test ./service -run "TestBackupPayload|TestRestoreBackupManifest|TestCreateBackup|TestBuildQingLongManifest" -count=1
go test ./...
```

---

## 场景：默认 Python 版本与不可用运行时兜底

### 1. Scope / Trigger

- 触发：修改 `server/service/python_runtime.go`、`server/handler/deps.go`、`web/src/views/deps/index.vue` 时必须看本节。
- 原因：系统默认 Python 版本可能配置成 `3.12`，但当前机器真实可用的是 `3.10/3.11`。如果前端直接拿默认版本作为展示版本，会出现页面默认查询不可用解释器、列表空白甚至接口报错。

### 2. Signatures

- 后端默认版本：`DefaultPythonVersion() string`
- 后端运行时列表：`PythonRuntimeInfos() []PythonRuntimeInfo`
- 前端展示版本选择：`resolveDisplayPythonVersion(runtimes, defaultVersion)`

### 3. Contracts

- 后端 `default_version` 继续返回系统真实默认值，不能因为当前机器暂时没装对应解释器就偷偷改配置。
- 前端“当前展示的 Python 版本”和“系统默认 Python 版本”允许不同：
  - 默认版本可用 → 直接展示默认版本
  - 默认版本不可用 → 自动切到第一个可用版本
- 页面必须明确提示用户：当前展示版本与系统默认版本分别是什么。

### 4. Validation & Error Matrix

- 默认版本可用 → `pythonVersion === pythonDefaultVersion`
- 默认版本不可用但存在其它可用版本 → 自动切到首个 `available=true` 的版本
- 所有版本都不可用 → 回退到默认版本或首个候选版本，但页面必须展示“需先安装”

### 5. Good/Base/Bad Cases

- Good：默认 `3.12` 不可用、`3.10` 可用时，页面自动展示 `3.10` 列表，同时说明“系统默认版本仍是 3.12”。
- Base：默认 `3.11` 可用时，页面继续展示 `3.11`。
- Bad：默认 `3.12` 不可用时，页面仍强行请求 `3.12`，导致空白或报错。

### 6. Tests Required

- 前端构建：`cd web && npm run build`
- 后端测试：`cd server && go test ./...`
- 运行态验收：依赖页在默认版本不可用时仍能打开并自动展示可用版本列表

### 7. Wrong vs Correct

#### Wrong

```ts
pythonDefaultVersion.value = res.default_version || "3.12"
pythonVersion.value = pythonVersion.value || pythonDefaultVersion.value
```

#### Correct

```ts
pythonDefaultVersion.value = res.default_version || "3.12"
pythonVersion.value = resolveDisplayPythonVersion(
  pythonRuntimes.value,
  pythonDefaultVersion.value,
)
```

---

## 场景：Docker 镜像档位、Python 运行时与更新托管

### 1. Scope / Trigger

- 触发：修改 Dockerfile、Python 安装脚本、发布矩阵、Compose / Watchtower 配置、面板更新 API / CLI，或任务 Python 版本选择逻辑时必须看本节。
- 原因：镜像标签同时决定基础系统、Python 小版本、工具档位和更新方式。任一层漂移都会造成标签与实际内容不符、重复 Python、旧标签断更，或页面显示更新成功但容器没有被选中。

### 2. Signatures

- 构建参数：`PYTHON_RUNTIME_MODE=single|all`
- 构建参数：`PYTHON_RUNTIME_VERSION=3.10|3.11|3.12`
- 工具档位：`INSTALL_FULL_TOOLS=true|false`
- 运行时环境：`DAIDAI_PYTHON_RUNTIME_MODE`、`DAIDAI_PYTHON_VERSION`
- Compose 环境：`DAIDAI_PANEL_IMAGE` -> `image` + `IMAGE_NAME`
- 更新管理环境：`PANEL_UPDATE_MANAGER=watchtower`、`WATCHTOWER_HTTP_API_URL`、`WATCHTOWER_HTTP_API_TOKEN`
- Watchtower 调用：`POST /v1/update?async=true&container=<锚定且转义的容器名>`
- 更新状态：`idle|running|restarting|completed|failed`，Watchtower 接管终态为 `completed / watchtower-triggered`
- 当前镜像版本列表：`CurrentPythonRuntimeVersions() []string`
- 单版本识别：`SinglePythonRuntimeVersion() (string, bool)`
- 启动策略修正：`ApplySinglePythonRuntimePolicyOnStartup()`
- 启动目录清理：`CleanupManagedPythonArtifactsOnStartup()`

### 3. Contracts

- 正式浮动标签固定为 10 个：`latest`、`latest-full`、`latest-3.10`、`latest-3.11`、`latest-all`、`debian`、`debian-full`、`debian-3.10`、`debian-3.11`、`debian-all`。
- `latest` / `debian` 默认使用 `PYTHON_RUNTIME_MODE=single` 和 `PYTHON_RUNTIME_VERSION=3.12`，只内置 Python `3.12`。
- `latest-3.10`、`latest-3.11`、`debian-3.10`、`debian-3.11` 分别只内置对应 Python 小版本；`latest-all`、`debian-all` 同时安装 `3.10 / 3.11 / 3.12`。
- `latest-full` 与 `debian-full` 仍只保留一套 Python 3.12；`INSTALL_FULL_TOOLS=true` 只增加 Go、Docker CLI、wget 和原生编译工具，不能重新引入发行版 Python。
- Alpine 的 `latest-3.10`、`latest-3.11`、`latest-all` 只发布 `amd64 / arm64`；如果某个平台没有 python-build-standalone 资产，脚本必须失败而不是回退成错误版本。默认 `latest` / `latest-full` 可以在 32 位平台使用经过小版本校验的发行版 Python 3.12。
- Debian 所有变体和 Alpine 64 位变体只使用 `/opt/daidai-python` 独立运行时；all 镜像只能有三套目标 Python，不能再附带系统 Python。
- 六个旧无连字符浮动标签和 Debian 旧固定版本格式必须与新标签由同一个 build-push 矩阵项推送，不能增加重复构建任务。
- 精简镜像的页面手动更新、静默自动更新和 `ddp update` 统一调用 Watchtower HTTP API；请求必须使用 `async=true` 并精确限定当前容器，202 纯文本响应也属于“已接管”成功态。
- Watchtower 只能刷新容器当前镜像引用。官方固定版本标签和 digest 必须禁用一键/自动更新并提示切换到同族浮动标签，不能把“请求已接管”误报成能够跨版本升级。
- Compose 的实际 `image` 与容器内 `IMAGE_NAME` 必须共用 `DAIDAI_PANEL_IMAGE`；Watchtower API 地址使用稳定服务名 `http://watchtower:8080`。
- 单版本镜像启动后，后端 `SupportedPythonVersions()` / 依赖安装版本 / 任务表单选项必须只暴露当前镜像小版本。
- 单版本镜像启动后，必须把 `python_default_version` 和历史任务 `python_version` 切回当前镜像小版本；默认 `latest` / `debian` 即 `3.12`。
- 单版本镜像启动清理只能删除 `data/deps/python/<不支持版本>` 这类面板托管 Python 小版本目录，不能删除脚本、日志、备份、Node.js 依赖或未知目录。
- `all` 镜像不得清理 `3.10 / 3.11 / 3.12` 任意一个托管目录。

### 4. Validation & Error Matrix

- `PYTHON_RUNTIME_MODE` 不是 `single|all` -> 构建失败，不得回退到 single。
- `PYTHON_RUNTIME_VERSION` 不是 `3.10|3.11|3.12` -> 构建失败，不得回退到 3.12。
- 独立 Python patch 版本、pip 或 venv 校验失败 -> 构建失败，不得发布该标签。
- 64 位或 Debian 镜像检测到系统 `python3` -> 构建失败，防止双 Python 回归。
- 精简镜像检测到 Go、gofmt、Docker CLI、wget 或编译链 -> 构建失败。
- Watchtower 缺 URL / token -> 禁用手动触发并给出缺失配置提示；定时轮询状态仍可展示。
- 官方固定版本标签或 digest 使用 Watchtower -> 禁用一键 / 自动更新，提示切换到同族浮动标签。
- 旧 Docker Socket 链目标不是 `latest-full|debian-full` -> 拉取前失败并提示改用 Watchtower 或 full 镜像。
- Watchtower 返回 202 纯文本 `Accepted` -> 记录为“已接管”，不能宣称镜像已完成更新；4xx / 5xx -> 进入 failed 并允许重试。

### 5. Good/Base/Bad Cases

- Good：`debian-full` 只有独立 Python 3.12，同时具备 Go、gofmt、Docker CLI、wget 和编译链；页面触发后显示 Watchtower 已接管。
- Base：`latest` 在 amd64 只有独立 Python 3.12 和精简工具；Alpine 386 / arm/v7 使用经过版本校验的单套系统 Python 3.12。
- Bad：给 `debian-all` 再安装系统 Python，或把 `debian-full` 更新目标静默改成 `latest`。
- Bad：Watchtower 请求同时使用容器名和可能过期的 `IMAGE_NAME` 过滤，收到 202 后实际零匹配。

### 6. Tests Required

- `SupportedPythonVersions()` 在 `DAIDAI_PYTHON_RUNTIME_MODE=single` 时只返回当前版本。
- `CleanupManagedPythonArtifactsOnStartup()` 在 single `3.12` 时删除 `3.10 / 3.11` 目录并保留 `3.12`。
- `CleanupManagedPythonArtifactsOnStartup()` 在 `all` 时保留三个版本目录。
- `ApplySinglePythonRuntimePolicyOnStartup()` 必须把旧默认版本和旧任务版本切回镜像版本。
- Python 依赖创建在 single 镜像里只创建当前小版本依赖记录。
- 发布 workflow 必须逐 job 验证 10 个正式标签、6 个旧浮动别名、3 个 Debian 旧固定别名及同一次 build-push 输出。
- `latest-full`、`debian-full` 的 Docker Socket 兼容边界，以及精简 / 自定义固定标签的拒绝行为必须有表格测试。
- Watchtower 请求必须断言只有 `async=true` 和锚定容器过滤；故意设置 stale `IMAGE_NAME` 时不得生成 image 过滤。
- 前端必须识别 `completed`，停止轮询、解除 loading，并允许关闭“Watchtower 已接管”弹窗。
- 三份 Compose 必须通过 `docker compose config`，且展开后 `image == IMAGE_NAME`、面板 / Watchtower token 一致、Socket 只挂给 Watchtower。
- 修改后至少运行：

```bash
cd server
go test ./service -run "TestSupportedPythonVersions|TestCleanupManagedPythonArtifactsOnStartup|TestApplySinglePythonRuntimePolicy" -count=1
go test ./handler -run "TestPythonDependencyCreate" -count=1
```

### 7. Wrong vs Correct

#### Wrong

```text
full_tools=true -> 安装系统 Python + 独立 Python
Watchtower -> /v1/update?image=<可能过期的 IMAGE_NAME>
固定标签 2.4.0-debian-full -> 显示可自动升级到后续版本
```

#### Correct

```text
full_tools=true -> 只增加开发工具，Python 仍只有目标运行时
Watchtower -> /v1/update?async=true&container=^daidai-panel$
固定标签 / digest -> 明确提示先切换到 debian-full 等同族浮动标签
```

---

## 场景：auto_update_last_checked_at 配置键注册

### 1. Scope / Trigger

- 触发：修改系统设置概览、更新检查时间展示、`configApi.get('auto_update_last_checked_at')` 相关逻辑时必须看本节。
- 原因：前端会读取 `auto_update_last_checked_at`。如果后端未把它注册成正式配置键，`/api/configs/auto_update_last_checked_at` 会返回 404，页面虽然能兜底，但运行态会持续报错。

### 2. Signatures

- 前端读取：`configApi.get('auto_update_last_checked_at')`
- 后端注册：`newTrimmedStringConfig("auto_update_last_checked_at", "上次检查更新时间", "", "...", "network")`
  （参数顺序：`key, label, defaultValue, description, group`）

### 3. Contracts

- 只要前端直接读取某个系统配置键，这个键就必须在 `registeredSystemConfigSpecs` 中注册。
- 该键允许为空字符串，表示“从未检查”。

### 4. Validation & Error Matrix

- 配置未写入数据库但已注册 → `GET /configs/:key` 返回默认值结构，不能再 404
- 配置已写入 → 返回实际保存值

### 5. Good/Base/Bad Cases

- Good：系统设置概览首次进入时显示“从未检查”，控制台和网络都不报错。
- Base：用户点过检查更新后，页面能显示最后检查时间。
- Bad：前端直接请求一个未注册配置键，导致 404。

### 6. Tests Required

- 后端测试：`cd server && go test ./...`
- 浏览器验收：系统设置概览不再触发 `/api/configs/auto_update_last_checked_at` 404

### 7. Wrong vs Correct

#### Wrong

```go
newBoolConfig("auto_update_enabled", "静默更新", "false", "...", "network")
// 忘记注册 auto_update_last_checked_at
```

#### Correct

```go
newBoolConfig("auto_update_enabled", "静默更新", "false", "...", "network")
newTrimmedStringConfig("auto_update_last_checked_at", "上次检查更新时间", "", "上次自动检查更新时间", "network")
```

---

## 场景：Windows 发布产物与源码一致性

### 1. Scope / Trigger

- 触发：修改 Windows 打包、`server/*.exe`、README Windows 发布说明、release workflow 时必须看本节。
- 原因：仓库源码目录如果长期保留手工构建或调试阶段的 `server/daidai-panel.exe`，很容易和当前源码脱节，导致“源码已修复，但本地 exe 仍是旧行为”。

### 2. Signatures

- GitHub Release Windows 构建：`.github/workflows/release.yml`
- Windows 正式产物名：`daidai-server.exe`
- 仓库开发态忽略：`.gitignore` 中应忽略 `server/daidai-panel.exe`、`server/ddp.exe`

### 3. Contracts

- 仓库源码目录中的本地 Windows 可执行文件不作为可信发布产物。
- 正式 Windows 发布包必须以 release workflow 使用 `-ldflags "-X daidai-panel/handler.Version=..."` 产出的 zip 为准。
- 本地开发产生的 `server/daidai-panel.exe`、`server/ddp.exe` 必须被 `.gitignore` 忽略，避免把旧二进制误提交到仓库。

### 4. Validation & Error Matrix

- 源码 `handler.Version` 已更新，但本地 exe 行为仍是旧接口 / 旧版本 → 优先检查是否误用了仓库里旧 exe，而不是当前源码构建产物。
- 工作树中出现 `server/daidai-panel.exe` 脏改动 → 视为发布一致性风险，不要混进功能提交。

### 5. Good/Base/Bad Cases

- Good：发布前用 workflow 或等价命令重新构建 Windows zip，并验证 `/api/system/version` 与源码版本一致。
- Base：开发阶段允许本地临时 exe 存在，但必须被 git 忽略。
- Bad：直接把仓库中历史遗留的 exe 当作正式发布产物发给用户。

### 6. Tests Required

- `go test ./...`
- Windows 产物启动后 `/api/system/version` 返回与本次源码一致的版本号
- `git status` 不应包含本地 exe 脏改动

### 7. Wrong vs Correct

#### Wrong

```text
源码修完后直接使用仓库里已有的 server/daidai-panel.exe 做验收或发版
```

#### Correct

```text
源码修完后重新构建 Windows 发布产物，验收时优先使用当前源码编译出的二进制或 GitHub Release workflow 产物
```

---

## 场景：Magisk / APatch 模块版 Python 版本对齐

### 1. Scope / Trigger

- 触发：修改 `server/service/python_runtime.go`、`server/service/runtime_exec.go`、`Magisk/service.sh`、模块运行时自检脚本时必须看本节。
- 原因：模块版当前通常只有一个容器内 `python3`，不保证真的同时存在 3.10 / 3.11 / 3.12 三套解释器。`v2.2.19` 起如果仍把默认 Python 版本硬绑到 `3.12`，老任务会统一报“Python 3.12 不可用”。

### 2. Signatures

- 模块运行态判断：`service.IsMagiskModuleRuntime() bool`
- 默认版本决策：`DefaultPythonVersion() string`
- 任务环境决策：`ResolvePythonVersionFromEnv(envVars map[string]string) string`
- 模块容器启动脚本：`Magisk/service.sh`

### 3. Contracts

- 模块版运行态下，默认 Python 版本必须优先跟随容器里真实 `python3` 小版本。
- 若任务 / 配置里保存的是 `3.12`，但模块当前真实 `python3` 是 `3.11`，且系统里也不存在额外 `python3.12`，运行时必须自动回退到 `3.11`。
- `Magisk/service.sh` 创建托管 venv 时，目录名必须使用真实 `python3` 小版本，不能硬编码 `deps/python/3.12`。
- Docker / Windows / 普通 Linux 多版本环境继续沿用原有多版本逻辑，不受模块版兼容分支影响。

### 4. Validation & Error Matrix

- 模块版 + 系统 `python3` 为 3.11 + 配置默认值为 3.12 -> 最终任务运行版本应回退到 3.11
- 模块版 + 系统里确实存在 `python3.12` -> 可以继续使用 3.12
- 非模块版 -> 不允许因为当前 `python3` 是 3.11 就偷偷改掉用户显式指定的 3.12

### 5. Good/Base/Bad Cases

- Good：用户从 `v2.2.10` 升级到 `v2.2.19+` 后，历史 Python 任务在 APatch / Magisk 设备上继续可跑，不因默认版本固定成 3.12 全挂。
- Base：模块版只有一个 `python3` 时，面板至少能稳定对齐到这个实际版本。
- Bad：容器里实际 `python3` 是 3.11，但 `service.sh` 仍创建 `deps/python/3.12`，后端再按严格版本校验把它判成不可用。

### 6. Tests Required

- 后端测试：`cd server && go test ./...`
- 回归点：
  - `TestDefaultPythonVersionFallsBackToActiveSystemPythonOnMagiskRuntime`
  - `TestResolvePythonVersionFromEnvFallsBackToActiveSystemPythonOnMagiskRuntime`
  - `TestMagiskServiceScriptExportsAndroidRuntimeEnv`

### 7. Wrong vs Correct
#### Wrong
```go
const defaultPythonRuntimeVersion = "3.12"
return defaultPythonRuntimeVersion
```

```sh
python3 -m venv "$DAIDAI_DIR/deps/python/3.12"
```

#### Correct
```go
return resolveEffectivePythonVersionForCurrentRuntime(version)
```

```sh
PY_MINOR=$(python3 -c "import sys; print(f'{sys.version_info.major}.{sys.version_info.minor}')")
python3 -m venv "$DAIDAI_DIR/deps/python/$PY_MINOR"
```

---

## 场景：面板全局时区配置

### 1. Scope / Trigger

- 触发：修改面板日志时间、任务调度日期判断、任务运行环境、系统设置配置项或 Linux 二进制发行包启动行为时必须看本节。
- 原因：裸 Linux 二进制运行环境可能没有 `TZ`，`/etc/localtime` 也可能缺失或指向 UTC。只改某一处时间格式不能解决问题，必须统一处理 Go 进程本地时区和脚本子进程 `TZ`。

### 2. Signatures

- 配置键：`model.PanelTimezoneConfigKey = "timezone"`
- 默认值：`model.DefaultPanelTimezone = "Asia/Shanghai"`
- 后端应用：`service.ApplyPanelTimezone(value string) error`
- 启动应用：`service.ApplyRegisteredPanelTimezone() error`
- 当前运行时读取：`service.CurrentPanelTimezone() string`
- 任务环境构造：`service.BuildManagedRuntimeEnvMapForPythonVersion(...)`
- 前端字段：`SettingsConfigForm.timezone`

### 3. Contracts

- `timezone` 必须注册到系统配置表，默认值为 `Asia/Shanghai`。
- 后端必须使用 `time.LoadLocation` 校验时区名，并内嵌 Go `time/tzdata`，不能依赖宿主机一定安装 tzdata。
- 面板启动时必须在 `model.InitDefaultConfigs()` 之后调用 `ApplyRegisteredPanelTimezone()`，因为要读取默认配置或用户已保存配置。
- 应用时区必须同时设置：
  - `time.Local`
  - 进程环境变量 `TZ`
  - 内部当前面板时区缓存
- 保存 `timezone` 配置后必须立即重载运行时，不要求用户重启面板。
- 任务运行环境必须写入 `envMap["TZ"] = CurrentPanelTimezone()`，并覆盖用户普通环境变量里同名 `TZ`，保证面板日志和脚本时间一致。
- Windows Python 的 CRT 不能正确解析 `Asia/Shanghai` 等 IANA 名称。只有 Python 脚本和 Python 模块的**启动环境**需要按任务启动时的当前偏移转换为 POSIX 固定偏移；Node.js 和其他运行时继续使用 IANA 名称。
- Windows CRT 的 POSIX 时区缩写必须是三个 ASCII 字母，且偏移符号与 UTC 偏移相反，例如 UTC+8 写成 `CST-8`、UTC-4 写成 `EDT4`、UTC+5:30 写成 `IST-5:30`。无法生成三字母缩写时使用稳定的 `DDT`。
- Windows Python 会延迟解析 `TZ`。bootstrap 必须在 env.json 恢复 IANA 名称前调用一次 `time.localtime()` 初始化本地时区；脚本最终读取 `os.environ["TZ"]` 时仍应得到用户设置的 IANA 名称。
- 有夏令时的地区在每次 Python 任务启动时重新计算当前偏移。长驻 Python 任务跨越夏令时切换点后需要重启任务，才能使用新偏移。
- 前端系统设置页必须提供可见入口，并把 `timezone` 纳入同一组保存键。

### 4. Validation & Error Matrix

- `timezone` 缺失或空值 -> 使用 `Asia/Shanghai`
- `timezone=Asia/Tokyo` / `UTC` -> 保存成功，并立即影响 `time.Local` 和后续任务 `TZ`
- `timezone=Bad/Zone` -> 保存失败，返回用户可读错误
- `timezone=Local` -> 保存失败，要求填写明确 IANA 时区，避免不同宿主环境表现不一致
- 用户环境变量表里也配置了 `TZ=UTC` -> 任务最终仍使用面板全局时区
- Windows Python + `Asia/Shanghai` -> 启动环境使用 `CST-8`，脚本本地时间为 `+08:00`，脚本读取 `TZ` 仍为 `Asia/Shanghai`
- Windows Node.js + `Asia/Shanghai` -> 启动环境保持 IANA 名称，本地时间仍为 `GMT+0800`
- Linux / Docker / 面具版 Python -> 启动环境保持 IANA 名称，不转换为固定偏移

### 5. Good/Base/Bad Cases

- Good：Linux tar 包直接启动，宿主机没有设置 `TZ`，面板仍按 `Asia/Shanghai` 写日志，脚本也拿到 `TZ=Asia/Shanghai`。
- Good：Windows Python 以 `CST-8` 初始化本地时间后，bootstrap 把脚本可见的 `TZ` 恢复为 `Asia/Shanghai`，时间和配置语义同时正确。
- Base：Docker 用户原本设置 `TZ=Asia/Shanghai`，升级后系统设置同样默认 `Asia/Shanghai`，行为不变。
- Bad：只在前端显示时加 8 小时，后端日志和任务脚本仍按 UTC 运行，定时任务日期判断继续错。
- Bad：把所有运行时的 `TZ` 都改成 `CST-8`；Windows Node.js 会把它解释成 UTC，Linux 也会失去 IANA 夏令时规则。
- Bad：Windows Python 启动后立刻恢复 `Asia/Shanghai`，却没有先调用 `time.localtime()`；Python 第一次取本地时间时仍会错误解析成 `+01:00`。

### 6. Tests Required

- 默认配置：`GetRegisteredConfig("timezone") == "Asia/Shanghai"`
- 校验：有效 IANA 时区可保存，无效时区和 `Local` 被拒绝。
- 运行时应用：`ApplyPanelTimezone("UTC")` 后 `time.Local.String()=="UTC"` 且 `os.Getenv("TZ")=="UTC"`。
- 保存立即生效：通过配置接口保存 `timezone` 后，`CurrentPanelTimezone()` 立即变为新值。
- 任务环境：`BuildManagedRuntimeEnvMapForPythonVersion` 返回的 `TZ` 必须等于当前面板时区，并覆盖用户环境变量表里的同名 `TZ`。
- 偏移转换：覆盖 UTC、正负偏移、分钟偏移、夏令时和四字母缩写，确认生成 Windows CRT 可识别的三字母 POSIX 值。
- Windows 真实进程：Python 脚本和 Python 模块均输出 `+08:00` 且读取到 `TZ=Asia/Shanghai`；Node.js 仍输出 `GMT+0800`。

### 7. Wrong vs Correct

#### Wrong
```go
// 错误：只设置子进程环境，Go 进程自己的 time.Now() 仍可能按 UTC。
envMap["TZ"] = "Asia/Shanghai"
```

```go
// 错误：依赖宿主机 Local，精简 Linux 上可能仍是 UTC。
time.Now().Format("2006-01-02 15:04:05")
```

#### Correct
```go
if err := service.ApplyRegisteredPanelTimezone(); err != nil {
    return fmt.Errorf("failed to apply panel timezone: %w", err)
}
```

```go
// 正确：任务环境强制跟随面板全局时区，避免脚本时间和面板日志不一致。
envMap["TZ"] = service.CurrentPanelTimezone()
```

```go
// 正确：只给 Windows Python 的启动环境转换格式，不能修改原任务变量或 Node 环境。
cmd.Env = buildPythonBootstrapProcessEnv(envVars)
```

---

## 场景：版本发布前预检

### 1. Scope / Trigger

- 触发：准备推送 `main`、打 `vX.Y.Z` tag、触发 `.github/workflows/release.yml` 之前必须看本节。
- 原因：这个仓库历史上多次出现“主 Release 已成功，但 Docker job 因缓存/平台问题报错”、“README / Magisk 版本号没同步”、“更新日志缺失或 title marker 缺失”这类可提前在本地发现的问题。

### 2. Signatures

- 预检脚本：`scripts/release-preflight.ps1 -Version X.Y.Z`
- 目标 workflow：`.github/workflows/release.yml`

### 3. Contracts

- 打 tag 前必须先运行一次 `scripts/release-preflight.ps1 -Version X.Y.Z`
- 预检至少覆盖：
  - Git 工作区干净
  - `docs/release-notes/vX.Y.Z.md` 存在且包含 `release-title`
  - README 最新稳定版、Magisk `module.prop`、`Magisk/update.json` 版本号已同步
  - `go test ./...` 通过
  - `npm run build` 通过
  - `release.yml` 基本语法检查通过（若本机有 `actionlint`）
  - 远端不存在同名 tag

### 4. Validation & Error Matrix

- 工作区不干净 -> 直接阻断发版
- 更新日志缺失 / title marker 缺失 -> 直接阻断发版
- 远端已存在同名 tag -> 直接阻断发版
- `actionlint` 不存在 -> 允许继续，但必须给出黄色告警而不是静默跳过

### 5. Good/Base/Bad Cases

- Good：先跑预检，再 push main、push tag；高频低级错误在本地就被拦住
- Base：即使没装 `actionlint`，也至少完成版本同步、构建、测试、tag 冲突检查
- Bad：直接打 tag 触发 CI，等远端失败后再补版本文件或更新日志

### 6. Tests Required

- 本地执行：`powershell -ExecutionPolicy Bypass -File .\scripts\release-preflight.ps1 -Version 2.2.20`
- 修改预检脚本后至少手动跑一次，确认脚本本身可用

### 7. Wrong vs Correct
#### Wrong
```text
改完代码 -> 直接 git push origin main && git push origin v2.2.20
```

#### Correct
```text
先跑 release-preflight -> 通过后再推 main 和 tag
```

---

## 场景：Node preload 兼容青龙脚本的 `process.env` 字符串检测

### 1. Scope / Trigger

- 触发：修改 `server/service/runtime_exec.go` 里 Node / TypeScript 托管运行时、`writeNodePreloadScript`、环境变量注入、`NODE_OPTIONS` / preload 相关逻辑时必须看本节。
- 原因：少数青龙脚本会执行 `JSON.stringify(process.env).indexOf("GITHUB")`，只要任务环境变量的 key 或 value 包含大写 `GITHUB` 就 `process.exit(0)` 静默退出，表现为日志只有“开始”，退出码却是 0。

### 2. Signatures

- Node preload 生成入口：`writeNodePreloadScript(tempDir, envFile string, envVars map[string]string) (string, error)`
- Node 命令入口：`createManagedNodeCommand(...)`
- TypeScript Node 命令入口：`createManagedTSNodeCommand(...)`
- 环境文件：`env.json`，由 preload 读入并写入 `process.env`

### 3. Contracts

- preload 必须继续把 `env.json` 中的任务环境变量写入真实 `process.env`，不能删除用户显式配置的 `GITHUB_*` 变量。
- 仅对 `JSON.stringify(process.env)` 做兼容过滤：返回的 JSON 字符串中不应包含 key 或 value 带大写 `GITHUB` 的环境项。
- `process.env.GITHUB_*` 直接读取必须仍然可用，避免破坏确实依赖 GitHub 变量的脚本。
- 普通 `JSON.stringify({ GITHUB_ACTIONS: 1 })` 等非 `process.env` 对象必须保持 Node 原生行为。

### 4. Validation & Error Matrix

- `env.json` 包含 `GITHUB_ACTIONS=1` -> `JSON.stringify(process.env)` 不包含 `GITHUB`，但 `process.env.GITHUB_ACTIONS === "1"`。
- `env.json` 不含 `GITHUB` -> 普通环境注入和脚本执行行为不变。
- 用户脚本 stringify 普通对象 -> 不过滤、不改写。
- 如果删除真实 `process.env.GITHUB_*` -> 错误，会破坏显式读取变量的脚本。

### 5. Good/Base/Bad Cases

- Good：`hex-ci/smzdm_script` 这类脚本不再因为环境里有 `GITHUB` 而静默成功退出，后续签到日志能继续输出。
- Base：普通 Node 脚本继续通过 `process.env.SMZDM_COOKIE`、`process.env.NODE_PATH` 等读取任务环境。
- Bad：直接清理所有 `GITHUB_*` 环境变量，导致需要 GitHub token 或仓库信息的脚本读取不到配置。

### 6. Tests Required

- 回归测试：`TestNodePreloadKeepsGithubEnvReadableButHiddenFromStringify`
  - 断言 `JSON.stringify(process.env)` 不含 `GITHUB`。
  - 断言 `process.env.GITHUB_ACTIONS` 仍可直接读取。
  - 断言普通任务环境变量仍可直接读取。
- 修改后至少运行：

```bash
cd server
go test ./service -run "TestNodePreloadKeepsGithubEnvReadableButHiddenFromStringify|TestBuildManagedRuntimeEnvMap" -count=1
go test ./...
```

### 7. Wrong vs Correct

#### Wrong

```js
// 错误：删除真实变量会破坏用户脚本显式读取 GITHUB_TOKEN / GITHUB_ACTIONS 的场景。
delete process.env.GITHUB_ACTIONS;
delete process.env.GITHUB_TOKEN;
```

#### Correct

```js
// 正确：只兼容 JSON.stringify(process.env) 这种粗暴检测，不删除真实 process.env。
const originalJSONStringify = JSON.stringify;
JSON.stringify = function(value, replacer, space) {
  if (value === process.env) {
    const envCopy = {};
    for (const [key, envValue] of Object.entries(process.env)) {
      if (String(key).includes('GITHUB') || String(envValue).includes('GITHUB')) {
        continue;
      }
      envCopy[key] = envValue;
    }
    return originalJSONStringify.call(JSON, envCopy, replacer, space);
  }
  return originalJSONStringify.call(JSON, value, replacer, space);
};
```

---

## 场景：`config.sh` 多行环境变量安全解析

### 1. Scope / Trigger

- 触发：修改 `server/service/runtime_exec.go` 里的 `loadConfigShellVars()`、任务环境合并优先级或配置文件语法时必须看本节。
- 原因：`config.sh` 允许用引号包住多行账号。如果按行独立解析，`process.env`、`os.environ` 和 Shell 任务只能拿到首行；如果直接 `source config.sh`，又会执行用户文件里的任意 Shell 命令。

### 2. Signatures

- 配置读取：`func loadConfigShellVars(envMap map[string]string)`
- 任务环境入口：`BuildManagedRuntimeEnvMapForPythonVersion(...)`
- 配置文件：`filepath.Join(config.C.Data.Dir, "config.sh")`
- Node.js 注入：`env.json -> writeNodePreloadScript() -> process.env`

### 3. Contracts

- 只解析 `KEY=VALUE` 和可选 `export KEY=VALUE`，禁止通过 `source`、`bash -c` 或等价方式执行 `config.sh`。
- 单引号和双引号内的真实跨行内容必须保留 `\n`，不能只记录首行，也不能自动改成 `&` 或字面量 `\\n`。
- 单行值、空值、引号内的 `=` / `#` 继续按原内容读取。
- 同一 `config.sh` 里同名键重复赋值时，后面的合法赋值覆盖前面的赋值。
- 环境变量页面（数据库 `env_vars`）的同名键优先级高于 `config.sh`；面板全局 `TZ` 仍在两者之后强制覆盖。
- 历史上能读取的非空键名不应在这条链路新增强制过滤；不同运行时后续可按自己的环境变量能力过滤。

### 4. Validation & Error Matrix

- `export CK='a\nb\nc'` -> `envMap["CK"] == "a\nb\nc"`。
- `export CK="a=1\nb#2"` -> 保留换行、`=` 和 `#`。
- 引号未闭合且遇到后续合法 `export NEXT=...` -> 忽略损坏项，继续读取 `NEXT`。
- 文件不存在或无法读取 -> 保持原有环境映射，不阻断任务构建。
- 数据库和 `config.sh` 同时存在同名键 -> 使用数据库值。

### 5. Good/Base/Bad Cases

- Good：用户在一个 `export csCk='...'` 里换行填写四个账号，Node.js `process.env.csCk` 读到完整四行。
- Base：`KEY=value`、`export KEY="value"`、注释和空行行为不变。
- Bad：用 `bufio.Scanner` 逐行立即赋值，导致引号跨行值只剩第一行。
- Bad：为复用 Shell 解析直接 `. config.sh`，导致任务构建阶段执行用户命令。

### 6. Tests Required

- `TestLoadConfigShellVarsSupportsMultilineQuotedValues`：断言单引号多行、双引号多行、单行、空值和历史键名。
- `TestLoadConfigShellVarsIgnoresBrokenMultilineAndKeepsFollowingExport`：断言损坏项不入环境，后续合法 `export` 仍可读取。
- `TestBuildManagedRuntimeEnvMapKeepsDatabaseEnvPriorityOverConfigFile`：断言环境变量页面的同名值优先。
- `TestConfigShellMultilineValueReachesNodeProcessEnv`：真实启动 Node.js，断言 `process.env` 收到完整换行值。
- 修改后至少运行：

```bash
cd server
go test ./service -run "TestLoadConfigShellVars|TestBuildManagedRuntimeEnvMapKeepsDatabaseEnvPriorityOverConfigFile|TestConfigShellMultilineValueReachesNodeProcessEnv" -count=1
go test ./...
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：第一行立即写入环境，后续没有 '=' 的账号行会被丢弃。
for scanner.Scan() {
    line := strings.TrimSpace(scanner.Text())
    envMap[key] = strings.Trim(value, "\"'")
}
```

#### Correct

```go
// 正确：先收集到同类型闭合引号，完成后再写入配置值。
if closeAt := findClosingQuote(value[1:], quote); closeAt < 0 {
    pendingKey = key
    pendingQuote = quote
    pendingValue.WriteString(value[1:])
}
```

---

## 场景：任务前后置钩子的环境变量回传

### 1. Scope / Trigger

- 触发：修改 `server/service/task_hook_env.go`、`server/service/task_executor.go` 里前置 / 后置钩子的调用点，或 `server/service/runtime_exec.go` 的 `shellEnvBootstrap` 时必须看本节。
- 原因：这条链路（青龙 `task_before` 语义：前置脚本里 `export` 的变量对目标脚本生效）踩点极密集，而**每一个坑的失败模式都是静默的**：要么用户的 `export` 完全不生效，要么反过来把 bootstrap 自身的环境（`PATH` / `HOME` / `HTTP_PROXY`）当成「新增变量」污染进任务环境，要么把超预算的大账号变量凭空弄没。三种都不报错。

### 2. Signatures

- 采集包装：`func captureHookEnvExports(envVars map[string]string, onOutput OnOutputFunc, run func(hookEnv map[string]string))`
- 采集现场：`type hookEnvCapture`（临时目录 + `hook-env.dump` + `.base` + `.ok` 标记）
- 差集合并：`func mergeHookEnvExports(envVars, baseline, final map[string]string) (applied, ignored, notices []string)`
- 保护判定：`func hookEnvProtection(name string) (protected, report bool)`
- 放行但提示：`var hookEnvRuntimeCriticalNames` + `func hookEnvRuntimeOverrideNotice(name, before, after string) string`
- 开关常量：`const hookEnvDumpPathEnvKey = "DAIDAI_HOOK_ENV_DUMP"`
- shell 侧：`const shellEnvBootstrap`（`runtime_exec.go`）

### 3. Contracts

- **采集必须用 `trap ... EXIT`，并且装在 `. "$__dd_script"` 之前。** 用户脚本是被 **source** 的（不是 exec），一句 `exit 0` 会终止整个 bootstrap shell，任何追加在其后的 dump 代码永远不会执行 —— 而 `exit 0` / `[ -z "$X" ] && exit 1` 恰恰是前置脚本最常见的收尾写法。
- **必须采两次快照做差集**：source 用户脚本之前落 `.base` 基线，`trap` 里落最终快照。只采一次会把 bootstrap 进程自身就有的 `PATH` / `HOME` / `LANG` / `HTTP_PROXY` 当成「前置脚本新增的变量」合并进任务环境；代理地址还会被**冻结成快照**（它本来是命令创建时从 `system_configs` 实时读的）。
- **纯增量覆盖，禁止用 dump 结果替换 `envVars`。** `planShellEnvExport` 会把单条超过 `MAX_ARG_STRLEN`、或累计超出导出预算的变量「只赋值不 export」，这类变量在钩子进程里 `env -0` 根本看不到；替换式合并会让它们在目标脚本里凭空消失，表现成「加了前置脚本之后某个账号变量突然没了」。
- **`unset` 不传导。** 差集只能区分「新增」和「变更」，「缺席」既可能是被 unset，也可能是上面那批从来没进过钩子环境的大变量 —— 按缺席删键会直接删掉用户的账号变量。想清空写 `export VAR=`。
- **保护名单 = `TZ` + 全部 `DAIDAI_` 前缀 + shell 内部易变量。** 前两类是用户**有意**去改的运行时契约（时区链路、脚本令牌、`DAIDAI_NOTIFY_CHANNEL_ID` 的渠道绑定），拦下来必须往任务日志写一行「已忽略受保护变量: …」，否则用户会一直以为改生效了；`PWD` / `SHLVL` / `IFS` / `BASH_*` / `COMP_*` 这类静默拦，报出来纯属噪音。
- **`PATH` 不在保护名单里。** 托管解释器用 `resolveManagedBinary` + `sanitizeManagedPath` 算出绝对路径再 exec，完全不受 `envVars["PATH"]` 影响；`envVars["PATH"]` 只决定脚本自己 fork 出来的 `pip` / `npm` / `git` 用哪个 PATH，那正是 shell 语义下用户想要的。
- **`PATH` / `PYTHONPATH` / `NODE_OPTIONS` / `NODE_PATH` 属于「放行但提示」，不属于保护名单。** 它们都是面板注入的运行时关键变量（`PYTHONPATH` / `NODE_PATH` / `NODE_OPTIONS` 由 `AppendScriptHelperPaths` 注入 venv 的 site-packages、托管 `node_modules` 与 `sendNotify.js` 的 `--require`；`PATH` 由 `BuildManagedRuntimeEnvMapWithScriptToken` 注入），但覆盖 PATH 类变量是 shell 语义、也是用户的合法诉求（追加写法必须能用），所以**照常生效**，只在「面板注入的旧值确实被整体冲掉」时额外打一行带**追加写法**的诊断提示（`hookEnvRuntimeOverrideNotice`）。判定复用 `applied`：没改动的键、用追加写法改的键都不提示，避免刷屏。这类覆盖的失败模式极隐蔽 —— `PYTHONPATH` 被冲掉后目标脚本会突然找不到全部已装依赖；`NODE_OPTIONS` 被冲掉后只有脚本自己 fork 出来的**嵌套** node 进程失去 notify 注入（目标脚本本身走 `createManagedNodeCommand` 里显式的 `--require`，不受影响，所以更难联想）。
- **开关是「`DAIDAI_HOOK_ENV_DUMP` 非空」**且**「同名 `.ok` 标记文件存在」两个条件同时成立**。只有第二道能挡住这种情况：用户在「环境变量」页手建一条同名变量、值随手填成某个真实路径（比如 `/app/config.yaml`），那样每个 bash 任务都会用 `>` 把那个文件截断。`.ok` 只有面板自己会创建，误设的值就只是个空转。
- **`RunInlineScript` 还有订阅钩子（`subscription_hook.go`）这个调用方**，`RunHookScript` 也同时服务 `task_after.sh` / `extra.sh`。采集逻辑必须对它们完全 no-op（靠上面那个门禁），不得改变这些调用方的输出与退出码。
- 后置脚本自身的 `export` **不回传** —— 它跑完任务就结束了，没有下游消费方。
- 失败分级要精确：基线没落盘 = bootstrap 压根没跑（绝大多数用户没有全局 `task_before.sh`），必须**完全静默**；基线在但最终快照缺失 = 钩子跑了而 `trap` 没能落盘（用户自己装了 EXIT trap，或进程被 SIGKILL），必须**出声**，否则用户以为 `export` 生效了。
- **新增任何「面板自己打进任务日志」的元信息行（`[前置脚本环境变量] …`、`[前置脚本执行失败: …]`、`[后置脚本执行失败: …]` 等），必须同步登记到 `task_executor.go` 的 `panelMetaLinePrefixes`**，否则它会混进任务成功通知的日志摘录（`summarizeTaskSuccessOutput`，上限 30 行 / 1500 字符），把用户真正想看的脚本输出挤掉。这个失败模式同样是静默的：任务照常成功，只是通知里看不到有用内容。

### 4. Validation & Error Matrix

- 前置脚本 `export A=1` 后 `exit 0` → `A` 仍然回传（`trap EXIT` 兜住）
- 前置脚本 `export PATH=/custom:$PATH` → 生效（`PATH` 不保护）
- 前置脚本 `export TZ=UTC` / `export DAIDAI_TOKEN=x` → 被忽略，任务日志出现「已忽略受保护变量: …」
- 前置脚本 `export PYTHONPATH=/my/lib`（整体覆盖）→ 生效，且任务日志多一行「注意：PYTHONPATH 是面板注入的运行时变量…请改用 `export PYTHONPATH=...:$PYTHONPATH` 的追加写法」
- 前置脚本 `export PYTHONPATH=/my/lib:$PYTHONPATH`（追加写法）→ 生效且**不提示**
- 前置脚本 `cd /tmp` → `PWD` 变了但静默丢弃，不进日志
- 前置脚本 `unset X` → 不传导，`X` 保持原值
- 只赋值未 export 的超大账号变量 → 合并后仍然存在（增量合并，不是替换）
- 临时目录建不出来 → 退回「执行但不回传」的旧行为并写一行日志，**不得连钩子都不跑**
- 订阅钩子 / `task_after.sh` / `extra.sh` → 不注入 `DAIDAI_HOOK_ENV_DUMP`，采集代码整段不装

### 5. Good/Base/Bad Cases

- Good：前置脚本里 `export RUN_ID="$(date +%s)"` 然后 `exit 0`，目标脚本 `os.environ["RUN_ID"]` 读得到，任务日志写明「已生效: RUN_ID」。
- Base：没有前置脚本的任务，日志里一行多余输出都没有，行为与改动前逐条一致。
- Bad：把 dump 代码追加在 `. "$__dd_script"` 之后。用户一句 `exit 0`，回传功能完全失效且无任何提示。
- Bad：只采一次快照。`HTTP_PROXY` 被冻结成快照值，用户在设置页改了代理却发现任务还在用旧地址。
- Bad：用 final 整体替换 `envVars`。超预算的大账号变量在目标脚本里凭空消失。
- Bad：把 `PATH` 也加进保护名单。用户改 `PATH` 想让脚本用自己那套 `pip` 却怎么改都不生效。

### 6. Tests Required

见 `server/service/task_hook_env_test.go`：

- 覆盖 `exit 0` 收尾仍能回传、`exit 3` 收尾退出码不被 trap 改写且仍能回传、两次快照差集、保护名单（报告 / 静默两类）、`PATH` 可改、`unset` 不传导、只赋值未 export 的大变量不丢、`.ok` 门禁、订阅钩子 no-op。
- **注意其中依赖真 bash 的用例在 Windows 上会 skip**（`requireUsableBash` 对 `runtime.GOOS == "windows"` 直接 `t.Skip`），只有 CI 的 `ubuntu-latest` 才真正执行 —— 本机全绿不等于这条链路验过，改动这一节的代码必须看 CI 结果。
- ⚠️ 过滤器不能只写 `-run "HookEnv"`：那样会漏掉 `TestTaskBeforeInlineScriptExportsMergeIntoTaskEnv` 等 5 条用例，而 trap 方案的主验收恰好就在里面。
- 修改后至少运行：

```bash
cd server
go test ./service -run "HookEnv|TaskBefore" -count=1
go test ./...
```

### 7. Wrong vs Correct

#### Wrong

```sh
# 错误：dump 追加在 source 之后。用户脚本一句 exit 0 就永远走不到这里。
. "$__dd_script" "$@"
__dd_dump_env "$DAIDAI_HOOK_ENV_DUMP"
```

```go
// 错误：替换式合并。只赋值未 export 的大变量在钩子里看不到，会被整个抹掉。
for key := range envVars {
    delete(envVars, key)
}
for key, value := range final {
    envVars[key] = value
}
```

#### Correct

```sh
# 正确：先落基线，再把 dump 装成 EXIT trap，最后才 source 用户脚本。
__dd_dump_env "${DAIDAI_HOOK_ENV_DUMP}.base"
trap '__dd_dump_env "$DAIDAI_HOOK_ENV_DUMP"' EXIT
. "$__dd_script" "$@"
```

```go
// 正确：只回写「钩子里新增」和「钩子里改过值」的键，缺席一律不动。
for key, value := range final {
    if before, existed := baseline[key]; existed && before == value {
        continue
    }
    if protected, report := hookEnvProtection(key); protected {
        if report {
            ignored = append(ignored, key)
        }
        continue
    }
    envVars[key] = value
}
```

---

## 场景：系统配置注册表默认值与渲染 schema

### 1. Scope / Trigger

- 触发：改动 `server/model/system_config_registry.go` 的 `SystemConfigDefinition`、任意 `newXxxConfig` 构造函数、任意一项配置的默认值或 normalize 函数，以及 `server/handler/config.go` 的 `buildConfigResponseItem` 时必须看本节。
- 原因：`GET /api/configs` 下发的是完整 schema，Web 和 APP 都据此渲染系统设置页。注册表既是服务端的取值来源，也是客户端唯一的界面描述来源，一旦分叉两边都会静默错。

### 2. Signatures

- 声明结构：`model.SystemConfigDefinition`（含 `Label` / `GroupLabel` / `Order` / `Secret` / `Min` / `Max`）
- 构造函数统一参数顺序：`(key, label, defaultValue, description, group, ...)`
  - `newTrimmedStringConfig(key, label, defaultValue, description, group)`
  - `newSecretStringConfig(key, label, defaultValue, description, group)`
  - `newValidatedStringConfig(key, label, defaultValue, description, group, normalize)`
  - `newBoolConfig(key, label, defaultValue, description, group)`
  - `newIntConfig(key, label, defaultValue, description, group, minValue, maxValue)`
  - `newEnumConfig(key, label, defaultValue, description, group, options)`
- 顺序与分组名补齐：`finalizeSystemConfigSpecs(specs []systemConfigSpec) []systemConfigSpec`
- 分组中文名：`systemConfigGroupLabels`
- 按 key 取归一化函数：`model.NormalizeSystemConfigValue(key, value string) (string, error)`

### 3. Contracts

- **声明的默认值必须等于实际生效的默认值**：对每一项配置，`NormalizeSystemConfigValue(key, "")` 必须与 `def.DefaultValue` 完全相等。
  `newValidatedStringConfig` 把 `DefaultValue` 原样存进 definition、注册时不过 normalize，所以这两处一旦各写一份字面量就会静默错开。有共用默认值时应抽成常量（例如 `defaultBackupScheduleSelection`），不要在两处各写一遍。
- 默认值本身必须是合法且已归一化的值：`NormalizeSystemConfigValue(key, def.DefaultValue)` 必须无错且原样返回。
- `Label` 是输入框标题用的短词，必须非空；`Description` 是长句说明，只能当 hint，不得当标题。
- 新增分组 slug 必须同步在 `systemConfigGroupLabels` 补中文名，`GroupLabel` 不允许退化成英文 slug。
- `Order` 由 `finalizeSystemConfigSpecs` 按注册下标写入，必须在 `registeredSystemConfigSpecs` 的初始化表达式里调用，**不能挪到 `init()`**：`registeredSystemConfigMap` 按值拷贝存 spec，`init()` 里再改切片会让 map 拿到旧数据。
- 整数配置必须下发 `Min` / `Max`，且与 normalize 闭包里的校验边界一致；`Min` / `Max` 用局部拷贝取地址，不要直接 `&minValue`，避免调用方改 `*def.Min` 反过来改掉校验行为。
- 凭据类配置必须标 `Secret`，并同步更新 `TestRegisteredSecretConfigsAreMarked` 的名单。
  `Secret` 目前**只是渲染提示**，服务端仍明文回传 `value`。要改成服务端打码，必须同时定义「未修改」的写入哨兵值并同步改 Web/APP，否则保存整组配置时会把掩码写回数据库、覆盖真实密钥。
- `/api/configs` 的响应字段只允许新增，不允许改名或改类型：老客户端拿到多出来的键必须无感。

### 4. Validation & Error Matrix

- 注册表默认值与 `normalize("")` 不一致 → `TestEveryRegisteredConfigDefaultMatchesNormalizedEmpty` 失败
- 默认值本身非法（枚举不在 options / 整数越界）→ `TestEveryRegisteredConfigDefaultIsCanonical` 失败
- 新增分组忘配中文名 → `TestEveryRegisteredConfigGroupHasLabel` 失败
- 整数配置漏下发 min/max，或与校验边界不一致 → `TestRegisteredIntConfigsExposeMinMax` / `TestRegisteredIntConfigMinMaxMatchesValidation` 失败
- 库里没有记录 / 值为空串 → `GET /configs` 与 `GetRegisteredConfig()` 都返回 `def.DefaultValue`
- 库里已存在旧值 → `InitDefaultConfigs()` 只在值为空或校验不过时重写，**不会**把已有值升级成新默认值

### 5. Good/Base/Bad Cases

- Good：`backup_schedule_selection` 的默认值与 `normalizeBackupScheduleSelectionValue` 共用同一个常量，新装实例的定时备份默认包含任务视图。
- Base：新增一项配置时同时写好 key/label/默认值/说明/分组，客户端无需发版即可显示。
- Bad：注册表写 7 项、normalize 写 8 项。`/api/configs` 报出去的 `default_value`、`InitDefaultConfigs()` 首次建行写入的值、`GetRegisteredConfig()` 的回退值全都用缺项的那份，表现为「从未保存过备份设置的实例，定时备份不含任务视图」，且 Web 上那个勾选框默认没勾。
- Bad：把长句 `Description` 直接当输入框标题，`panel_runtime_mode` 那种三行说明会把表单撑烂。

### 6. Tests Required

见 `server/model/system_config_registry_test.go` 与 `server/handler/config_regression_test.go`：

- `TestEveryRegisteredConfigDefaultMatchesNormalizedEmpty`（**最重要的一条**，锁死声明默认值 == 生效默认值）
- `TestEveryRegisteredConfigDefaultIsCanonical`
- `TestBackupScheduleSelectionDefaultIncludesTaskViews`
- `TestEveryRegisteredConfigHasRenderMetadata` / `TestEveryRegisteredConfigGroupHasLabel`
- `TestRegisteredIntConfigsExposeMinMax` / `TestRegisteredIntConfigMinMaxMatchesValidation`
- `TestRegisteredSecretConfigsAreMarked` / `TestRegisteredEnumConfigsHaveOptions`
- `TestConfigListExposesRenderSchema` / `TestConfigListReportsCompleteBackupScheduleSelectionDefault`
- 修改后至少运行：

```bash
cd server
go test ./model ./handler -run "TestEveryRegisteredConfig|TestRegistered|TestBackupScheduleSelectionDefault|TestConfigList" -count=1
go test ./...
```

> **突变验证**：把 `backup_schedule_selection` 的默认值改回 7 项（去掉 `task_views`），`TestEveryRegisteredConfigDefaultMatchesNormalizedEmpty`、`TestBackupScheduleSelectionDefaultIncludesTaskViews`、`TestConfigListReportsCompleteBackupScheduleSelectionDefault` 必须确定性变红；若不红说明用例没有真正在检测这条不变量。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：默认值在注册表和 normalize 里各写一份字面量，改一处不会有任何报错。
newValidatedStringConfig(
    "backup_schedule_selection", "备份内容",
    "configs,tasks,subscriptions,env_vars,logs,scripts,dependencies",
    "...", "backup", normalizeBackupScheduleSelectionValue,
)

func normalizeBackupScheduleSelectionValue(value string) (string, error) {
    defaultValue := "configs,tasks,subscriptions,env_vars,logs,scripts,dependencies,task_views"
    // ...
}
```

```go
// 错误：取值区间只被闭包捕获，客户端拿不到，只能等用户填了越界值再被服务端 400 打回。
def: SystemConfigDefinition{Key: key, ValueType: SystemConfigTypeInt},
normalize: func(value string) (string, error) {
    if parsed < minValue || parsed > maxValue { /* ... */ }
},
```

#### Correct

```go
// 正确：默认值收成一个常量，注册表和 normalize 共用同一份。
const defaultBackupScheduleSelection = "configs,tasks,subscriptions,env_vars,logs,scripts,dependencies,task_views"
```

```go
// 正确：取值区间拷一份挂到 def 上，客户端可以做前端校验，服务端仍然独立校验一次。
minBound, maxBound := minValue, maxValue
def: SystemConfigDefinition{
    Key: key, ValueType: SystemConfigTypeInt,
    Min: &minBound, Max: &maxBound,
},
```

---

## 场景：通知渠道字段注册表与 config 值类型

### 1. Scope / Trigger

- 触发：改动 `server/service/notifier.go` 里任意 `sendXxx` 读取的 `cfg["..."]` 键、`sendToChannel` 的渠道分支、`server/model/notify_channel_registry.go`、`server/model/notify_channel_config.go`，或 `server/handler/notification.go` 的 `Create` / `Update` / `Types` 时必须看本节。
- 原因：「每个通知渠道有哪些配置字段」这份知识长期只活在 `notifier.go` 的函数体里，服务端从未声明式地持有过。客户端只能各抄一份，抄漏就表现为「面板支持这个配置，但用户没有任何输入框可填」。APP 曾因此缺 31 个键；`web/src/views/api-docs/apiData.ts` 的 wecom_app 消息类型漏 `mpnews` 也是同一种漂移。

### 2. Signatures

- 渠道声明：`model.NotifyChannelDefinition` / `model.NotifyFieldDefinition` / `model.NotifyFieldCondition`
- 控件枚举：`model.NotifyWidgetInput` / `NotifyWidgetPassword` / `NotifyWidgetTextarea` / `NotifyWidgetSelect`
- 读取入口：`model.NotifyChannelDefinitions()` / `model.GetNotifyChannelDefinition(type)` / `model.NotifyChannelConfigKeys()`
- 值归一：`model.NormalizeNotifyChannelConfig(raw string) (string, error)`
- 下发接口：`GET /api/notifications/types`（`handler.NotificationHandler.Types`，直接吐注册表）
- 写入口：`POST /api/notifications`、`PUT /api/notifications/:id`、`service.restoreNotifyChannels`

### 3. Contracts

- **`notifier.go` 是权威，注册表向它对齐，不是反过来。** 要加字段先在 `notifier.go` 里真的读它，再回注册表声明。
- 注册表声明的键集合与 `notifier.go` 实际读取的 `cfg["..."]` 键集合**双向相等**，白名单只允许放行「服务端确实读了、但不是通过字面量读的」这一种情况（目前只有 `smtp_ssl` 一族的 SSL 别名）。
- 注册表声明的渠道类型集合与 `sendToChannel` 的 switch 分支**双向相等**。`/notifications/types` 不得再手写渠道列表。
- `ShowWhen` 的语义固定为「单键等值命中」，**不要扩成表达式引擎**。同一渠道内同键多次声明是允许的，但各条的 `ShowWhen` 必须互斥。
- **不支持条件 options**。像 wecom_app 的 `safe` 那种「选项集合随另一个字段变」的情况，一律把选项常驻，并在就近注释里写清为什么可以常驻（服务端是否透传、错值由谁报错）。
- `Required` 的口径必须严格：**当且仅当 `notifier.go` 对该字段单独判空并直接返回错误**。二选一约束（email 的 `smtp_user`/`from`、wxpusher 的 `uids`/`topic_ids`）和 `notifier.go` 不校验的 8 个渠道（serverchan / pushdeer / chanify / igot / pushover / discord / slack / custom）一律不标。要让它们必填，先去 `notifier.go` 补判空。
- `Default` 只记录 `notifier.go` 在该字段为空时**实际使用的回退值**。「留空 = 完全不发这个参数」的字段不写 `Default`。
- **落库的 config 必须是「顶层对象 + 值全是字符串」**。`sendToChannel` 是 `json.Unmarshal` 到 `map[string]string`，出现任何非字符串值都会让该渠道所有通知（含测试按钮）全挂。
- 归一规则：字符串原样；布尔 / 数字 / null 转成字符串（**安全可逆**，同时让老客户端写坏的记录一编辑就自愈）；对象 / 数组直接 400 并指出是哪个键（**不可逆**，`fmt.Sprint` 出来是 Go 语法垃圾）。
- 数字必须用 `json.Decoder` + `UseNumber()` 解析。默认的 `interface{}` 反序列化得到 `float64`，`fmt.Sprint(float64(1000000))` 是 `"1e+06"`，会直接毁掉用户填的整数。
- `/notifications/types` 的响应只允许新增键，不允许改名或改类型；`type` / `name` / 顺序是老客户端的契约，改动必须同步更新 `TestNotifyChannelTypesRemainBackwardCompatible` 的基线。
- **`proxy` 键（telegram、wecom_app 声明）语义统一**：渠道值非空用它；空则回落系统设置 `proxy_url`；再空走进程环境变量 `HTTP(S)_PROXY`，最后才直连（`NewHTTPClientWithProxy`）。wecom_app 的取 token 与发消息共用同一个 client。`proxy` 是正向代理，与 wecom_app 的 `base_url`（反代基础地址）可叠加。
- 保存期校验：`model.ValidateNotifyChannelConfig(type, normalized)`（Create）/ `ValidateNotifyChannelConfigChange(type, normalized, previous)`（Update）。只校验该渠道类型**声明过**的键，校验表目前只登记 `proxy`（复用 `normalizeProxyURL`）；空值、未知类型、解不开的 JSON 一律放行。

### 4. Validation & Error Matrix

- `notifier.go` 新增 `cfg["x"]` 但注册表没声明 → `TestNotifySchemaCoversAllConfigKeysReadByNotifier` 失败（服务端读得到但用户填不了）
- 注册表声明了 `notifier.go` 不读的键 → 同一条用例失败（假字段）
- 渠道类型两边不一致 → `TestNotifySchemaCoversAllChannelTypesHandledByNotifier` 失败
- SSL 别名被删但白名单还在 → `TestNotifierSmtpSSLAliasLoopStillExists` 失败
- `sendXxx` 被拆到别的文件 → `TestNotifierSourceHoldsEverySenderCalledBySendToChannel` 失败
- config 值是布尔 / 数字 / null → 归一成字符串，**不报错**
- config 值是对象 / 数组 → `400`，错误信息必须指出是哪个键
- config 顶层不是对象、JSON 非法、结尾有多余内容 → `400`，中文提示，不得把 Go 原始错误透给用户
- config 为空串 → 归一成 `"{}"`
- `PUT` 的 `config` 字段不是 JSON 字符串 → `400`，且不得改动已有 config
- 备份里带着坏 config → 恢复时尽力归一；归一不了就保留原文继续恢复，**不得让整批恢复失败**
- `proxy` 为**新填写或改动过**的非法值 → Create / Update `400`，错误点名字段：`通知渠道配置项「代理地址 (可选)」(proxy) 无效：…`，且不改动库里的数据
- 类型不变时 `proxy` 与库里现存值（两侧 TrimSpace 后）相同 → 放行 `200`，**哪怕它本身非法**（Web / APP 保存时整份回传 config，无条件校验会让存量记录改任何字段都存不进去）
- 切换渠道类型（`req["type"]` 为非空字符串且不等于库里的类型）→ 新类型声明的键一律按新值校验；`req["type"]` 不是字符串时按库里的类型校验
- 备份恢复、青龙导入 → 不做这层校验；wecom_app 发送时渠道代理非法 → **发送期显式报错、不发出任何请求**（不能交给 `NewHTTPClientWithProxy`：它遇到解析失败的地址会静默回落环境代理或直连，可信 IP 场景下只剩一个无头绪的 60020）

- 网络错误（`*url.Error`）→ 只保留底层原因（`stripRequestURL`），**不回显请求 URL**：wecom_app 的 gettoken 带 corpsecret、message/send 带 access_token，telegram 的路径带 bot token；这些错误会流到测试按钮、`/notifications/send` 的响应（operator 与 Open API 可见，而渠道配置只有 admin 能读）和面板日志。telegram 网络错误前缀为「请求 Telegram API（scheme://host）失败: 」，host 经 `redactProxyURL` 去掉 userinfo 与路径。
- HTTP≥400 的回显正文、wecom_app errcode 附带的 `errmsg` → `redactSecrets` 把密钥的原文 / QueryEscape / EscapedPath / PathEscape 四种形态（`secretForms`，先长后短）替换成 `***`，再截断到 512 字节（`notifyErrorBodyEchoLimit`）；telegram 改用 `redactTelegramBotPath`，**只替换 `/bot` 之后紧跟的 token**（另外覆盖整条 URL 被再转义一遍后的 `%2Fbot…` 形态；token 脱离 `/bot` 单独回显时不脱敏），避免误填的短 token 把「HTTP 404」这类状态码和业务文案改坏；业务错误（HTTP 200 的 `ok:false`）同样要脱敏，自建网关的 description 也可能回显请求路径
- wecom_app errcode 60020 → 追加「企业可信 IP」提示，只罗列本次生效的代理 / 反代配置（地址只留 scheme://host），并写明「以 errmsg 里的 from ip 为准」；`base_url` 主机是 `qyapi.weixin.qq.com` 时说明是官方地址、未经反代，其余用条件句「若该地址是反代服务器…」，**不自行断言出口路径**；其它 errcode 的文案逐字不变

> **Warning**：`proxy` 这类「别的渠道已经读过」的键落在绑定用例的盲区里。`TestNotifySchemaCoversAllConfigKeysReadByNotifier` 比的是全渠道**并集**，给 wecom_app 漏读或漏声明 `proxy` 它都不会红，必须靠 `TestNotifyChannelProxyFieldDeclared` 与 `TestSendWecomApp*` 这类定向用例兜住。

### 5. Good/Base/Bad Cases

- Good：面板给某渠道加一个新 config 键，改完 `notifier.go` 跑测试立刻变红，提示去注册表补声明；补完 Web 和 APP 不发版就能渲染出这个输入框。
- Base：22 个渠道 / 93 个字段槽原样下发，老客户端只读 `type` 和 `name`，对多出来的 `icon` / `fields` 无感。
- Bad：客户端把 `smtp_ssl` 写成 JSON 布尔 `false`。服务端 `Unmarshal` 到 `map[string]string` 直接失败，该渠道所有通知全挂，报的还是一句用户看不懂的 `cannot unmarshal bool into Go value of type string`。
- Bad：为了「严格」把非字符串值一律 400。库里已经存在的坏记录会因为「原有的坏值」而永远存不进去，用户只能去改数据库。
- Bad：把嵌套对象 `fmt.Sprint` 成 `map[Authorization:Bearer xxx]` 存下去。把「发不出去」换成了「发出去的是垃圾」，更难排查。
- Bad：`/notifications/types` 继续手写渠道列表。加渠道漏改一处，用户就会在下拉里看到一个打开没有任何输入框的渠道。

### 6. Tests Required

见 `server/service/notifier_schema_binding_test.go`、`server/model/notify_channel_registry_test.go`、`server/model/notify_channel_config_test.go`、`server/handler/notification_schema_test.go`：

- `TestNotifySchemaCoversAllConfigKeysReadByNotifier`（**最重要的一条**，双向绑死 schema 与 notifier）
- `TestNotifySchemaCoversAllChannelTypesHandledByNotifier`
- `TestNotifierSmtpSSLAliasLoopStillExists` / `TestNotifierSourceHoldsEverySenderCalledBySendToChannel`（防白名单和扫描范围腐化）
- `TestNotifyChannelRegistryHasNoStructuralDefects` / `TestNotifyChannelDuplicateKeysAreMutuallyExclusive`
- `TestNotifyChannelTypesRemainBackwardCompatible` / `TestNotifyChannelDefinitionsReturnsDeepCopy`
- `TestNormalizeNotifyChannelConfigCoercesScalarValues` / `TestNormalizeNotifyChannelConfigRejectsUnrecoverableValues` / `TestNormalizeNotifyChannelConfigIsIdempotent`
- `TestCreateNotificationChannelCoercesNonStringConfigValues` / `TestUpdateNotificationChannelHealsLegacyBrokenConfig`
- `proxy` 相关（issue #123）：
  - `TestNotifyChannelProxyFieldDeclared`（给「声明」这一半单独上锁：telegram 与 wecom_app 都恰好声明一次，input、非必填、无 Default、无 ShowWhen）
  - `TestValidateNotifyChannelConfigProxy` / `TestValidateNotifyChannelConfigIgnoresUndeclaredOrUnknown` / `TestValidateNotifyChannelConfigChangeOnlyChecksNewValues`（含旧值两侧带空白的放行例）
  - `TestCreateNotificationChannelRejectsMalformedProxy` / `TestUpdateNotificationChannelRejectsMalformedProxyWithoutType` / `TestUpdateNotificationChannelValidatesProxyAgainstEffectiveType` / `TestUpdateNotificationChannelKeepsUnchangedLegacyProxy`
  - `TestSendWecomAppUsesChannelProxy` / `TestSendWecomAppChannelProxyOverridesGlobal` / `TestSendWecomAppFallsBackToGlobalProxy`（回归锁，修复前就是绿的）/ `TestSendWecomAppRejectsMalformedProxy`
  - 行为用例一律用 **loopback httptest 源站 + 记录代理、双向计数**；不要用 `.invalid` 域名（本机 fake-ip 会应答 503，突变时报错不指向「直连了」）
- `TestCommittedDemoFixturesMatchRegistry`（`server/cmd/gen-demo-fixtures`：生成到临时目录与仓库 fixture 做 CRLF 归一后逐字比对，陈旧的演示站 fixture 在 CI 里直接红）
- 修改后至少运行：

```bash
cd server
go test ./model ./service ./handler -run "TestNotify|TestNormalizeNotifyChannelConfig|TestNotifier|TestCreateNotificationChannel|TestUpdateNotificationChannel|TestValidateNotifyChannelConfig|TestSendWecomApp|TestSendTelegram|TestRedactSecrets" -count=1
go test ./cmd/gen-demo-fixtures -count=1
go test ./...
```

> **突变验证**：往 `notifier.go` 任意 `sendXxx` 里加一句 `_ = cfg["__mutation_test__"]`，`TestNotifySchemaCoversAllConfigKeysReadByNotifier` 必须确定性变红并在错误信息里点名这个键；随后从注册表里删掉任意一个字段声明，同一条用例必须从另一个方向再红一次。两个方向都红才说明绑定是真的双向的。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：渠道列表手写一份，和字段注册表各活各的。加渠道漏改一处就静默不一致。
func (h *NotificationHandler) Types(c *gin.Context) {
    types := []map[string]string{
        {"type": "webhook", "name": "Webhook"},
        // ... 22 条硬编码
    }
    response.Success(c, gin.H{"data": types})
}
```

```go
// 错误：默认 interface{} 反序列化 + fmt.Sprint，整数会被写成 "1e+06"。
var object map[string]interface{}
_ = json.Unmarshal([]byte(raw), &object)
for key, value := range object {
    normalized[key] = fmt.Sprint(value)   // 30 -> "30"，但 1000000 -> "1e+06"
}
```

#### Correct

```go
// 正确：渠道列表和字段定义同一个源，结构上不可能分叉。
func (h *NotificationHandler) Types(c *gin.Context) {
    response.Success(c, gin.H{"data": model.NotifyChannelDefinitions()})
}
```

```go
// 正确：UseNumber 保住整数原文；可逆的转，不可逆的报错并点名是哪个键。
decoder := json.NewDecoder(strings.NewReader(trimmed))
decoder.UseNumber()
```

---

## 场景：通知渠道 push_scope（默认推送 / 绑定推送）

### 1. Scope / Trigger

- 触发：修改 `server/service/notifier.go` 的 `loadEnabledNotificationChannels()`、`model.NotifyChannel.PushScope` 及其归一函数、`server/handler/notification.go` 的 `Create` / `Update` / `Send`，或备份链路里 `BackupNotifyChannel` 的四处手抄点时必须看本节。
- 原因：`push_scope` 决定「一条通知到底发给谁」，而它的四个写入点分散在 handler、notifier、备份采集、备份恢复里，任何一处漏改都表现为**静默的投递面变化**：要么用户设的隔离被悄悄取消（该收不到的收到了），要么老库整批退出广播（升级后一条通知都收不到）。两种方向都没有报错、没有日志，只能靠读代码发现。

### 2. Signatures

- 表列：`model.NotifyChannel.PushScope string`（**一等表列，不是 config JSON 里的键**）
- 枚举与归一：`model.NotifyPushScopeDefault` / `model.NotifyPushScopeBound`、`model.NormalizeNotifyPushScope(raw string) (string, bool)`、`(*NotifyChannel).EffectivePushScope()`
- 唯一筛选点：`func loadEnabledNotificationChannels(channelIDs []uint) ([]model.NotifyChannel, error)`
- 写入口：`POST /api/notifications`、`PUT /api/notifications/:id`、`service.restoreNotifyChannels`
- 备份结构：`service.BackupNotifyChannel.PushScope`

### 3. Contracts

- **`push_scope` 是一等表列，不进 `notify_channel_registry.go`。** 那张注册表是 config JSON 的 schema 真源，被 `TestNotifySchemaCoversAllConfigKeysReadByNotifier` 用 go/ast 与 `notifier.go` 实际读的 `cfg["..."]` 键**双向绑死**；往里塞一个 `notifier.go` 根本不从 config 读的键，会直接把那条用例弄红。
- **取值必须是字符串枚举，不能改成 `IsDefault bool`。** 同一张表的 `Enabled bool gorm:"default:true"` 已经有一个踩过的活体坑：GORM 的 `ConvertToCreateValues` 把 `false` 当零值从 INSERT 里省掉，DB 侧的 `DEFAULT true` 反而生效（`DefaultValueInterface`），于是 `restoreNotifyChannels` 的 `tx.Create` 会把一条禁用渠道静默写回启用 —— 回归测试为此被迫写成 `Select("*").Create` + 单独 `Update`。bool 版的 push_scope 会以同样的方式把用户设的 bound 悄悄翻成 default，也就是把隔离意图反着执行。字符串的 Go 零值 `""` 归一后正好是 default，漏填只会退回升级前的老行为，方向安全。
- **定向发送完全忽略 `push_scope`。** `channelIDs` 非空时只按 ID 精确命中 —— 「绑定推送」存在的意义就是只在被显式指定时才推，再叠一层过滤等于把功能做废。
- **广播过滤条件必须写 `COALESCE(push_scope, '') <> 'bound'`，禁止写 `= 'default'`。** 这一列的语义是「空即默认」：老库补列、手工改库、以及未来任何忘了填这一列的写入路径都会留下空串或 `NULL`，等值比较会让这些历史行静默退出广播。`COALESCE` 那一层是为了兜 `NULL` —— SQL 里 `NULL <> 'bound'` 求值为 `NULL`（不成立），不兜同样会漏。
- **广播 0 命中严格不兜底，但必须留一行 warn 日志。** 不允许「广播没命中就退回全部已启用渠道」——那等于取消隔离。改动前这条路径是完全静默的，用户只要把所有渠道都设成 bound，系统通知（资源告警、登录通知、静默更新结果）就会全部人间蒸发且零线索，所以 `log.Printf("warn: notification broadcast skipped: ...")` 是这条路径唯一可查的痕迹，不得删。
- **`PUT /notifications/:id` 是按键更新，请求里没出现的键一概不动已有值。** 独立发版的 Flutter APP 编辑渠道时不带 `push_scope`，改成「缺省即 default」会让用户在 Web 上设的 bound 被 APP 的一次保存悄悄清掉。**显式传 `null` 同样视为「未提供」**：APP 很可能把未填字段序列化成 null，按类型错误 400 会让它一升级就全线保存失败，代价远大于收益。其余非字符串类型仍然 400，拼错的字符串值也仍然 400。
- **`notifier.go` 里定向分支那句 `未找到已启用的通知渠道` 被 `notification_send_regression_test.go` 逐字断言，不得改动**（改文案会挂用例，也会让老客户端的错误匹配失效）。只有广播分支那句可以改。
- **备份四处手抄一个都不能漏**：`backup_types.go` 的结构体字段、`backup_runtime.go` 的采集、旧版备份转换、恢复落库。漏一处的表现是「还原之后所有渠道全退回默认推送」，用户的隔离配置一次备份往返就没了。恢复口对非法值一律按 default 落库，**不得让整批恢复失败**。

### 4. Validation & Error Matrix

- `Create` 不带 `push_scope`（老客户端）→ 空串归一成 `default`，与升级前行为一致
- `Create` / `Update` 传 `"bind"` 之类拼错值 → `400`，**不做「就近纠正」**（把 bind 当 default 落库等于反着执行用户意图），且不落库
- `Update` 不带 `push_scope` → 跳过该键，已有值不变
- `Update` 传 `"push_scope": null` → 跳过该键，返回 `200`，已有值不变；同一请求里的其它键仍生效
- `Update` 传数字 / 布尔 / 数组 → `400`
- 广播时库里只有 bound 渠道 → 同步口返回「暂无参与广播的默认推送渠道」；异步口只写 warn 日志，**不退回全量**
- 历史行 `push_scope` 为空串或 `NULL` → 仍参与广播
- 渠道测试按钮（`SendNotificationToChannel`）→ 完全绕过筛选，`push_scope` 不得拦它，否则用户没法验证 bound 渠道的配置

### 5. Good/Base/Bad Cases

- Good：用户建一个「脚本专用」渠道设成 bound，系统告警和其它任务都不会打扰它，只有显式绑定了它的那个任务能发进去。
- Base：老库全部是空串 / `default`，升级后广播行为与升级前逐条一致。
- Bad：广播过滤写成 `push_scope = 'default'`。升级后老库整批静默退出广播，无报错、无日志。
- Bad：`push_scope` 用 `bool`。GORM 零值替换把 bound 翻成 default，用户的隔离被反着执行。
- Bad：`Update` 把缺省当成 default。APP 保存一次就把 Web 上设的 bound 清掉。
- Bad：广播 0 命中时兜底退回全部已启用渠道。功能等于没做，而且用户完全看不出来。

### 6. Tests Required

见 `server/handler/notification_push_scope_test.go`、`server/service/notifier_push_scope_test.go`、`server/database/notify_channel_push_scope_migration_test.go`：

- `TestNotificationBroadcastOnlyHitsDefaultPushScopeChannels`（核心验收：广播不碰 bound）
- `TestNotificationSendTargetsBoundChannelExplicitly`（定向必须忽略 push_scope，少了它 bound 就是死渠道）
- `TestNotificationBroadcastIncludesLegacyBlankPushScopeRow`（锁死「不等于 bound」而不是「等于 default」，含空串与 `NULL` 两种历史形态）
- `TestNotificationTestButtonWorksForBoundChannel`
- `TestNotificationSendRejectsBlankChannelTargets`（点名了渠道却没有有效 ID 时 `400`，不退化成广播）
- `TestUpdateNotificationChannelKeepsPushScopeWhenFieldAbsent`（缺席与显式 `null` 都不清值；非法值与非字符串类型仍 `400`）
- `TestCreateNotificationChannelHandlesPushScope`
- 修改后至少运行：

```bash
cd server
go test ./handler ./service ./database -run "PushScope|TestNotificationSend|TestNotificationBroadcast" -count=1
go test ./...
```

> **突变验证**：把 `loadEnabledNotificationChannels` 的广播条件改成 `push_scope = 'default'`，`TestNotificationBroadcastIncludesLegacyBlankPushScopeRow` 必须变红；把定向分支也加上 push_scope 过滤，`TestNotificationSendTargetsBoundChannelExplicitly` 必须从另一个方向再红一次。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：等值比较。空串 / NULL 的历史行会静默退出广播，升级后「一条通知都收不到」且零线索。
query = query.Where("push_scope = ?", model.NotifyPushScopeDefault)
```

```go
// 错误：把缺席（以及显式 null）当成 default，APP 一次保存就清掉用户设的 bound。
updates["push_scope"] = model.NotifyPushScopeDefault
```

#### Correct

```go
// 正确：只排除明确写着 bound 的行，并用 COALESCE 兜住 NULL。
query = query.Where("COALESCE(push_scope, '') <> ?", model.NotifyPushScopeBound)
```

```go
// 正确：null 视为「未提供」直接跳过；其余非字符串类型仍然 400。
if v == nil {
    continue
}
raw, ok := v.(string)
if !ok {
    response.BadRequest(c, "推送范围必须是字符串")
    return
}
```

---

## 场景：Magisk 模块版的部署类型与在线升级

### 1. Scope / Trigger

- 触发：修改 `server/handler/system_update_magisk.go`、`system_update.go` 的部署类型判定与分派、`server/handler/system.go` 的 `Info` / `Restart` / `StopPanel`、任何 `Magisk/*.sh`，或前端 `OverviewHeroCard.vue` / `UpdateProgressDialog.vue` / `useSettingsOverview.ts` 里与 `deployment_type` 有关的分支时必须看本节。
- 原因：模块版的文件分布在**四个互不相同的位置**，任何只改其中一处的升级实现都会在下一次开机被静默回滚：

| 位置 | 路径 | 在线升级能不能改到 |
|---|---|---|
| 模块本体（宿主 Android 侧） | `/data/adb/modules/daidai-panel/` | 能（best-effort，只写三样） |
| 容器 rootfs | `/data/daidai` 或 `/data/local/daidai` | 不动 |
| 面板实际运行的文件（容器内） | `/usr/local/bin/daidai-server`、`/app/web`、`/app/Dumb-Panel` | 能 |
| 宿主持久目录 | `/data/adb/daidai-panel/`（`ports.conf`、`service.log`、`deps-snapshot/`、`stopped`、`watchdog.gen`） | 不动 |

`Magisk/service.sh` 每次开机把第 1 处拷进第 3 处。所以在线升级必须**两处都写**。
**模块脚本（`*.sh`）本身在线升级永远改不到** —— 由模块脚本实现的能力只能靠重刷 zip 获得，这一点必须在 UI 提示、README、release notes 三处如实说明，不要写成「外壳有变更会被自检拦下」。

### 2. Signatures

- 部署类型常量：`panelUpdateDeploymentMagisk = "magisk"`
- 运行态判定：`func isMagiskPanelUpdateRuntime() bool`
- 方案构建：`func buildMagiskPanelUpdatePlan(release *panelReleaseInfo) (*panelUpdatePlan, error)`
- 未命中哨兵：`var errMagiskRuntimeNotDetected = errors.New(...)`
- 执行入口：`func executeMagiskPanelUpdateWithOptions(plan *panelUpdatePlan, options panelUpdateExecutionOptions)`
- 面板进程定位：`func findMagiskPanelServerPID() int`
- 外壳版本：Go `const currentMagiskShellVersion`（= service.sh 当前 export 的值）与 `const requiredMagiskShellVersion`（在线升级放行的最低值）↔ shell `export DAIDAI_MAGISK_SHELL_VERSION`
- plan 新增字段：`DataDir` / `WebDir` / `ModuleDir`
- 升级窗口哨兵：`<DataDir>/.updating`（Go 常量 `magiskUpdatingSentinelName` ↔ service.sh 的 `UPDATING_FLAG`）
- 手动停止开关：`/data/adb/daidai-panel/stopped`（Go 常量 `magiskStopFlagPath` ↔ 四个 shell 的 `STOP_FLAG` / `$PERSIST_DIR/stopped`）
- 守护代次标记：`/data/adb/daidai-panel/watchdog.gen`（Go 常量 `magiskWatchdogGenName` ↔ service.sh 的 `WATCHDOG_GEN_FILE`）
- 停止接口：`func (h *SystemHandler) StopPanel(c *gin.Context)` + `func writeMagiskStopFlag() error` + `const magiskStopSupportedShellVersion`
- 进程退出注入点：`var panelProcessExit` / `var panelProcessExitDelay`（Restart 与 StopPanel 共用，仅为可测）

### 3. Contracts

- **判定顺序固定**：Watchtower → **magisk** → Docker → binary。magisk 必须排在 Docker 探测之前，否则模块版会拿到「未提供 Docker CLI，请配置 Watchtower」这段与 Android 完全无关的报错（`buildPanelUpdatePlanForRelease` 会把 Docker 与 binary 两段错误拼起来抛出，用户看到的第一句必然是 Docker 那句）。
- **只有 `errMagiskRuntimeNotDetected` 才继续往下走**。模块版自身的失败（例如外壳版本过旧）必须原样抛给用户，用 `errors.Is` 判定，不要包装它。
- **升级范围严格限定三样**：`daidai-server`、`ddp`、前端目录。容器 rootfs、apt/apk 系统包、Python venv、`config.yaml`、`ports.conf` 一概不动。更新包里自带的 `config.yaml` 必须跳过——它会覆盖模块生成的端口配置。
- **进程路径与名字必须是 `/usr/local/bin/daidai-server`**（这条 argv0 是三方共同依赖的契约）：容器启动脚本用 `pgrep -f /usr/local/bin/daidai-server` 去重；`service.sh` 的守护（`panel_is_running`）与 `action.sh` 的 `panel_pids` 逐个读 `/proc/<pid>/cmdline` 按这条前缀匹配；Go 侧 `findMagiskPanelServerPID` 拿 argv0 与 `magiskPanelBinaryPath` 做全等比较。改名会让下次开机再拉起一个实例抢同一个端口，也会让动作按钮永远探不到面板、停止功能直接失效。
  - 注意 `action.sh` / `service.sh` **不用 `pkill -f daidai-server` 停面板**：执行它的 `sh -c` 自身 cmdline 就含这串字符、会被自己命中。停止路径必须先探到 PID 再 `kill -TERM` / `kill -KILL`。
- **启动前必须 `cd` 到数据目录**。否则 `appboot.ResolveConfigPath()` 四个候选全落空，`main.go` 直接 `log.Fatalf`。
- **二进制必须 rename 覆盖**，不能直接写正在执行的文件（`ETXTBSY`）。
- **前端换完必须重启进程**，不能指望热生效：`main.go` 只在启动时对白名单里的几个子目录调 `engine.Static`（`assets` / `fonts` / `sponsor-portal`；v3.2.0 前还有一个 `monaco`，随编辑器换成 CodeMirror 6 删掉），新版 dist 多出顶层目录时不重启会走 SPA fallback，静态资源等于坏掉。
  - v3.2.2 把 Monaco 作为可切换的第二引擎加了回来，**白名单不用动、后端零改动**：新方案是裁剪 ESM + 动态 `import()`，chunk / worker / 图标字体全部落在 `dist/assets/` 下，已被 `assets` 覆盖。这里的判据始终是「**新版 dist 有没有多出顶层目录**」，不是「前端引入了哪个库」——升级前端时按目录清单对一遍即可，不必逐个依赖去猜。
- **运行态判定只校验目录、不校验文件名**：`ddp` 装在 `/usr/local/bin/ddp`，写死 `daidai-server` 会让 CLI 分支全成死代码。相应地 plan 必须记录真正的面板 PID，CLI 发起时由 helper 显式 `kill -TERM`，且此时**不得**自杀（会截断 CLI 输出）。
- **`os.Executable()` 要剥掉 `" (deleted)"` 后缀**：二进制被替换后 `/proc/self/exe` 会带这个后缀。
- **外壳版本是两个常量，别当成一个**：
  - `DAIDAI_MAGISK_SHELL_VERSION`（service.sh）↔ `currentMagiskShellVersion`（Go）：**每改一次 `Magisk/*.sh` 或 rootfs 结构就一起加一**，`magisk_assets_test.go` 静态断言两者逐字相等。它只描述「仓库里的外壳长什么样」。
  - `requiredMagiskShellVersion`（Go）：在线升级放行的**最低**外壳版本，**只有当新面板无法在旧外壳上运行时才提**。提了就意味着所有还在跑旧外壳的用户必须先手动重刷一次模块 zip 才能继续在面板内一键升级——这是很贵的操作，不要因为「改了 shell」就顺手提。
  - 不变式：`requiredMagiskShellVersion <= currentMagiskShellVersion`（有测试钉死）。反过来会让刚打出来的 zip 装上去就被自己的外壳自检拦住。
  - 外壳只是多了增量能力时（例如 v2 的手动停止），保持 required 不动，改由**接口层 + 前端按外壳版本 gating** 并提示重刷 ZIP，提示里必须带上实际外壳版本号。
  - ⚠️ 版本门禁本身有 off-by-one：`resolveMagiskShellVersion` 读的是**当前进程**的 env，而门禁在**发起升级的旧进程**里执行，所以提高 required 也挡不住本次升级、只挡下一次。别把它当成能拦住「本次」的手段。
- **手动停止开关（v2 外壳）的四条契约**：
  - 路径固定 `/data/adb/daidai-panel/stopped`。**绝不能**放进 `$rootfs/app/Dumb-Panel/`：那里的 `.updating` 每次开机被无条件删除，同目录的跨重启标记迟早被同类清理误伤；rootfs 重装还会整体删除它。
  - `service.sh` 的早退点必须在「模块→容器条件同步 + deps 回填」**之后**、「进容器拉起面板」**之前**。放太靠前 → 停止状态下刷入新 zip 再重启，新二进制同步不进容器，点启动跑的还是旧版本，表现成「刷了新版但版本号没变」。
  - `uninstall.sh` 必须**无条件**删除停止开关与守护代次标记（放在 `.keep_on_uninstall` 判断之外）。落进 KEEP 分支的话，「停止 → 保留数据卸载 → 重装」会得到一个永远起不来的新模块，且零线索。`customize.sh` 安装收尾同样无条件清掉开关：「刚装完的模块必须能起来」优先级更高。
  - `/system/restart` **绝对不能**写这个开关。restart 的语义是「重来一次」，写了开关就变成永久停机，而此时 Web 已经没了，用户在面板上再无自救手段。这条有回归测试（`system_stop_panel_test.go`）。
- **守护子 shell 必须有代次去重**。`service.sh` 只对 `daidai-server` 做了 pgrep 去重，对自己 fork 的守护没有任何去重手段；文档与 `action.sh` 又在教用户重跑 `service.sh`。做法是 fork 前写 `watchdog.gen`，守护每轮比对、值变即自退。结束守护**不能**用 `pkill -f service.sh`（会误杀正在执行的 service.sh 本身）。
- **`service.sh` 的模块→容器同步必须是条件覆盖**（模块内文件更新才 cp）。这是模块目录写不进去时（KernelSU 下分区可能只读）唯一的防回滚保险。`-nt` 不被支持时必须回落成无条件同步——宁可丢一次在线升级，也不能让刷入新模块后同步不进容器。
- **构建方案失败时也要回填 `deployment_type`**（`detectPanelDeploymentTypeHint`）。否则前端只能看到空对象，会退回到「请在宿主机执行 docker compose pull」那句兜底，对 Android 模块版和裸机二进制部署都是误导。

### 4. Validation & Error Matrix

- 非模块版 -> `errMagiskRuntimeNotDetected`，继续走 Docker / binary，**不算升级失败**。
- `DAIDAI_MAGISK_SHELL_VERSION` 缺失或小于 required -> 不生成 plan，返回「请重新刷入模块 zip」。
- Release 缺少本机架构的 `daidai-linux-<arch>.tar.gz` -> 明确报缺哪个包。
- 更新包里没有 `web/` 目录 -> 直接终止，不做半截替换。
- 模块目录不存在 / 不可写 -> **只告警不中断**，提示「本次升级只在容器内生效」，靠 `service.sh` 的条件同步兜底。
- `module.prop` 缺 `version=` 行 -> 报错（说明模块结构已被改动，不该盲写）。
- helper 启动失败 -> 必须清掉 `.updating` 哨兵，否则存活守护永久不敢接管。

### 5. Good/Base/Bad Cases

- Good：面板内点「立即更新」，几十秒完成，容器与已装依赖不动，不用重启手机；重启后仍是新版本。
- Good：管理器里点动作按钮停止面板，等 3 分钟不自动回来，重启手机仍是停止；再点一次恢复。
- Base：外壳只是多了增量能力时，在线升级照常放行，面板里那项功能显示为禁用并提示「需重刷模块 ZIP（当前外壳版本 N）」。
- Base：新面板确实无法在旧外壳上运行时，面板自检后拒绝在线升级并提示重刷 ZIP。
- Bad：`/system/restart` 顺手写了停止开关 —— 用户点一次「重启面板」变成永久停机，Web 没了，只能去模块管理器抢救。
- Bad：只写容器内路径 —— 下次开机被 `service.sh` 用模块里的旧文件覆盖，升级静默回滚。
- Bad：复用二进制链路 —— `InstallDir` 会是 `/usr/local/bin`，前端落到 `/usr/local/bin/web`（config 里是 `/app/web`），新进程 cwd 错导致找不到 config.yaml 直接 `log.Fatalf`，且进程改名后 pgrep 去重失效。
- Bad：`ddp update` 发起时不找面板 PID —— 会在面板还活着的时候替换二进制并再起一个实例。

### 6. Tests Required

见 `server/handler/system_update_magisk_test.go` 与 `magisk_assets_test.go`：

- helper 脚本必须包含 `TARGET_BIN='/usr/local/bin/daidai-server'`、`mv -f "$TARGET_BIN.new" "$TARGET_BIN"`，且 `cd "$DATA_DIR"` 出现在 `nohup "$TARGET_BIN"` **之前**。
- CLI 场景（`CurrentPID != ServerPID`）：`kill -TERM` → `kill -KILL` → 替换文件，三者顺序不能乱。
- `rewriteMagiskModuleProp` 只改 `version` / `versionCode` 两行，`updateJson`、`id`、`author` 必须原样保留（debian flavor 的 `updateJson` 与 alpine 不同，整体重写会抹平它）。
- `replaceDirAtomically` 必须清掉旧的 hash 产物，且不留 `.new` / `.old` 残留。
- `buildPanelUpdateTarget` 的 magisk 分支不得带出 `image_name` / `container_name`。
- 非模块版必须返回 `errMagiskRuntimeNotDetected` 本身（用 `errors.Is` 判定的前提）。
- `service.sh` 必须包含 `file_needs_sync`、`panel_is_running`、`UPDATING_FLAG`，且 `DAIDAI_MAGISK_SHELL_VERSION` 与 `currentMagiskShellVersion` 逐字一致；同时断言 `requiredMagiskShellVersion <= currentMagiskShellVersion`。
- 停止开关链路（`TestMagiskScriptsShareStopFlagPath`）：Go 常量与四个 shell 的字面量同路径；`service.sh` 的早退点位置（同步之后、拉起容器之前）；`action.sh` 的停/启两条路径且不得出现 `pkill -f service.sh`；`uninstall.sh` 的两条 `rm -f` 排在 `KEEP_FLAG` 分支之后；`customize.sh` 先写开关再 `rm -rf "$rootfs"`、收尾无条件清开关。
- 停止接口行为（`system_stop_panel_test.go`）：`/system/restart` 不写停止开关且退出码仍是 1；`/system/stop` 在模块版 + 外壳 >= 2 时写开关并以 0 退出；非模块版、旧外壳一律 400 且不留文件；`/system/info` 平铺返回 `deployment_type` / `magisk_shell_version` 且老字段位置不变。

> **这些都只是静态字符串断言，防不住 shell 逻辑写错。** 改 `Magisk/service.sh` 后必须真机跑完整回路：装 → 重启 → 面板内升级 → 再重启确认不回滚 → 杀掉面板进程确认自动拉起。Debian flavor 至今没做过真机安装。

### 7. Wrong vs Correct

#### Wrong

```sh
# 错误：POSIX 规定 read 在读到 EOF 而没遇到分隔符时返回非 0，
# 而 /proc/<pid>/cmdline 是 NUL 分隔、不以换行结尾的。
# `|| continue` 会对每个条目都触发，下面的 case 永远执行不到，函数恒返回「未运行」。
# 后果：守护每轮无条件重跑容器启动脚本，把用户改过的 SSH 密码改回默认值、
# 覆盖 config.yaml、放开目录权限，还持续累积 ruri 挂载 —— 全程静默。
read -r proc_cmdline < "$proc_dir/cmdline" 2>/dev/null || continue
case "$proc_cmdline" in
  /usr/local/bin/daidai-server*) return 0 ;;
esac
```

```sh
# 错误：无条件覆盖，会把面板内在线升级的结果在下次开机悄悄回滚掉。
cp -f $MODDIR/system/bin/daidai-server $rootfs/usr/local/bin/daidai-server
```

#### Correct

```sh
# 正确：不判 read 的退出码，先清空变量再读，然后直接判断内容。
proc_cmdline=""
read -r proc_cmdline 2>/dev/null < "$proc_dir/cmdline"
case "$proc_cmdline" in
  /usr/local/bin/daidai-server*) return 0 ;;
esac
```

```sh
# 正确：只有模块里的文件确实更新（或容器里没有）才同步。
if file_needs_sync "$MODDIR/system/bin/daidai-server" "$rootfs/usr/local/bin/daidai-server"; then
  cp -f "$MODDIR/system/bin/daidai-server" "$rootfs/usr/local/bin/daidai-server"
fi
```

---

## 场景：订阅同步按脚本认任务，已有任务一律不动（#125，取代订阅锁）

### 1. Scope / Trigger

- 触发：修改 `server/service/subscription.go` 的 `syncSubscriptionTasks` / `scanSubscriptionTaskCandidates`、
  `server/service/subscription_task_script_match.go`、执行器 `script_runner.go` 的 `classifyCommandRunner` /
  `splitCommandTokens`，或者改任务命令的格式时，必须看本节。
- 背景：v3.0.5 起用「订阅锁」`subscription_locked` 挡住同步覆盖用户手改的名称/定时，但匹配仍按命令原文：
  命令一加参数（`task x.js now`、`task x.js desi JD_COOKIE`）就对不上 → 新建一条重复任务；
  开着自动删除时，还会把改过命令的那条连历史日志删掉（#125）。
  #125 起改成「按脚本认任务、认出就一律不动」，订阅锁整套下线。

### 2. Signatures

- 同步入口：`syncSubscriptionTasks(sub *model.Subscription, emit PullCallback)`
- 扫描：`scanSubscriptionTaskCandidates(sub, options) subscriptionCandidateScan`，比旧版多给 `seenFiles`（本次读到的全部文件）。
  `collectSubscriptionTaskCandidates(sub, options) (map[string]subscriptionTaskCandidate, []string)` 签名不变，是它的包装（有测试直接调）。
- 字典与求键（`subscription_task_script_match.go`）：`newSubscriptionScriptIndex(scriptsDir, candidates)`、
  `normalize(ref)`、`candidateKey(command)`、`scriptKeyOf(command)`、`taskCommandScriptRefs(command)`。
- 删除判定：`newSubscriptionStaleTaskJudge(index, candidates, saveDir, seen).judge(command) (verdict, script, statErr)` →
  `staleTaskKeep` / `staleTaskKeepUnscanned` / `staleTaskKeepStatError` / `staleTaskDelete`（`script`、`statErr` 只给日志用）。
  判定里的 Stat 是包级变量 `subscriptionScriptStat = os.Stat`（只为单测能构造 EACCES、网络盘错误码）；「这个前缀不是脚本」由
  `subscriptionScriptNotAScript(err)` 判，只收「不存在」类 `subscriptionScriptMissing(err)` 与名字类 `subscriptionScriptBadName(err)`
  （平台相关部分 `subscriptionScriptBadNamePlatform`，build tag 分文件）；其余 Stat 错误都让删除退成保留，**不设白名单**。
- 条件删除：`deleteSubscriptionTaskIfUnchanged(task *model.Task) (removed bool, err error)`。
- 条件解除关联：`detachSubscriptionTaskIfUnchanged(task *model.Task, label string) (detached bool, err error)`（新旧标签相同 → 直接 `(false, nil)`）；
  配套 `otherLiveSubscriptionLabels(selfID uint) ([]string, error)`、`usedByOtherSubscription(task, otherLabels) bool`、
  `hasLabelFold(labels, target) bool`（忽略大小写，与 `queryTasksByLabel` 的 LIKE 对齐）、`withoutLabel(labels, target)`（`withLabel` 的反操作，也忽略大小写）。
- 首词判定：`classifyCommandRunner(first string) commandRunnerKind`，执行器 `ParseCommandExecutionPlan` 与同步共用。
- 已下线：`model.Task.SubscriptionLocked` 与 `ToDict` 的 `subscription_locked`、`PUT /api/v1/tasks/:id/restore-subscription-default`、
  `database.unlockNonSubscriptionTasks`、`handler` 的加锁推导。库里的 `subscription_locked` 列**保留、不读不写、不 DROP**（旧版本回退照常用）。

### 3. Contracts

- **字典键**：候选命令去掉 `task ` 后归一化。相对路径拼到 `Abs(ScriptsDir)` 下，绝对路径直接求相对路径；
  落在目录外的绝对路径，解析**所在目录**的软链接后对 `Real(ScriptsDir)` 再求一次（Docker `/ql/data/scripts`、`/ql/scripts`、
  `/ql/data/repo` 别名，见 `docker/entrypoint.sh`；只解析目录不解析文件，仓库里的文件软链接保持自己的键）；
  正斜杠；`.`、`..`、`../` 开头的丢弃；**只在 Windows 上**转小写（Linux 上反斜杠是文件名字符，与执行器一致）。
  Windows 上大小写冲突的键标为歧义：不参与认领，只用于删除时的保留。
- **任务的键**：纯文本，除上面的别名兜底外零 I/O；首词用 `classifyCommandRunner`，不另写解释器列表：
  `task` / `desi` 跳过开头的 `-l`、`-m <值>`，按 `--` 切断，取字典里命中的最长前缀；
  解释器 / 托管命令从第 2 个 token 起跳过 `-` 开头的 token，取第一个命中的最长窗口（`python -m <模块>` 没有键）；其它首词没有键。
  **每条任务至多一个键；判据是「在候选集里」，不是「文件存在」。**
- **认出就一律不动**：本订阅已有任务的命令与候选完全相同，或键相同 → 不新建，不改名称、定时、命令、状态、日志；
  同一脚本有多条任务也都不动。想恢复订阅默认 → 删掉任务重新拉取。
  `task x.js now extra` 这类跑不起来的命令也算已存在（有意的取舍）：不替用户改命令，失败会在任务日志里暴露。
- **接管**：非本订阅托管、但命令与候选相同或键相同的任务（无标签、删订阅再重建留下的悬空旧标签、别的订阅）
  只加本订阅标签，其余字段不动，不再加锁；有几条接管几条，每条一行 `[关联已有任务]`；本订阅已有同脚本任务时不接管。
  候选池全表加载后在 Go 里过滤，**不写 `NOT IN`**。
- **自动删除**（开关语义不变，只看 `task ` 开头的命令）：
  1. 本次候选为空 → 熔断，整段跳过；有托管任务时打 `[跳过自动删除] 本次没有识别到任何候选脚本…`。
  2. 命令与候选完全相同或键命中 → 保留。
  3. 否则取命令引用的脚本（`taskCommandScriptRefs`：带受支持扩展名、在脚本目录内的前缀，从长到短取第一个真实存在的，与执行器同口径）：
     在当前 SaveDir 内、文件存在、但 `seenFiles` 里没有 → 保留，并打 `[保留任务] <名>：脚本 <路径> 还在订阅目录里，但本次扫描没有读到…`；
     其余（文件已不存在、被白/黑名单或依赖/辅助脚本规则排除、在当前 SaveDir 外）→ 删。
     提取不出脚本路径的命令（托管可执行命令如 `task dailycheckin`、引号未闭合）也按失效处理，与改动前一致。
  4. **Stat 错误反过来判：只有「不存在」类与名字类算「这个前缀不是脚本」，其余一律让删除退成保留**（`subscriptionScriptNotAScript`）。
     - 不是脚本（continue 试更短的前缀，都不在则按删除规则删）：`fs.ErrNotExist` / `ENOTDIR`（`subscriptionScriptMissing`，
       ENOTDIR 必须单列，Go 在 Unix 上只把 ErrNotExist 映射到 ENOENT）；名字类 `subscriptionScriptBadName`——通用的
       `ENAMETOOLONG` / `ELOOP` / `EINVAL`，加 Windows 的 ERROR_INVALID_NAME(123) / ERROR_BAD_PATHNAME(161) /
       ERROR_FILENAME_EXCED_RANGE(206) / ERROR_DIRECTORY(267) / ERROR_CANT_RESOLVE_FILENAME(1921)（平台相关的错误码用 build tag 分文件：
       `subscription_task_stat_windows.go` / `subscription_task_stat_other.go`）。参数里带 URL / 盘符 / `? * | < >` / 超长段 / 软链接环时命中，
       这些前缀根本不可能是一个文件。
     - 其余一切 Stat 错误都可能盖住一个真实文件：权限类（EACCES / EPERM / ERROR_ACCESS_DENIED）、IO / 网络类（EIO / ESTALE /
       EHOSTDOWN / ECONNRESET，Windows 网络盘的 ERROR_UNEXP_NET_ERR(59) / ERROR_NETNAME_DELETED(64) / ERROR_SEM_TIMEOUT(121) /
       ERROR_IO_DEVICE(1117)，ERROR_SHARING_VIOLATION），以及没见过的错误码。PUID 降权运行、NFS root_squash、SMB / CIFS 断连、
       存储异常、文件被独占打开会命中：ref 在当前 SaveDir 内的先记下、继续试更短的前缀；一个真实存在的都没找到 →
       `staleTaskKeepStatError`，保留并打 `[保留任务] <名>：无法确认脚本 <路径> 是否还在（<错误原文>），未删除…`。
     - **不许改回白名单**（只列「可能盖住真实文件」的错误、其余当不是脚本）：IO / 网络类错误开放、平台相关、列不全，漏一个就是
       连日志删掉一个还在的任务；名字类是封闭的一小撮。Go 为 Windows 定义的 `syscall.EIO` / `ESTALE` / `ETIMEDOUT` / `ENOTCONN` /
       `ENAMETOOLONG` / `ELOOP` 是自造值（`APPLICATION_ERROR` 起，见 `syscall/zerrors_windows.go`），`os.Stat` 永远不会返回；
       Windows 上的判定只能写真实错误码的数值（syscall 包没导出这几个具名常量）。
     - Go 自己把 Windows 的 ERROR_BAD_NETPATH(53) 也映射成 `fs.ErrNotExist`（`syscall.Errno.Is`），这个网络类错误码因此按「不存在」处理，
       与 fix-r1 相同。
     - 执行器 `resolveCommandScriptPath` 走 `ResolveWithinBase(mustExist=true)`：对任何 Stat 错误都报错、退到更短的前缀
       （`findTaskScriptTarget` 只保留能解析成功的最长前缀），judge 与它选同一个前缀；差别只在「一个前缀都解析不出」时——
       执行器运行期报错，这里是删除、要正面证据，于是保留而不删。
  5. **任务还被其他仍存在的订阅使用 → 只解除关联、不删**：判为删、但标签里还有别的订阅的 `subscription:<id>`
     （每次同步查一次 `subscriptions` 表的 id，用 `hasLabelFold(labels, subscriptionTaskLabel(id))` 忽略大小写判定，与 `queryTasksByLabel`
     的 LIKE 同口径——用户手写成 `Subscription:2` 时两边一致，否则会误删别的订阅在用的任务）→
     不删行、不动日志，只按条件摘掉本订阅的标签（`withoutLabel` 也忽略大小写；`UPDATE tasks SET labels=? WHERE id=? AND command=? AND labels=?`，
     要求 `RowsAffected==1`），打 `[解除关联] <名>：仍被其他订阅使用，只移除本订阅的标签，未删除`；删不删交给那个订阅自己的同步。
     场景：跨订阅接管（共用 SaveDir、单文件订阅的 SaveDir 设在别的仓库里）之后，A 加黑名单或挪走 SaveDir。
     **只看仍然存在的订阅**：已删订阅留下的悬空旧标签（E9）不挡删除。读订阅列表失败 → 整段跳过删除（`[跳过自动删除]`，计入失败）。
     快照过期（标签或命令被改、已被删）→ 不改 + `[保留任务] …同步期间…`；标签本就不带（`detach` 新旧相同）→ 静默、不刷屏；出错 → `failed++` + `[解除关联失败]`。
     汇总 `[共解除关联 N 个任务]`；解除关联也算变更（不再打 `[同步完成] 本次未对定时任务做任何变更`）。
- **条件删除**：一个事务里先删该任务的 `task_logs`，再 `DELETE FROM tasks WHERE id=? AND command=?`；
  `RowsAffected≠1` 或任一步出错就回滚（日志原样恢复）；提交后再 `RemoveJob`。
  **顺序不能反**：`task_logs` 对 `tasks` 有外键（`PRAGMA foreign_keys=ON`），先删任务会 `FOREIGN KEY constraint failed`，
  每一条有历史日志的失效任务都删不掉。
  任一步出错 → `failed++` + `[自动删除任务失败]`；快照过期（命令被改、已被删）→ 不删 + `[保留任务] …同步期间…`。
  事务内只能用 `tx`（`database` 的连接池只有 1 个连接）。
- **autoAdd / autoDelete 是三态**（inherit / enabled / disabled），见 `getSubscriptionTaskSyncOptions` →
  `resolveSubscriptionAutoAddTask` / `resolveSubscriptionAutoDelTask`：订阅自己设了就用订阅的，inherit 才跟随全局
  `auto_add_cron` / `auto_del_cron`。旧文档里「`autoAdd` 是 `sub.AutoAddTask || 全局`（OR）」早已过时。
- `force_overwrite` / `overwrite_mode` 与本机制正交：只作用于 git 工作区文件（`reset --hard` vs `stash`），从不写任务表。

### 4. Validation & Error Matrix

| 情形 | 结果 | 日志 |
|---|---|---|
| 命令与候选完全相同 | 不动 | — |
| 改成 now / desi / conc / `--` / `-m` / `-l` / `./`、引号、多空格、`desi X ENV`、`node X`、`python3 X`、目录内绝对路径、Docker 别名路径；Windows 正斜杠 / 大小写 | 不动、不新建、不删 | — |
| 上游改了名称或 cron | 已有任务不变 | 不再有 `[自动更新任务]` |
| 同一脚本多条任务；复制任务再改副本 | 都不动、不新建 | — |
| a.js 的任务改成跑 b.js | 该任务不动；a.js 新建一条 | `[自动添加任务]` |
| 未托管的同脚本任务（无标签 / 悬空旧标签） | 只加标签 | `[关联已有任务]` |
| 上游删了脚本（规范命令、改过参数的都算） | 按开关删，连日志 | `[自动删除任务]` |
| 黑名单排除、改 SaveDir（文件还在盘上） | 按开关删 | `[自动删除任务]` |
| 候选为空（检出为空、单文件订阅 SaveDir 为空） | 一条不删 | `[跳过自动删除]` |
| 脚本在当前 SaveDir 内、文件在、扫描没读到（目录联接、NAS / Magisk） | 保留 | `[保留任务] …本次扫描没有读到…` |
| `node X` 的脚本从订阅里消失 | 保留（不在删除范围） | — |
| 快照之后命令被改 / 删任务出错 | 不删；日志不动 | `[保留任务] …同步期间…` / `[自动删除任务失败]` |
| 判为删、但任务还带着别的仍存在订阅的标签（跨订阅接管后 A 加黑名单 / 挪走 SaveDir；单文件订阅挪走 SaveDir） | 不删；只摘掉本订阅的标签，名称、定时、命令、日志不动 | `[解除关联] <名>：仍被其他订阅使用…` / `[共解除关联 N 个任务]` |
| 失效任务只多带了已删订阅的悬空旧标签 | 按开关删，连日志 | `[自动删除任务]` |
| 解除关联时快照过期（标签或命令被改）/ 出错 | 标签不动、不删 | `[保留任务] …同步期间…` / `[解除关联失败]` |
| 别的订阅的标签只差大小写（用户手写 `Subscription:2`） | 不删；只解除关联（`hasLabelFold` 认它归别的订阅） | `[解除关联] <名>…` |
| 本订阅自己的标签只差大小写 | 一次摘掉（`withoutLabel` 忽略大小写），下轮不再空转 | `[解除关联] <名>…`（仅一次） |
| 读订阅列表失败 | 一条不删，计入失败 | `[跳过自动删除] 读取订阅列表失败…` |
| 脚本在当前 SaveDir 内，Stat 报的错误不是「不存在」类、也不是名字类（EACCES / EPERM / EIO / ESTALE / EHOSTDOWN / ECONNRESET；Windows 的 ERROR_ACCESS_DENIED / ERROR_SHARING_VIOLATION / 网络盘 59 / 64 / 121 / 1117；没见过的错误码），且没有更短的前缀真实存在 | 保留 | `[保留任务] <名>：无法确认脚本 <路径> 是否还在（<错误原文>）…` |
| Stat 报「不存在」类（ENOENT / ENOTDIR / ErrNotExist；Windows 上 Go 把 ERROR_BAD_NETPATH 也归到这里） | 算文件不在，按删除规则处理 | `[自动删除任务]` |
| Stat 报名字类错误（ENAMETOOLONG / ELOOP / EINVAL；Windows 的 ERROR_INVALID_NAME / ERROR_BAD_PATHNAME / ERROR_FILENAME_EXCED_RANGE / ERROR_DIRECTORY / ERROR_CANT_RESOLVE_FILENAME：参数带 URL / 盘符 / `? * \| < >` / 超长段 / 软链接环） | 不是脚本，退到更短前缀；都不在则按删除规则删 | `[自动删除任务]` |
| Linux 上 `task REPO/A.JS`（只差大小写） | 认不出：新建规范任务，旧任务按删除规则处理 | 与改动前一致 |

### 5. Good/Base/Bad Cases

- Good：用户把 `task repo/a.js` 改成 `task repo/a.js desi JD_COOKIE 1-3`，之后拉取多少次都还是这一条，ID、命令、名称、定时、日志都不变。
- Base：新脚本照常新建；上游删脚本按开关删；黑名单排除、改 SaveDir 照删。
- Bad：按命令原文匹配——#125 本身。
- Bad：判据写成「文件存在 / 能执行就算已有」——黑名单排除、改 SaveDir 之后旧任务再也删不掉。
- Bad：删除只看「不在候选里」——检出为空、目录联接、NAS 读目录异常时，整个订阅的任务连日志被删（E4/E5/E6d）。
- Bad：先删日志再删任务，但不在同一事务里、不查错误——删任务失败时日志已经没了（E7）。
  「先删日志」这个顺序本身是外键所迫、没有错；反过来先删任务会 `FOREIGN KEY constraint failed`。
- Bad：判为失效就直接删，不看任务是否还带着别的、仍存在订阅的标签——跨订阅接管之后，A 加黑名单或挪走 SaveDir
  会把 B 还在用的任务连日志删掉，B 下次只能按上游默认值重建（review-r1 F1）。
- Bad：`os.Stat` 报任何错都当「文件不在」——EACCES / EIO / ESTALE 下脚本其实还在，任务连日志被删（review-r1 F2）。
- Bad：把任何非「不存在」错都当「无法确认」保留、不单列名字类——命令参数里带 URL / 盘符 / `? * | < >` / 超长段的失效任务永远删不掉，
  还每次拉取误报「可能是权限不足或存储异常」（review-r2 R2-1）。
- Bad：改成白名单，只把权限 / EIO / ESTALE / 断连这些「可能盖住真实文件」的错误当保留，其余当「不是脚本」——Go 为 Windows 定义的
  `syscall.EIO` 这类是自造值、`os.Stat` 永远不会返回，白名单在 Windows 上只剩 ERROR_ACCESS_DENIED / ERROR_SHARING_VIOLATION：
  SMB / 网络盘报的 59 / 64 / 121 / 1117 等、Linux CIFS 断连报的 EHOSTDOWN / ECONNRESET 全部判删，任务连日志丢失，比 fix-r1 还倒退
  （verify-r3 R3-1）。应反过来，只列封闭的「不存在」类与名字类，其余一律保留。
- Bad：标签比较一边用 LIKE（不分大小写）、一边精确比较——大小写不同的订阅标签会误删别的订阅在用的任务，或每次同步空转一次 `[解除关联]`（review-r2 R2-2）。
- Bad：解除关联后没有 `continue`，落进条件删除——条件删除只比对 id+command、不看 labels，会把还被别的订阅使用的任务连日志删掉（review-r2 R2-3）。
- Bad：再给「认出的任务」加任何回灌（名称、定时、状态）——用户改过的东西又会被悄悄改回去，锁的老问题原样回来。

### 6. Tests Required

- `subscription_task_script_match_test.go`：`scriptKeyOf` / `normalize` 表驱动（task、desi、解释器、托管命令、`-m`、`-l`、`--`、引号、
  带空格路径、目录内外绝对路径、`.bak`、引号未闭合、空串、Windows 反斜杠与大小写、Linux 上反斜杠不算分隔符）；
  Windows 候选大小写冲突；删除判定的「扫描漏读」（人为缺项的 `seen` 集合）；Docker 别名（Linux，Windows 上 `t.Skip`）。
- `subscription_task_sync_existing_test.go`：各种改过的命令 × 自动删除开 / 关 × 两轮；上游改名称 / cron；复制任务；同脚本多条；
  改指向别的脚本；上游删脚本（开 / 关）；黑名单、改 SaveDir；候选为空与单文件订阅；Windows 目录联接做 SaveDir；Docker 别名（Linux）；
  大小写与分隔符；接管（无标签、悬空旧标签、按原文、多条、托管优先）；`node X` 不被删；条件删除（快照过期、删任务失败时日志不动）。
- 跨订阅解除关联（`subscription_task_cross_sub_test.go`）：`TestSyncSubscriptionTasksCrossSubStaleTaskOnlyDetaches`
  （共用 SaveDir，A 接管 B 改过名称 / 定时、有日志的任务后 A 加黑名单 / 改 SaveDir，× 是否改过命令：B 的任务行、名称、定时、命令、日志都在，
  只少了 A 的标签，之后两边再同步都不变）、`TestSyncSubscriptionTasksSingleFileMovedOutOfOtherRepoDetaches`（单文件订阅 SaveDir 设到 B 的仓库再挪走）、
  `TestSyncSubscriptionTasksDanglingLabelDoesNotBlockDelete`（悬空旧标签照删）、`TestDetachSubscriptionTaskIfUnchangedSkipsChangedTask`
  （快照过期：标签或命令被改都不动）、`TestSyncSubscriptionTasksReportsFailedDetach`（出错计入失败、任务与日志都在）。
  标签大小写（R2-2）：`TestSyncSubscriptionTasksCaseVariantOtherLabelOnlyDetaches`（别的订阅标签只差大小写 → 只解除关联、不删）、
  `TestSyncSubscriptionTasksCaseVariantOwnLabelDetachesWithoutSpin`（本订阅标签只差大小写 → 一次摘掉、下一轮不空转）。
- Stat 错误（F2、R2-1、R3-1）：`TestSubscriptionStaleTaskJudgeStatErrorIsNotAbsence`（替换 `subscriptionScriptStat`：EACCES / EIO / ESTALE → 保留；
  Windows 网络盘的 59 / 64 / 121 / 1117 → 保留（`unsure_windows_*`，非 Windows 下 `t.Skip`）；EHOSTDOWN / ECONNRESET → 保留（`unsure_unix_*`，Windows 下 `t.Skip`）；
  ENOENT / ENOTDIR / ErrNotExist → 删；名字类 ENAMETOOLONG / ELOOP / EINVAL → 删；Windows 的 123 / 161 / 206 / 267 / 1921 → 删
  （`not_a_script_windows_*`，非 Windows 下 `t.Skip`）；最长前缀报名字类、脚本本身报 IO 错误 → 按脚本本身保留（`bad_name_prefix_then_unsure_script`）；
  SaveDir 外照删；更长前缀报错、更短前缀存在按更短的判；真实 ENOTDIR）；
  `TestSyncSubscriptionTasksUnreadableSubdirKeepsTask`（Linux 非 root，子目录 chmod 0311 / 0000；root 与 Windows 下 `t.Skip`）；
  `TestSyncSubscriptionTasksUrlOrDriveArgFollowsSwitch`（Windows：参数带 URL / 盘符，脚本在时保留、上游删后按开关删含日志；Linux 下 `t.Skip`）、
  `TestSyncSubscriptionTasksLongArgFollowsSwitch`（Linux：超长参数段 ENAMETOOLONG，删脚本后按开关删；Windows 下 `t.Skip`）。
- Linux 专属用例在 WSL 里跑交叉编译的测试二进制（`GOOS=linux go test -c`）；chmod 用例要以非 root 身份跑
  （`setpriv --reuid=65534 --regid=65534 --clear-groups`，`TMPDIR` 指向 nobody 可写的目录）。
- 改 `classifyCommandRunner` 时跑 `script_runner` 相关测试，执行器行为必须逐字节不变。

### 7. Wrong vs Correct

#### Wrong

```go
// 按命令原文认任务：命令一加参数就对不上 → 新建重复任务；
// 自动删除再把改过命令的那条连日志删掉：不看它是否还被别的订阅使用，
// 日志与任务分两步删、不在同一事务里、不查错误（删任务失败时日志已经没了）。
if existing, ok := managedByCommand[command]; ok { /* 回灌名称/定时 */ }
// ...
if _, ok := candidates[command]; !ok {
    database.DB.Where("task_id = ?", task.ID).Delete(&model.TaskLog{})
    database.DB.Delete(&task)
}
```

#### Correct

```go
// 认出就不动：命令原文相同，或跑的是同一个脚本。
if managedCommands[command] {
    continue
}
if key, ok := scriptIndex.candidateKey(command); ok && managedKeys[key] {
    continue
}
// ... 自动删除：候选为空先熔断；删除要有正面证据（Stat 只有「不存在」类与名字类算不是脚本，
// 其余 Stat 错误都保留，见 subscriptionScriptNotAScript）；
// 还被别的、仍存在的订阅使用 → 只解除关联；否则按快照条件删，删不到就回滚（日志随之恢复）。
verdict, scriptPath, statErr := judge.judge(task.Command) // 后两个只在保留判定时用于日志
if verdict == staleTaskDelete {
    if usedByOtherSubscription(task, otherLabels) { // hasLabelFold：与 queryTasksByLabel 的 LIKE 同口径，忽略大小写
        changed, err := detachSubscriptionTaskIfUnchanged(task, label) // UPDATE … WHERE id=? AND command=? AND labels=?
        // err → failed++ + [解除关联失败]；!changed → 快照过期时 [保留任务] …同步期间…，标签本就不带则静默；否则 detached++ + [解除关联]
        continue // 解除关联之后绝不能再走删除：条件删除只比对 id+command，不看 labels
    }
    // 事务：先删 task_logs，再 DELETE … WHERE id=? AND command=?；RowsAffected≠1 或出错就回滚
    removed, err := deleteSubscriptionTaskIfUnchanged(task)
    // ...
}
```

---

## 场景：脚本树隐藏名单与启动期隔离名单必须分开

### 1. Scope / Trigger

- 触发：想让某个目录名「在脚本管理里不出现」时必须看本节。
- 原因：`ShouldIgnoreScriptEntryName` 被 `QuarantineUnexpectedScriptEntriesOnStartup`
  复用，命中即 `os.Rename` **物理搬走**。往它的名单里加 `.git`，
  会在「脚本根目录本身是 git 仓库」时把整个仓库搬走。
- 配套阅读：`## 场景：脚本目录污染隔离与 Windows 资源监控`

### 2. Signatures

- 隔离语义（会搬走文件）：`ShouldIgnoreScriptEntryName(name string) bool`
- 隐藏语义（只是不展示 / 不可访问）：`ShouldHideScriptTreeEntryName(name string) bool`
- 逐段路径判定：`ShouldHideScriptTreePath(scriptsDir, targetPath string) bool` /
  `ShouldHideScriptTreeRelativePath(relPath string) bool`

### 3. Contracts

- **两套名单语义不同，绝不可合并**。隐藏名单复合隔离名单（`Ignore || hidden`），
  反向不成立：`ShouldIgnoreScriptEntryName(".git")` 必须**恒为 false**。
- 路径判定必须**逐段遍历**。旧的 `ShouldIgnoreScriptPath` 只判第一段，
  导致 `SmallWorld/.git/**`、`SmallWorld/node_modules/**` 全部漏网。
- 「树里隐藏」与「API 读不到」是**两套独立闸门**，都要接：
  - 展示：`handler/script_file_ops.go` 的 Tree + List
  - 访问：`handler/script.go` 的 `safePath`（13 个入口的唯一收口）
  - 写入：`script_file_mutate.go` 的 `resolveScriptUploadPath` / `validateScriptLeafName` / `copyDir`
  - CLI：`cmd/ddp/script_commands.go` 的 `resolveCLIScriptPath`（管 `script cat` / `script fetch`）
- 名单**硬编码，不做可配置** —— 一旦可配置，用户清空配置就重新打开凭据读取路径。
- **不要一刀切隐藏 dotfile**：`.env` / `.hidden-dir` 必须保持可见，已有回归断言钉死
  （历史见 `docs/release-notes/v2.2.17.md`）。

### 4. Validation & Error Matrix

- `safePath` 命中隐藏段 → 返回「该路径不可访问」错误，13 个入口一并拒绝
- `validateScriptLeafName` 命中 → 拒绝改名成该名字
- `copyDir` 遍历命中 → 跳过，避免产生**看不见的凭据副本**
- `ShouldIgnoreScriptEntryName` 命中 → 启动期 `os.Rename` 搬走（**只用于真正的污染目录**）

### 5. Good/Base/Bad Cases

- Good：`.git` 在树里不出现，`GET /api/scripts/content?path=X/.git/config` 被拒。
- Base：`.env`、`.hidden-dir`、`.github` 仍然可见可读。
- Bad：把 `.git` 加进 `ShouldIgnoreScriptEntryName` —— 脚本根目录是 git 仓库时仓库被搬走。
- Bad：只改 `runScriptList` 不改 `resolveCLIScriptPath` —— `ddp script cat X/.git/config`
  仍直接打印 PAT（CLI 的扩展名闸门对无扩展名文件一律放行）。

### 6. Tests Required

- `ShouldHideScriptTreeEntryName(".git")` 为真，且 `ShouldIgnoreScriptEntryName(".git")`
  **仍为假**（守住 quarantine 不误搬）
- quarantine 不搬走 `.git` 仓库的用例
- `.hidden-dir` / `.env` 可见断言**必须保留**，同用例追加 tree 不含 `.git`
- GetContent / Download / Delete / Copy / CLI 命中隐藏段的拒绝用例

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：直接往隔离名单里加，启动期会把整个 git 仓库 os.Rename 搬走。
func ShouldIgnoreScriptEntryName(name string) bool {
    switch strings.ToLower(name) {
    case "node_modules", "__pycache__", ".git":
        return true
    }
    ...
}
```

#### Correct

```go
// 正确：另起一套隐藏语义，复合隔离名单但不反向污染它。
var hiddenScriptTreeNames = map[string]bool{
    ".git": true, ".svn": true, ".hg": true, ".bzr": true,
}

func ShouldHideScriptTreeEntryName(name string) bool {
    return ShouldIgnoreScriptEntryName(name) ||
        hiddenScriptTreeNames[strings.ToLower(strings.TrimSpace(name))]
}
```

---

## 场景：备份 tar 与还原过滤规则必须对称

### 1. Scope / Trigger

- 触发：给备份打包（`backup_runtime.go` 的 `addDirectoryToTar`）或还原
  （`copyDirectoryContents` / `restoreDirectoryWithStage`）加任何过滤规则时必须看本节。

### 2. Contracts

- 还原链路是 **stage 目录填完后整目录 rename 顶掉 live**，随后 `os.RemoveAll` 旧目录。
- 因此：**备份端不打包 X + 还原端也跳过 X ⇒ 每次恢复备份都会删掉 live 的 X**。
- 对 `.git` 而言，后果是所有 git 订阅退化成 `git init` 重来分支。

### 3. Validation & Error Matrix

- 想让敏感内容不进备份包 → 优先**清洗内容**，而不是排除文件
- 确需排除 → 必须同时保证还原端不会因为跳过它而连带删除 live 副本

### 4. Good/Base/Bad Cases

- Good：tar 仍打包 `.git`，但写入 tar 前剥掉 `.git/config` 里的 `user:token@`，
  **磁盘上的真实文件不动**（动了后续 fetch 就失去鉴权）。
- Base：JSON 备份走 `allowedExts` 白名单，`.git/config` 本就进不去。
- Bad：备份端排除 `.git`、还原端也跳过 `.git` —— 恢复一次备份，git 订阅全废。

### 5. Tests Required

- tar 内 `.git/config` 已脱敏，且 `.git/HEAD` 等其余文件照常打包
- 磁盘上的 `.git/config` 字节与打包前**完全相同**
- 凭据清洗只剥离含冒号的 userinfo（`user:token@`）；
  `ssh://git@host/...` 与 scp 风格 `git@host:repo.git` 的 `git@` 是用户名不是凭据，
  一起剥掉会让还原后的 SSH 订阅连不上

---

## 场景：登录失败响应的机器可读 code 与 4xx 中间态

### 1. Scope / Trigger

- 触发：修改 `server/handler/auth.go` 的登录失败分支、或调整登录相关限流时必须看本节。
- 原因：面板用 **401 承载 2FA / 验证码挑战**，这是「成功语义、4xx 载体」的中间态。
  客户端只要把 4xx 一律当失败抛异常，这个信号就会被吞掉，且 **CI 全绿**——
  服务端测试只测服务端，客户端在独立仓库独立发版，没有任何机制发现这种脱节。

### 2. Signatures

- `POST /api/auth/login` 与 `POST /api/v1/auth/login`（`router.go` 注册**两次**）
- 常量：`LoginCodeTwoFactorRequired` / `LoginCodeInvalidTOTP` /
  `LoginCodeInvalidCredentials` / `LoginCodeCaptchaRequired` / `LoginCodeAccountLocked`

### 3. Contracts

- 所有登录失败分支返回体在保留原有 `error` 中文文案的前提下，**增加**稳定 `code` 字段。
  只增不删——Web 与存量客户端都在读 `error`。
- 2FA 挑战分支（`ErrTOTPRequired` / `ErrInvalidTOTP`）**必须一并回带**
  `captcha_required` / `captcha_id` / `captcha_threshold` / `require_after_failures`。
  漏了的话，客户端第二次带 `totp_code` 的 POST 无从得知还要重做人机验证——
  这是修好客户端之后的**第二个阻断点**。
- `ErrTOTPRequired` 是**中间态**，不写「登录失败」登录日志；
  `ErrInvalidTOTP` 是真失败，照常记录。
- 限流器**必须每次注册各构造一个**：`RegisterRoutes` 被调用两次，
  共用一个闭包会让两个前缀共享同一个按 IP 计数的桶，手机与浏览器同出口互相挤占。

### 4. Validation & Error Matrix

- 未提供 totp → 401 `two_factor_required` + captcha 上下文，**不记失败日志**
- totp 错误 → 401 `invalid_totp` + captcha 上下文，记失败日志
- 密码错误 / 用户不存在 → 401 `invalid_credentials`（同一 code，避免用户枚举）
- 未提交验证码 → 401 `captcha_required`
- 验证码校验不通过 → 401 `captcha_required` + `captcha_invalid` + `captcha_reason`
- 账号锁定 → 429 `account_locked` + `locked` + `remaining_seconds`

### 5. Good/Base/Bad Cases

- Good：开 2FA 的账号登录 → 401 + `two_factor_required` + captcha 上下文 → 客户端展示验证码框。
- Bad：客户端把 4xx 一律 `throw` —— 中间态被吞，界面显示「请输入两步验证码」
  却没有任何能输验证码的地方，**永久死锁**。
- Bad：只给 2FA 两个 code 写断言 —— 另外三个 code 删掉也没测试会红。

### 6. Tests Required

- 五个 code **每个都要有断言**，且各自能独立失败
  （`captcha_required` 的两个分支要能互相隔离：只删其中一处，另一处的用例必须仍绿）
- 2FA 挑战响应含 captcha 上下文字段
- 2FA 中间态**不**产生失败登录日志，totp 错误**会**产生
- 两个路由前缀限流各自独立计数
- 现成工具：`service.GenerateCurrentTOTPForTest`
- ⚠️ 测账号锁定时**直接播种 `model.LoginAttempt` 行**，不要连发 5 次错误登录——
  第 6 个请求会先被 `RateLimit(5, time.Minute)` 挡住并返回**同样的 429**，
  断言就变成在测限流器而不是锁定逻辑。

### 7. Wrong vs Correct

#### Wrong

```dart
// 错误（客户端）：全局收紧 validateStatus，登录的 401 中间态在读到 body 之前就抛了。
validateStatus: (status) => status != null && status < 400,
...
final response = await _dio.post(ApiEndpoints.login, data: data); // 就地 throw
if (result['two_factor_required'] == true) { ... }                // 死代码
```

#### Correct

```dart
// 正确：登录接口做请求级放宽，显式区分「中间态」与「真失败」。
final response = await _dio.post(
  ApiEndpoints.login, data: data,
  options: Options(validateStatus: (s) => s != null && s < 500),
);
final body = response.data;
if (body is Map && (body['two_factor_required'] == true || body['captcha_required'] == true)) {
  return body;          // 中间态：正常返回，交给上层展示输入框
}
if (response.statusCode! >= 400) {
  throw DioException.badResponse(...);   // 其余 4xx 保持原有错误提示语义
}
```

---

## 场景：Magisk 容器脚本的 flavor 隔离（musl vs glibc）

### 1. Scope / Trigger

- 触发：修改 `Magisk/customize.sh` 中任何与 DNS、apt/apk、用户降权相关的逻辑时必须看本节。
- 原因：Alpine(musl) 与 Debian(glibc) 的**解析器语义不同**，
  对一方是修复的改动，对另一方可能是回归。

### 2. Contracts

- **glibc**：A/AAAA 两条查询用同一源端口并发发出，不少家用路由 / 运营商 DNS 只回一条，
  只能等超时重试 → `EAI_AGAIN`，报的就是 `Temporary failure resolving`。
  `options single-request-reopen` 是针对它的。
- **musl**：向所有 nameserver **并行**发查询并采信第一个确定性应答（**NXDOMAIN 也算**）。
  给它配多条 DNS，若某条是强制解析器抢先回 NXDOMAIN，
  会在**原本能正常工作的网络上开始失败**。
- 因此：多源 DNS / `options` / apt 加固**只对 Debian 分支生效**，
  Alpine 分支保持单条写死，resolv.conf 内容要与改动前**逐字节一致**。
- `apt` 的 `DropPrivs()` 会 `setgroups()` **清空附加组** →
  给 `_apt` 加 `aid_3003` 组在机制上不可能生效，加了只会掩盖问题。
  正确做法是 `APT::Sandbox::User "root"`。
- `_apt` 在 bookworm 是 **uid 42 / gid 65534**（bullseye 才是 100）→ 必须 `id -u _apt` 动态取。

### 3. Validation & Error Matrix

- **绝不能 `> $rootfs/etc/nsswitch.conf`**：Debian 的该文件由 base-files 提供、本来就在，
  截断写会连 `passwd:` / `group:` / `shadow:` 一起删掉，
  直接搞坏紧随其后的 `usermod` / `chpasswd` 与 `service.sh` 的 adduser / sshd。
  只能 `grep -q '^hosts:' || echo ... >>`。
- 镜像源必须有回退列表，但 `mirrors.nju.edu.cn` 字面量要保留
  （`magisk_assets_test.go` 有断言）。

### 4. Tests Required

- `magisk_assets_test.go` 断言 Alpine 分支（`else`..`fi`）内**可执行行有且只有**
  单条 `echo "nameserver 223.5.5.5" > ...`，做**全等**比较而非 `Contains`
- 断言多源 DNS 的每一行都只出现在 `if [ "$FLAVOR" = "debian" ]` 分支体内
  （堵住「挪到公共段照样覆盖 Alpine」这条绕法）
- ⚠️ 静态字符串断言只能防「整段被删掉」，**防不住逻辑写错**；
  shell 侧改动仍必须真机验证。

### 5. Wrong vs Correct

#### Wrong

```sh
# 错误：DNS 多源回退写在公共段，Alpine 被一起改掉。
: > $rootfs/etc/resolv.conf
for p in net.dns1 net.dns2; do ...; done
echo 'options single-request-reopen timeout:2 attempts:3' >> $rootfs/etc/resolv.conf
```

#### Correct

```sh
# 正确：只给 Debian 上多源 DNS，Alpine 保持改动前的单条写死。
if [ "$FLAVOR" = "debian" ]; then
  : > $rootfs/etc/resolv.conf
  for p in net.dns1 net.dns2; do ...; done
  echo 'options single-request-reopen timeout:2 attempts:3' >> $rootfs/etc/resolv.conf
else
  # musl 并行查询且采信 NXDOMAIN，多源反而可能在强制 DNS 的网络上引入新失败。
  echo "nameserver 223.5.5.5" > $rootfs/etc/resolv.conf
fi
```

## 场景：容器降权（PUID/PGID）与依赖安装的 HOME 契约

### 1. Scope / Trigger

- 触发：改 `docker/entrypoint.sh` 的降权段，或改 `server/service/dependency_*.go` 里
  与 npm / pip 环境变量相关的逻辑时必须看本节。
- 症状特征：**面板能开、一装依赖就 `EACCES`**。它与「数据目录整体不可写、面板压根起不来」
  是两类完全不同的故障，不能共用一次可写性探测。

### 2. Contracts

- **HOME 是唯一落点**：npm 的 cache（`$HOME/.npm`）、`$HOME/.npmrc`，
  pip 的 `pip.conf` 与 `--user` 落点全都只认 `HOME`。降权只 chown 数据目录是不够的。
- **`adduser -D -H` / `useradd -M` 都是「不创建家目录」**，但 `/etc/passwd` 里的
  家目录字段照写。「声明了却从不落盘」正是 EACCES 的直接成因。
- **su-exec 与 gosu 对 HOME 的处理不一致**：su-exec 按 passwd 无条件覆写；
  gosu 只在 HOME 为空时才设置，而 Docker 默认已注入 `HOME=/root`。
  ⇒ 只靠 passwd 字段修不好 gosu 那条路，必须用 `/usr/bin/env "HOME=..."` 显式钉。
- **必须写绝对路径 `/usr/bin/env`**：entrypoint 导出的 `PATH` 首位是
  `${DATA_DIR}/deps/nodejs/node_modules/.bin` —— 面板用户可写。
  裸写 `env` 会被一个同名的 npm bin 劫持，表现成「容器每 2 秒重启、只有一个退出码」。
- **必须以 `user:group` 形式降权**：只给用户名时两个工具都取 passwd 里的**主组**，
  用户填的 `PGID` 被静默丢掉（群晖 / OMV 常见 `PUID=1000 PGID=100`）。
- **跨层契约**：entrypoint 的 `DAIDAI_HOME` 必须与 Go 侧 `resolveWritableHome` 的回落目录
  是同一个（`${DATA_DIR}/.home`），否则会变成「entrypoint 建在 A、代码写到 B」。
- **`HOME` 为空时不要重定向**：那时 npm / pip 会按 uid 解析家目录，结果通常是对的
  （裸机 systemd 以 root 跑、没写 `Environment=HOME`）。重定向会把用户手写在
  `/root/.npmrc` 里的私有源与 token 静默弄失效。只处理「有值但不可写」。
- **判据必须是「能不能真的写进去」**：只 `Stat` 判断存在性不够 ——
  只读挂载、属主不符、NFS `root_squash` 都要真写一次才暴露。

### 3. Validation & Error Matrix

- **UID/GID 撞车是常态不是异常**：群晖的 `users=100`、Debian 镜像
  （`node:20-bookworm-slim`）自带的 `node=1000`。建组建用户失败时必须**复用**现成账号，
  且每一行都要带 `|| true` —— entrypoint 顶部有 `set -e`，裸奔一行就把容器带崩。
- **只设 `PGID` 时 `TARGET_UID` 会取到 0** → 造出 uid=0 的假降权用户。必须显式跳过并说明。
- **`mkdir` 家目录必须带兜底**：`:ro` 挂载 / `root_squash` 下它会 EACCES，
  裸写会被 `set -e` 静默带出，用户只看到「容器无限重启且 docker logs 一行都没有」，
  而后面那道可写性预检本来能给出「数据目录不可写 + 三条原因 + 修复命令」。
- **非 root 下的 Linux 系统依赖必须提前拦下**：`apt-get` / `apk` 需要 root，这是降权的固有代价。
  拦截信息要**按部署形态给出路**（容器 / systemd / Magisk），给二进制部署的用户一段
  `docker exec` 指引等于没给。
- **卸载路径不能因为构造命令失败就删记录**：那看起来像卸载成功，实际包还在系统里，
  为此写的中文说明一个字都不会显示。要与 NodeJS / Python 分支一致，标记 failed + 写日志。

### 4. Tests Required

- `docker/test-entrypoint-puid.sh`（已接进 CI）：把 entrypoint 原样跑起来，
  只桩掉 nginx / find / su-exec / gosu / daidai-server，覆盖八种组合并验到最终 uid、gid、
  HOME 指向、以及**在 `$HOME/.npm/_cacache` 下真的建出目录**。
  脚本自己 `unshare --mount` + tmpfs on `/tmp`：entrypoint 有一句 `chown -R ... /tmp`，
  不隔离会改掉宿主 / runner 的 `/tmp` 属主。
  **收尾要 userdel，所以本机已存在 `daidai` 账号时必须直接退出**（仓库推荐的 systemd
  部署就会建这么一个服务账号）。
- 撞车用例的前提条件要由脚本**自己预置**，并且让现成账号的主组**不等于** `PGID`，
  否则在某些机器上会静默退化成「无冲突」，把复用逻辑整段删掉也照样绿。
- `docker_entrypoint_assets_test.go`：静态断言锁住关键行与跨层 HOME 契约。
  这类断言只能防「被删掉 / 改回旧写法」，防不住逻辑写错。
- Go 侧的纯逻辑要与「读环境变量 + 看 `runtime.GOOS`」分开
  （`resolveWritableHome` / `redirectHomeEnv`），否则整块逻辑在 Windows 开发机上零覆盖。

### 5. Wrong vs Correct

#### Wrong

```sh
# 错误一：声明了家目录却从不创建；错误二：只传用户名，PGID 被丢掉；
# 错误三：裸写 env，会被 node_modules/.bin 里的同名包劫持。
adduser -D -H -u "${TARGET_UID}" -G daidai daidai
su-exec "${RUN_AS_USER}" env "HOME=${DAIDAI_HOME}" /app/daidai-server &
```

#### Correct

```sh
# 家目录真的建出来并纳入 chown；带兜底，只读目录下让后面的预检去报错。
mkdir -p "${DAIDAI_HOME}" 2>/dev/null || true
chown -R "${TARGET_UID}:${TARGET_GID}" "${DATA_DIR}" /tmp 2>/dev/null || true
RUN_AS_SPEC="${TARGET_USER}:${TARGET_GID}"
su-exec "${RUN_AS_SPEC}" /usr/bin/env "HOME=${DAIDAI_HOME}" /app/daidai-server &
```

## 场景：Magisk 容器内 sshd 的配置托管与可观测性

### 1. Scope / Trigger

- 触发：改 `Magisk/service.sh` / `customize.sh` 里任何与 sshd 相关的逻辑时必须看本节。

### 2. Contracts

- **OpenSSH 是「第一次取到的值胜出」**（与 nginx / Apache 的直觉相反）。
  Debian 的 `sshd_config` 顶部有未注释的 `Include /etc/ssh/sshd_config.d/*.conf`，
  被 include 的 snippet 先解析、因而**覆盖**主文件里的一切；Alpine 3.18 没有这一行。
- **`Match` 块有两个方向的陷阱**：
  - 无差别删除同名指令会波及 `Match Address ...` 里的作用域限定 —— 那是用户的安全策略；
  - 追加到文件末尾时，只要尾部有一个生效的 `Match` 块，写入就落进块内，
    而 **`Port` 在 `Match` 内是非法指令**，`sshd -t` 报错、sshd 直接起不来。
  - ⇒ 只在**第一个生效的 `Match` 之前**做删除，并把托管指令插在那个位置。
- **Alpine 的 openssh-server 是 `--without-pam` 构建**（PAM 版是独立包 + 独立
  `sshd.pam` 二进制），Debian 是 `--with-pam` 且 `UsePAM yes` 默认生效。
  这是「Alpine 正常、Debian 不通」最干净的结构性解释。
  Debian 的 `pam_loginuid` 要写 `/proc/self/loginuid`，容器里 `-S` 把宿主 `/proc` bind 了进来，
  写入失败即 `PAM_SESSION_ERR`，表现为「密码对了、连上立刻断开」。
  ⇒ 降为 `optional`，**不要用 `UsePAM no`**（Debian 有过「构建时没链上 crypt，
  `UsePAM=no` 下正确密码也被拒」的先例）。守卫用 `[ -f /etc/pam.d/sshd ]`，
  Alpine 上天然空操作 —— 比再引入一处 flavor 判断更不容易忘。
- **容器与宿主共享进程表**：`ruri` 走 chroot 且命令行不带 `-u`，不建任何 namespace。
  ⇒ `pgrep -x sshd` 会命中整机任何叫 sshd 的进程（含上次安装遗留的孤儿）。
  进程存活判据要按端口或按 `/proc/<pid>/root` 归属，不能按进程名。
- **`nc -z` 不可靠**：`PATH` 里的 `nc` 可能解析到 busybox applet，它不认 `-z`。
  直接读 `/proc/net/tcp{,6}`（`$4 == "0A"` 是 LISTEN）零外部依赖。
- **`getent shadow` 在 musl 上恒为空**（musl 的 getent 不支持 shadow 数据库），
  判断密码哈希要用 `awk -F: ... /etc/shadow`。
- **容器里没有任何 syslogd** ⇒ sshd 必须 `-D -e`：`-e` 才有日志，
  没有 `-D` 时 sshd 会 `daemon(0,0)` 把 stdio 重定向到 `/dev/null`，
  `-e` 就只剩 daemonize 之前那几条 fatal 看得到。
- **日志滚动要放在「确实要重启该进程」的分支里**：进程仍持有 fd 时 `mv`，
  它会继续往 `.old` 写，新文件永远长不到阈值 —— 滚动再也不会触发。

### 3. Validation & Error Matrix

- 安装期的运行时验证清单**必须包含 sshd**（二进制 / 配置 / 特权分离用户 / host key /
  root 密码 / `sshd -t`）。`sshd_config` 是 conffile、解包即落盘，
  `[ -f /etc/ssh/sshd_config ]` 那道守卫在「已解包未配置」时会误判成正常。
- SSH 自检失败**不中止安装**（面板 Web 不依赖它），但必须 `ui_print` 显式告警。
- 启动后的端口复检要**重试若干秒**：中低端手机上 sshd 要一两秒才加载完 host key 并 bind，
  只探一次会打出误导性的「端口未监听」，用户翻日志又什么错都没有。

### 4. Tests Required

- `magisk_assets_test.go` 的静态断言查禁用字面量时要**逐行跳过注释**
  （`assertNotInExecutableLines`）：脚本里常有注释专门解释「为什么不能再这么写」，
  整文件 `Contains` 会把它当成违规。
- 改 `Magisk/*.sh` 必须同步 `DAIDAI_MAGISK_SHELL_VERSION` 与
  `currentMagiskShellVersion`；`requiredMagiskShellVersion` **只有当新面板无法在旧外壳上
  运行时**才提 —— 提了就意味着所有老用户必须先重刷 ZIP 才能继续在面板内一键升级。
- 能离线验的一定要离线验：`sshd_config` 的重写逻辑可以对着 Debian / Alpine 的**真实出厂
  配置**与带 `Match` 块的加固配置跑，并用真实 `sshd -t` 解析结果。

## 场景：shell 语法门禁的两个盲区（heredoc 与 `-c '...'` 内联脚本）

### 1. Scope / Trigger

- 触发：新增或修改任何「以字符串形式传给别的解释器」的 shell 片段时。

### 2. Contracts

- `bash -n <文件>` **看不到**两类代码：
  - `cmd -c '<脚本>'` 里的内联脚本 —— 对外层解释器它只是一个普通字符串；
  - heredoc（`cat << 'EOF' ... EOF`）里的脚本 —— 同理。
- 这两类恰恰是 Magisk 模块里**每次开机真正执行**的东西（容器启动脚本近 300 行）。
  少一个 `fi`、多一个引号，`go test` 与 CI 全绿，而用户刷进去之后
  开机脚本从错误点开始整段不执行，面板与 SSH 一起消失。

### 3. Validation & Error Matrix

- `scripts/check-shell-syntax.sh` 会把这两类单独抽出来检查，并对
  shebang 是 `/bin/sh` 的脚本额外跑 `dash -n`（Alpine 上真正解析它的是 busybox ash）。
- 抽取规则必须自带**失配保护**：抽到的段数 / 行数低于预期就直接判失败，
  否则规则一旦漂移，这道门禁会静默变成空转。

### 4. Tests Required

- 门禁本身要验牙：制造一次真实的语法错误（在 heredoc 里删一个 `fi`、
  在内联脚本里让引号不配对），确认它会变红。

### 5. Wrong vs Correct

#### Wrong

```sh
# 错误：只对文件本身做语法检查，heredoc 与 -c 内联脚本完全不在覆盖范围内。
bash -n Magisk/service.sh
```

#### Correct

```sh
# 正确：额外把 heredoc 与内联脚本抽出来，各自再过一遍 bash / dash / busybox ash。
bash scripts/check-shell-syntax.sh
```

---

## 场景：删除任务时一并删除脚本（issue #124）

### 1. Scope / Trigger

- 触发：改动 `server/service/task_script_target.go`、`task_script_cleanup.go`、`task_script_reparse_*.go`、`task_script_ctime_*.go`、`server/handler/task_script_cleanup.go`、三个删除入口（`task_mutate.go` 的 `Delete`、`task_batch.go` 的 `Batch` / `BatchDelete`）、`server/middleware/openapi.go` 的 `AppTokenHasScope`、`server/service/panel_log.go` 的 `[任务删除]` 判级，或前端 `web/src/views/tasks/components/TaskDeleteDialog.vue`、演示站 `web/src/demo/db.ts` 的 `planDemoTaskScriptDeletion` 时必须看本节。
- 原因：删脚本不可撤销（面板没有回收站），误删的代价远大于少删。判定必须以**真实执行口径**为准，**任何不确定都保留文件并说明原因**。

### 2. Signatures

- 预览：`POST /api/v1/tasks/delete-preview`，body `{"task_ids":[uint]}` → `{"data":{"checked":true,"tasks":[TaskInfo],"scripts":[Item]}}`（operator；应用令牌另需 scripts scope）
- 执行（三个既有入口各加可选开关）：
  - `DELETE /tasks/:id?delete_script=1&confirm_script_path=<p>`
  - `PUT /tasks/batch`，body `{ids, action:"delete", delete_scripts: bool, confirm_script_paths: []string|null}`
  - `DELETE /tasks/batch/delete`，body `{task_ids, delete_scripts, confirm_script_paths}`
- service：`ResolveTaskScriptTarget(command string, base scriptsBase) TaskScriptTarget`；`PreviewTaskScriptDeletion(ids []uint, env TaskScriptCleanupEnv)`；`CollectTaskScriptTargets(ids, env) *TaskScriptCleanup`（删任务**之前**调）；`(*TaskScriptCleanup).Execute(confirm *[]string) TaskScriptDeleteResult`（删任务**之后**调，持包级锁）
- handler：`parseDeleteScriptQuery` / `beginTaskScriptCleanup` / `finishTaskScriptCleanup` / `(*TaskHandler).DeletePreview`
- 其它：`middleware.AppTokenHasScope(c, "scripts")`、`(*TaskExecutor).HasRunningProcess(taskID)`、测试注入点 `isReparsePointFn`

### 3. Contracts

- **不带开关时三个入口的响应逐字节不变**；开关打开只在原响应上追加 `scripts` 字段，`message` / `count` 口径不变。
- 路径**只**由服务端从 `tasks.command` 按 `ParseCommandExecutionPlan`（真实执行口径）解析；请求里不接受文件路径。`confirm_script_path(s)` 只能收窄：没传 = 不收窄（给不先调预览的 APP / Open API）；空串或 `[]` = 一个都不删。
- 返回的数组恒为 `[]`；路径一律相对脚本目录、正斜杠；不回显绝对路径，也不回显底层错误原文（原文只进面板日志）。
- 保留原因 `reason`（服务端文案是唯一来源，前端原样展示，不另写映射）：`check_failed` / `hidden_path` / `symlink` / `not_regular_file` / `managed_helper` / `subscription_managed` / `task_not_deleted` / `shared` / `referenced_in_hook` / `task_running`；执行阶段另有 `not_confirmed` / `changed` / `not_found` / `remove_failed`。执行阶段先报 `not_found` / `changed` 再做结构性复核；逐段检查出错按 `check_failed` 处理但排在 `hidden_path` 之后。
- 共用判定：其他任务**全表加载后在 Go 里过滤**；同一文件用 `os.SameFile` 认；非 script 类的命令（托管命令、`python -m`、解析失败）按命令文本匹配（`text_match=true`，`python -m a.b` 映射到 `a/b.py`、`a/b/__main__.py` 及各级 `__init__.py`）；其他任务与订阅的前置/后置命令按 token 匹配（`referenced_in_hook`，含 `python -m` 与 node 省略扩展名的写法；钩子里先 `cd` 进子目录再 `python -m` 的，模块候选按「整路径相等或以 `/候选` 结尾」比较，偏差只朝多保留走）。共用提示按共用方人数写「删除后它 / 它们会无法运行」。
- 只对**普通文件** `os.Remove`；删除前 Lstat + 身份复核（`SameFile` + size + modtime，Linux 另比 ctime，Windows 在 Collect 时固化文件身份）；**绝不 `RemoveAll`、不删父目录、不动 `script_versions`**。
- 路径逐段 `Lstat`（只在字面路径与真实路径一致时才查）：任一段 `Mode()&(ModeSymlink|ModeIrregular)!=0`，或 Windows 中间目录段带 `FILE_ATTRIBUTE_REPARSE_POINT`，即判 `symlink` 保留。Go 1.23+ 起 Windows 目录联接报 `ModeIrregular` 而不是 `ModeSymlink`；末级文件不查 reparse 属性（Data Deduplication 文件是普通文件）。脚本目录本身或其上级挂在联接下时，`resolveScriptsBase` 退回 `Real=Abs`，与执行解析器口径一致。
- 脚本目录根下的 `notify.py` / `sendNotify.js` / `task_before.sh` / `task_after.sh` / `extra.sh` 按 `managed_helper` 一律保留（面板隐式引用它们：删了会被静默替换，或全局钩子静默失效）。
- 应用令牌带开关或调预览，必须 `AppTokenHasScope(c,"scripts")`，否则 `403` 且任务和文件都不动。以后在别的 handler 里给新开关动其它资源时照此仿写。
- 留痕：每删一个文件写一行「`[任务删除] <用户>(<IP>) 删除任务 [ids] 时一并删除了脚本 <path>`」判 INFO；删除失败与逐段检查失败写含「失败，已保留」的行判 ERROR。`detectPanelLogLevel` **先判失败行再判成功行**，文件名、用户名里出现这些字眼时误判只会朝 ERROR 走。

### 4. Validation & Error Matrix

- 预览 `task_ids` 绑定失败 → `400「请求参数错误」`；去重后为空 → `400「请选择要删除的任务」`（`binding:"required"` 会放行 `[]`，必须显式判断）
- viewer 调预览或带开关 → `403「权限不足」`；应用令牌缺 scripts → `403`，什么都不删
- `delete_scripts` 传成字符串 → 整个请求绑定失败 `400`，任务也不删
- `?delete_script=0` / `abc` / 空 → 走旧逻辑，响应逐字节不变
- 单删任务不存在 → `404「任务不存在」`（带不带开关都一样；前端弹窗收到 404 会提示并刷新列表）
- 其他任务仍引用 → `shared`；订阅目录 / 单文件订阅下载目标 → `subscription_managed`（不看 `subscription:` 标签，标签可伪造）；运行中或排队中（status 2 / 0.5，或执行器进程表里还有）→ `task_running`；确认后文件被替换 → `changed`；确认后文件消失 → `not_found`（前端按「已经不存在」计为达成）
- 读任务快照出错 → 每个任务 `found=false`、`script_status=unresolved`、独立 note，`scripts=[]`；前端按「没能检查脚本文件」展示，不说「没有可删的脚本」

### 5. Good/Base/Bad Cases

- Good：选中共用同一脚本的两个任务一起删 → 合并成一项、`task_ids` 两个，可删。
- Base：不勾选 → 请求与改动前逐字节一致（单删不带 params，批量只带 `{ids, action}`）。
- Bad：查「其他任务」写成 `Where("id NOT IN ?", ids)`。集合为空或 nil 时 GORM 生成 `NOT IN (NULL)`，查出 0 行，共用判定静默失效 → 误删。
- Bad：用 `extractTaskScriptPath` 解析。它只做文本处理、不查文件系统：`task a.py b.js` 返回不存在的「a.py b.js」，目录外的绝对路径原样返回。
- Bad：把 `plan.FullPath` 当字面路径。它已经 EvalSymlinks，删软链接会删到链接目标。
- Bad：只看 `ModeSymlink` 判断软链接。Windows 目录联接会穿过去，删到脚本目录外。
- Bad：对整条错误文本做密钥替换。误填的短 token 会把「HTTP 404」改成「HTTP ***0***」；telegram 只替换 `/bot` 之后那一段。

### 6. Tests Required

见 `server/handler/task_delete_scripts_test.go`、`server/service/task_script_cleanup_test.go`、`task_script_target_test.go`、`task_script_cleanup_linux_test.go`、`task_script_cleanup_windows_test.go`、`panel_log_test.go`：

- 兼容（**先写，并要求在改动前的代码上就是绿的**）：`TestTaskDeleteGoldenWithoutScriptSwitch` / `TestTaskDeleteGoldenNotFound` / `TestTaskBatchDeleteGoldenWithoutScriptSwitch` / `TestTaskBatchDeleteEndpointGoldenWithoutScriptSwitch` / `TestTaskBatchNonDeleteActionIgnoresScriptSwitch` / `TestTaskDeleteGoldenAppTokenWithoutScriptSwitch`（逐字节比较响应体；覆盖 `delete_script=0/abc/空`、只带 confirm、`delete_scripts:false`、`confirm_script_paths:null`）
- 接口：`TestTaskDeleteWithScriptSwitchDeletesScript` / `TestTaskBatchDeleteScriptsMergesAndKeepsShared` / `TestTaskDeleteScriptsConfirmNarrowing` / `TestTaskDeleteScriptsNestedArraysNeverNull` / `TestTaskDeleteScriptsAppScopeMatrix` / `TestTaskDeletePreviewValidation` / `TestTaskDeletePreviewMatchesExecution` / `TestTaskBatchDeleteScriptsRejectsNonBoolSwitch`
- 共用判定：`TestCollectOtherTasksNeverUsesNotIn`（行为 + AST 双层，**最重要的一条**）/ `TestTaskScriptPreviewSharedDetection` / `TestTaskScriptPreviewSharedDetailText` / `TestTaskScriptPreviewModuleSharedDetection` / `TestTaskScriptPreviewBrokenCommandSharedDetection` / `TestTaskScriptPreviewHookReferences`
- 结构性保护：`TestTaskScriptPreviewStructuralProtections` / `TestTaskScriptPreviewSymlinkKept` / `TestTaskScriptCleanupSymlinkedScriptsDirStillDeletable` / `TestPathHasLinkSegment` / `TestTaskScriptReparsePointSegmentsViaInjectedCheck` / `TestTaskScriptLinkCheckFailureIsLogged`；linux：`TestTaskScriptAliasAbsolutePathKeptAsSymlink` / `TestTaskScriptHintPathUnderSymlinkedScriptsDir`；windows（`mklink /J` 与 `FSCTL_SET_REPARSE_POINT`，不许 skip）：`TestTaskScriptJunctionKept` / `TestTaskScriptJunctionAboveScriptsDir` / `TestTaskScriptDedupReparseFileDeletable`
- 订阅与 helper：`TestTaskScriptPreviewGitSubscription` / `TestTaskScriptPreviewGitSubscriptionAtScriptsRoot` / `TestTaskScriptPreviewSingleFileSubscriptionAndLabels` / `TestSingleFileSubscriptionDestPathMatchesPull` / `TestTaskScriptPreviewManagedHelpersAndWarnings` / `TestTaskScriptPreviewGlobalHooksKept`
- 状态、文案与日志：`TestTaskScriptPreviewRunningStates` / `TestTaskExecutorHasRunningProcess` / `TestTaskScriptPreviewSnapshotError` / `TestTaskScriptPreviewReasonPriority` / `TestTaskScriptPreviewTaskStatusesAndNotes` / `TestDetectPanelLogLevelForScriptDeletion`
- 执行阶段：`TestTaskScriptExecuteDeletesOnlyTheFile` / `TestTaskScriptExecuteConfirmNarrowing` / `TestTaskScriptExecuteDetectsChangesAfterCollect` / `TestTaskScriptExecuteSameSizeReplacementChanged` / `TestTaskScriptExecuteSymlinkReplacementChanged` / `TestTaskScriptExecuteRechecksReferencesAfterCollect` / `TestTaskScriptExecuteConcurrentOnlyOneRemoves`
- 解析：`TestParseCommandExecutionPlanRecordsScriptToken` / `TestResolveTaskScriptTargetClassifiesCommands`（顺带钉住解析器的错误文案）/ `TestResolveTaskScriptTargetTextCandidatesForNonScriptKinds` / `TestResolveTaskScriptTargetSymlink*`
- 修改后至少运行：

```bash
cd server
go test ./service ./handler -run "TestTaskScript|TestTaskDelete|TestTaskBatch|TestCollectOtherTasks|TestPathHasLinkSegment|TestResolveTaskScriptTarget|TestParseCommandExecutionPlanRecordsScriptToken|TestTaskExecutorHasRunningProcess|TestSingleFileSubscriptionDestPath|TestDetectPanelLogLevel" -count=1
go test ./...
```

- windows / linux 专属用例在另一平台不参与编译（CI 是 ubuntu）：改 `pathHasLinkSegment` / `isReparsePoint` 时 Windows 本机要跑一遍；软链接用例交叉编译到 linux 后在 WSL 里跑，要先 `cd` 到 `server/service` 源码目录，否则读相对路径的用例会假红。
- Web 没有单测：`TaskDeleteDialog.vue` 靠 `npm run build`（vue-tsc）加浏览器实测，覆盖单删、批量、预览失败降级、移动端全屏、观察者无入口、快速关开不串、文件已不存在、任务已不存在；演示站判定改动后要与服务端逐条对拍。

> **突变验证**：把「其他任务」的查询改成 `Where("id NOT IN ?", ids)`，`TestCollectOtherTasksNeverUsesNotIn` 必须变红；把 `pathHasLinkSegment` 短路成 `return false, nil`，`TestPathHasLinkSegment` 与 `TestTaskScriptReparsePointSegmentsViaInjectedCheck` 在 Linux 上也必须变红（`TestTaskScriptJunctionKept` 只在 Windows 上兜）。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：ids 为空或 nil 时生成 NOT IN (NULL)，查出 0 行，共用判定静默失效。
database.DB.Where("id NOT IN ?", ids).Find(&others)
```

```go
// 错误：FullPath 已解析软链接；RemoveAll 还会把「名字像脚本的目录」整个删掉。
os.RemoveAll(plan.FullPath)
```

#### Correct

```go
// 正确：全表加载，在 Go 里剔除本次范围内的任务。
database.DB.Select("id", "name", "command", "status", "task_before", "task_after").Find(&all)
```

```go
// 正确：删除前 Lstat 复核是普通文件且身份未变，只删这一个文件。
if li, err := os.Lstat(literalAbs); err == nil && li.Mode().IsRegular() && sameFileIdentity(li, collected) {
    err = os.Remove(realPath)
}
```
