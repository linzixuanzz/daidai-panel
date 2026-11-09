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
- `testutil.SetupTestEnv` 在进入与退出时都会把 middleware 可信代理重置回默认值——v3.4.4 起默认**只信任本机**（`127.0.0.1/32`、`::1/128`）。用例要模拟「私网反代转发」时，必须在 `SetupTestEnv` **之后**显式 `middleware.ConfigureTrustedProxyCIDRs("192.168.0.0/16")`，并在 `t.Cleanup` 里 `ConfigureTrustedProxyCIDRs("")` 还原；别依赖「私网默认可信」。

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
- 关停两段：`func (s *SchedulerV2) SignalStop()`（等正在跑的 cron 回调最多 `schedulerCronCallbackWait` = 1 秒）/ `func (s *SchedulerV2) WaitWorkers(timeout time.Duration) bool`；
  包级入口 `func HaltSchedulerV2()`（SignalStop + StopAllRunningTasks，可重复调用；v3.3.6 起 StopAllRunningTasks 先 SIGTERM、所有进程组共用最多 `shutdownTermGrace` = 2 秒宽限再 SIGKILL，见「场景：任务进程组的清理与终止」）/
  `func ShutdownSchedulerV2()`（保持无参，`schedulerShutdownWait` = 3 秒，v3.3.6 前是 4 秒），面板关停的整体顺序见「场景：停机收尾」
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
  前两步是 `HaltSchedulerV2()`，后两步在 `ShutdownSchedulerV2()` 里（它开头会再调一次 `HaltSchedulerV2`，重复调用无害）。v3.3.5 起每一步都有上限：
  - `SignalStop` 等正在跑的 cron 回调**最多 1 秒**，到点打一行日志放手：回调要读写库，唯一的连接被恢复备份这类长事务占着时会一直卡，而杀任务排在它后面（原来无限等）。
  - 面板关停（`server/main.go` 的 `shutdownPanel`）里，HTTP 关停（`server.Shutdown(5s)`）**先起协程、与 `HaltSchedulerV2` 同时开始**：`HaltSchedulerV2` 卡住时 SSE 照样断开。
  - `StopAllRunningTasks` 先对所有进程组（任务进程与正在跑的钩子）发 SIGTERM、共用最多 2 秒的宽限、到点还在的统一 SIGKILL，阻塞到 KILL 落地再返回（v3.3.6，#159；Magisk 部署下宽限为 0、立即整组 KILL）。
    TERM 宽限就发生在 `HaltSchedulerV2` 这一步里，口径见「场景：任务进程组的清理与终止」。
  - `WaitWorkers` 与 `executor.Wait` **共用一个 3 秒截止时间**（原来 5 秒 + 5 秒串行；v3.3.6 起从 4 秒缩到 3 秒，把位置让给上一条的 2 秒宽限，总账不变），传给它们的剩余时间**至少 1ms**：两者都把 `<= 0` 当成「一直等」，算成 0 就变回无限等。
  - 截止时先 `revokeUnsettledScriptTokens` 吊销仍在执行窗口里（没结算完）的执行注入脚本的凭据，再 `MarkActiveTasksInterrupted` 把它们标成中断。
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
- 执行卡住（不登记进程、不理停止）时 `ShutdownSchedulerV2` 5 秒内返回、任务标成中断、凭据吊销；cron 回调卡住时 `SignalStop` 1.5 秒内返回
  （`service/panel_shutdown_test.go` 的 `TestShutdownSchedulerV2IsBoundedWhenRunIsStuck`、`TestSchedulerV2SignalStopDoesNotWaitForStuckCronCallback`，下面的命令已覆盖）。
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
- CLI 停止入口：`ddp task stop`（`cmd/ddp/commands.go` 的 `stopTaskNow`）
- 按库里 PID 兜底：`func TerminateProcessByPid(pid int)`（v3.3.6 起手动、批量、定时三个停止入口用它；直接调立即 SIGKILL 的 `KillProcessByPid` 的停止入口只剩 `ddp task stop`）
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
- 🔴 **按库里的 PID 兜底只在 `StopTask` 返回 false 时做**（v3.3.6，#159）：返回 true 说明执行器已经认领这次停止（对登记的进程组发了 SIGTERM、安排了 5 秒后的 SIGKILL，库里的 pid 就是其中之一），
  紧跟着再按 PID 补一刀，要么是立即 SIGKILL（宽限形同虚设），要么是第二个 TERM（打断脚本正在跑的收尾）。
  手动停止（`task_control.go` 的 `Stop`）、批量停止（`task_batch.go` 的 `case "stop"`）、定时停止（`stopTaskBySchedule`）一律写成先记下 `stopped := executor.StopTask(id)`，
  再 `if !stopped && pid > 0 { 打停止标记; TerminateProcessByPid(pid) }`。兜底时照旧必须先打停止标记，不能只发信号。
  `ddp task stop` 是一次性进程，异步补刀活不到 5 秒后，仍是 `MarkManualStop` + 立即 `KillProcessByPid`。进程组怎么终止见「场景：任务进程组的清理与终止」。
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
- Bad：`StopTask` 返回 true 之后照旧按库里的 PID `KillProcessByPid`（v3.3.5 及以前的写法）—— 紧跟在 TERM 后面的 SIGKILL 让 trap 了 TERM 的脚本来不及收尾。
- Bad：把 Aborted 当成功写入统计，导致用户分不清自然完成和计划终止。

### 6. Tests Required

- `applyManualStopOverride`：
  - 命中主动停止标记时应强制返回 `RunAborted` / `LogStatusAborted`。
  - 标记读即清，重复调用不得继续覆盖状态。
  - 未打停止标记的自然失败不能被改成 Aborted。
- handler：
  - 创建任务时 `notify_on_abort` 能保存并回传。
  - `PUT /tasks/:id/stop` 把运行中日志改成 `LogStatusAborted`，任务 `last_run_status` 改成 `RunAborted`。
  - 执行器认领了停止时不再按 PID 补刀（Linux，`handler/task_stop_graceful_linux_test.go` 的 `TestStopTaskEndpointLetsScriptHandleSigterm`，断言点见「场景：任务进程组的清理与终止」）。
    批量停止与定时停止里同样的 `!stopped` 条件目前没有用例守着（已知缺口），改那两处时照单个停止的写法核对。
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
// 错误：执行器已经认领了停止（StopTask 返回 true：已对整组发 TERM、安排了 5 秒后的 KILL），又按库里的 PID 立即补一刀 SIGKILL，
// 宽限形同虚设，trap 了 TERM 的脚本来不及收尾（v3.3.5 及以前的写法）。
executor.StopTask(taskID)
if task.PID != nil && *task.PID > 0 {
    markManualStop(taskID)
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
// 正确：执行器认领了就不按 PID 兜底；只有执行器不认识这次执行（例如 ddp task run 在别的进程里跑的）才兜底。
// 兜底照旧先打停止标记（完成结算统一写入 Aborted），再用 TerminateProcessByPid：先 TERM、5 秒后组还在才 KILL，不阻塞接口。
stopped := false
if executor := GetTaskExecutor(); executor != nil {
    stopped = executor.StopTask(taskID)
}
if !stopped && task.PID != nil && *task.PID > 0 {
    markManualStop(taskID)
    TerminateProcessByPid(*task.PID)
}
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

- 触发：修改 `server/service/resource_monitor*.go`、`server/handler/script_file_ops.go`、`server/service/backup*.go`、`server/service/script_dir_guard.go`、
  `script_notify_helpers.go` 的 `CleanupManagedHelperCopiesUnderRoot`、`server/cmd/ddp/runtime.go` 的 `bootstrap`、`server/main.go` 里脚本目录扫描、备份恢复、资源监控或启动期清理逻辑时必须看本节。
- 原因：Windows 运行态如果只实现 Linux 资源采集，仪表板会长期显示 `0 B / 0 B`；脚本目录如果混入 `%SystemDrive%` 这类异常目录，会污染脚本管理、统计和备份恢复链路。

### 2. Signatures

- Windows 资源补齐：`fillWindowsResourceInfo(info *ResourceInfo)`
- 忽略判断（展示 / 统计 / 备份打包与恢复跳过，**不搬走**）：`ShouldIgnoreScriptEntryName(name string) bool`
- 绝对路径判断：`ShouldIgnoreScriptPath(scriptsDir, targetPath string) bool`
- 相对路径判断：`ShouldIgnoreScriptRelativePath(relPath string) bool`
- 启动期隔离名单（命中即搬走）：`quarantinedScriptDirNames`（`service/script_dir_guard.go`，目前只有 `%systemdrive%`）
- 启动期隔离：`QuarantineUnexpectedScriptEntriesOnStartup()`，末尾调 `removeLeftoverManagedNodeModulesLinks(scriptsDir, managedNodeModules string)`。
  `main` 里和 `CleanupManagedHelperCopiesUnderRoot` 放进**同一个后台 `go` 协程**（软链那遍在前），在 `InitSchedulerV2` 之前启动，不等待、不计时（v3.3.6，#158；以前同步调用并计时，挡在监听前面）。
  根目录两份 helper 的 `EnsureBuiltinNotifyHelpers` 只读写两个文件，照旧同步执行（开机任务一跑就要用）。日志口径见 `logging-guidelines.md`「约定：启动耗时与关停日志」。
- 通知脚本副本清理：`CleanupManagedHelperCopiesUnderRoot(scriptsDir string) error`（`service/script_notify_helpers.go`），清理范围不变：
  读不了或已消失的目录跳过（`WalkDir` 回调拿到 err 时 `return nil`）；单个目录 `cleanupManagedHelperCopies` 出错不中止、接着清其余目录，遍历完返回第一个错误（没有就 nil）。
  唯一调用方是 main 里的后台协程（出错打 `cleanup duplicated notify helpers failed: …`）；**ddp 不再调用**（`cmd/ddp/runtime.go` 的 `bootstrap` 只保留 `EnsureBuiltinNotifyHelpers`）。

### 3. Contracts

- `GetResourceInfo()` 在 Windows 下必须返回可用的 `memory_total`、`memory_used`、`disk_total`、`disk_used`，不能继续全量为 `0`。
- 启动期隔离**只认 `quarantinedScriptDirNames`**：脚本目录**顶层**命中 `%SystemDrive%` 这类真正的污染目录时，必须自动移动到 `data/quarantine/scripts/`，而不是继续暴露给脚本管理页。
- 🔴 **`node_modules` / `__pycache__` 不隔离**（v3.3.5，#156）：它们只在 `ShouldIgnoreScriptEntryName` 里，作用仅限展示 / 统计 / 备份跳过。
  「忽略名单就是隔离名单」的旧写法已删，隔离循环不得再调 `ShouldIgnoreScriptEntryName`。
  - 为什么：`__pycache__` 是脚本根目录里被 import 的模块（如面板放的 `notify.py`）每次运行都会重新生成的；`node_modules` 可能是用户自己 `npm install` 的真目录（搬走后 `require` 直接失败），
    也可能是 Node 任务运行时面板自建、被 SIGKILL 来不及删的软链。旧写法每次启动都把它们搬一遍，#156 的用户隔离区里已经攒到 `__pycache__.duplicate-34`、`node_modules.duplicate-10`。
  - 已经搬进 `quarantine/scripts/` 的副本不自动删（用户可手动删 `__pycache__.duplicate-*`、`node_modules.duplicate-*`）。
- **启动后在后台协程里只清「悬空的面板式」`node_modules` 软链**（`removeLeftoverManagedNodeModulesLinks`，在隔离循环之后调；v3.3.5 回归复核后收窄）：
  - 先解析脚本根：`filepath.EvalSymlinks(scriptsDir)`，失败退回原值；解析结果 `Lstat` 仍不是目录（Windows junction：Go 1.23 起 `EvalSymlinks` 不解析它、原样返回）时，
    再 `os.Readlink` 补一层（得到绝对路径才用）。不解析的话，脚本目录本身是软链 / junction（脚本放到别的盘、NAS 上）时 `WalkDir` 只访问根这一项、不进链接，整棵树一条都清不到。
  - 然后 `WalkDir` 整棵树，下面三条**同时满足**才删，其余一律不动：
    1. 名字是 `node_modules`（忽略大小写），`os.Readlink` 成功（软链 / junction），目标是**绝对路径**（面板只建绝对路径的；相对路径要在 `Stat` 之前放过，否则会按面板进程的工作目录解析、判不准悬空）；
    2. 目标 `filepath.Clean` 后等于当前托管依赖目录 `<data.dir>/deps/nodejs/node_modules`，或以 `<分隔符>deps<分隔符>nodejs<分隔符>node_modules` 结尾（数据目录搬家、改过 `data.dir` 之前建的，指向旧数据目录）；Windows 上两项都忽略大小写；
    3. `os.Stat(目标)` 报**不存在**（悬空）。`Stat` 报别的错（例如没权限）按「还在」处理，不删。
  - 一律不动：不悬空的面板软链（指向旧数据目录的也算）、真 `node_modules` 目录（整棵 `SkipDir`，里面的软链哪怕悬空也不碰）、指向别处的软链（悬空的也算）、相对路径的软链。
  - `.git`、`__pycache__` 等 `ShouldHideScriptTreeEntryName` 命中的目录整棵跳过；读不了的目录跳过；删除失败只打日志。不往 quarantine 里搬任何东西。
  - 为什么要清悬空的：`ensureManagedNodeModulesAccess` 只在不存在时才建、从不修已有的，悬空软链留着会一直挡住那个目录里的任务（ESM 的 `import` 没有 `NODE_PATH` 兜底，一直报找不到模块）。
    托管目录被删（依赖全卸光、数据目录被清过）、数据目录搬家都会造出它；以前靠「隔离把顶层那个软链搬走」顺手自愈。
  - 🔴 为什么不删不悬空的：它与面板现在会建的等价，留着无害；而且启动时**并不是**没有进程在用它（v3.3.6 起这一遍在后台跑，开机任务可能正同时在跑，更不能删不悬空的）——`ddp task run` 是另一个进程，二进制 / Magisk 下被强杀的面板还可能留下仍在跑的孤儿任务，
    删掉它们正在用的软链，正在 `require` 的依赖会突然消失（回归复核实测：v3.3.5 第二波按「目标等于托管目录就删」，正在用这条软链的 node 进程报 `ENOENT` 退出）。
- **`CleanupManagedHelperCopiesUnderRoot` 遍历时遇到 `ShouldHideScriptTreeEntryName` 命中的目录（`node_modules`、`__pycache__`、`.git` 等）直接 `SkipDir`**：
  它们不会是任务的工作目录，面板不会往里放通知脚本副本；订阅仓库的 `.git`、真实 `node_modules` 动辄上千个目录，进去就是白扫一遍。
- 🔴 **两遍整树遍历都不在同步启动路径上，只挪一遍没用**（v3.3.6，#158）：慢盘上时间几乎全花在「第一次读目录」上，谁排第一谁付。
  只把副本扫描挪走，软链那遍就变成冷缓存上的第一遍，那几秒原样出现在下一行日志里（#158 日志里那遍软链遍历「不到 3 秒、没打日志」，只是因为它排第二）。
  实测（3560 个目录，另有 8000 个两遍都跳过的 `.git` 目录；只看比例，NAS 冷盘按同样结构放大到秒级）：

| 环境 | 副本扫描（每个目录盲开 2 次文件） | 软链遍历（不开文件） |
|---|---|---|
| WSL ext4，每轮先 `drop_caches` | 252–255ms | 单独冷跑 243–368ms；紧跟在副本扫描后面只要 13–16ms |
| Windows NTFS（热缓存） | 460–527ms | 233–288ms |
| WSL 访问 `/mnt/c`（9p，近似 Docker Desktop 的绑定挂载） | 9.8–11.6s | 4.2–4.7s（冷热一样） |

  - 两遍放进同一个协程、不合并：合并只省后台 IO（本地盘十几毫秒，9p 上约 10 秒），却要统一根目录解析（软链那遍先 `EvalSymlinks`，副本扫描不解析）、错误策略与根目录文件排除，改动面和回归面都更大（backlog）。
  - 不做「一次性标记」：副本还有写入路径（恢复 v1.9.7 及以前做的备份、订阅仓库里提交了托管副本、手动把根目录的 helper 拷进子目录），打了标记就再也不清了。
  - 后台协程只删文件 / 软链、搬目录，不碰数据库、不做登记，关停不等它（单个 `os.Remove` / `os.Rename` 是原子的，进程退出时停在哪一步都不留半成品）；没有 `recover`（两个函数只做文件操作，读码没有 panic 来源）。
- **时序窄缝（已接受）**：悬空的面板式软链从「一定早于开机任务清掉」变成「启动后几秒内清掉」。只影响「数据目录搬家后第一次启动、开机任务恰好是那个目录里用 ESM `import` 的 Node 脚本」：那一次报找不到包，之后正常（CommonJS 有 `NODE_PATH` 兜底）。
  这条窄缝不可接受时，只把 `QuarantineUnexpectedScriptEntriesOnStartup` 挪回同步位置即可（代价是本地盘上启动又多一次冷遍历）。
- 子目录里的通知脚本副本「挡住根目录新 helper」由每次执行前的按次清理负责：`BuildNotifyHelperEnv` 清工作目录、`script_runner.go` 的 `buildCmd` 清脚本所在目录，
  任务执行器、调试运行 / 运行代码、`ddp python`、`ddp task run` 都经过它们，启动时那遍整树扫描只是兜底。
  例外：`ddp shell` 的工作目录就是脚本根目录，`cleanupManagedHelperCopies` 遇到根目录直接返回，按次清理是空操作；ddp 不再整树清以后，进 shell 再 `cd` 到子目录跑脚本时，子目录里的旧副本只靠面板下次启动的后台遍历清掉（已接受）。
- 脚本文件树、脚本统计、备份打包、备份恢复复制链路都必须复用同一套 `ShouldIgnoreScript*` 判断，避免有的地方隐藏、有的地方继续打包。
- 备份恢复遇到命中异常规则的脚本相对路径时必须跳过，不能把污染目录重新写回脚本根目录。
- 备份恢复写回脚本目录、日志目录、`panel.log` 这类 live 资源时，禁止“先清空 live，再逐步复制”；必须先把新内容完整写入同目录 staging 位置，确认成功后再原子切换到 live 目录/文件。

### 4. Validation & Error Matrix

- Windows 资源采集 API 调用失败 → 返回 0，但不能影响服务启动。
- 顶层是 `quarantinedScriptDirNames` 里的污染目录（`%SystemDrive%`）→ 展示 / 统计 / 备份跳过；启动期隔离到 quarantine（`os.Rename` 失败、跨设备 EXDEV 只打日志跳过）。
- `node_modules` / `__pycache__` 真目录 → 展示 / 统计 / 备份照旧按忽略规则跳过（口径不变）；启动期**原地不动**，quarantine 里不再多出副本。
- `node_modules` 是面板式的绝对路径软链 / junction（目标是当前托管依赖目录，或以 `<分隔符>deps<分隔符>nodejs<分隔符>node_modules` 结尾的旧数据目录），且目标已不存在
  → 启动时删掉，打一行 `leftover node_modules link removed: <链接> -> <目标>`；删除失败 → 只打日志。
- 同样是面板式软链，但目标还在（或 `Stat` 报「不存在」以外的错）→ 不动：与面板现建的等价，`ddp task run`、孤儿任务可能正在用。
- `node_modules` 是指向别处的软链（悬空与否）、相对路径软链 → 不动。
- 脚本目录本身是软链 / junction → 解析成真实目录后照常清理；根链接本身、里面的脚本不动。
- 后台遍历途中目录被删（脚本管理、订阅拉取正在增删目录）或读不了 → 跳过，其余目录照清；某个目录的副本清理失败 → 接着清其余目录，遍历完返回第一个错误，main 打一行 `cleanup duplicated notify helpers failed: …`。
- 启动后几秒内（后台遍历还没走到）就有开机任务跑在带悬空面板软链的目录里 → 这一次 ESM `import` 可能报找不到包（已接受的窄缝，见上）。
- quarantine 目标重名 → 追加 `.duplicate-N` 后缀，不能覆盖旧证据目录。
- 备份恢复中遇到 `%SystemDrive%/...` 相对路径 → 直接跳过，不报错中断整个恢复流程。
- 备份恢复 staging 构建失败 → 直接返回错误，live 目录/文件必须保持恢复前原样，不能出现“旧数据已删，新数据没写完”的半恢复状态。

### 5. Good/Base/Bad Cases

- Good：Windows 仪表板显示真实内存/磁盘占用；脚本页只显示正常脚本文件；异常 `%SystemDrive%` 目录被移到 `data/quarantine/scripts/%SystemDrive%`；
  脚本根目录的 `__pycache__`、用户自己的 `node_modules` 重启后原地还在，悬空的面板 `node_modules` 软链被清掉、不悬空的原样保留。
- Base：Linux 继续沿用 `/proc` 和 `df` 采集逻辑，不受 Windows 分支影响。
- Bad：只在前端隐藏 `%SystemDrive%`，但备份仍把异常目录继续打包；或只修仪表板展示，不修 `/api/system/info` 的 0 值来源；或恢复时先删 live 目录，复制中途失败后留下空目录/半目录。
- Bad：隔离循环改回 `if !ShouldIgnoreScriptEntryName(entry.Name())`，或把 `node_modules` / `__pycache__` 加进 `quarantinedScriptDirNames` —— 每次启动搬一份进 quarantine（#156 回归）。
- Bad：清软链时按名字删所有 `node_modules` 软链，或删前不比对目标 —— 用户指向自己项目的软链被删。
- Bad：不悬空的面板软链也删（v3.3.5 第二波的写法，以为「启动时没人在用」）—— `ddp task run`、孤儿任务正在 `require` 的依赖突然消失，node 进程报 `ENOENT` 退出。
- Bad：直接 `WalkDir(scriptsDir)`、不先解析脚本根 —— 脚本目录本身是软链 / junction 时一条都清不到；只用 `EvalSymlinks` 也不够，Windows junction 它原样返回。
- Bad：把任意一遍整树遍历挪回同步启动路径，或只把其中一遍挪到后台 —— 留在同步路径上的那遍变成冷缓存上的第一遍，慢盘上照样几秒（#158）。
- Bad：ddp 的 `bootstrap` 重新整树扫（`CleanupManagedHelperCopiesUnderRoot` / `QuarantineUnexpectedScriptEntriesOnStartup`）—— 每条 ddp 命令都要多等几秒；
  会跑脚本的 `ddp python`、`ddp task run` 运行前已经各自清了自己的目录。
- Bad：副本扫描回调里遇到 err 照旧 `return err` —— 后台遍历正赶上目录被删时整棵停下，排在后面的目录都清不到。

### 6. Tests Required

- `TestShouldIgnoreScriptEntryName`
- `TestShouldIgnoreScriptPath`
- `TestShouldIgnoreScriptRelativePath`
- `TestQuarantineUnexpectedScriptEntriesOnStartup`
- `TestQuarantineDoesNotMoveGitRepository`
- `TestQuarantineKeepsNodeModulesAndPycacheInPlace`：顶层真 `node_modules`、`__pycache__` 原地不动，`%SystemDrive%` 仍被隔离，quarantine 里只有它一项。
- `TestQuarantineKeepsLiveNodeModulesLinks`：不悬空的软链一律原样保留——根目录与各级子目录里指向托管目录的面板软链、指向旧数据目录（目标还在）的、指向别处的；
  托管目录、旧目录、用户目录与真 `node_modules` 目录的内容都不受影响；相对路径软链保留（只在类 Unix 上验）；不创建 quarantine 目录。
- `TestQuarantineRemovesDanglingPanelNodeModulesLink`：托管目录与旧数据目录都删掉后，指向当前托管目录（根目录、子目录）与指向旧数据目录 `…/deps/nodejs/node_modules` 的悬空软链都被删；
  指向别处的悬空软链、真 `node_modules` 里面的悬空面板式软链、相对路径的悬空软链（只在类 Unix 上验）都保留。
- `TestQuarantineCleansDanglingLinksWhenScriptsDirIsLink`：脚本目录本身是链接（Windows 上是 junction，其它平台是软链）时，里面悬空的面板软链照样被删；
  里面不悬空的面板软链、根链接本身与里面的脚本都不动。
- 突变：把实现换回第二波（不判悬空、不解析脚本根），上面三条都红。
- `TestCleanupManagedHelperCopiesUnderRootSkipsHiddenDirs`：普通子目录里的托管副本照清，`node_modules` / `.git` / `__pycache__` 里的不被遍历。
- `TestCleanupManagedHelperCopiesUnderRootContinuesPastBrokenDir`（v3.3.6）：`a/notify.py` 是个目录（读它报错）、`b/sendNotify.js` 是托管副本、根目录也有一份托管 helper：
  返回非 nil 的错误；`b` 里的副本被删；根目录那份不动。它只管「单个目录出错不中止」这一半：`a/notify.py` 这个目录本身读得了，`WalkDir` 回调拿不到 err。
- `TestCleanupManagedHelperCopiesUnderRootSkipsUnreadableDir`（v3.3.6）：空目录 `a` 先 `chmod 0`（`t.Cleanup` 里改回 0755，否则 `t.TempDir` 删不掉）、`b` 里有托管副本：调用后 `b` 的副本被删（返回值不断言）。
  管另一半「读不了 / 已消失的目录跳过」。靠目录权限造读错误，Windows 与 root 上 `t.Skip`，只在 Linux 普通用户下真跑（WSL 默认用户 uid 1000、CI 的 GitHub runner）。
  突变：只把回调退回 `return err` 时只有这条红、上一条照样绿——两条缺一不可。
- `TestMainRunsScriptTreeWalksInBackground`（`service/startup_wiring_test.go`，AST 读 `../main.go`）：调用顺序 `EnsureBuiltinNotifyHelpers` → `QuarantineUnexpectedScriptEntriesOnStartup` → `CleanupManagedHelperCopiesUnderRoot` → `InitSchedulerV2`（各恰好一次）；
  两遍遍历在**同一个** `go` 语句里；`EnsureBuiltinNotifyHelpers` 不在任何 `go` 语句里。
- `TestDdpBootstrapDoesNotWalkScriptTree`（同上，AST 读整个 `../cmd/ddp/runtime.go`）：不出现 `service.CleanupManagedHelperCopiesUnderRoot` 或 `service.QuarantineUnexpectedScriptEntriesOnStartup` 调用。
  两条 AST 用例按相对路径读源码：在 WSL 里跑交叉编译的测试二进制时，要在仓库（或整个工作区副本）的 `server/service` 目录里跑。
- ⚠️ 软链用例用面板自己的 `createManagedDirectoryLink` 建链（Windows 上是 `mklink /J` 建 junction，其它平台 `os.Symlink`），建不了就 `t.Skip`；
  相对路径软链那段只在类 Unix 上验。Windows 上跑过不算数：必须交叉编译到 Linux / WSL 再跑一遍（`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./service`）。
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

```go
// 错误：隔离循环复用忽略名单 —— node_modules / __pycache__ 每次启动都被搬进 quarantine（#156，攒到 .duplicate-34）
for _, entry := range entries {
    if !ShouldIgnoreScriptEntryName(entry.Name()) {
        continue
    }
    _ = os.Rename(filepath.Join(scriptsDir, entry.Name()), uniqueQuarantinePath(...))
}
```

```go
// 错误：目标等于托管目录就删，且直接从 scriptsDir 遍历（v3.3.5 第二波）——
// 删掉 ddp task run / 孤儿任务正在用的软链（node 报 ENOENT）；脚本目录本身是软链 / junction 时一条都清不到；
// 数据目录搬家后指向旧目录的悬空软链又认不出来，一直挡住任务。
_ = filepath.WalkDir(scriptsDir, func(path string, d fs.DirEntry, err error) error {
    if err == nil && strings.EqualFold(d.Name(), "node_modules") {
        if target, readErr := os.Readlink(path); readErr == nil && filepath.Clean(target) == managedNodeModules {
            _ = os.Remove(path)
        }
    }
    return nil
})
```

```go
// 错误：只把副本扫描挪到后台，软链那遍留在同步路径（#158）——它变成冷缓存上的第一遍，慢盘上照样挡住监听几秒
go service.CleanupManagedHelperCopiesUnderRoot(cfg.Data.ScriptsDir)
service.QuarantineUnexpectedScriptEntriesOnStartup()
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

```go
// 正确：隔离只认污染目录名单；node_modules 只清悬空的面板式软链
for _, entry := range entries {
    if !quarantinedScriptDirNames[strings.ToLower(strings.TrimSpace(entry.Name()))] {
        continue
    }
    ...
}
removeLeftoverManagedNodeModulesLinks(scriptsDir, filepath.Join(config.C.Data.Dir, "deps", "nodejs", "node_modules"))
```

```go
// 正确：先解析脚本根，再只删「绝对路径 + 面板式目标 + 目标不存在」的软链
root := scriptsDir
if resolved, err := filepath.EvalSymlinks(scriptsDir); err == nil {
    root = resolved
}
if info, err := os.Lstat(root); err == nil && !info.IsDir() { // Windows junction：EvalSymlinks 原样返回
    if target, err := os.Readlink(root); err == nil && filepath.IsAbs(target) {
        root = target
    }
}
panelLinkSuffix := string(filepath.Separator) + filepath.Join("deps", "nodejs", "node_modules")
_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
    // ……名字是 node_modules：Readlink 失败 = 真目录，SkipDir；以下是软链 / junction
    cleanTarget := filepath.Clean(target)
    panelStyle := cleanTarget == managedNodeModules || strings.HasSuffix(cleanTarget, panelLinkSuffix) // Windows 上忽略大小写
    if !filepath.IsAbs(target) || !panelStyle {
        return nil // 相对路径、指向别处的：用户自己的
    }
    if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
        return nil // 目标还在：与面板现建的等价，可能正被 ddp task run / 孤儿任务使用
    }
    _ = os.Remove(path) // 失败只打日志
    return nil
})
```

```go
// 正确：根目录两份 helper 同步准备好；两遍整树遍历放进同一个后台协程（软链那遍在前），排在 InitSchedulerV2 之前、不等它
if err := service.EnsureBuiltinNotifyHelpers(cfg.Data.ScriptsDir); err != nil {
    log.Printf("prepare builtin notify helpers failed: %v", err)
}
go func() {
    service.QuarantineUnexpectedScriptEntriesOnStartup()
    if err := service.CleanupManagedHelperCopiesUnderRoot(cfg.Data.ScriptsDir); err != nil {
        log.Printf("cleanup duplicated notify helpers failed: %v", err)
    }
}()
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

- 触发：修改静默更新巡检 `server/handler/system_update_auto.go`、系统设置概览的手动「检查更新」（`useSettingsOverview.ts` 的 `handleCheckUpdate`）、「代理设置」页静默更新一组（`ProxyConfigCard.vue`），或 `systemConfigSchema.ts` 的 `CONFIG_KEYS_RENDERED_ELSEWHERE` / `READ_ONLY_CONFIG_KEYS` 时必须看本节。
- 原因：这个键是「运行状态」而不是用户偏好，却同时被后端巡检（判断是否满 24 小时）、手动检查（写入）、Web 展示（只读）三处使用。v3.2.9 删掉了概览页的「系统更新设置」卡，展示位置挪走了，但写入一处都不能跟着删；它必须保持注册，否则 `/api/configs/auto_update_last_checked_at` 会 404。

### 2. Signatures

- 后端注册：`newTrimmedStringConfig("auto_update_last_checked_at", "上次检查更新时间", "", "上次自动检查更新时间", "network")`
  （参数顺序：`key, label, defaultValue, description, group`）
- 后端读写：`runPanelAutoUpdateCheck()`（`autoUpdateLastCheckedAtKey`，未满 24 小时直接返回，否则先写 `time.Now().Format(time.RFC3339)` 再查新版本）
- Web 写入：`handleCheckUpdate()` 拿到检查结果后 `configApi.set({ key: 'auto_update_last_checked_at', value: new Date().toISOString() })`（失败静默）
- Web 读取：`useSettingsConfig.ts` 的 `autoUpdateLastCheckedAt = computed(() => String(rawConfigs.value.auto_update_last_checked_at?.value ?? '').trim())` → `index.vue` 传给 `ProxyConfigCard` 的 prop `autoUpdateLastCheckedAt` → 静默更新开关下方 `上次检查更新时间：{{ formatDateTime(..., '从未检查') }}`
- 备份：`shouldSkipRestoredSystemConfigKey` / `protectedRuntimeSystemConfigKeys` 把它与 `auto_update_pending_*` 一起排除在还原之外

### 3. Contracts

- 只要前端或后端读取某个系统配置键，这个键就必须在 `registeredSystemConfigSpecs` 中注册。
- 该键允许为空字符串，表示「从未检查」。
- **不是用户偏好**：不进 `configForm`、任何保存按钮都不回写它；Web 只从 `rawConfigs`（`GET /api/configs` 原样下发的值）只读展示，切到「代理设置」标签时 `handleTabChange` 重跑 `loadSystemConfigs` 顺带刷新。
- 唯一展示位置是「代理设置」页静默更新开关下方。`CONFIG_KEYS_RENDERED_ELSEWHERE.auto_update_last_checked_at` 指向 `web/src/views/settings/components/ProxyConfigCard.vue`，兜底区因此不再渲染它；`READ_ONLY_CONFIG_KEYS` 里的只读说明保留。
- 概览页不再有「系统更新设置」卡（`UpdateSettingsCard.vue` 已删），但**手动检查更新仍必须写入该键**：后端巡检按它判断距上次检查是否满 24 小时，删掉这次写入会让用户刚手动查过、巡检又立刻再查一遍。
- 备份还原不得覆盖它：还原旧备份会把「上次检查时间」倒回去，触发一次多余的检查（`TestRestoreBackupManifestSkipsAutoUpdateRuntimeStateConfigs`）。

### 4. Validation & Error Matrix

- 配置未写入数据库但已注册 → `GET /configs/:key` 返回默认值结构（空串），不能 404；代理设置页显示「从未检查」
- 配置已写入 → 返回实际保存值；代理设置页按本地时间格式化显示
- 手动检查更新失败（接口报错）→ 不写入；写入这一步自己失败 → 静默忽略，不打断检查结果提示
- 还原备份 → 该键保持还原前的当前值

### 5. Good/Base/Bad Cases

- Good：首次进入「代理设置」显示「上次检查更新时间：从未检查」，控制台和网络都不报错；点概览页「检查更新」后回到「代理设置」能看到新时间。
- Base：开着静默更新，巡检每 24 小时写一次，代理设置页随之更新。
- Bad：前端直接请求一个未注册配置键，导致 404。
- Bad：删概览页卡片时把 `handleCheckUpdate` 里的写入一起删掉，巡检的 24 小时判断失去手动检查这条输入。
- Bad：把它放进 `configForm` 随「保存配置」回写，页面上的旧值会覆盖巡检刚写入的新值。

### 6. Tests Required

- 后端测试：`cd server && go test ./...`（`TestRestoreBackupManifestSkipsAutoUpdateRuntimeStateConfigs` 锁住还原不覆盖）
- Web：`cd web && npx vue-tsc --noEmit -p tsconfig.app.json`
- 浏览器验收：「代理设置」页静默更新开关下方显示「上次检查更新时间」；概览页点「检查更新」后网络面板有一次 `POST /api/configs`（body 的 `key` 为 `auto_update_last_checked_at`），切回「代理设置」时间已更新；全程不触发该键的 404

### 7. Wrong vs Correct

#### Wrong

```go
newBoolConfig("auto_update_enabled", "静默更新", "false", "...", "network")
// 忘记注册 auto_update_last_checked_at
```

```ts
// 错误：删概览页卡片时连这次写入一起删了。巡检按这个键判断是否满 24 小时，手动检查从此不算数。
async function handleCheckUpdate() {
  const res = await systemApi.checkUpdate()
  updateInfo.value = res.data
}
```

#### Correct

```go
newBoolConfig("auto_update_enabled", "静默更新", "false", "...", "network")
newTrimmedStringConfig("auto_update_last_checked_at", "上次检查更新时间", "", "上次自动检查更新时间", "network")
```

```ts
// 正确：概览页不再展示，但手动检查照样记一笔；展示改由「代理设置」页从 rawConfigs 只读取值。
const now = new Date().toISOString()
void configApi.set({ key: 'auto_update_last_checked_at', value: now }).catch(() => {})
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
- **新增任何「面板自己打进任务日志」的元信息行（`[前置脚本环境变量] …`、`[前置脚本执行失败: …]`、`[后置脚本执行失败: …]`、v3.3.6 修复 A 的 `[已结束残留的后台进程：…]` 等），必须同步登记到 `task_executor.go` 的 `panelMetaLinePrefixes`**，否则它会混进任务成功通知的日志摘录（`summarizeTaskSuccessOutput`，上限 30 行 / 1500 字符），把用户真正想看的脚本输出挤掉。这个失败模式同样是静默的：任务照常成功，只是通知里看不到有用内容。
  失败摘要（`summarizeTaskFailureOutput`）**不整体套** `isPanelMetaLine`：「[脚本进程被信号终止：…]」这类行对诊断有用。新增的行如果会落在失败输出末尾、干扰失败摘要（Python 摘要认不出最后一行的异常、通用摘要的「上下文」取成这行提示），
  要另在 `normalizeTaskFailureLines` 里按前缀单独丢掉（TrimSpace 之后比前缀），目前只有 `[已结束残留的后台进程：` 这一种（v3.3.6）。

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
- 分组中文名是 Web 标签页、服务端提示文案、APP（按 `group_label` 渲染）共用的叫法，三处必须同名。当前 `tasks` =「任务运行」（v3.2.9 由「任务执行」改名，对齐 Web「任务运行」标签页与 `deps.go` / `runtime_exec.go` 里「系统设置 - 任务运行 - …」的指路文案）。改分组名、改任一项的 `Description` 都要重新生成演示站 fixtures（`go run ./cmd/gen-demo-fixtures`，保持 CRLF），否则 `TestCommittedDemoFixturesMatchRegistry` 红。
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
- `sendToChannel` 的 switch 里只许出现渠道类型字符串 case。按正文格式分支的逻辑（#135）放在 `notify_content_format.go` 的 `adaptNotifyContent` 与 `notifier.go` 的各 `sendXxxWithFormat`（内部 switch 用 `NotifyContent*` 常量）；`notify_content_format.go` 这类新文件不读 `cfg["..."]`、不定义 `send` 开头的函数——两个扫描器都只看 `notifier.go`。详见「脚本通知的正文格式与按渠道发送」场景。
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
- 唯一筛选点：`func loadEnabledNotificationChannels(channelIDs []uint, channelTypes ...string) ([]model.NotifyChannel, error)`（`channelTypes` 是 #135 加的变参过滤，老调用 `loadEnabledNotificationChannels(ids)` 原样可用）
- 写入口：`POST /api/notifications`、`PUT /api/notifications/:id`、`service.restoreNotifyChannels`
- 备份结构：`service.BackupNotifyChannel.PushScope`

### 3. Contracts

- **`push_scope` 是一等表列，不进 `notify_channel_registry.go`。** 那张注册表是 config JSON 的 schema 真源，被 `TestNotifySchemaCoversAllConfigKeysReadByNotifier` 用 go/ast 与 `notifier.go` 实际读的 `cfg["..."]` 键**双向绑死**；往里塞一个 `notifier.go` 根本不从 config 读的键，会直接把那条用例弄红。
- **取值必须是字符串枚举，不能改成 `IsDefault bool`。** 同一张表的 `Enabled bool gorm:"default:true"` 已经有一个踩过的活体坑：GORM 的 `ConvertToCreateValues` 把 `false` 当零值从 INSERT 里省掉，DB 侧的 `DEFAULT true` 反而生效（`DefaultValueInterface`），于是 `restoreNotifyChannels` 的 `tx.Create` 会把一条禁用渠道静默写回启用 —— 回归测试为此被迫写成 `Select("*").Create` + 单独 `Update`。bool 版的 push_scope 会以同样的方式把用户设的 bound 悄悄翻成 default，也就是把隔离意图反着执行。字符串的 Go 零值 `""` 归一后正好是 default，漏填只会退回升级前的老行为，方向安全。
- **定向发送完全忽略 `push_scope`。** `channelIDs` 非空时只按 ID 精确命中 —— 「绑定推送」存在的意义就是只在被显式指定时才推，再叠一层过滤等于把功能做废。`/notifications/send` 的 `channel_name(s)` 在 handler 里解析成 ID 并入 `channelIDs`，与 ID 同级，同样算定向。
- **`channelTypes` 只是过滤，不算定向**（#135）：定向时与 ID 取交集，广播时在「不等于 bound」的集合上再按类型筛。按类型选渠道若算点名，脚本一句 `notify.wxpusher_bot()` 就能打到别的脚本专用的绑定推送渠道。
- **广播过滤条件必须写 `COALESCE(push_scope, '') <> 'bound'`，禁止写 `= 'default'`。** 这一列的语义是「空即默认」：老库补列、手工改库、以及未来任何忘了填这一列的写入路径都会留下空串或 `NULL`，等值比较会让这些历史行静默退出广播。`COALESCE` 那一层是为了兜 `NULL` —— SQL 里 `NULL <> 'bound'` 求值为 `NULL`（不成立），不兜同样会漏。
- **广播 0 命中严格不兜底，但必须留一行 warn 日志。** 不允许「广播没命中就退回全部已启用渠道」——那等于取消隔离。改动前这条路径是完全静默的，用户只要把所有渠道都设成 bound，系统通知（资源告警、登录通知、静默更新结果）就会全部人间蒸发且零线索，所以 `log.Printf("warn: notification broadcast skipped: ...")` 是这条路径唯一可查的痕迹，不得删。
- **`PUT /notifications/:id` 是按键更新，请求里没出现的键一概不动已有值。** 独立发版的 Flutter APP 编辑渠道时不带 `push_scope`，改成「缺省即 default」会让用户在 Web 上设的 bound 被 APP 的一次保存悄悄清掉。**显式传 `null` 同样视为「未提供」**：APP 很可能把未填字段序列化成 null，按类型错误 400 会让它一升级就全线保存失败，代价远大于收益。其余非字符串类型仍然 400，拼错的字符串值也仍然 400。
- **`notifier.go` 里定向分支那句 `未找到已启用的通知渠道` 被 `notification_send_regression_test.go` 逐字断言，不得改动**（改文案会挂用例，也会让老客户端的错误匹配失效）。按类型过滤时只允许在后面追加 `（类型：…）`，前半句不动；广播分支带类型时是 `暂无参与广播的默认推送渠道（类型：…）；设为「绑定推送」的渠道需要用 channel_name 或 channel_id 点名`。托管 notify.py 的青龙同名函数靠这两句**开头**判断「没有匹配渠道、跳过」，改前半句会让它们改成抛错。
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

## 场景：脚本通知的正文格式与按渠道发送（issue #135）

### 1. Scope / Trigger

- 触发：修改 `server/handler/notification.go` 的 `Send`、`server/service/notify_content_format.go`、`notifier.go` 的 `sendToChannel` / `SendNotificationSyncWithOptions` / `loadEnabledNotificationChannels` / 任意 `sendXxxWithFormat`、`script_notify_helpers.go` 生成的 notify.py / sendNotify.js，或 `docs/script-api.md`、`web/src/views/api-docs/apiData.ts` 里 `/notifications/send` 的说明时必须看本节。
- 原因：脚本令牌是 operator，列不了渠道（`GET /notifications`、`GET /notifications/types` 都要 admin），「只发邮件」「HTML 正文」只能交给服务端按类型筛、按渠道映射。新能力叠在「不传就与 v3.2.8 逐字节一致」的承诺上：任务通知、系统通知、老脚本都不传这些字段，任何 `format == ""` 分支写偏都表现为全体用户的报文静默变化。

### 2. Signatures

- 接口：`POST /api/v1/notifications/send`（`JWTAuth` + `OpenAPIAccess("notifications")` + `RequireRole("operator")`）
- 归一：`service.NormalizeNotifyContentType(raw string) (string, bool)`；常量 `NotifyContentText` / `NotifyContentMarkdown` / `NotifyContentHTML`
- 分发：`service.NotificationDispatchOptions{ChannelIDs []uint; ChannelTypes []string; Context map[string]string; ContentType string}`、`SendNotificationSyncWithOptions(title, content string, options NotificationDispatchOptions) (NotificationDispatchResult, error)`
- 筛选：`func loadEnabledNotificationChannels(channelIDs []uint, channelTypes ...string) ([]model.NotifyChannel, error)`
- 渠道分发：`func sendToChannel(ch model.NotifyChannel, title, content string, context map[string]string, contentType string) error`（测试按钮 `SendNotificationToChannel` 传 `nil, ""`）
- 格式适配（`notify_content_format.go`）：`adaptNotifyContent(channelType, contentType, content string) (string, string)`、`notifyChannelAcceptsHTML map[string]bool`、`notifyHTMLToText(raw string) string`、`encodeNotifyQuotedPrintable(content string) string`
- 发送函数（`notifier.go`）：`sendWebhookWithFormat` / `sendEmailWithFormat` / `sendDingtalkWithFormat` / `sendPushplusWithFormat` / `sendWxPusherWithFormat`（`cfg, title, content, format`）；`sendWecomWithFormat` / `sendWecomAppWithFormat`（`cfg, title, content, context, format`）。旧签名 `sendEmail(cfg, title, content)`、`sendWecomWithContext(...)` 等保留为 `format=""` 的包装（测试在直接调）。
- 托管 helper：`managedNotifyHelperToken = "DAIDAI_PANEL_MANAGED_NOTIFY_HELPER v1"`；notify.py `def send(title, content, ignore_default_config=False, **kwargs):`、`def send_to(channel_type, title, content, content_type=None, **kwargs):`、`_channel_sender(name, channel_type)` 生成的 17 个青龙同名函数、`_no_channel_reason(err)`；sendNotify.js `async function sendTo(channelType, text, desp, params = {})`，`RESERVED_PARAM_KEYS` 增加 `content_type` / `channel_type` / `channel_types` / `channel_name` / `channel_names`

### 3. Contracts

请求字段（全部可选、只做加法）：

| 字段 | 类型 | 规则 |
|---|---|---|
| `content_type` | string | 小写、去首尾空白、丢掉 `;` 之后的参数后：`text` / `plain` / `txt` / `text/plain` → `text`；`markdown` / `md` / `text/markdown` → `markdown`；`html` / `text/html` → `html`。**空串 = 调用方没声明，不等于 text** |
| `channel_type` / `channel_types` | string / []string | 单值排在数组前面合并；大小写不敏感，归一成注册表 `Type` 的写法再过滤；是**过滤**，不是点名 |
| `channel_name` / `channel_names` | string / []string | 单值排在数组前面合并；按名称精确匹配（区分大小写、不 trim，名称列有唯一索引）；解析出的 ID 并入 `channelIDs`，与 `channel_id(s)` 同级 = 点名 |

- **选择语义**：有点名（ID 或名称）→ 按 ID 精确命中、忽略 `push_scope`；没点名 → 广播集合（`COALESCE(push_scope, '') <> 'bound'`）。`channel_types` 在两者之上取交集，**广播时照样遵守 push_scope**。名称解析不看 `enabled`：禁用渠道交给下游报「未找到已启用的通知渠道」。
- **响应 `data` 只增不改**：新增 `content_type`（归一后的值，没传为 `""`）与 `channel_types`（归一后的类型名，没传为 `[]`）；`requested_ids` 含按名称解析出的 ID；`used_all` 仍是「没有任何点名」，`channel_types` 不影响它。
- **`content_type` 为空 = 各渠道报文与 v3.2.8 逐字节一致。** `adaptNotifyContent` 与每个 `sendXxxWithFormat` 在 `format == ""` 时必须原样走改动前的分支。由 `notifier_legacy_payload_test.go` 的 golden 守护：golden 是在 v3.2.8 的 `notifier.go` 上用同一套用例真跑抓下来的请求（方法、路径、关键请求头、请求体），不是新代码和自己的包装函数比。有意改变某渠道默认报文时，必须同时改 golden 并在提交说明写清为什么老行为可以变。igot / qmsg / pushover 地址写死、这次没改，golden 覆盖不到；pushplus 在 `notifier_content_format_test.go` 单独守；serverchan 两代接口（Turbo 版、Server酱³）的地址后来（v3.3.4）也改成了包级变量，请求路径与请求体（`title` / `desp`）由 `notifier_serverchan_test.go` 守。
- **渠道映射（只在 `content_type` 非空时生效）**：
  - 进渠道前先 `adaptNotifyContent`：渠道不在「能收 HTML」名单且调用方声明 html → `notifyHTMLToText` 去标签，格式交还成 `""`，按渠道配置的文本类消息发（不强制改成 text 消息）。能收 HTML 的只有 `webhook` / `email` / `pushplus` / `wxpusher` / `custom`。`notifyChannelAcceptsHTML` 必须覆盖注册表全部类型；ntfy 的 Markdown 头、gotify extras、Bark markdown 字段依赖服务端版本、老版本静默忽略，**不因「新版本支持」标 true**。
  - email：html → `MIME-Version: 1.0` + `Content-Type: text/html; charset=UTF-8` + `Content-Transfer-Encoding: quoted-printable`（压缩成一行的 HTML 超过 998 字节会被部分 SMTP 拒收；不带 MIME-Version 部分客户端照样显示源码）；text / markdown 仍是原 `text/plain` 报文，逐字节不变。
  - wxpusher：text → 1、html → 2、markdown → 3，覆盖渠道配置的 `content_type`（那是「渠道默认怎么发」，与请求字段不是一回事）。调用方声明 html 时正文**不转义**、不套 pre-wrap 的 div，标题仍 `html.EscapeString`；**没声明格式、只是渠道配成 2 时继续转义**——任务通知里嵌着脚本日志，放开会把日志里的 `<` 当标签渲染。
  - pushplus：html / markdown / text → `template` 为 `html` / `markdown` / `txt`；渠道配置的 template 是 `json`（不分大小写）时不覆盖。
  - dingtalk：text → text 消息，markdown → markdown 消息。
  - wecom（机器人）：只在 text / markdown / markdown_v2 之间切。text → text；markdown → 仅当配置是 text **且 `mentioned_list`、`mentioned_mobile_list` 都为空**才切 markdown（markdown 消息没有 @ 列表，@ 会被脚本一个参数静默丢掉）；原本 markdown_v2 的保持 v2；image / news / template_card 不受影响。
  - wecom_app：**永不因调用方格式从 text 切到 markdown**。企业微信 markdown 应用消息在微信插件（微工作台）里不显示，也没有 `safe` / `enable_id_trans`，管理员设的保密消息会被脚本一个参数变成普通消息；markdown 原文按文本发照样读得懂。只允许「配置 markdown + 调用方 text」切回 text；image / file / video / news / mpnews / template_card 不受影响。
  - 调用方格式**改变了消息类型**时（wecom / wecom_app），不套渠道配置的 `content_template`，改用该分支默认模板（text `{{title}}\n{{content}}`、markdown `**{{title}}**\n{{content}}`），否则 markdown 模板里的 `**` 原样出现在文本消息里；类型没变照常套。
  - webhook：format 非空时 JSON 多带 `content_type` 键；为空时键集合仍只有 `title` / `content`。
  - custom 原样透传（不提供 `{{content_type}}` 占位）；telegram 不设 `parse_mode`；serverchan、discord 等其余渠道 markdown 原样发、html 已在前面去标签。
- **去标签必须保持线性**：`notifyHTMLToText` 用 `golang.org/x/net/html` 的 tokenizer，外加只记 SVG / MathML 元素的 `notifyForeignStack`（按名字计数，结束标签在栈里没有同名元素时不扫栈）。**不许换成 `html.Parse` 建树**：它的开放元素栈在深层嵌套下是平方复杂度，x/net v0.33 实测 10 万层 `<pre>` 71 秒，另一输入直接顶到 5 分钟测试超时；通知正文来自脚本，不能被一段畸形 HTML 卡住发送。行为口径：块级标签与 br 断行、同一行 td / th 用「 | 」分隔、丢弃 script / style / title / noscript / iframe / noembed / noframes / template、隐藏 SVG 的 title / desc / style / script 与 MathML 的 annotation(-xml)、外来内容里自闭合真闭合（`<svg><title/></svg>` 之后的正文不能丢）、breakout 表里的开始标签和 `</p>` `</br>` 跳出外来内容、`</template>` 连同模板里打开的 SVG / MathML 一起关、实体反转义、合并多余空行。已知残余：HTML 父元素的结束标签（`</div>` 等）不会关闭没闭合的 svg style / script。
- **护栏约束**：
  - `sendToChannel` 的 switch 里**不许出现任何新的字符串 case**：`TestNotifySchemaCoversAllChannelTypesHandledByNotifier` 把函数里全部字符串 case 当渠道类型比对注册表，写一句 `case "html"` 就被当成「注册表没声明的渠道」。格式分支放在 `adaptNotifyContent` 与各 `sendXxxWithFormat`，内部 switch 用 `NotifyContent*` 常量。
  - 被 `sendToChannel` 调用的 `sendXxx` / `sendXxxWithFormat` **必须定义在 `notifier.go`**（`TestNotifierSourceHoldsEverySenderCalledBySendToChannel`）；新文件**不读 `cfg["..."]`、不定义 `send` 开头的函数**——schema 绑定扫描器只解析 `notifier.go`，挪出去的配置读取会静默逃出双向绑定。配置读取保持 `cfg["content_template"]`、`cfg["mentioned_list"]` 这种字面量写法。
- **托管 helper**：
  - 托管标记保持 ` v1`（改了之后磁盘上的老 v1 文件会被当成用户自定义、永久停止更新），`def send(title, content, ignore_default_config=False, **kwargs):` 这一行逐字不变（测试逐字断言）。
  - `send` 从 kwargs 取出 `content_type`、`channel_type(s)`、`channel_name(s)` 放进请求体，**不再进 context**；`content_type` 不是 str 时（老脚本拿它当模板变量，如 `2`）留在 context、不发给面板被 400。sendNotify.js 同口径（`typeof value === 'string'` 才进请求体）。
  - 有类型或名称选择器、又没显式传 `channel_id(s)` 时不回落 `DAIDAI_NOTIFY_CHANNEL_ID`；显式传了空的类型 / 名称原样发出去由面板 400，不能按真假判断丢掉、悄悄变成广播。
  - 17 个青龙同名函数：`wxpusher_bot`→wxpusher、`smtp`→email、`pushplus_bot`→pushplus、`dingding_bot`→dingtalk、`feishu_bot`→feishu、`telegram_bot`→telegram、`wecom_bot`→wecom、`wecom_app`→wecom_app、`bark`、`gotify`、`iGot`→igot、`serverJ`→serverchan、`pushdeer`、`qmsg_bot`→qmsg、`pushme`、`ntfy`、`custom_notify`→custom。类型名必须在注册表里；不加 email / telegram / discord / slack 这类裸名（和常见包重名，`from notify import *` 会互相覆盖），不加 `__all__`，面板独有类型用 `send_to`。
  - **没有匹配渠道时青龙同名函数跳过、`send` / `send_to` 抛错**：同名函数捕获 `RuntimeError`，只有 HTTP 400、且去掉「发送失败: 」前缀后**以**「暂无参与广播的默认推送渠道」或「未找到已启用的通知渠道」**开头**时，打印 `<函数名> 跳过推送：<面板原文>` 并返回 None（与青龙一样，脚本里下一句推送照常执行）；其余错误照抛。只比开头：渠道自己发送失败时报错是「渠道名: 下游正文」，下游正文里碰巧含这两句不能当成没有渠道。
  - notify.py 保持 Python 3.6 语法（不用海象运算符、仅位置参数等），`ast.parse(..., feature_version=(3, 6))` 兜底。

### 4. Validation & Error Matrix

| 请求 | 结果 |
|---|---|
| 不带任何新字段；显式 `null`；`channel_types: []` / `channel_names: []` | 200，筛选与各渠道报文与 v3.2.8 逐字节一致；响应 `data` 只多出 `content_type: ""`、`channel_types: []` |
| `content_type: "Text/HTML; charset=UTF-8"` / `"text/plain"` / `"MD"` | 200，响应回显 `html` / `text` / `markdown` |
| `content_type: "json"` / `"application/json"` / `"; charset=utf-8"` | 400 `content_type 只能是 text / markdown / html（不传则按渠道配置发送）`，不发送 |
| `content_type: 2`、`channel_types: "webhook"`（类型不对） | 400 `请求参数错误`（绑定失败，不点名字段；现状如此） |
| `channel_type: "WebHook"`、`channel_types: ["CUSTOM", " Webhook "]` | 200，回显 `["webhook"]` / `["custom", "webhook"]` |
| `channel_type: "weixin"` | 400 `未知的通知渠道类型：weixin（可选：webhook、email、telegram、…）`，列出注册表全部类型（脚本令牌调不了 `/notifications/types`） |
| `channel_type: "  "`、`channel_types: [""]` | 400 `通知渠道类型无效：channel_type / channel_types 不能为空`，不退化成「不过滤」 |
| `channel_name: ""`、`channel_names: [" "]` | 400 `通知渠道名称无效：channel_name / channel_names 不能为空` |
| `channel_names: ["广播渠道", "不存在的渠道"]`；名称带尾随空格（`"广播渠道 "`） | 400 `未找到名称为「不存在的渠道」的通知渠道`（逐个列出查不到的名称），绝不退化成广播、也不发给查得到的那几个 |
| 按名称点名绑定推送渠道 | 200，只发这一个；`requested_ids` 含它的 ID，`used_all=false` |
| 点名 + 类型交集为空 | 400 `发送失败: 未找到已启用的通知渠道（类型：webhook）` |
| 广播 + 类型无命中（含该类型只有 bound 渠道） | 400 `发送失败: 暂无参与广播的默认推送渠道（类型：email）；设为「绑定推送」的渠道需要用 channel_name 或 channel_id 点名` |
| wecom_app（text、safe=1、enable_id_trans=1）+ `markdown` | 仍发 text 消息，带 `safe` 与 `enable_id_trans` |
| wecom 机器人 text + `mentioned_list` + `markdown` | 仍发 text，保留 @ 列表；@ 列表为空时切 markdown |
| 渠道配 markdown + 自定义 `content_template`，调用方 `text` | 发 text 消息，用 `{{title}}\n{{content}}` 默认模板 |
| notify.py 同名函数，面板没有该类型的默认推送渠道 / 只有 bound / 点名的渠道已禁用 | 打印跳过、返回 None，后续调用照常发 |
| notify.py 同名函数，渠道真的发送失败（下游 400 正文里恰好含「暂无参与广播的默认推送渠道」）；`channel_name` 写了不存在的名称 | 抛 `RuntimeError` |
| `send` / `send_to`，没有匹配渠道 | 抛 `RuntimeError`（保持严格） |

### 5. Good/Base/Bad Cases

- Good：`notify.send("日报", html, content_type="html")` → 邮件收到渲染好的表格、WxPusher 显示表格、Telegram / 钉钉收到去标签的纯文本；没配 WxPusher 时 `notify.wxpusher_bot(...)` 打印跳过，下一句 `notify.smtp(...)` 照常发。
- Base：任务通知、系统通知、老脚本都不传 `content_type`，23 个渠道配置的报文与 v3.2.8 逐字节一致。
- Bad：空串归一成 text——钉钉默认 markdown、WxPusher 配成 2 的渠道，老脚本一升级报文就变了。
- Bad：按类型选渠道算点名——`notify.wxpusher_bot()` 打到别的脚本专用的绑定推送渠道。
- Bad：wecom_app 跟着 markdown 切消息类型——保密消息变普通消息，微信插件里还看不到（Wave 2 复查 major）。
- Bad：WxPusher 渠道配成 2 时一律不转义——任务日志里的 `<` 被当成真标签渲染，详情页多一个注入面。
- Bad：在 `sendToChannel` 里写 `case "html"`，或把 `sendXxxWithFormat` 拆到新文件——前者 schema 绑定红，后者读到的 `cfg` 键静默逃出绑定。
- Bad：用 `html.Parse` 去标签——一段深嵌套的畸形 HTML 让一次发送卡几十秒到几分钟。
- Bad：同名函数用子串匹配「没有渠道」——渠道真失败、下游正文恰好含这句时被静默吞掉。
- Bad：把 `text/html`、`text/plain` 这类 MIME 写法当非法值 400——#135 用户最可能先试的就是它们。

### 6. Tests Required

见 `server/service/notifier_legacy_payload_test.go`、`server/service/notifier_content_format_test.go`、`server/service/script_notify_helpers_test.go`、`server/handler/notification_send_format_test.go`：

- `TestSendToChannelEmptyContentTypeKeepsLegacyPayloads`（**最重要的一条**：23 个渠道配置的 v3.2.8 真实报文 golden，另加邮件 text / markdown 仍等于原 `text/plain` 报文；不许为了让它变绿去改 golden）
- 归一与去标签：`TestNormalizeNotifyContentType` / `TestNotifyChannelHTMLCapabilityCoversRegistry`（加渠道漏登记能力表直接红）/ `TestNotifyHTMLToText`（SVG / MathML 自闭合、`</p>` `</br>` 跳出、template、iframe 等）
- 渠道映射：`TestSendEmailHTMLContentTypeUsesTextHTML` / `TestSendWxPusherContentTypeOverridesChannelFormat` / `TestSendPushplusContentTypeMapsTemplate` / `TestSendDingtalkAndWecomContentTypeSwitchesTextMessages` / `TestSendWecomBotContentTypeKeepsMentionsAndSwitchedTemplate` / `TestSendWecomAppContentTypeSwitchesTextMessages`（safe=1、enable_id_trans=1 + markdown 仍是 text）/ `TestSendToChannelStripsHTMLForTextOnlyChannels` / `TestSendWebhookForwardsDeclaredContentType` / `TestSendNotificationSyncFiltersByChannelType`
- 接口：`TestNotificationSendRejectsInvalidContentType` / `TestNotificationSendAcceptsMIMEContentTypeAndAnyCaseChannelType` / `TestNotificationSendRejectsUnknownOrBlankChannelType` / `TestNotificationSendChannelTypeRespectsPushScope` / `TestNotificationSendByChannelNameTargetsBoundChannel` / `TestNotificationSendForwardsContentTypeToWebhook`
- 文档：`TestNotifySendDocsListEveryChannelType`（`docs/script-api.md` 与 `apiData.ts` 的渠道类型说明必须列全注册表类型、不得再指向 `/notifications/types`；只拷了 server 目录时 skip）
- helper（真跑 Python / Node，找不到解释器会 skip，改 helper 后用 `-v` 确认没被 skip）：`TestManagedNotifyPyForwardsContentTypeAndSelectors`（含 3.6 语法解析、`content_type=2` 留在 context）/ `TestManagedSendNotifyJSForwardsContentTypeAndSelectors` / `TestManagedNotifyPyQingLongSendersSkipWhenNoChannelMatches`（用服务端真实报错文本；渠道真失败必须抛）/ `TestManagedNotifyPyChannelSendersUseRegisteredTypes`（恰好 17 个、类型都在注册表）/ `TestManagedNotifyHelperTokenStaysV1` / `TestManagedHelperContentIncludesUsageDocs`
- 护栏仍须全绿：`TestNotifySchemaCoversAllConfigKeysReadByNotifier` / `TestNotifySchemaCoversAllChannelTypesHandledByNotifier` / `TestNotifierSourceHoldsEverySenderCalledBySendToChannel`
- 性能：仓库里**没有**常驻的去标签性能用例。改 `notifyHTMLToText` 时要自己拿超大 / 畸形输入测一遍（10 万层 `<pre>`、20 万层 `<div>`、几十万个不同的 SVG 标签名、MB 级 CDATA 与实体），每个都应在百毫秒量级。
- 修改后至少运行：

```bash
cd server
go test ./service -run "TestSendToChannelEmptyContentTypeKeepsLegacyPayloads|TestNormalizeNotifyContentType|TestNotifyHTMLToText|TestNotifyChannelHTMLCapability|TestSendEmailHTML|TestSendWxPusherContentType|TestSendPushplusContentType|TestSendDingtalkAndWecom|TestSendWecomBotContentType|TestSendWecomAppContentType|TestSendToChannelStripsHTML|TestSendWebhookForwards|TestSendNotificationSyncFilters|TestManagedNotify|TestManagedSendNotifyJS|TestManagedHelperContent|TestNotifySchema|TestNotifierSource" -count=1 -v
go test ./handler -run "TestNotificationSend|TestNotifySendDocs" -count=1
go test ./...
```

> **突变验证**：把 `sendWecomAppWithFormat` 改回「调用方 markdown 时 text → markdown」，`TestSendWecomAppContentTypeSwitchesTextMessages` 必须变红；把 `_no_channel_reason` 的 `startswith` 改成子串包含，`TestManagedNotifyPyQingLongSendersSkipWhenNoChannelMatches` 必须变红；把 WxPusher「没声明格式时转义」那一支删掉，golden 的 `wxpusher_2` 必须变红。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：空串当 text。老脚本、任务通知一个字段没传，钉钉默认的 markdown 就被改成文本消息。
if value == "" {
    return NotifyContentText, true
}
```

```go
// 错误：wecom_app 跟着调用方切 markdown。markdown 应用消息没有 safe / enable_id_trans，
// 微信插件里也不显示；类型变了还照套渠道的 markdown content_template。
if msgType == "text" && format == NotifyContentMarkdown {
    msgType = "markdown"
}
contentTemplate := cfg["content_template"]
```

```python
# 错误：子串匹配。渠道真发送失败、下游正文里恰好有这句时，也被当成「没有渠道」静默跳过。
if "暂无参与广播的默认推送渠道" in str(err):
    return None
```

#### Correct

```go
// 正确：空串原样返回空串（没声明），各渠道走改动前的分支。
if value == "" {
    return "", true
}
```

```go
// 正确：只允许 markdown → text；切了类型就不用渠道配置的模板。
if msgType == "markdown" && format == NotifyContentText {
    msgType = "text"
}
contentTemplate := cfg["content_template"]
if msgType != configuredMsgType {
    contentTemplate = ""
}
```

```python
# 正确：去掉「发送失败: 」前缀后只比开头。
if detail.startswith("发送失败: "):
    detail = detail[len("发送失败: "):]
for no_channel in _NO_CHANNEL_MARKERS:
    if detail.startswith(no_channel):
        return detail
return None
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
- 面板自请求退出的唯一出口：`var panelProcessExit`（默认 `os.Exit`；面板主程序启动时用 `SetPanelExitRequester` 换成「请求 main 走 `shutdownPanel` 收尾后按原退出码退出」，ddp 等其它进程不接线）/ `var panelProcessExitDelay`（只为可测）。
  Restart（退出码 1）、StopPanel（0）与二进制 / Magisk 在线升级让位（0）共用，v3.3.5 起不再直接 `os.Exit`，见「场景：停机收尾」。

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
  `splitCommandTokens`，或者改任务命令的格式时，必须看本节。cron 从哪来、没识别到 cron 声明的脚本怎么处理见下一节（#134），
  两节共用同一套「认任务 / 接管 / 删除判定」。
- 背景：v3.0.5 起用「订阅锁」`subscription_locked` 挡住同步覆盖用户手改的名称/定时，但匹配仍按命令原文：
  命令一加参数（`task x.js now`、`task x.js desi JD_COOKIE`）就对不上 → 新建一条重复任务；
  开着自动删除时，还会把改过命令的那条连历史日志删掉（#125）。
  #125 起改成「按脚本认任务、认出就一律不动」，订阅锁整套下线。

### 2. Signatures

- 同步入口：`syncSubscriptionTasks(sub *model.Subscription, emit PullCallback)`
- 扫描：`scanSubscriptionTaskCandidates(sub, options) subscriptionCandidateScan`，比旧版多给 `seenFiles`（本次读到的全部文件）
  与 `undeclaredCron`（过了全部过滤规则、不是辅助脚本、没识别到 cron 声明、默认规则又没有可用值，因而不建任务的文件，#134）。
  `collectSubscriptionTaskCandidates(sub, options) (map[string]subscriptionTaskCandidate, []string)` 签名不变，是它的包装（有测试直接调）。
- 字典与求键（`subscription_task_script_match.go`）：`newSubscriptionScriptIndex(scriptsDir, candidates)`、
  `normalize(ref)`、`candidateKey(command)`、`scriptKeyOf(command)`、`scriptKeyIn(command, known func(key string) bool)`、`taskCommandScriptRefs(command)`。
- 未建任务的受管脚本：`newSubscriptionUndeclaredScripts(index, scriptsRoot string, files []string) *subscriptionUndeclaredScripts`，
  每项带 `command`（`task <相对 ScriptsDir 的路径>`，与候选命令同口径）、`key`（归一不了为空）、`claimable`（键有歧义时 false）；
  `hasKey(key)` / `hasCommand(command)` 对 nil 安全（nil 即空集合）。
- 删除判定：`newSubscriptionStaleTaskJudge(index, candidates, saveDir, seen, undeclared *subscriptionUndeclaredScripts).judge(command) (verdict, script, statErr)` →
  `staleTaskKeep` / `staleTaskKeepUnscanned` / `staleTaskKeepStatError` / `staleTaskDelete`（`script`、`statErr` 只给日志用；`undeclared` 传 nil 就是 #134 之前的判定）。
  判定里的 Stat 是包级变量 `subscriptionScriptStat = os.Stat`（只为单测能构造 EACCES、网络盘错误码）；「这个前缀不是脚本」由
  `subscriptionScriptNotAScript(err)` 判，只收「不存在」类 `subscriptionScriptMissing(err)` 与名字类 `subscriptionScriptBadName(err)`
  （平台相关部分 `subscriptionScriptBadNamePlatform`，build tag 分文件）；其余 Stat 错误都让删除退成保留，**不设白名单**。
- 接管池：`loadSubscriptionAdoptPool(index, managed []model.Task, undeclared *subscriptionUndeclaredScripts) (*subscriptionAdoptPool, error)`，
  `(*subscriptionAdoptPool).take(command, key string, keyOK bool) []*model.Task`（按 id 升序去重；`command` 为空时不取 `byCommand[""]` 里的空命令任务；nil 池返回 nil）。
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
  **没识别到 cron 声明、本次不建任务的脚本（undeclared）同样接管**（#134，与 v3.2.8 它们还是兜底候选时一致）：认法与候选相同
  （精确命令，或 `claimable` 时按键），本订阅已有任务在跑它（命令相同或 `scriptKeyIn(command, undeclared.hasKey)` 命中）就不接管。
  不接管的话，青龙导入、手建、删订阅再重建留下的同脚本任务不归订阅管，上游删了脚本也不会被自动删除。
  接管只在自动添加开着时做。候选池只在「有待新建的候选」或「有还没任务的 undeclared 脚本」时全表加载一次，在 Go 里过滤，**不写 `NOT IN`**；
  池里一个任务至多一个键（一个文件只会落在候选与 undeclared 之一）。读任务列表失败 → `failed++` +
  `[关联已有任务失败] 读取任务列表出错，本次不新建任务，以免与已有任务重复: …`，本轮不建、不接管，也不打 #134 的「未建定时任务」提示。
- **自动删除**（开关语义不变，只看 `task ` 开头的命令）：
  1. 本次候选**与** undeclared **都**为空 → 熔断，整段跳过；有托管任务时打 `[跳过自动删除] 本次没有识别到任何候选脚本，为防误删，未删除任何任务`。
     只有 undeclared、没有候选（整个仓库都没写 cron 头）时不熔断：改动前这些文件按兜底 cron 都是候选、不会熔断，只看候选的话这类订阅从此再也删不掉上游已删脚本的任务。
  2. 命令与候选完全相同或键命中 → 保留。命令与 undeclared 的精确命令相同，或 `scriptKeyIn(command, undeclared.hasKey)` 命中 → 同样保留
     （#134：升级前兜底建出来的 0 点任务、用户改过定时和参数的都算）。精确命令不能省：文件名带引号（`it's.js`）或被空格隔开的 `--`（`a -- b.js`）时
     命令切不开、求不出键，升级前原样建出的命令只能靠它认出。
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
- **条件删除**：一个事务里先经 `DeleteTaskLogs(tx, "task_id = ?", task.ID)` 把该任务的 `task_logs` 并入执行趋势计数再删（v3.3.6，#158；它必须是事务第一条语句，这里本来就是，见「场景：执行日志删除与执行趋势归档」），
  再 `DELETE FROM tasks WHERE id=? AND command=?`；`RowsAffected≠1` 或任一步出错就回滚（日志与刚并入的计数一起原样恢复）；提交后再 `RemoveJob`。删日志出错的文案仍是 `删除历史日志失败: %w`。
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
| 候选与没识别到 cron 声明的脚本都为空（检出为空、单文件订阅 SaveDir 为空、只剩辅助脚本） | 一条不删 | `[跳过自动删除]` |
| 只有没识别到 cron 声明的脚本、没有候选（整个仓库都没写 cron 头） | 不熔断：这些脚本的已有任务保留，上游删掉的照删 | `[自动删除任务]`（仅被删的） |
| 没识别到 cron 声明的脚本的已有任务（原样命令、改过参数 / 定时、文件名切不开的原样命令） | 不动、不新建、不删 | — |
| 没识别到 cron 声明的脚本 + 无本订阅标签 / 悬空旧标签的同脚本任务（青龙导入、手建） | 自动添加开着时只加标签；关着时不动 | `[关联已有任务]` |
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
- Bad：把没声明 cron 的脚本移出候选（#134），却只改扫描不改认任务——删除判定不认 undeclared，存量 0 点任务连日志被删（实测判 `staleTaskDelete`）；
  熔断只看候选，整仓无 cron 头的订阅永久跳过自动删除；接管池只按候选求键，青龙导入的同脚本任务从此不归订阅管（Wave 2 复查）。

### 6. Tests Required

- `subscription_task_script_match_test.go`：`scriptKeyOf` / `normalize` 表驱动（task、desi、解释器、托管命令、`-m`、`-l`、`--`、引号、
  带空格路径、目录内外绝对路径、`.bak`、引号未闭合、空串、Windows 反斜杠与大小写、Linux 上反斜杠不算分隔符）；
  Windows 候选大小写冲突；删除判定的「扫描漏读」（人为缺项的 `seen` 集合）；Docker 别名（Linux，Windows 上 `t.Skip`）；
  undeclared 的保留判定 `TestSubscriptionStaleTaskJudgeKeepsUndeclaredCronScripts`（改过参数、`desi`、`-m`、`./`、带空格路径、`it's.js` / `a -- b.js` 原样命令 → 保留；
  原样命令加了参数又切不开、被规则排除、SaveDir 外 → 删；传 nil 时回到改动前的「删」）。
- 未识别 cron 的脚本的接管、熔断与保留（`subscription_undeclared_cron_test.go`，清单见下一节）：`TestSyncSubscriptionTasksKeepsExistingTasksOfUndeclaredCronScripts`
  （含 `only_undeclared_scripts`：只有 undeclared 时不熔断）、`TestSyncSubscriptionTasksAdoptsUnmanagedTaskOfUndeclaredCronScript`、
  `TestSyncSubscriptionTasksUndeclaredSkipsAdoptionWhenAlreadyManaged`、`TestSyncSubscriptionTasksUndeclaredNeverAdoptsEmptyCommandTask`、
  `TestSyncSubscriptionTasksKeepsUndeclaredCronTasksWithOddFileNames`；熔断本身仍由 `TestSyncSubscriptionTasksEmptyCandidatesSkipsAutoDelete` 锁住。
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
// ... 自动删除：候选与没识别到 cron 声明的脚本都为空才熔断；删除要有正面证据（Stat 只有「不存在」类与名字类算不是脚本，
// 其余 Stat 错误都保留，见 subscriptionScriptNotAScript）；
// 还被别的、仍存在的订阅使用 → 只解除关联；否则按快照条件删，删不到就回滚（日志随之恢复）。
if len(candidates) == 0 && len(scan.undeclaredCron) == 0 {
    // [跳过自动删除] …
}
judge := newSubscriptionStaleTaskJudge(scriptIndex, candidates, saveDir, seenKeys, undeclared) // undeclared 的精确命令与键都判保留（#134）
verdict, scriptPath, statErr := judge.judge(task.Command) // 后两个只在保留判定时用于日志
if verdict == staleTaskDelete {
    if usedByOtherSubscription(task, otherLabels) { // hasLabelFold：与 queryTasksByLabel 的 LIKE 同口径，忽略大小写
        changed, err := detachSubscriptionTaskIfUnchanged(task, label) // UPDATE … WHERE id=? AND command=? AND labels=?
        // err → failed++ + [解除关联失败]；!changed → 快照过期时 [保留任务] …同步期间…，标签本就不带则静默；否则 detached++ + [解除关联]
        continue // 解除关联之后绝不能再走删除：条件删除只比对 id+command，不看 labels
    }
    // 事务：先经 DeleteTaskLogs 把 task_logs 并入执行趋势计数再删（事务第一条），再 DELETE … WHERE id=? AND command=?；
    // RowsAffected≠1 或出错就回滚（计数一起回滚）
    removed, err := deleteSubscriptionTaskIfUnchanged(task)
    // ...
}
```

---

## 场景：订阅脚本的 cron 来源与「没识别到 cron 声明」的脚本（issue #134）

### 1. Scope / Trigger

- 触发：修改 `server/service/subscription.go` 的 `scanSubscriptionTaskCandidates`（cron 来源分支）、`getSubscriptionTaskSyncOptions` / `subscriptionDefaultCronRule`、
  `syncSubscriptionTasks` 里的扫描日志与「未建定时任务」提示、头部解析 `eachSubscriptionScriptHeadLine` / `resolveSubscriptionScriptCron` /
  `extractSubscriptionCronExpression*`、`subscriptionHelperScriptNames`，注册表 `default_cron_rule`，`task_script_cleanup.go` 的 `subscriptionReaddSuffix`，
  或演示站 `web/src/demo/db.ts` 的 `demoSubscriptionReaddsTask` 时必须看本节。认任务、接管、删除判定见上一节（#125）。
- 背景：v2.2.10 ～ v3.2.8 在 `default_cron_rule` 留空（出厂默认）时硬兜底 `FallbackSubscriptionCron = "0 0 * * *"`：头部认不出 cron、又不在辅助脚本名单里的文件
  一律建成**启用**的每天 0 点任务，而且关不掉（非法值拒写、空值被换成 0 点）。`config.py`、`mysend.py` 这类库文件和拉取后钩子半夜跑，删了下次拉取又回来。
  #134 起留空 = 不建，兜底常量已删除。

### 2. Signatures

- 选项：`getSubscriptionTaskSyncOptions(sub) subscriptionTaskSyncOptions`，`defaultCron`（只存合法的用户规则，否则 `""`）、`ignoredDefaultCron`（库里存着但 `cron.Parse` 不认的原值，只给日志用）
- 读规则：`subscriptionDefaultCronRule() (rule, ignored string)`（扫描与删任务预览共用）
- 注册表：`newValidatedStringConfig("default_cron_rule", "默认 Cron 规则", "", "订阅脚本未声明 cron 时使用；留空则不为这类脚本建定时任务", "subscription", normalizeDefaultCronRule)`（保存时非法值拒写）
- 候选：`subscriptionTaskCandidate.DefaultRule bool`（cron 来自默认规则，只用于日志分开计数与标注）；扫描结果 `subscriptionCandidateScan.undeclaredCron []string`
- 解析：`resolveSubscriptionScriptCron(path string) string`（只认脚本自己的声明，认不出返回 `""`）；`resolveCronForSubscriptionTask(path, defaultCron string) string` 只剩测试在用
- 头部读取：`eachSubscriptionScriptHeadLine(path string, fn func(line string) bool)`，常量 `subscriptionScriptHeadLines = 120`、`subscriptionScriptHeadLineMax = bufio.MaxScanTokenSize`（与任务名 `resolveSubscriptionTaskName` 共用）
- 提示文件名：`formatSubscriptionUndeclaredCronFiles(files []string) string`（最多列 5 个）
- 删任务预览：`subscriptionReaddSuffix(sub *model.Subscription, scriptPath string) string` → `taskScriptDetailSubReaddSuffix = "，并自动重新创建对应的任务"`（拼进 `taskScriptDetailSubGitForceFmt` / `taskScriptDetailSubSingleFileFmt`）
- 已删除：`FallbackSubscriptionCron`

### 3. Contracts

- **cron 来源优先级**（对过了扩展名、子目录、白 / 黑名单与依赖规则的每个文件）：
  1. 脚本头部声明了 cron → 候选，按声明建启用任务；
  2. 没认出 + `isSubscriptionHelperScript`（`subscriptionHelperScriptNames`：sendNotify / notify / utils / common / sign 等）→ 跳过：不建、不进 `undeclaredCron`、不提示；
  3. 没认出 + `defaultCron` 非空（合法）→ 候选，按默认规则建**启用**任务，`DefaultRule=true`；
  4. 其余 → **不建**，记进 `undeclaredCron`。
- 默认规则非法（只有直接改库、恢复旧备份能绕过 `normalizeDefaultCronRule`）→ 当作没配，走第 4 条；原值放进 `ignoredDefaultCron` 供日志警告。
- **不做存量迁移**：升级前兜底建出来的 0 点任务一律不动，自动删除也不能判它们失效；没有本订阅标签的同脚本任务照旧只加标签接管（规则见上一节）。
  想让这类脚本也建任务，就在订阅设置填默认 Cron 规则。
- **头部解析规则**：
  - 只看前 120 行，cron 与任务名同一个上限（以前 cron 只看 50 行：名字认得出、cron 却认不出）。
  - 只剥**第 1 行开头**的 UTF-8 BOM（U+FEFF）：正则里的 `\s` 不匹配它，Windows 编辑器保存的脚本第 1 行声明以前认不出。第 2 行起的 U+FEFF 不是 BOM，不剥。
  - 行尾 `\n` / `\r\n` 去掉；没有换行结尾的最后一行照常读。
  - **超长行不中止扫描**：不再用 `bufio.Scanner`（遇到 >64KB 的行报 `ErrTooLong` 后静默停止，后面的声明一行都读不到）。超长行只取前 64KB 参与匹配、余下丢弃，
    照常计作一行、继续往下读。读缓冲用默认大小、只有长行才另攒：每个文件开 64KB 缓冲时一次同步几千个文件的分配量是 Scanner 的十来倍，小内存 NAS / Magisk 上 GC 明显变频繁。
  - 标签行（`cronLabelPrefixRe`：`cron:`、`cron`、`* cron`、`@cron:`、`# cron`、`// cron`）：值整体是合法表达式就用；否则**只在前缀为空（顶格，Python docstring 常见）
    或含 `# * @ /`（注释行）时**剥掉「成对包住整个值」的 `"` / `'` 再校验——缩进的裸 `cron: '0 0 * * *'` 是 JS / TS 对象字面量的属性，不剥；最后再取前 6 / 5 个字段。
    **表达式后面的文字（文件名、说明）不参与判断。**
  - 青龙指令行 `cron "EXPR" file, tag:…` 与文件名行 `EXPR file`：仍要求文件名与本脚本相同（不分大小写）。
- **撤回项，不要再加回来**：「标签行表达式后面跟着别的脚本文件名，就不采用这一行」在 Wave 3 实现过、又撤回。它把真实样本 QLScriptPublic 的 `jlld.js`
  （照抄了 `leidacar.js` 的头部：`* cron 27 17 * * *  leidacar.js`）和说明文字（`// cron: 0 8 * * * 需要 Node.js 18 以上`、`# cron: 5 8 * * * 依赖 sendNotify.js`）
  都判成无效；#134 之后认不出就是不建任务，代价大于它要挡的低频误判。
- **已接受的取舍**（不要顺手「修」）：缩进 + 引号的 `    cron: "10 8 * * *"` 仍认不出（会列进提示，所以提示说「没有识别到 cron 声明」，不说「未声明 cron」）；
  顶格引号写法出现在 heredoc、模板字符串、docstring 代码块里会被当成声明；标签行上写着别的脚本名的声明在 51 ～ 120 行也会被采用。v3.2.8 认得出的声明现在都认得出。
- **拉取日志**（文案逐字）：
  - `[扫描脚本] 目录 %s 共扫描 %d 个候选文件（按白/黑名单过滤后），识别出 %d 个声明 cron 的脚本`：只计脚本自己声明的。
  - `[默认 Cron 规则] %d 个脚本没有识别到 cron 声明，使用订阅设置里的默认规则 %s`；这类新建任务打 `[自动添加任务] <名> (cron: <表达式>，默认规则)`。
  - `[警告] 订阅设置里的默认 Cron 规则「%s」无效，已忽略`：只在自动添加开着且 `ignoredDefaultCron` 非空时打（默认规则只在新建时用得上）。
  - `[提示] %d 个脚本没有识别到 cron 声明，未建定时任务（%s）。需要的话在订阅设置%s「默认 Cron 规则」，或手动为脚本建任务`：文件名相对订阅目录、正斜杠，
    最多列 5 个（超出写成「如 a、b、c、d、e 等」）；动作词默认「填写」，库里存着非法规则时为「修正」。只在自动添加开着、且读任务列表没失败时打；
    **在接管之后计算**，已经有任何任务（本订阅的、刚接管的，不论原来带什么标签）的脚本不列——叫人给已有任务的脚本再建一条只会建出重复任务。
- **删任务预览的「并自动重新创建对应的任务」**：`subscriptionReaddSuffix` 只在 `resolveSubscriptionAutoAddTask(sub)` 为真**且**
  （`resolveSubscriptionScriptCron(path) != ""`，**或**不是 `isSubscriptionHelperScript` 且 `subscriptionDefaultCronRule()` 返回合法非空规则）时返回后缀——
  「下次拉取真的会给它建任务」才许诺。白 / 黑名单、依赖规则不在这里判断（与改动前的文案口径一致）。演示站 `db.ts` 的 `demoScriptDeclaresCron` /
  `demoSubscriptionReaddsTask` 照抄这条口径（近似解析：只认标签行、前 120 行），服务端改判定要同步。

### 4. Validation & Error Matrix

| 情形 | 结果 | 日志 |
|---|---|---|
| 脚本声明了 cron（含首行 BOM、`cron: "0 8 * * *"` / `'…'` 引号值、声明在 51 ～ 120 行、前面有 >64KB 长行） | 按声明建启用任务 | `[自动添加任务] <名> (cron: …)` |
| 没认出 + 辅助脚本名（sendNotify.js、notify.py 等） | 不建、不提示；填了默认规则也不建 | — |
| 没认出 + 默认规则合法 | 按规则建启用任务 | `[默认 Cron 规则] …` + `[自动添加任务] <名> (cron: …，默认规则)` |
| 没认出 + 默认规则留空 | 不建 | `[提示] N 个脚本没有识别到 cron 声明，未建定时任务（…）。…填写「默认 Cron 规则」…` |
| 没认出 + 库里默认规则非法 + 自动添加开 | 不建 | `[警告] 订阅设置里的默认 Cron 规则「…」无效，已忽略` + 提示里是「修正」 |
| 同上但自动添加关 | 不建、不接管 | 无警告、无提示 |
| 没认出的脚本已有本订阅任务，或本轮刚被接管 | 不动 | 不列进提示 |
| 读任务列表失败 | 不建、不接管 | `[关联已有任务失败] …`，不打提示 |
| 缩进的对象属性 `  cron: '0 0 * * *'`（带不带逗号） | 不认 | — |
| 缩进 + 引号 `    cron: "10 8 * * *"`（已接受） | 不认，默认规则留空时不建 | 列进提示 |
| `jlld.js` 里的 `* cron 27 17 * * *  leidacar.js`；`// cron: 0 8 * * * 需要 Node.js 18 以上` | 认（撤回项） | — |
| 第 121 行起的声明；第 2 行起行首带 U+FEFF | 不认 | — |
| 删任务预览：自动建任务开 + 没识别到 cron + 默认规则留空 / 非法，或文件是辅助脚本 | 文案不带后缀（「…下次拉取会把它还原。」） | — |
| 删任务预览：自动建任务开 + 脚本声明了 cron，或默认规则合法且不是辅助脚本 | 带「，并自动重新创建对应的任务」 | — |

### 5. Good/Base/Bad Cases

- Good：拉一个带 `config.py` / `mysend.py` 的仓库，库文件不再建 0 点任务，日志一行说清几个、哪些、想建怎么办。
- Base：订阅设置填了默认 Cron 规则 → 与 v3.2.8 相同，按规则建启用任务；升级前已有的 0 点任务原样保留、不被删。
- Bad：留空硬兜底成每天 0 点——#134 本身。
- Bad：库里的默认规则被忽略时不吭声，提示还叫用户去「填写」设置页里明明填着的值。
- Bad：提示在接管之前算、只看本订阅的任务——照提示手建任务后每次拉取仍提示，青龙导入的任务被叫去再建一条（Wave 2 复查 major）。
- Bad：标签行尾是别的脚本名就不采用（撤回项）——照抄头部的真实脚本、写着「需要 Node.js」的说明都建不出任务。
- Bad：删任务预览只看「自动建任务开着」就许诺「并自动重新创建对应的任务」——没 cron 声明、默认规则留空时拉取根本不会建。

### 6. Tests Required

- 解析（`server/service/subscription_cron_test.go`）：
  - `TestResolveCronForSubscriptionTaskStripsBOMOnFirstLine`（标签 / CRLF / 文件名行 / 指令行；文件中间的 U+FEFF 不剥）
  - `TestResolveCronForSubscriptionTaskAcceptsQuotedLabelValue`（`//` `#` `*` `@`、无冒号、顶格 docstring、首尾内侧空格、6 字段）
  - `TestResolveCronForSubscriptionTaskIgnoresQuotedValueInCode`（对象字面量带 / 不带逗号、tab 缩进、`const cron = …`、引号不成对、空引号）
  - `TestResolveCronForSubscriptionTaskScansFirst120Lines`（51 / 120 认、121 不认，任务名同一上限）
  - `TestResolveCronForSubscriptionTaskSurvivesVeryLongLine`（长行之后的 cron 与任务名、声明在长行开头、长行只算一行、无换行结尾）
  - `TestResolveCronForSubscriptionTaskJSDocCronAcceptsMismatchedFilenameHint` / `TestResolveCronForSubscriptionTaskLabelAcceptsDescriptionMentioningFiles`（锁住撤回项）
- 同步（`server/service/subscription_undeclared_cron_test.go`）：`TestGetSubscriptionTaskSyncOptionsHasNoMidnightFallback`（留空为空、合法值 trim 后原样、脏值为空且进 `ignoredDefaultCron`）、
  `TestSyncSubscriptionTasksUndeclaredCronHint`（最多 5 个的逐字文案；自动添加关时不提示）、`TestSyncSubscriptionTasksKeepsExistingTasksOfUndeclaredCronScripts`、
  `TestSyncSubscriptionTasksAdoptsUnmanagedTaskOfUndeclaredCronScript`、`TestSyncSubscriptionTasksUndeclaredHintClearsAfterHandMadeTask`、
  `TestSyncSubscriptionTasksUndeclaredNoAdoptionWhenAutoAddOff`、`TestSyncSubscriptionTasksUndeclaredNeverAdoptsEmptyCommandTask`、
  `TestSyncSubscriptionTasksUndeclaredSkipsAdoptionWhenAlreadyManaged`、`TestSyncSubscriptionTasksKeepsUndeclaredCronTasksWithOddFileNames`、
  `TestSyncSubscriptionTasksWarnsAboutInvalidDefaultRule`（逐字警告、「修正」、自动添加关不警告、留空不警告）
- 主流程（`server/service/subscription_sync_integration_test.go`）：`TestSyncSubscriptionTasksUndeclaredCronFollowsDefaultRule`（留空不建 / 填了按规则建）、
  `TestSyncSubscriptionTasksScriptCronTakesPriorityOverDefault`；原先钉住 0 点兜底的 `subscription_chinese_dir_test.go`、`subscription_ql_filter_test.go`、
  `subscription_depend_on_test.go`、`subscription_pattern_pin_test.go` 已改成新口径。
- 删除判定：`TestSubscriptionStaleTaskJudgeKeepsUndeclaredCronScripts`（见上一节）。
- 删任务预览（`server/service/task_script_cleanup_test.go`）：`TestTaskScriptPreviewGitSubscription`（没 cron + 规则留空 / 规则合法 / 库里规则非法 / 辅助脚本 + 规则合法）、
  `TestTaskScriptPreviewSingleFileSubscriptionWithoutCronDeclaration`。
- 注册表描述改了要跑 `go test ./cmd/gen-demo-fixtures`（演示站 `configs.json` 逐字比对）。
- 修改后至少运行：

```bash
cd server
go test ./service -run "Subscription|Undeclared|ResolveCron|ResolveSubscription|TaskScriptPreview" -count=1
go test ./model ./cmd/gen-demo-fixtures -count=1
go test ./...
```

> **突变验证**：把 `newSubscriptionStaleTaskJudge` 的最后一个参数改传 nil，`TestSyncSubscriptionTasksKeepsExistingTasksOfUndeclaredCronScripts` 必须变红（存量任务连日志被删）；
> 把熔断条件改回 `len(candidates) == 0`，它的 `only_undeclared_scripts` 子用例必须变红；删掉 undeclared 的接管循环，`TestSyncSubscriptionTasksAdoptsUnmanagedTaskOfUndeclaredCronScript`
> 与 `TestSyncSubscriptionTasksUndeclaredHintClearsAfterHandMadeTask` 必须变红；删掉 `subscriptionReaddSuffix` 的辅助脚本判断，`TestTaskScriptPreviewGitSubscription` 的 helper 子用例必须变红。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：留空硬兜底成每天 0 点，而且关不掉；库文件、钩子脚本全被建成启用任务，删了下次拉取又回来。
defaultCron := strings.TrimSpace(model.GetRegisteredConfig("default_cron_rule"))
if defaultCron != "" && !cron.Parse(defaultCron).Valid {
    defaultCron = "" // 非法值还被静默吞掉，日志一个字都不提
}
if defaultCron == "" {
    defaultCron = FallbackSubscriptionCron // "0 0 * * *"
}
```

```go
// 错误：把没声明 cron 的脚本从候选里拿掉，却不交给删除判定——「文件在、扫描读到、不在候选里」判删，存量任务连日志没了。
if cronExpr == "" {
    continue
}
```

#### Correct

```go
// 正确：辅助脚本跳过；有合法默认规则才建；否则不建，但记进 undeclaredCron，
// 删除判定、接管、「未建定时任务」提示都靠它认出这些仍归订阅管的脚本。
if cronExpr == "" {
    if isSubscriptionHelperScript(info.Name()) {
        continue
    }
    if options.defaultCron == "" {
        undeclaredCron = append(undeclaredCron, path)
        continue
    }
    cronExpr, defaultRule = options.defaultCron, true
}
```

---

## 场景：脚本树隐藏名单与启动期隔离名单必须分开

### 1. Scope / Trigger

- 触发：想让某个目录名「在脚本管理里不出现」，或想往启动期隔离名单里加东西时必须看本节。
- 原因：脚本目录有**三套**名单，语义一层比一层重，放错一层就是静默事故：
  - 隐藏（`ShouldHideScriptTreeEntryName`）：树里不展示、API / CLI 读不到；
  - 忽略（`ShouldIgnoreScriptEntryName`）：展示 / 统计 / **备份打包与恢复**都跳过；
  - 隔离（`quarantinedScriptDirNames`）：启动期命中即 `os.Rename` **物理搬走**。
  v3.3.5（#156）之前隔离循环直接复用忽略名单（「忽略名单就是隔离名单」），`node_modules` / `__pycache__` 因此每次启动都被搬走；现在隔离只认 `quarantinedScriptDirNames`。
- 配套阅读：`## 场景：脚本目录污染隔离与 Windows 资源监控`

### 2. Signatures

- 隔离语义（会搬走文件，只看脚本目录顶层）：`quarantinedScriptDirNames`（`map[string]bool`，键是小写名，目前只有 `%systemdrive%`），只有 `QuarantineUnexpectedScriptEntriesOnStartup` 读它
- 忽略语义（展示 / 统计 / 备份跳过，不搬走）：`ShouldIgnoreScriptEntryName(name string) bool` = `node_modules` / `__pycache__` ∪ 隔离名单
- 隐藏语义（只是不展示 / 不可访问）：`ShouldHideScriptTreeEntryName(name string) bool` = 忽略 ∪ `hiddenScriptTreeNames`（`.git` / `.svn` / `.hg` / `.bzr`）
- 逐段路径判定：`ShouldHideScriptTreePath(scriptsDir, targetPath string) bool` /
  `ShouldHideScriptTreeRelativePath(relPath string) bool`

### 3. Contracts

- **三套名单语义不同，绝不可合并**，只能「重的包含轻的」：隐藏 ⊇ 忽略 ⊇ 隔离，反向不成立。
  - `ShouldIgnoreScriptEntryName(".git")` 必须**恒为 false**：忽略名单决定备份打包 / 恢复跳过哪些路径，加进去订阅仓库的 git 元数据就进不了备份。
  - `quarantinedScriptDirNames` **只放真正的污染目录**，`.git`、`node_modules`、`__pycache__` 一个都不能进：命中即搬走，脚本根目录本身是 git 仓库时整个仓库会被搬走。
  - 隔离循环只认 `quarantinedScriptDirNames`，不得改回调用 `ShouldIgnoreScriptEntryName`。
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
- `ShouldIgnoreScriptEntryName` 命中 → 展示 / 统计 / 备份跳过，**启动期不动它**
- `quarantinedScriptDirNames` 命中（脚本目录顶层）→ 启动期 `os.Rename` 搬进 `data/quarantine/scripts/`（**只用于真正的污染目录**）

### 5. Good/Base/Bad Cases

- Good：`.git` 在树里不出现，`GET /api/scripts/content?path=X/.git/config` 被拒；订阅仓库的 `.git` 照样进备份。
- Base：`.env`、`.hidden-dir`、`.github` 仍然可见可读。
- Bad：把 `.git` 加进 `quarantinedScriptDirNames` —— 脚本根目录是 git 仓库时仓库被搬走；加进 `ShouldIgnoreScriptEntryName` —— 订阅仓库的 git 元数据进不了备份。
- Bad：隔离循环改回复用 `ShouldIgnoreScriptEntryName` —— `node_modules` / `__pycache__` 每次启动被搬一份（#156）。
- Bad：只改 `runScriptList` 不改 `resolveCLIScriptPath` —— `ddp script cat X/.git/config`
  仍直接打印 PAT（CLI 的扩展名闸门对无扩展名文件一律放行）。

### 6. Tests Required

- `ShouldHideScriptTreeEntryName(".git")` 为真，且 `ShouldIgnoreScriptEntryName(".git")`
  **仍为假**（守住备份带上 `.git`）
- quarantine 不搬走 `.git` 仓库的用例（`TestQuarantineDoesNotMoveGitRepository`），
  也不搬走 `node_modules` / `__pycache__`（`TestQuarantineKeepsNodeModulesAndPycacheInPlace`）
- `.hidden-dir` / `.env` 可见断言**必须保留**，同用例追加 tree 不含 `.git`
- GetContent / Download / Delete / Copy / CLI 命中隐藏段的拒绝用例

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：为了「树里不出现」往忽略名单里加 .git —— 备份打包 / 恢复跳过订阅仓库的 git 元数据。
// （v3.3.5 之前隔离循环复用这份名单，那时更是会把整个 git 仓库 os.Rename 搬走。）
func ShouldIgnoreScriptEntryName(name string) bool {
    switch strings.ToLower(name) {
    case "node_modules", "__pycache__", ".git":
        return true
    }
    ...
}

// 错误：往隔离名单里加「不想看见」的目录 —— 命中即搬走，每次启动搬一份
var quarantinedScriptDirNames = map[string]bool{
    "%systemdrive%": true, "node_modules": true, "__pycache__": true,
}
```

#### Correct

```go
// 正确：另起一套隐藏语义，复合忽略名单但不反向污染它；隔离名单只放污染目录。
var hiddenScriptTreeNames = map[string]bool{
    ".git": true, ".svn": true, ".hg": true, ".bzr": true,
}

func ShouldHideScriptTreeEntryName(name string) bool {
    return ShouldIgnoreScriptEntryName(name) ||
        hiddenScriptTreeNames[strings.ToLower(strings.TrimSpace(name))]
}

var quarantinedScriptDirNames = map[string]bool{
    "%systemdrive%": true,
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
- 现成工具：`service.GenerateCurrentTOTPForTest`；v3.4.4 起还有 `GenerateNextTOTPForTest`（下一个时间步的码）
- 🔴 v3.4.4 起 TOTP **一码一次**：`service.ValidateUserTOTP` / `VerifyAndEnableTwoFactor` 校验通过即记下该用户已用的时间步（进程内，`step > lastUsed` 才算数，校验与记录在同一把锁里）。
  所以同一用例里「启用 2FA → 用同一个码登录 / 关闭」会 401/400；第二步要用 `GenerateNextTOTPForTest`。
  前端凡是「把码交给后端且后端已验过」之后还留着弹窗允许重试的（例：添加通行密钥的 begin 成功后用户取消系统弹窗），**必须清空验证码框并提示换新码**，否则重试必回 `invalid_totp`。
  `POST /security/2fa/setup` 在 2FA 已启用时回 400，不能借它绕过「关闭需验证码」。
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

## 场景：登录会话号与撤销契约（v3.3.4）

### 1. Scope / Trigger

- 触发：修改 `server/middleware/auth.go`（签发、`JWTAuth`、`AccessTokenRevoked`）、`server/service/auth.go` 的 `RefreshToken`、`server/service/login_security.go` 的会话 / 黑名单函数、`server/handler/auth.go` 的登录与退出、`server/handler/security.go` 的会话管理、`server/handler/mcp_auth.go`、`server/handler/log.go` 的日志流心跳，或新增任何「改用户状态」的入口（网页、开放接口、命令行）时必须看本节。
- 原因：JWT 无状态，撤销只能靠黑名单。v3.3.4 之前撤销只拉黑「登录那一刻」的两个 jti，续期得来的 access 无处可查——退出、撤销、改密、禁用、删除、改角色之后，续期过的设备照样能用到自然过期；黑名单行一过期被清理，已撤销的 token 还会复活。

### 2. Signatures

- claims：`middleware.Claims.SessionID`（JSON `sid`，omitempty）；常量 `middleware.ScriptTokenUsername = "internal-script-notify"`
- 签发：`GenerateAccessTokenInfo`（登录用，sid = 自己的 jti）/ `GenerateSessionAccessTokenInfo(username, role, sid)`（只给续期用）/ `GenerateTemporaryAccessTokenInfo`（应用令牌、脚本令牌，不带 sid）
- 判定：`middleware.IsUserSessionToken(claims)`、`middleware.AccessTokenRevoked(claims)`（`JWTAuth` 与 `authenticateMCPBearer` 共用）
- `JWTAuth` 额外写入上下文：`sid`（新令牌取 sid，老令牌取 jti）、`token_expires_at`、`claims`
- 会话：`service.CreateSessionWithRefresh(...) error`、`BlockSessionTokens`、`RevokeSession(sid, tokenExpiresAt)`、`RevokeAllUserSessions(userID)`、`RevokeOtherUserSessions(userID, sid)`、`CleanExpiredSessions()`
- 日志流：`handler.logStreamHeartbeatInterval`（包级变量，默认 30s，只为测试能调短）

### 3. Contracts

- **会话号**：用户 access 的 `sid` = `user_sessions.jti` = 登录那枚 access 的 jti；续期出的 access 沿用同一个 sid。refresh、应用令牌、脚本令牌不带 sid，形状与改动前一致。不加列、不改表结构。
- 🔴 **每请求只查一次库**（数据库单连接，#153）：
  - 非用户主体（`app:` 前缀与**精确的** `internal-script-notify`）→ 只按 jti 查（老规则）。它们没有用户行、也没有会话行，落进用户会话的判定会一律 401，所有任务通知、开放 API、MCP、企业微信触发全挂；
  - 带 sid 的用户令牌 → `jti IN (jti, sid)`；
  - 升级前签发、没有 sid 的用户令牌 → 一条语句里两个子查询：不在黑名单，且会话行（`s.jti = jti`）还在、用户启用、用户名没改——也就是只放行「登录时那一枚」；
  - 不许在 `JWTAuth` 里另加查询（查用户表、查会话表），也不许改成进程内缓存：`ddp` 是第二个写同一个库的进程，缓存看不到它的撤销。
- **撤销**：撤销一个会话 = 会话号一行 + refresh 一行进黑名单，并删会话行。退出登录、「撤销其他会话」、会话列表的 `current` 一律按 `sid` 认当前会话，不按 jti（续期过的令牌 jti 不等于会话号）。
- 🔴 **黑名单行到期不变式：每条黑名单行的到期 ≥ 它拦截的每一枚 token 的到期。** 会话号行 = max(会话记录的最晚 access 到期, refresh 到期 + access 有效期)；同一 jti 再写时到期只延不缩；退出时找不到会话行的兜底 = max(令牌自身到期, now + access 有效期)。**禁止写死 24 小时**——那正是「清理任务一跑、已撤销的 token 复活」的根因。
- **续期**：refresh 不轮换、响应形状不变（`{"access_token": …}`）；必须按 `refresh_jti` 找到会话行，且会话属于该用户（删号后重建同名账号时拒绝），否则 401；签出带原 sid 的 access，并把会话的 `expires_at` 推到「最晚 access 到期」（在 Go 里比较取大，不在 SQL 里比时间文本）；更新影响 0 行（会话恰好此刻被撤销）→ 401、不下发。守住客户端不变式「续期成功 ⇒ 新 access 立即可用」。
- **会话清理**：只删 access 与 refresh **都**过期的行（`expires_at < now AND (refresh_expires_at IS NULL OR refresh_expires_at < now)`）。只看 refresh 的话，「refresh 已过期、续期出的 access 还有效」时会话行被删，之后改密 / 禁用遍历不到它，那枚 access 漏拉黑。
- **登录**：会话行落不下去 → 500「登录失败」、不发 token，否则签出一枚有 sid、没有会话行、永远撤销不掉的令牌。会话行先于「登录成功」日志与登录通知落库。**落会话后重读用户（F2）**：会话行落库之后按 id 重读一次用户，与这次校验时读到的那份比启用状态、角色、密码哈希、用户名；有变化（含用户已被删）就用 `RevokeSession` 撤销刚建的会话，并按原失败分支拒绝登录、不发 token——被禁用回 403「账号已被禁用」，其余回 401「用户名或密码错误」（角色变了也回 401：校验用的账号快照已过时，重新登录就按新角色签发）。它补的是「登录校验完、会话行还没落库」时管理员改了用户状态的窗口：那次 `RevokeAllUserSessions` 遍历不到还没建的会话行。
- 🔴 **硬约束：任何用户状态变更都必须调 `RevokeAllUserSessions`，命令行也不例外。** 现有调用点：改自己的密码、改自己的用户名、管理员改角色 / 禁用（`PUT /users/:id`）、管理员重置密码、删除用户、`ddp reset-password`、`ddp reset-username`。2FA 开关、账号锁定按产品语义不撤销。新令牌每请求只查黑名单、不查用户状态，漏调一处就是静默重开洞，代码评审时当硬约束守。
  - 🔴 **必须先把用户状态落库，再调 `RevokeAllUserSessions`；F2 的重读依赖这个顺序。** 改动早于登录的重读，登录自己看得见、当场撤掉刚建的会话；晚于重读，会话行已经在库里，随后的撤销一定遍历得到。反过来先撤销后落库，一次登录能落在撤销的 Find 之后、落库之前：重读看不到变化、照发 token，它的会话行也躲过了这次撤销。上面 7 个调用点都是这个顺序；删除用户是先删用户行、再撤销（`UserHandler.Delete` 曾写反：用户行随后被删，这行孤儿会话再没有哪条改用户状态的路径会撤它，被删的管理员拿着这枚 access——role 仍是 admin——能调管理员接口到自然过期）。
- **会话列表**：列「access 或 refresh 仍有效」的会话，每项下发 `current`（按 sid 认）；`GetLoginStats` 的活跃会话同一口径。
- **日志流复查**：`/logs/:id/stream` 的心跳是固定周期的 ticker（v3.3.4 之前是静默满 30s 才发），每个周期先调 `AccessTokenRevoked`：已撤销就先写 `event: done` + `data: reconnect` 再收流，否则照发 `: keepalive`。🔴 不能不发 done 就断开（网页的日志弹窗会卡在「运行中」）；也不能把复查挂在「静默心跳」上（一直有输出的任务永远等不到它）。
- **401 口径**：用户令牌失效 → `{"error":"登录已失效，请重新登录","code":"session_revoked"}`（只增加 code，两端都不读它）；脚本 / 应用令牌被拉黑仍回 `{"error":"令牌已被撤销"}`（`docs/script-api.md` 已写）；refresh 被拒仍回 `令牌无效或已过期`。会话层面的拒绝一律 401，不能用 403：两端遇 403 都不登出。
- **升级 / 回退**：升级不踢人——登录时的老 access 照常可用（旧版 APP 只持有它），续期得来的老 access 回 401，网页与 APP v1.3.0+ 用老 refresh 自动续一次。回退旧版本不需要迁移：旧版忽略 sid，旧版撤销时本来就写 `session.JTI` 那一行。

### 4. Validation & Error Matrix

| 场景 | 结果 |
|---|---|
| 会话被撤销后，该会话任意一枚 access（含续期得来的） | 401 `session_revoked` |
| 被撤销会话的 refresh | 401 `令牌无效或已过期` |
| 升级前续期得来的老 access（没有 sid） | 401 `session_revoked`；老 refresh 找得到会话就续出带 sid 的新 access |
| 升级前「删号未撤销」留下的孤儿会话 | 老 A1 与 R1 都 401 |
| 删号后重建同名账号，拿旧 refresh 续期 | 401 |
| 脚本令牌在任务结束后被拉黑 | 401 `令牌已被撤销`（不带 code） |
| 登录时会话行写入失败 | 500 `登录失败`，响应里没有 token |
| 执行日志流连着时会话被撤销 | 下一个心跳周期内 `event: done` / `data: reconnect`，随后收流 |

### 5. 明确接受的残留（不在本轮消除，发布说明同步写明）

- 依赖安装、订阅拉取、安卓运行环境下载三条 SSE 连上后不复查撤销，受各自的时长上限约束（约 5-30 分钟），内容也不是用户敏感信息。
- 新令牌每请求只查黑名单、不查用户状态，正确性完全依赖上面「任何用户状态变更都必须调 `RevokeAllUserSessions`」这条硬约束。
- 升级前「用续期得来的 token 退出」遗留的会话：会话行与 refresh 都还在，服务端无法与正常会话区分；发布说明提示用户到「安全 → 会话管理」检查一遍。
- 退出登录只拉黑会话号那行与 refresh 那行，**不拉黑发起退出的那枚续期 access 自己的 jti**（新版靠会话号命中即可作废整条会话，不需要它）。一旦回退到旧版本，旧版不认 sid、只按 jti 查黑名单，这枚 access 在回退期间可用到自然过期。已接受：正常退出后这枚令牌只剩泄漏出去的副本（发起退出的客户端已经把它丢弃），且回退本身就是把缺口整体退回旧版状态、不会比旧版更差；要多拉黑它就得在退出时凭 jti 再写一行黑名单，属回退期才有意义的额外成本，本轮不做。

### 6. Tests Required

- `go test -list '^TestRV' ./handler/ ./cmd/ddp/` 列出 28 条：`server/handler/session_revocation_test.go` 26 条 = 初版设计用例 19 条 + 日志流复查 1 条 + 评审修复后补的回归用例 6 条（F1–F5 各 1 条、删号顺序 1 条）；`server/cmd/ddp/reset_revoke_test.go` 2 条（设计用例）。写法：有效期设成生产默认的 480h / 1440h；「清理后复活」用「按清理条件、把 now 换成 A2 到期前 1 分钟删黑名单」模拟；升级兼容用 `jwt.MapClaims` 手签不带 sid 的老令牌；每请求 SQL 条数用计数 logger 数；并发窗口用只触发一次的 GORM 查询回调（`Callback().Query().After("gorm:query")`）确定性地塞进去，不靠并发抢时序。只用改动前就有的 API（日志流心跳变量除外）。
- 放到改动前的代码上实测 23 红 5 绿：绿的 5 条是 4 条 `TestRVGuard*` 与 `TestRVUpgradeLegacyLoginAccessKeepsWorking`，也就是 21 条设计用例（handler 19 + ddp 2）16 红 5 绿；日志流复查与 6 条回归用例全红（旧代码只按静默 30s 发心跳、不复查撤销；删号顺序那条在旧代码上红在「回调没触发」——旧版删号根本不撤销，也就没有那次 Find）。注意这份测试文件在改动前的代码上并不能直接编译：日志流复查那条用例引用了改动后才有的包级变量 `logStreamHeartbeatInterval`，要先补一行编译垫片（`var logStreamHeartbeatInterval = 30 * time.Second`）才编得过。
- 评审必做逐条有断言：豁免 `app:` 与精确的 `internal-script-notify`（`TestRVGuardNonUserTokensUnaffected`，形近用户名在 `TestRVUpgradeLegacyRefreshedAccessRecoversWithOneRefresh`）；会话清理条件与续期推后 `expires_at`（`TestRVRevocationRowOutlivesEveryIssuedAccess` 后半段）；MCP Bearer 同一判定（`TestRVAdminRevokeKillsRefreshedAccess`）；登录落会话失败回 500（`TestRVDisplacedDeviceLosesRefreshedAccess` 末尾）；退出兜底不写死 24 小时（`TestRVLogoutWithRefreshedAccessEndsWholeSession` 末尾）。
- 评审修复的回归保护（每条守一处评审发现的缺口）：F1 登录只写 `last_login_at` 一列、不整行写回（`TestRVLoginUpdatesOnlyLastLoginColumn`）；F2 登录落会话后重读用户（`TestRVLoginRevokesSessionWhenUserDisabledMidLogin`）；F3 撤销只删这次拉黑过的会话行、不按 `user_id` 整批删（`TestRVRevokeAllKeepsSessionInsertedDuringRevoke`）；F4 会话号那行黑名单的到期上界含「refresh 到期 + access 有效期」（`TestRVRevocationRowCoversRefreshedAccessAfterCleanup`）；F5 老令牌分支那条查询里 `u.username = ?` 与 `u.enabled = ?` 两个条件都要在（`TestRVUpgradeLegacyAccessRejectedAfterUsernameChangeOrDisable`）；删号先删用户行、再撤销（`TestRVLoginRacingUserDeleteLeavesNoUsableToken`）。F4、F5 与删号顺序三条的用例注释写了用 `go test -overlay` 只改回那一处看它变红的办法；删号顺序那条已这样实测过：把 `UserHandler.Delete` 改回先撤销后删行，并发登录拿到的 access 调 `/system/version` 与 `GET /users` 都回 200，库里留下 1 行孤儿会话。
- ⚠️ `testutil.MustCreateRefreshToken` 会顺手落一行会话（与真实登录一致）。**不要**把 `TestRefreshOnlyAcceptsBearerRefreshToken` 改成期望 401——那等于把「续期要绑活会话」这条契约测反。
- 造应用令牌要走真实的 `POST /open-api/token`（没有 jti）；`testutil.MustCreateAppToken` 签出的令牌带 jti，形状与线上不同。
- 修改后至少运行：

```bash
cd server
go test ./handler ./cmd/ddp -run "TestRV|TestRefreshOnlyAcceptsBearerRefreshToken" -count=1
go test ./...
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误一：按 jti 认当前会话。续期过的令牌 jti 不等于会话号，「撤销其他会话」会把自己也撤掉。
service.RevokeOtherUserSessions(user.ID, c.GetString("jti"))

// 错误二：兜底写死 24 小时。清理任务一跑，还没到期的 token 就复活了。
blockToken(jti, "access", nil, time.Now().Add(24*time.Hour))

// 错误三：新加的禁用 / 删除 / 改密入口只改了用户表。令牌每请求不查用户表，被禁用的人照样能用。
database.DB.Model(&user).Update("enabled", false)
```

#### Correct

```go
service.RevokeOtherUserSessions(user.ID, c.GetString("sid"))

// 至少盖住令牌自己的到期
expiresAt := time.Now().Add(config.C.JWT.AccessTokenExpire)
if tokenExpiresAt.After(expiresAt) {
    expiresAt = tokenExpiresAt
}
blockToken(sessionID, "access", nil, expiresAt)

database.DB.Model(&user).Update("enabled", false)
service.RevokeAllUserSessions(user.ID) // 命令行也一样
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
- `subscription_managed` 的说明里「，并自动重新创建对应的任务」（`taskScriptDetailSubReaddSuffix`）只在下次拉取真的会给这个文件建任务时才带，由 `subscriptionReaddSuffix` 判定（自动建任务开着，且脚本声明了 cron 或「不是辅助脚本 + 默认 Cron 规则合法非空」），口径见 #134 场景；演示站 `db.ts` 同步照抄。
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
- 订阅与 helper：`TestTaskScriptPreviewGitSubscription`（含 #134 的「重新创建」后缀子用例）/ `TestTaskScriptPreviewGitSubscriptionAtScriptsRoot` / `TestTaskScriptPreviewSingleFileSubscriptionAndLabels` / `TestTaskScriptPreviewSingleFileSubscriptionWithoutCronDeclaration` / `TestSingleFileSubscriptionDestPathMatchesPull` / `TestTaskScriptPreviewManagedHelpersAndWarnings` / `TestTaskScriptPreviewGlobalHooksKept`
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

---

## 场景：执行器的「执行窗口」与 per-run 停止（v3.2.8 重写）

### 1. Scope / Trigger

- 触发：修改 `server/service/task_executor.go` 的 `OnTaskExecuting` / `RunTask` / `runTask` / `StopTask` / `StopAllRunningTasks` / 重试循环 / 结算块，或任何「停止一个任务」的新入口时必须看本节。
- 原因：进程表只覆盖**已经起来的进程**，而一次执行里有三段没有进程可杀：准备阶段（建日志、装环境、前置钩子）、两次重试之间的等待、以及进程刚被杀掉、循环正要起下一轮。停止请求落在这三段里，旧实现要么无效、要么只杀掉一轮就被重试续上，用户看到的是「点了停止没反应」或「停了又自己跑起来」。

### 2. Signatures

- 执行窗口：`func (e *TaskExecutor) beginExecuting(taskID uint) *executingRun` / `func (e *TaskExecutor) endExecuting(run *executingRun)`（幂等）
- 准备阶段窗口：`preparedRuns map[*ExecutionRequest]*executingRun` + `func (e *TaskExecutor) closePreparedRun(req *ExecutionRequest)`
- 每次执行的停止意图：`executingRun.stop runStopKind`（`runStopNone` / `runStopManual` / `runStopHalt`）+ `executingRun.stopCh`
- 停止入口：`func (e *TaskExecutor) StopTask(taskID uint) bool` / `func (e *TaskExecutor) StopAllRunningTasks() int`（返回终止的进程数，含钩子；v3.3.6 起两者都先 SIGTERM，见「场景：任务进程组的清理与终止」）
- 重试等待：`func (e *TaskExecutor) waitRetryInterval(run *executingRun, d time.Duration) bool`
- 进程登记释放：`func (e *TaskExecutor) releaseRunProcesses(run *executingRun)`
- 关停（v3.3.5）：
  - 执行器级标记 `TaskExecutor.halted bool`（受 `processLock` 保护，只由 `StopAllRunningTasks` 置位、不复位）+ `func (e *TaskExecutor) halting() bool`；
  - 钩子登记 `executingRun.hookProcesses map[int]*os.Process` + `registerHookProcess(run, process)` / `releaseHookProcess(run, process)`：
    钩子经 `runInlineScript` / `runHookScript` 的 `onStart OnProcessStartFunc` 回调登记（导出的 `RunInlineScript` / `RunHookScript` 传 nil，其它调用方行为不变）；
  - 凭据登记 `executingRun.scriptToken *ScriptTokenInfo` + `attachRunScriptToken(run, token)` / `func (e *TaskExecutor) revokeUnsettledScriptTokens() int`。

### 3. Contracts

- **停止必须同时做两件事**：杀掉已登记的进程 **+** 给「此刻在窗口里的每一次执行」置停止意图。只杀进程会被重试循环续上：被杀那一轮算失败 → 起新进程，`MaxRetries>0` 的任务点一次停止只停一轮。
- 重试循环的**每一轮起点**都要检查本次执行的停止意图并跳出；重试等待必须是 `select { case <-run.stopCh: ... case <-timer.C: }`，不能用 `time.Sleep`（`RetryInterval` 可配成几分钟，用户会以为按钮坏了）。
- **停止意图记在 `executingRun` 上，不能按 taskID 记一笔**。`AllowMultipleInstances=true` 时，按 taskID 记会让「停止之后才启动」的实例一进循环就跳出，一个进程都不起。
- **手动停止的结算按 run 判定**：`run.stop == runStopManual` 的执行各自结算为 Aborted；任务级的 `manualStopMarks`（外部入口按 PID 兜底停止时打的）只有在「本任务此刻没有别的执行在窗口里」时才允许认领，否则一次没被停的执行会抢走这笔标记、被误判成已终止并吞掉成功通知。
- **窗口全开全关成对**：准备阶段开的窗口记在 `preparedRuns`，建日志失败 / `OnTaskFailed` / `RunTask` 缺日志兜底都必须 `closePreparedRun`；`runTask` 接手后用 `defer e.endExecuting(run)` 收口（含 panic 路径）。漏关的表现是空闲任务被永远当成「正在执行」：`StopTask` 恒返回 true，停止请求还会挂到下一次运行。
- **关机用 `runStopHalt`，不是手动停止**：`StopAllRunningTasks` 除杀进程外还要拦住窗口里的执行继续启动新进程。写成手动停止会把「面板重启」谎报成「用户终止」。
  v3.3.5 起（D15）被它打断的执行**在 `runTask` 里自己结算**：
  - 日志自己写 `[面板正在关闭，任务已中断]`（只在没成功时写）与 `[面板正在关闭，跳过后置脚本]`，都由 `stopNotice` 产出、各独占一行，被 `isPanelMetaLine` 认出。
    前提是上一行也换了行：`script_runner.go` 的 `runSingleCommand` 里「[脚本进程被信号终止：…]」那行补了结尾 `\n`，否则中断提示粘在它行尾，按行首认不出。
  - `halting()` 为真时**不启动**任务后置脚本、`task_after.sh`、`extra.sh`，也不再启动新的前置钩子（任务前置脚本、`task_before.sh`）。每个钩子最长 60 秒，关停等不起。
  - **不发失败通知**：判据是这次执行 `runStopKind(run) == runStopHalt` 且没成功。任务自己先失败、关停时正卡在后置钩子里，同样算被关停打断，不发。
  - `last_run_status` 仍记 `RunFailed`（不是 Aborted）。
  - 🔴 **停止种类在主命令返回那一刻读，读到 `runStopHalt` 一律按失败结算**（v3.3.6，#159 修复 B 改成先 TERM 之后补的回归修复）：`runCommandWithPlanFunc` 一返回就读 `haltedThisRound := e.runStopKind(run) == runStopHalt`，
    为真时这一轮不论退出码都不走成功那条 `break`（`IsSuccessExitCode(rc) && !haltedThisRound` 才算成功），照常走失败分支（依赖自动安装、运行时失败提示与改前同一条路径），
    下一轮开头的停止检查写「取消后续执行」后跳出、不再起新进程（`MaxRetries=0` 时直接出循环，没有这一行）；随后照常写「任务已中断」「跳过后置脚本」、不发任何通知。
    原因：关停改成先 SIGTERM 后，trap 了 TERM、收完尾以 0 退出的脚本会被判成成功——记成功、不写「任务已中断」、还发成功通知。与手动停止按 `runStopManual` 判已终止同一思路：看这次执行收到的停止种类，不看退出码。
    读到的还是 `runStopNone`，说明主命令在关停之前就自己返回了，照旧按退出码判（成功就记成功）。脚本自己退出时没有「被信号终止」那行，退出码照实记（改前被 KILL 是 -1）。
    `StopAllRunningTasks` 在发任何信号之前，已在 `processLock` 里把 `run.stop` 置成 `runStopHalt`，所以进程因 TERM 退出时一定读得到，没有竞态。
  - 前置 / 后置钩子进程登记在 `executingRun.hookProcesses`，刻意不进 `runningProcesses` / `processes`：**只有 `StopAllRunningTasks` 读这张表、按进程组杀**；
    `StopTask` 不读它，手动停止照旧跑后置脚本、结算 Aborted，行为不变。
  - `halted` 只由 `StopAllRunningTasks` 置位（生产上只有 `HaltSchedulerV2` 调它）。之后才开窗的执行（worker 恰好在关停前取到请求）在 `newExecutingRunLocked` 里**生来带 `runStopHalt`**：
    不起前置钩子、主进程一次不起；关停瞬间才起来的钩子由 `registerHookProcess` 直接杀掉、不登记。
  - 只有截止（`ShutdownSchedulerV2` 的 3 秒，v3.3.6 前是 4 秒）前**没结算完**的执行才由 `MarkActiveTasksInterrupted` 标「面板正在关闭或重启，任务已被中断」；
    截止时先 `revokeUnsettledScriptTokens` 吊销它们的脚本凭据（结算 defer 里那句吊销等不到了）。面板关停的整体顺序见「场景：停机收尾」。
- **结算只摘自己登记的 pid**，禁止 `delete(e.runningProcesses, taskID)`：多实例下先结算的那次会把另一次仍在跑的进程一起抹掉，之后停不掉它，`HasRunningProcess` 也会误报「没有进程在跑」——「删除任务时一并删除脚本」正是靠它兜底，会把还在跑的任务的脚本删掉。
- **杀进程不要持锁**：锁内只收集 victim 列表与落定标记，出锁后再发信号——`StopTask` 出锁后调 `TerminateProcessGroup`，`StopAllRunningTasks` 出锁后才 `signalGroupTerm`、等共享宽限、补 `KillProcessGroup`
  （Unix 下是 syscall，持锁会把进程登记、`HasRunningProcess` 这些短临界区堵住；关停那最多 2 秒的宽限更不能拿着锁等）。
  `Terminate*` 不阻塞（等待与补刀在单独的协程里），所以调试运行的 `debugRun.stop()` 可以在 `run.mu` 里调它。
- 面板自己打进任务日志的停止提示行（`[任务已被手动停止，…`、`[面板正在关闭，…`；「取消后续执行」「终止刚启动的进程」「任务已中断」「跳过后置脚本」都挂在这两个前缀下）必须登记进 `panelMetaLinePrefixes`，否则成功通知的日志摘录会被这些行顶掉用户真正想看的脚本输出。

### 4. Validation & Error Matrix

- 停止落在准备阶段（无进程）→ 置 `runStopManual`，循环起点跳出，日志写「终止刚启动的进程」类提示，结算 Aborted。
- 停止落在重试等待 → `stopCh` 立刻唤醒，不等满 `RetryInterval`，不再起下一轮。
- 停止命中已登记进程 + `MaxRetries>0` → 杀进程**并且**置停止意图，重试循环不得续跑。
- `AllowMultipleInstances=true`，停止后才启动的实例 → 不受上一次停止影响，正常执行。
- 关机 → 窗口内执行不再起新进程、不再启动前置 / 后置钩子，正在跑的钩子按进程组终止（v3.3.6 起与任务进程一起先 TERM、共享宽限再 KILL）；没成功的写「[面板正在关闭，任务已中断]」「[面板正在关闭，跳过后置脚本]」（各占一行），
  `last_run_status` 记失败（不是 Aborted），不发失败通知。
- 关机时主进程收到 TERM、在 trap 里收完尾以 0 退出 → 照样按失败结算（`haltedThisRound`）：`MaxRetries>0` 时「取消后续执行」「任务已中断」「跳过后置脚本」三行，`MaxRetries=0` 时后两行；
  成功、失败通知都不发；结束行里的退出码照实是 0，没有「被信号终止」那行。
- 关机时主进程已经成功、还没轮到后置脚本（主命令返回那一刻停止种类还是 `runStopNone`）→ 只写「[面板正在关闭，跳过后置脚本]」，照常记成功（成功通知照旧异步发出，面板随即退出，不保证送达）。
- 关停之后才开窗的执行 → 生来带 `runStopHalt`：前置钩子与主进程都不起，日志「取消后续执行」「任务已中断」「跳过后置脚本」三行，记失败。
- 已经进入后置钩子阶段才关停 → 正在跑的钩子整组被杀，后面的钩子一起来就被杀；这次执行照常在 `runTask` 里结算（输出不丢），
  失败的不发失败通知（`run.stop` 已是 `runStopHalt`），成功的照常记成功。「跳过后置脚本」的判断在进入后置阶段时已经做过，所以这种情况日志里没有那两行提示。
- 执行卡在读输出里（逃出进程组的孙进程攥着管道）→ `runTask` 结算不了，等到 `ShutdownSchedulerV2` 的 3 秒截止：凭据吊销，库里由 `MarkActiveTasksInterrupted` 标中断，这次输出丢失。
- 任务此刻没有任何执行在窗口里 + 外部入口打了任务级标记 → 下一次执行开窗时先 `consumeManualStop` 清掉残留，不得串到这一次。

### 5. Good/Base/Bad Cases

- Good：点运行后 0.3 秒点停止（进程还没登记）→ 状态直接回到禁用/启用，日志写明被手动停止，之后不再冒 pid。
- Base：运行中点停止 → 杀进程、结算 Aborted、不重试。
- Bad：`StopTask` 第一分支只 `KillProcessGroup` 就 `return true` —— 重试循环马上起新进程，用户点一次停止只停掉一轮。
- Bad：把停止请求按 taskID 记一笔并等「该任务所有执行都结束」才清 —— 多实例下误伤后启动的实例。

### 6. Tests Required

见 `server/service/manual_stop_executing_window*_test.go`：

- 停止已登记进程 → 重试循环必须结束（不得起新进程）。
- 停止落在重试等待 → 结算耗时远小于 `RetryInterval`。
- 多实例：停止不得泄漏到「停止之后才开始」的执行。
- 多实例：一次停止同时命中两个实例时两个都判 Aborted；没被停过的执行不得认领任务级标记。
- 关机：窗口内执行被拦住，且 `last_run_status` 仍是失败（`TestStopAllRunningTasksHaltsExecutingWindowAndRetries`）。
- 关机结算（`service/panel_shutdown_test.go`，断言点见「场景：停机收尾」）：被打断的跳过三种后置脚本、不发失败通知、两行提示各占一行且被 `isPanelMetaLine` 认出
  （`TestHaltedRunSkipsPostHooksAndFailureNotify`、`TestShutdownInterruptNoticesAreStandalonePanelMetaLines`）；正在跑的前置 / 后置钩子整组被杀、关停后开窗的执行不起钩子
  （`TestHaltKillsRunningPreHookAndSkipsNewOnes`、`TestHaltKillsRunningPostHook`）；手动停止照旧跑后置脚本、结算 Aborted（`TestManualStopStillRunsPostHooks`）。
- 关停先 TERM 之后的结算（Linux，`service/task_process_cleanup_linux_test.go`）：`TestShutdownSettlesTermTrappedZeroExitAsInterrupted`（trap TERM 后以 0 退出，`MaxRetries=1`、成功失败通知都开：
  有收尾标记；`RunFailed`；「取消后续执行」「任务已中断」「跳过后置脚本」各自整行；等 500ms 后本地 webhook 探针 0 次）、
  `TestShutdownAfterMainCommandSucceededStillSettlesAsSuccess`（关停落在后置脚本里：照旧 `RunSuccess`，主命令输出在，没有「任务已中断」）。突变：去掉 `!haltedThisRound`，第一条红。
- 准备阶段失败 / `runTask` 内 panic → 窗口必须关闭（之后 `StopTask` 不得恒 true）。
- 结算只释放自己的进程登记（另一实例仍可被停止）。
- 停止提示行必须在 `panelMetaLinePrefixes` 里。
- 修改后至少运行：

```bash
cd server
go test ./service -run "Stop|Executing|Manual|Executor|RunTask|Halt|Shutdown" -count=1
go test ./...
```

> **突变验证**：把重试循环起点的停止检查删掉，「停止已登记进程后不得起新进程」必须变红；把 `releaseRunProcesses` 换回整条 `delete`，多实例那条必须变红。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：只终止进程就返回，重试循环照常起下一轮。
if procs, ok := e.runningProcesses[taskID]; ok {
    TerminateProcessGroup(procs)
    return true
}
```

```go
// 错误：关停先 TERM 后只看退出码 —— trap 了 TERM、收完尾以 0 退出的脚本被记成成功、不写「任务已中断」、还发成功通知
if task.IsSuccessExitCode(result.ReturnCode) {
    success = true
    break
}
```

```go
// 错误：不可打断的重试等待，停止落在这里要白等满整个间隔。
time.Sleep(time.Duration(task.RetryInterval) * time.Second)
```

#### Correct

```go
// 正确：终止进程 + 给此刻窗口里的每一次执行置停止意图（出锁后再发信号）。
// TerminateProcessGroup 先 TERM、groupTermGrace（5 秒）后组还在才 KILL，补刀在协程里，StopTask 照样立即返回。
victims := e.markStopForTask(taskID, runStopManual) // 锁内
for _, p := range victims {                          // 锁外
    TerminateProcessGroup(p)
}
```

```go
// 正确：主命令一返回就读停止种类；被关停打断的这一轮不论退出码都按失败走，由下一轮开头的停止检查跳出
result, _, err := runCommandWithPlanFunc(plan, effectiveTimeout, envVars, maxLogSize, onOutputWithCollect, onStart)
haltedThisRound := e.runStopKind(run) == runStopHalt
// ……err 分支……
if task.IsSuccessExitCode(result.ReturnCode) && !haltedThisRound {
    success = true
    break
}
```

```go
// 正确：重试等待可被停止唤醒。
select {
case <-run.stopCh:
    return false
case <-timer.C:
    return true
}
```

---

## 场景：内置 MCP 服务（issue #128）

### 1. Scope / Trigger

- 触发：修改 `server/handler/mcp*.go`、`server/mcptools/`、`server/cmd/ddp/mcp.go`、`middleware/cors.go` 的 MCP 分支，或增删 MCP 工具时必须看本节。
- 原因：MCP 把面板的能力暴露给 AI 客户端。它与 Open API 共用凭据与权限范围，任何「工具比接口多给了一点」的偏差都是越权。

### 2. Signatures

- HTTP：`POST /api/v1/mcp`（兼容 `POST /api/mcp`），Streamable HTTP，Stateless + JSONResponse
- stdio：`ddp mcp`（`RemoteDispatcher`，把工具调用转成对本机面板的 HTTP 请求）
- 构建入口：`func BuildServer(dispatcher Dispatcher, allowMutations bool) *mcp.Server`
- 配置键：`mcp_enabled`、`mcp_allow_mutations`（分组 `mcp` / 「MCP 服务」）

### 3. Contracts

- **两级开关**：`mcp_enabled=false` → 403；`mcp_allow_mutations=false` → 只注册只读工具（v3.3.5 起 16 个，以 `server/mcptools/server.go` 的 `readToolNames` 为准），写类工具**不存在**（不是注册了再拒绝，避免 AI 反复试）。
- 鉴权走 Open API 应用凭据（Basic `app_key:app_secret`）或登录 Bearer；**权限范围仍由应用的 scope 决定**，工具不得绕过接口层自己查库。
- 工具调用在进程内走 `engine.ServeHTTP` 派发到既有 handler，**不复制业务逻辑**：接口改了行为，工具自动跟上。
- `list_envs` 必须与 Web 同口径遮蔽敏感值（`server/handler/envsecret.go` ↔ `web/src/utils/envSecret.ts`），否则「网页上打码、AI 一问就明文」。
- 每次请求现读配置（不进 `reloadRuntimeConfigKeys`），关掉开关立即生效。
- 跨域：MCP 端点必须校验 Origin，外站 Origin 一律 403。

### 4. Validation & Error Matrix

- `mcp_enabled=false` → 403，不暴露工具列表。
- 凭据错误 / 缺失 → 401。
- 外站 Origin → 403。
- 写开关关闭时调用写工具 → unknown tool（工具根本没注册）。
- 应用缺少对应 scope → 工具返回错误，与直接调接口一致。

### 5. Tests Required

- 开关矩阵：关/只读/读写三档下的工具数（v3.3.5：403 / 16 / 46，即 `readToolNames` 16 个 + `writeToolNames` 30 个；数字随名单变，断言按名单逐个核对：
  `TestReadOnlyServerListsExactlyTheReadTools`、`TestMutationServerAddsWriteToolsWithAnnotations`）与写工具可见性。
  `web/src/views/api-docs/apiData.ts` 的 MCP 条目里写死了「共 N 个工具 / M 个查询类 / K 个写入类」与工具表，增删工具时手工同步（没有测试守着）。
- 鉴权：Basic、Bearer、错误 secret、无凭据、外站 Origin。
- `list_envs` 遮蔽与 Web 实现逐字对齐（同一组样例）。
- `ddp mcp` stdio：initialize → tools/list → 一次真实工具调用。
- 修改后至少运行：

```bash
cd server
go test ./mcptools ./handler -run "MCP|Mcp" -count=1
go test ./...
```

---

## 场景：企业微信拉起任务（issue #145，v3.3.2）

### 1. Scope / Trigger

- 触发：修改 `server/handler/wecom_callback.go`、`server/pkg/wxcrypt/`、`server/pkg/trigticket/`、
  `server/model/wecom_trigger.go`，或新增任何一条「公开路由 + handler 内强鉴权」的入口时必须看本节。
- 原因：这是**「路由公开 + handler 内强鉴权 + 系统设置总开关」这套形态的第二个实例**（第一个是上一节的 MCP）。
  上一节此前是唯一实例，规范也只按 MCP 描述；从这一版起它是一套**可复用的形态**，
  再加第三个入口时照这里抄，不要另发明一种鉴权。

### 2. Signatures

- HTTP：`GET /api/v1/wecom/callback/:id`（企业微信后台保存配置时的 URL 验签）、
  `POST /api/v1/wecom/callback/:id`（用户回复文字 / 点菜单时的消息投递）
- 配置键：`model.WecomTriggerEnabledConfigKey` = `wecom_trigger_enabled`、
  `model.WecomTriggerAllowRunConfigKey` = `wecom_trigger_allow_run`（与 MCP 的两项一一对应）
- 运行时状态键（**不进注册表**）：`model.WecomTriggerLinkGenerationConfigKey` = `wecom_trigger_link_generation`
- 加解密：`wxcrypt.Signature` / `wxcrypt.VerifySignature` / `wxcrypt.Decrypt`
- 菜单票据：`trigticket.Issue` / `trigticket.Verify`（资源标识是 `task-trigger:<任务 ID>`）

### 3. Contracts

- **安全基线四件套一件不能少**（与 MCP 逐条对应）：默认关 + **每请求现读开关** + 凭据强校验 + 全局 CORS 兜底。
  现读开关意味着管理员在设置页关掉立即生效，不进 `reloadRuntimeConfigKeys`；
  Origin 校验不在 handler 里重写——`router.Setup` 的全局 CORS 中间件已经挡在前面
  （企业微信是 server-to-server、不带 Origin 而被放行，浏览器跨站请求带 Origin 会在进 handler 之前 403）。
- **两级开关**：`wecom_trigger_enabled=false` → 403（连验签都不做）；
  `wecom_trigger_allow_run=false` → 回调 URL 能在企业微信后台配通，指令却不会真的跑任务。
  两级的用途是**分两步上线**，不是冗余。
- 🔴 **`wxcrypt.Decrypt` 里「明文尾部的 receiveid 必须等于本企业 CorpID」是硬约束，不设跳过开关。**
  漏了这一步，别人拿自己企业的 Token/AESKey 就能构造出一条签名合法的消息打进来 —— 等于任何人可触发任务。
- 🔴 **签名比较一律用 `subtle.ConstantTimeCompare`。**
  签名是这条公网入口上唯一的身份凭证，逐字节短路比较会从响应耗时泄漏「已经对上了几位」，把离线爆破变成在线爆破。
  `handler/open_api.go` 里 `app.AppSecret != req.AppSecret` 那处裸 `!=` 是**历史遗留，不要照抄**；
  新代码照 `handler/mcp_auth.go` 与 `wxcrypt.VerifySignature` 写。
- 🔴 **触发一律走 `engineDispatcher` 在进程内回放 `/api/v1` 接口，不直调 `service.GetSchedulerV2().RunNow()`。**
  直调会绕开 `OpenAPIAccess` 的 scope 校验、调用限流与 `ApiCallLog` 审计——
  那三样正是「公网入口能触发执行」这件事的可追溯性来源。回放走的是和开放 API 客户端一模一样的路径
  （`JWTAuth` → `OpenAPIAccess` → `RequireRole`），所以这条入口不比现有开放 API 多任何权限。
- 🔴 **验签之后一律回 200 + 空串。** 企业微信要求 5 秒内响应，非 200 会被它当失败**重试三次**——
  同一条「运行 xx」指令因此被执行三遍。执行结果走现有的 `wecom_app` 通知渠道异步推回，不占这次响应。
  验签**之前**的失败照真实状态码回（401/403/404），这样管理员在企业微信后台配错时能立刻看到。
- **菜单链接票据长期有效**，拿到链接就等于拿到这一个任务的永久触发权（链接会留在企业微信服务器、
  内置浏览器历史和反代访问日志里）。三条配套约束绑在一起，缺一条这个取舍就不成立：
  票据**只绑单个 `task_id`**（不是整个 tasks 权限）；必须提供一键作废——
  止血手段是把 `system_config` 里的代次 `wecom_trigger_link_generation` **+1**，所有旧票据立刻验不过，
  不需要逐条记录已签发的票；调用方那一侧还压着上面两级总开关。

### 4. Validation & Error Matrix

- `wecom_trigger_enabled=false` → 403，不验签、不落任何记录。
- 接入配置不存在 / 自身被禁用 → 404 / 403（都在验签之前）。
- `msg_signature` 对不上 → 401「企业微信回调验签失败」。
- 解密后 receiveid 与 CorpID 不符 → 按验签失败处理，绝不放行。
- 验签通过、`wecom_trigger_allow_run=false` → 200 空串，不跑任务。
- 验签通过、指令解析失败 / 任务不存在 / scope 不足 → **仍然 200 空串**，原因通过通知渠道推回。
- 代次已 +1 的旧菜单链接 → 票据验不过，按无效链接处理。

### 5. Tests Required

- 开关矩阵：总开关关 / 开+不允许触发 / 全开三档下的行为。
- 验签：正确签名、错签名、receiveid 不匹配、时间戳与 nonce 参与排序的顺序。
- 回包口径：验签前的失败回真实状态码；验签后的各种失败都回 200 空串（防重试三遍那条回归）。
- 票据：绑 A 任务的票据拿去触发 B 任务必须失败；代次 +1 之后全部旧票据失效。
- 修改后至少运行：

```bash
cd server
go test ./pkg/wxcrypt ./pkg/trigticket -count=1
go test ./handler -run "Wecom" -count=1
go test ./...
```

---

## 场景：订阅过滤的正则片段与子目录范围（issue #129）

### 1. Scope / Trigger

- 触发：修改 `server/service/subscription_patterns.go`、`subscription.go` 的 sparse 规则构建 / 任务候选扫描，或改动白名单、黑名单、依赖规则、`sub_path`、`full_checkout` 时必须看本节。

### 2. Contracts

- **按片段判定是否正则**：片段含 `^ $ ( ) [ ] { } ? \` 或 `.*` `.+` 时按 RE2 正则匹配**仓库内相对路径**（不锚定，路径分隔一律 `/`）；否则保持历史的「子串包含」语义。顶层 `,` / `|` 分隔多个片段。
- 保存时校验：非法正则直接 400（只在值有变化时校验，避免老数据被卡住）。
- **正则表达不了 git 的检出规则**：白名单/依赖含正则片段时退化为整仓检出（tier-1），但**建任务的范围不变**。
- **`sub_path` 是硬边界**：填了子目录时，依赖规则的普通片段虽然会被并进 sparse（这些文件要落盘），但**不得因此把子目录外的文件纳入建任务范围**——否则依赖文件会被建成定时任务。判定统一走 `subscriptionSubPathCovers`。
- 兜底计数同步收口：白名单兜底（「一个都没命中就别全删」）的计数只数子目录范围内的文件，否则会出现「白名单只命中子目录外的依赖文件 → 兜底不触发 + 护栏挡掉 → 一个任务都不建」。

### 3. Tests Required

- 真实 git 仓库用例：`sub_path` + 依赖普通片段 → 只给子目录里的脚本建任务，依赖文件落盘但不建任务。
- 正则片段的三种写法（`scripts`、`scripts/`、`scripts/*`）结论一致。
- pin/golden 用例锁住「一组配置 → managed / candidates / patterns」的整体结论，改动时必须解释每一处差异。
- 修改后至少运行：

```bash
cd server
go test ./service -run "Subscription|Sparse|Depend|Pattern|SubPath|Pin" -count=1
go test ./...
```

> **突变验证**：把子目录护栏那一行改成恒 false，子目录用例必须变红。

---

## 场景：内嵌前端的静态服务与缓存（issue #126）

### 1. Scope / Trigger

- 触发：修改 `server/static_frontend.go`、`server/main.go` 的静态挂载、`docker/nginx.conf` 的缓存段时必须看本节。
- 原因：面板升级会整目录替换 `web/`，旧 `index.html` 引用的 hash 文件名全部消失。浏览器手里还是旧壳时，任何动态 import 都会失败，而且**没有报错**：切页没反应、编辑器空白。

### 2. Contracts

- `index.html` 与 SPA 深链：`Cache-Control: no-cache`（必须回源校验）。
- 带 hash 的资源（`assets/*`）：`immutable` 长缓存。
- **缺失的 hash 资源必须回 404**，绝不能掉进 SPA fallback 回 200 + HTML：前端据此判定「旧壳」，回 200 会让它把 HTML 当 JS 解析。
- 错误响应不得带长缓存。
- gzip 结果在内存里缓存，按文件内容失效。
- `docker/nginx.conf` 必须与上面这套口径一致（Docker 部署不走 Go 的静态层）。

### 3. Tests Required

- `/` 与深链：`no-cache`。
- hash 资源：`immutable` + gzip + 304。
- 缺失资源：404 且不是 HTML。
- `/api/**` 未匹配：404 JSON（不得回 HTML）。
- 修改后至少运行：

```bash
cd server
go test ./ -run "StaticFrontend" -count=1
go test ./...
```

---

## 场景：「运行中被禁用」标记的时钟判据

### 1. Scope / Trigger

- 触发：修改 `server/service/manual_stop.go` 的 `pendingDisableMarks` / `MarkPendingDisable` / `hasPendingDisable`，或任何用「两个墙钟时刻比先后」来判定状态归属的逻辑时必须看本节。

### 2. Contracts

- 标记存的是打标时刻，用途只有一个：防 SQLite 自增 id 复用（任务被标记后删掉，新建的同 id 任务不能继承这笔禁用意图）。
- 判据必须是 `!markedAt.Before(task.CreatedAt)`（不早于），**不能是 `After`（严格晚于）**：Windows 上 `time.Now()` 的粒度约 515µs，「建任务 → 立刻标记」经常落在同一个 tick 里，两个时刻相等时严格比较会把用户真实的禁用意图当成 id 复用丢弃。
- 放宽后被认可的标记集合是原来的**严格超集**，任何调用方都不会因此少认一笔标记（`ResolveTaskInactiveStatus`、任务列表的 `HasPendingDisable`、`ResolveTaskEnabledSwitch`、`RunNow`、AddJob 护栏都只会更符合用户刚点的那一下）。
- 残余误继承窗口：「打标 → 删任务 → 新建任务复用同一 id」必须整套挤进同一个 tick（中间还夹两次 SQLite 写），实际不可达。已实测 `CreatedAt` 在 GORM + `glebarez/sqlite` 下往返**纳秒无损**，不存在「存储精度截断把窗口放大」这一说。
- 已知未解：墙钟在「建任务」与「打标」之间被往回调时，标记会被判成 id 复用而丢弃。只有改存单调序号或在打标时连 `CreatedAt` 一起存才能免疫，本版没做。

### 3. Tests Required

- 创建时刻与打标时刻**完全相等** → `hasPendingDisable` 为真，`ResolveTaskInactiveStatus` 结算为禁用（还要覆盖「剥掉单调钟」的相等，模拟从库里读出来的任务）。
- 创建时刻晚于打标 1ns → 仍判 id 复用，结算回启用；`CreatedAt` 为零值 → 不命中。
- 修改后至少运行：

```bash
cd server
go test ./service -run "PendingDisable|Disabled|Scheduler|Enabled" -count=1
go test ./service -run "^TestSchedulerV2AddJobSkipsTasksPendingDisable$" -count=100
```

> **突变验证**：把判据还原成 `markedAt.After(...)`，「相等时必须认这笔标记」那条必须变红，而防复用那条仍绿。
> 这条偶发在修复前是 3~7/100，单跑 `-count=25` 复现不出来 —— 概率性用例必须跑够轮数才能下结论。

---

## 场景：换 Node / Python 运行时后的依赖自愈（模块版与 Docker）

### 1. Scope / Trigger

- 触发：修改 `server/service/node_abi_rebuild.go`、`server/service/python_libc_repair.go`、`server/service/python_runtime.go` 的模块版迁移、`Magisk/service.sh` 的 deps 快照回填、`Magisk/customize.sh` 的运行时版本、`server/handler/deps.go` 的卸载命令，或调整启动钩子顺序时必须看本节。
- 原因：模块刷新版 / Docker 换镜像会换掉容器里的 Python 与 Node，但 `deps/` 原样保留。旧记录指向已不存在的解释器、旧 ABI 的原生扩展加载失败、按另一种 C 库编译的扩展加载失败，用户看到的都是「定时任务突然报 ModuleNotFoundError / NODE_MODULE_VERSION 不匹配 / `libc.musl-x86_64.so.1: cannot open shared object file`」（#150）。

### 2. Signatures

- `service.ApplyMagiskPythonRuntimeMigrationOnStartup()`：`appboot.go` 中挂在 `ApplySinglePythonRuntimePolicyOnStartup()` 之后、`MergeDuplicatePythonDependencies()` 之前。
- `service.RebuildNodeDependenciesIfABIChanged()`：`main.go` 中挂在 `verifyInstalledDeps()` 之后。
- 标记文件：`<Data.Dir>/deps/nodejs/.daidai-node-abi`，内容为 `process.versions.modules`（常量 `nodeABIMarkerFileName`）。
- `service.RepairPythonPackagesForLibcChange()`（v3.3.3，#150）：`main.go` 中挂在 `RebuildNodeDependenciesIfABIChanged()` 之后，只在 Linux 执行，整个过程放后台 goroutine。
- 标记文件：`<venv>/.daidai-python-libc`（即 `<Data.Dir>/deps/python/<版本>/.daidai-python-libc`），内容为 `musl` 或 `glibc`（常量 `pythonLibcMarkerFileName`）。

### 3. Contracts

- **Python 迁移只收敛「旧且不存在」的版本**：以 `DAIDAI_PYTHON_VERSION` 为当前版本 C（必须确有解释器），只把**比 C 旧、且解释器确实不存在**的小版本 V 的 `dependencies.python_version` / `tasks.python_version` / `python_default_version` 改成 C。比 C 新的一律不动 —— Debian flavor 的系统 python3 是 3.11，用户显式选的 3.12 要靠一键安装补回，不能被降级。V 仍可用时也不动。
- 迁移用 raw SQL 只改 `python_version`，不动 `updated_at`（否则会打乱 `MergeDuplicatePythonDependencies` 挑选保留行）和 `status`；重装交给随后的启动校验。
- 条件驱动、每次启动都跑，不用一次性标记：前端仍可能提交旧版本的依赖，一次性标记会被这条写入路径打穿。
- **Node 侧只重建「现在确实加载失败」的包**：逐包起独立 node 进程 require 一次，只有报 `NODE_MODULE_VERSION` 不匹配 / `No native build was found` / `Could not locate the bindings file` 才算坏。原因是 `npm rebuild` 走 node-gyp 时第一步就删 build 目录，对本来能用的包（N-API 模块、自带新 ABI 预编译产物的包）重建失败会把它弄坏。只碰已经坏掉的包，就不需要备份 / 恢复、遗留进程回收这类防御层。
- 收集包时不跟随软链接（`file:` 本地目录包、workspace 指向仓库外的真实目录，不归面板管），递归 `@scope` 与嵌套 `node_modules`。
- 只在 Linux 执行：换 Node 大版本只发生在 Magisk 模块与 Docker 镜像；Windows / 二进制版用户自己管理 Node。
- **标记必须与它描述的二进制同源**：`Magisk/service.sh` 开机用 `cp -rf` 回填宿主 deps 快照（不删多余文件，快照每 10 分钟才刷新），回填后若快照里没有该标记，就要删掉容器里的标记。否则「重建完写了新标记 → 10 分钟内重启 → 旧二进制被盖回、新标记还在」会让面板再也不重建。`magisk_assets_test.go` 有静态断言锁这段位置与写法。
- 状态一律不改：无论重建成败都不碰依赖记录的 `status`。
- **Python 跨 C 库（musl ↔ glibc）只修 RECORD 归属得到的发行包**：Alpine 与 Debian 镜像的 Python 装在同一路径、同一版本，数据卷里的 venv 照样能跑，venv 健康检查（版本号 + `pip --version`）与启动校验（`pip show` 在不在）都看不出问题，`pip install` 对「已安装」的包直接跳过，所以必须由面板在「知道 C 库变了」时主动 `pip install --force-reinstall --no-deps <name>==<version>`（这里的 `--no-deps` 是 pip install 的合法选项）。
  - 判**当前** C 库看 venv 背后真实解释器（`filepath.EvalSymlinks`）的 ELF `PT_INTERP`：含 `ld-musl-` 为 musl、含 `ld-linux` 为 glibc，判不出就跳过。**不要**看解释器的 `DT_NEEDED`（musl 版 python-build-standalone 解释器 NEEDED 的是 `libc.so` 而不是 `libc.musl-*`），也不要看 `/lib/ld-musl-*` 在不在（Debian 装了 musl 包就会误判，把所有 glibc 扩展当成外来的反复重装）。
  - 判**外来**扩展看 site-packages 下 `*.so` 的 `DT_NEEDED`（`debug/elf` 的 `ImportedLibraries`）：当前 glibc 时找 `libc.musl-*` 或 `libc.so`，当前 musl 时找 `libc.so.6`。
  - 用 `encoding/csv` 解析 `*.dist-info/RECORD` 归属到发行包，名字与版本取自 dist-info 目录名（按最后一个 `-` 切）；不属于任何 RECORD 的孤儿文件只记日志、不处理（面板不知道该装什么）。逐包执行 pip：一条命令里任何一个包失败会整批不装。不整个重建 venv：面板没登记的包（终端手动装的、传递依赖）会全丢，重装窗口里任务大面积 `ModuleNotFoundError`。
  - 标记放 venv 目录里：venv 被隔离重建时标记跟着消失，与它描述的 site-packages 同源，不需要像 Node 那样改 `Magisk/service.sh`。**全部成功才写标记**，有失败就不写、下次启动再试 —— 与 Node「重建失败也写标记」相反，因为 pip 失败多半是开机时网络未通这类暂时问题，而面板的重装按钮对这种包是空操作，不自动重试用户就没有出路。依赖记录的 `status` 一律不改。
- **pip uninstall 没有 `--no-deps`**：它本来就不删依赖，带上这个选项 pip 在参数解析阶段就以退出码 2 退出、一个包都不卸。`BuildPipUninstallArgs` / `NewPipUninstallCommandForPythonVersion` 刻意不留「额外选项」参数；强制 / 批量卸载（先删记录、后台卸载）失败时必须把输出末尾写进面板日志，不能无声失败。

### 4. Validation & Error Matrix

- node 不可用 / `process.versions.modules` 不是纯数字 → 不做事、**不写标记**，下次启动再判。
- 标记与当前 ABI 相同 → 不做事。
- `node_modules` 里没有非点开头的真实子目录 → 直接写标记。
- 没有加载失败的包 → 写标记（本次无需重建）。
- 找不到 npm → warn 日志、**不写标记**（根本没尝试过重建）。
- 重建失败 / 重建后仍加载失败 → 写标记 + warn 日志提示到依赖管理里重装（同一 ABI 不再自动重试，避免没有编译链的精简镜像每次开机重跑）。
- 迁移侧：非模块运行态、`DAIDAI_PYTHON_VERSION` 为空或不在 3.10–3.12、当前版本探测不到解释器 → 一律不动任何记录。
- Python C 库侧：非 Linux / venv 不存在 / 解释器 C 库判不出 → 不做事、不写标记；标记与当前 C 库相同 → 不扫描；没有外来 `.so`（或外来的全是孤儿文件）→ 写标记、不跑 pip；有包重装失败 → warn 日志（提示删除 `deps/python/<版本>` 后重启可重建）、**不写标记**。

### 5. Good/Base/Bad Cases

- Good：Alpine 模块从 3.18（Python 3.11）刷到 3.23（Python 3.12），3.11 的依赖与任务被迁到 3.12 并由启动校验重装；带原生扩展的包里只有加载失败的那个被 `npm rebuild`。
- Base：Docker 换镜像后 ABI 变了，但所有原生扩展都是 N-API、照常加载 → 只写标记，不动任何包。
- Bad：把「比当前新的版本」也迁走（Debian flavor 上把用户选的 3.12 降成 3.11）；对全部包无差别 `npm rebuild`；重建失败后仍宣称已修复；标记写在 `deps/` 下却不处理快照回填。
- Good：Docker 从 `latest`（Alpine）换到 `debian`，数据卷里 musl 版 `pycryptodome` 报 `libc.musl-x86_64.so.1` 找不到 → 下次启动只重装 `pycryptodome==3.23.0` 这一个包，纯 Python 包一个不碰。
- Bad：只按 ABI 号 / 解释器版本判断运行时变了没有，漏掉 musl ↔ glibc（两个镜像 Python 同版本同路径、Node 同为 24，ABI 号都不变）。
- Bad：`pip uninstall -y --no-deps <name>` —— 选项不存在，退出码 2、什么都没卸；强制 / 批量卸载又是先删记录，v1.3.0 起多年「只删了记录」没人发现。当年的单测还把 `--no-deps` 断言成必须存在，把 bug 钉成了契约（#150 反转）。

### 6. Tests Required

- 迁移：3.11 缺失 → 依赖 / 任务 / 默认值迁到 3.12 并与既有同名依赖合并；3.11 仍可用 → 不动；比当前新的版本 → 不动；非模块态 → 不动；连续执行两次结果一致且 `updated_at` 不变。
- Node：ABI 一致不做事；无包 / 无原生扩展 → 写标记且不探测；都能加载 → 不重建；部分失败 → 只把失败包的 name 传给 rebuild（覆盖 `@scope`、嵌套 `node_modules`、同名去重）；找不到 npm → 不写标记；重建失败 → 写标记且依赖表逐字段不变；软链接包不被收集；`nodeABIRebuildSupported` 的平台判定。测试注入探测与命令构造，不真跑 node / npm。
- 静态门禁：`magisk_assets_test.go` 断言 `service.sh` 的标记删除位于快照回填之后，且文件名与 Go 常量一致。
- Python C 库（`python_libc_repair_test.go`，注入平台、解释器 C 库判定、`.so` 的 NEEDED 读取与 pip 命令构造，不读真 ELF、不真跑 pip）：标记相同不扫描；无外来 `.so` 写标记不跑 pip；同一发行包多个外来 `.so` 只修一次；两个包各修一次且健康包不动；当前 musl 时 glibc 扩展算外来；一个包失败其余照跑且不写标记；孤儿文件只记日志；RECORD 路径带逗号被加引号；C 库判不出 / venv 不存在 / 非 Linux 什么都不做。`startup_wiring_test.go` 钉住接线顺序。
- 卸载参数：`TestBuildPipUninstallArgsDropsUserFlag` 逐项断言完整参数 `uninstall -y --break-system-packages requests`，多塞任何选项都会变红。
- 修改后至少运行：

```bash
cd server
go test ./service -run "MagiskPython|NodeABI|NodeDependencies|NpmRebuild|PythonLibc|PipUninstall|MainWires" -count=1
go test ./handler -run "Magisk|NodeVersion|Dependency" -count=1
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：无差别重建，再用备份/恢复去兜 node-gyp 删掉的产物。
cmd := exec.Command("npm", "rebuild", "--prefix", nodeDir)
backup, _ := snapshotNativeAddons(nodeDir) // 备份→恢复→怕被快照带走→怕孤儿进程……防御层越叠越厚
```

#### Correct

```go
// 正确：先探测，只重建加载失败的包；已经坏掉的包重建失败也不会更糟，于是不需要备份。
broken := filterBrokenNodeAddons(nodeDir, collectNodeNativeAddonPackages(modulesDir))
if len(broken) == 0 {
    writeNodeABIMarker(nodeDir, markerPath, abi)
    return
}
cmd, err := newNpmRebuildCommandFunc(nodeDir, nodePackageNames(broken))
```

---

## 场景：资源采样与 MCP 工具对齐（v3.3.0，#139 #140）

### 1. Scope / Trigger

- 触发：修改 `server/service/resource_monitor.go` 的 CPU / 网速采集，或在 `server/mcptools` 新增 / 修改工具时必须看本节。

### 2. Signatures

- `service.GetResourceInfo()`：Linux 上 CPU 与网速取自 `defaultLinuxResourceSampler.current()`（`sync.Once` 懒启动后台循环，每 3 秒一次）；没有缓存时同步采样一次兜底，并发的首次请求只采一次。
- MCP：`readToolNames` / `writeToolNames`（`server/mcptools/server.go`）是工具的完整名单，测试按名单逐个核对注册与注解。

### 3. Contracts

- **不要在接口请求里现场采样 CPU**：请求并发时面板自己处理这批请求的开销会落进采样窗口，两核机器上读数常年 50% 左右（#140）。口径：忙碌 = 总计 − idle − iowait，总计不累加 `guest` / `guest_nice`（已计入 user / nice）。
- MCP 工具只做「转发到已有开放接口 + 整理结果」，权限沿用接口本身的 scope 与角色校验；写入 / 执行工具只能经 `addWriteTool` 在 `allowMutations` 时注册。
- **会覆盖已有数据的也算破坏性**：重命名 / 移动 / 复制（同名静默覆盖）、创建同名备份，与删除、恢复一样标 DestructiveHint。
- 面板接口对「全部失败」也回 200 时（批量删除脚本、删除不存在的备份），工具要自己判定并返回错误，不能把失败当成功转述给 Agent。
- 接口返回**裸数组**时（`GET /tasks/labels`，v3.3.5）不能走通用的 `call()`：它只把 2xx 解成对象，数组会被报成「无法解析的内容」。
  照 `tools_read.go` 的 `listTaskLabels` 直接 `t.d.Do` 取原始响应、单独解码：非 2xx 仍交 `apiError`；2xx 却解不出数组时如实报错，**不能**当成空列表；输出包成对象（`{total, labels}`）。
- `read_script` 按字节分段：`offset` / `limit`（上限 48 KiB），返回 `total_bytes`、`next_offset`、`truncated`；切分点必须落在 UTF-8 字符边界。

### 4. Validation & Error Matrix

- 采样读不到 `/proc/stat` 或两次快照总差为 0 → 使用率按 0 处理，不报错。
- 只读模式 → 写入工具一个都不注册；新增工具忘记加进名单 → 名单测试变红。
- `import_envs` 替换模式会先删全部变量：导入前先校验变量名与「看起来是脱敏值」的敏感变量，任一不合规就整体拒绝，避免删光后又导入失败。

### 5. Good/Base/Bad Cases

- Good：APP 首页并发请求资源、统计、仪表盘时，CPU 读数与 `vmstat` 一致。
- Bad：在资源接口里 `time.Sleep(500ms)` 现场采样；新增一个会覆盖文件的工具却不标 DestructiveHint。

### 6. Tests Required

- `resource_monitor_test.go`：由两份 `/proc/stat` 快照算使用率（普通 / iowait / guest / 计数回绕）、缓存命中不阻塞、并发兜底只采样一次。
- `mcptools`：每个工具断言路由、方法、参数映射与结果整理；只读模式不注册写工具；破坏性名单与注解一致；`read_script` 多段拼接等于原文。

### 7. Wrong vs Correct

#### Wrong

```go
idle1, total1 := readCPUStat()
time.Sleep(500 * time.Millisecond) // 请求并发时把面板自己的开销也量进去
idle2, total2 := readCPUStat()
```

#### Correct

```go
sample := defaultLinuxResourceSampler.current() // 有后台采样缓存直接返回，没有时才同步采样一次
// 使用率统一由纯函数 cpuUsagePercent(prev, cur procStatCPU) 计算，便于单测覆盖各种口径
```

---

## 场景：用户界面偏好按组写入（`/api/v1/auth/preferences`，v3.3.1 #143）

### 1. Scope / Trigger

- 触发：修改 `server/handler/user_preference.go`、`server/model/user_preference.go`、`database.go` 里 `user_preferences` 的补列、
  备份里的 `BackupUserPreference`，或前端 `web/src/utils/editorPreferences.ts` / `listPreferences.ts` 的同步逻辑时必须看本节。
- 跨层链路：DB 两列 JSON → handler 按组合并 → 同形响应 → 前端两套偏好同步（editor 靠组级 `stored` 决定上行还是下行，list 稀疏、逐键迁移）。
  前端侧契约见 `frontend/component-guidelines.md` 的「代码编辑器」与「列表页偏好」两节。

### 2. Signatures

- 路由（`server/handler/auth.go`）：`GET /api/v1/auth/preferences`、`PUT /api/v1/auth/preferences`，都只挂 `middleware.JWTAuth()`，不限角色。
- model：`UserPreference.Editor`；v3.3.1 新增 `List string` + `gorm:"type:text;not null;default:''" json:"list"`。
- database：`EnsureColumns` 里 `ensureTableColumns("user_preferences", {"list", "TEXT NOT NULL DEFAULT ''"})`。
  SQLite 的 `ADD COLUMN` 写 `NOT NULL` 必须同时给 `DEFAULT`；存量行补列后落 `''`，即「一个键都没存过」。
- 响应（GET 与 PUT 同形，由 `preferencesPayload` 拼）：`{ editor: 完整 5 项, stored: bool, list: 稀疏对象 }`。
  `list` 一个键都没有时编码成 `{}` 而不是 `null`：前端靠「list 是不是对象」判断服务端认不认识这一组。
- PUT 入参：`{ editor?: editorPreferencesPatch, list?: listPreferences }`，**两个都是指针**。
- `list` 白名单（`listPreferences` 结构体，全是指针 + `omitempty`）：
  - `tasks_page_size`：JSON number，取 10 / 20 / 50 / 100；
  - `envs_page_size`：JSON string，取 `"20"` / `"50"` / `"100"` / `"all"`（有 `"all"`，只能走字符串）；
  - `tasks_view_all_hidden` / `tasks_view_groups_hidden`：JSON bool，**不**兼容 `"on"` / `"off"`（新接口，没有历史客户端要照顾）；
  - `log_open_at_bottom`（v3.3.3，#147）：JSON bool，同上只收布尔。打开已结束的日志时是否定位到底部，面板网页与 APP 共用；
    没存过不下发，各端按自己的现状（网页顶部、APP 底部）。
  - 组名还叫 `list`，但它装的是「列表页 / 日志查看等跟账户走的零散界面开关」。再有这类开关照样往这里加键，不另开组、不加列、不改备份。
    加键清单：结构体字段、`empty()`、`mergeListPreferences`、`decodeListPreferences` 各一处。🔴 漏了 `empty()`，只带新键的 PUT 会被判成空补丁、200 静默 no-op。
- 常量：`editorPreferenceMaxBytes` / `listPreferenceMaxBytes`（都是 4KB）；包级锁 `preferenceWriteMu sync.Mutex`。
- 备份：`BackupUserPreference` 新增 `List string json:"list,omitempty"`，导出（`backup_runtime.go` 的 snapshot）与 `restoreUserPreferences` 成对带上。
  ⚠️ `restoreUserPreferences` 目前没有调用点（用户、2FA、偏好都只导出不恢复），补字段不代表恢复会生效。

### 3. Contracts

- **按组写入**：请求里带了哪组才写哪组的列，没带的那组的列**一个字节都不碰**。
  upsert 的 `DoUpdates: clause.AssignmentColumns(columns)` 按这次实际写了的组动态拼（外加 `updated_at`）；
  新建行时没写的那组列落列定义的 `DEFAULT ''`，也就是「没存过」。
  - 为什么：以前 `Editor` 入参是值类型、`DoUpdates` 写死 `editor`，于是只发 `{"list":{...}}`（或 `{}`）的请求也会把一整套 editor 默认值写进库，
    `stored` 从 false 翻成 true。前端的列表偏好迁移恰好就是「只发 list」，它一上线，每个升级用户存在本机 `dd:editor:*` 的编辑器偏好，
    都会在下次打开编辑器时被默认值静默冲掉 —— 不报错、测试全绿。
- **editor 组**：只要带了 editor 对象就写，**哪怕是空对象 `{}`**，照旧写入合并后的整套值、`stored` 置 true。只发 editor 的客户端（APP、历史前端）行为必须逐字不变。
- **list 组**：稀疏存储、逐键合并。`{"list":{}}`、或 list 里只有白名单外的键时不写：稀疏存储下空补丁没有东西可存，写下去只会凭空建出一行。
- **两组都不带**（`{}`、`{"editor":null,"list":null}`、`{"list":{}}`、`{"list":{"unknown_key":1}}`）→ 200 no-op：原样回当前值，不落库、不建行。
- **`stored` 只描述 editor 组**：GET 按「有行 + Editor 非空 + 能解析成 JSON 对象」判定；PUT 带了 editor 就是 true，没带时沿用这次读库的判定。
  🔴 **不能再写死 true**，否则只改 list 的请求会让前端误以为 editor 存过，拿默认值冲掉本机那份编辑器偏好。
- **先校验、再读库加锁**：两组的取值都校验完，才去查用户、拿 `preferenceWriteMu`、读库；任何一组非法就整单 400，另一组也一个字节不写，非法请求也不用排进锁里。
- **下发前清洗 list**（`decodeListPreferences`）：空串、不是 JSON、不是对象（含 `null`、数组、裸字符串）一律当 `{}`；
  是对象时逐键过类型与白名单，只丢脏的那一个键，白名单外的键不下发；JSON `null` 当「没有这个键」（解到 `*T` 上，免得被当成显式的 false）。
  在脏行上 PUT 时，以清洗后的值为底合并，脏键不会被原样写回。GET 读库失败按「没有行」处理，不 500。
- 用户显式存的 `false` 照常下发（`omitempty` 只看指针是否为 nil），与「从没存过」是两种形态，前端能分清。
- **4KB 上限**：两列各自按合并后编码的长度判，超了 400。现在 list 编码后撑死一百来字节，这道闸是给将来加键时留的。
- **`preferenceWriteMu` 把「读整行 → 合并 → upsert 整列」串成一段**：
  - 为什么非加不可：upsert 写回的是合并后的**整列** JSON。两个只改不同键的 PUT（两个标签页各改各的、
    首次迁移紧跟着一次改动）如果都先读到旧行、再先后写回，后写的那个会把先写的那个键改回旧值：服务端静默丢一个设置，
    下次加载时前端还拿旧值冲掉本机缓存，用户看到设置「自己变回去了」。
    `database.go` 的 `SetMaxOpenConns(1)` 只让单条语句轮流用连接，挡不住两段读-改-写在语句之间交错。
  - 为什么不用事务：单连接池下，事务里任何一处用了 `database.DB` 而不是 `tx`（`loadPreferenceRecord` 现在就是），都会去等那条被事务自己占着的连接，直接死锁。
    SQLite 文件只有这一个面板进程在写，进程内锁就够；所有用户共用一把，偏好写入频率很低，不值得按用户分锁。
  - 串行化管不了先后：两个 PUT 改**同一个**键时，以服务端后处理的那个为准。
  - 锁内读库失败必须 500 中止（「读取偏好失败」），不能像 GET 那样回落：拿空记录合并再写回，会把 list 里之前存过的其它键整片抹掉。
- 白名单与前端 `listPreferences.ts` 的 `TASKS_PAGE_SIZE_OPTIONS` / `ENVS_PAGE_SIZE_OPTIONS` 逐项对齐；editor 默认值与 `editorPreferences.ts` 逐字对齐。两边都有注释，改一边必须改另一边。

### 4. Validation & Error Matrix

| 输入 / 情形 | 结果 |
|---|---|
| body 不是 JSON；editor 或 list 不是对象；list 某键类型不对（`"50"`、`50.5`、`1`、`"1"`、布尔键传 `"on"`） | 400「请求参数错误」 |
| minimap / indent_guides 取值不认识 | 400「minimap / indent_guides 取值需为 true、false、on 或 off」 |
| `tasks_page_size` 不在白名单（如 30） | 400「tasks_page_size 取值需为 10、20、50 或 100」 |
| `envs_page_size` 不在白名单（如 `"ALL"`、`"1"`） | 400「envs_page_size 取值需为 20、50、100 或 all」 |
| editor 的枚举项非法 | 400，按字段报（如「word_wrap 取值需为 on 或 off」） |
| 合法的 editor + 非法的 list | 整单 400，editor 也不落库，`stored` 仍为 false |
| 合并后编码超过 4KB | 400「编辑器偏好数据过大」/「列表偏好数据过大」 |
| 用户不存在 | 404「用户不存在」（校验在前：非法 body 先得到 400） |
| PUT 锁内读库失败 / upsert 失败 | 500「读取偏好失败」/「保存偏好失败」 |
| `{}`、`{"list":{}}`、只有白名单外键的 list | 200 no-op，不建行，`stored` 如实 |
| GET 时 list 列是脏数据 | 200；整列解不开当 `{}`，单键脏只丢那一个键 |

### 5. Good/Base/Bad Cases

- Good：前端列表偏好迁移只发 `{"list":{"tasks_page_size":50}}`，editor 列逐字节不变、`stored` 仍为 false；两个改不同键的并发 PUT 都落库。
- Base：只发 editor 的 APP 客户端，行为与 v3.3.0 逐字一致（包括发 `{"editor":{}}` 会写入整套默认值）。
- Bad：`DoUpdates` 写死 `editor`；把 `Editor` 入参改回值类型；PUT 响应写死 `stored: true`；为了串行化包一层 `database.DB.Transaction`，里面却还在用 `database.DB`。

### 6. Tests Required

- `server/handler/user_preference_test.go`：
  - editor 组原有 7 条：`TestGetEditorPreferencesReturnsDefaultsForFreshUser`、`TestUpdateEditorPreferencesMergesPerField`、
    `TestUpdateEditorPreferencesAcceptsOnOffFlags`、`TestUpdateEditorPreferencesRejectsInvalidValues`、`TestEditorPreferencesAreIsolatedPerUser`、
    `TestGetEditorPreferencesFallsBackOnCorruptedRow`、`TestEditorPreferencesStoredFlagTracksPersistence`。
  - v3.3.1 新增：
    - `TestUpdateListPreferencesNeverTouchesEditorColumn`：三种起点（没有行 / 有行但 editor 为空 / editor 已存过），Raw SELECT 确认 editor 列逐字节不变、`stored` 不被翻成 true；
    - `TestUpdateEditorPreferencesNeverTouchesListColumn`：反方向；
    - `TestListPreferencesSparseStorageAndTypes`：只下发存过的键，每个键的 JSON 类型正确，显式存的 false 照常下发；
    - `TestUpdateListPreferencesRejectsInvalidValues`：11 种非法输入全部 400，且一个键都不落库、不建行，同请求里合法的 editor 也不落；
    - `TestListPreferencesAreIsolatedPerUser`；
    - `TestGetListPreferencesDropsCorruptedValues`：脏 JSON 仍 200，逐键丢弃，在脏行上合并不写回脏键；
    - `TestUpdatePreferencesWithoutAnyGroupIsNoop`：4 种 no-op body 不建行；已存过两组时 `{}` 两列都不动；
    - `TestUpdateListPreferencesConcurrentDifferentKeysDoNotOverwrite`：20 轮，每轮复位后同时放出一对单键 PUT，两个键最后都必须为 true。
      测试库与生产一样是单连接池——换成多连接反而测不出来。突变验证：注释掉锁后 5/5 失败。
- `server/database/user_preference_list_migration_test.go`：`TestEnsureColumnsAddsUserPreferenceListToLegacyDatabase`。
- `server/service/backup_user_preferences_test.go`：`TestSnapshotConfigBundleKeepsUserPreferenceColumns`。
- 修改后至少运行：

```bash
cd server
go test ./handler -run Preferences -count=1
go test ./database -run UserPreferenceList -count=1
go test ./service -run UserPreferenceColumns -count=1
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：值类型 + DoUpdates 写死 editor —— 只发 list 的请求也会把 editor 默认值写进库、stored 翻成 true；
// 而且读-合并-写没有串行化，两个改不同键的并发 PUT 会互相覆盖。
var req struct {
    Editor editorPreferencesPatch `json:"editor"`
    List   *listPreferences       `json:"list"`
}
record, _ := loadPreferenceRecord(user.ID)
// ...合并...
database.DB.Clauses(clause.OnConflict{
    Columns:   []clause.Column{{Name: "user_id"}},
    DoUpdates: clause.AssignmentColumns([]string{"editor", "updated_at"}),
}).Create(&upsert)
response.Success(c, preferencesPayload(editorPrefs, true, listPrefs))
```

#### Correct

```go
var req struct {
    Editor *editorPreferencesPatch `json:"editor"`
    List   *listPreferences        `json:"list"`
}
// 先校验两组（非法整单 400）……
preferenceWriteMu.Lock()
defer preferenceWriteMu.Unlock()
record, err := loadPreferenceRecord(user.ID) // 锁内读失败必须 500，不能回落成空记录
var columns []string
if req.Editor != nil { /* 合并、编码、查 4KB */ columns = append(columns, "editor") }
if writeList      { /* 同上 */                columns = append(columns, "list") }
if len(columns) == 0 { response.Success(c, preferencesPayload(editorPrefs, editorStored, listPrefs)); return }
columns = append(columns, "updated_at")
// DoUpdates: clause.AssignmentColumns(columns)；stored 只在写了 editor 时置 true
```

---

## 场景：Playwright 运行环境——浏览器目录、一键安装与重建后自愈（v3.3.1 #142）

### 1. Scope / Trigger

- 触发：修改 `server/service/playwright_env.go`、`playwright_runtime.go`、`server/handler/deps_playwright.go`，
  `deps.go` 的 `runCmdWithSSEThen` / `depFollowUpStep` / `buildDependencyFailureHint` / `installDependency`，
  `linux_packages.go` 的包锁与 apt 选项、`linux_mirror.go` 的 apt 源改写、`dependency_reconcile.go`、`backup_runtime.go` 的 `reinstallDependency`，
  或 `main.go` 的启动顺序时，必须看本节。
  （v3.3.5 起：启动校验怎么判「已安装」见「场景：启动依赖校验的已装包快照（契约 S1）」；`runCmdWithSSEThen` 的输出接法见「场景：依赖命令的输出管道（契约 P1）」。）
- 背景：镜像不预装 Playwright（体积不变，用户 2026-09-18 拍板），改成「面板一键安装 + 容器重建后自动重装」。
  三样东西各有去处：Chromium 与 pip 包都在数据卷里，重建不丢；系统库登记成 Linux 依赖，重建后由启动校验在后台按记录重装。

### 2. Signatures

- `service`（`playwright_env.go`）：
  - `PlaywrightBrowsersPathEnv = "PLAYWRIGHT_BROWSERS_PATH"`、`PlaywrightDownloadHostEnv = "PLAYWRIGHT_DOWNLOAD_HOST"`；
  - `DefaultPlaywrightDownloadHostARM64`、`DefaultPlaywrightDownloadHost() string`（v3.3.2 / #146）；
  - `DefaultPlaywrightBrowsersPath() string`、`ResolvePlaywrightBrowsersPath() string`、`ApplyPlaywrightBrowsersPathProcessEnv()`；
  - 包内：`runningInContainer()`、`applyPlaywrightBrowsersPathDefault(envMap)`、`migrateLegacyPlaywrightBrowsers(legacy, target)`、
    `nonEmptyEnvValues(map[string]string) map[string]string`。
- `service`（`playwright_runtime.go`）：
  - `DetectLinuxOSRelease() LinuxOSRelease{ID, VersionID, VersionCodename}`；
  - `PlanPlaywrightRuntime() PlaywrightRuntimePlan`（`supported` / `reason` / `distribution` / `version_id` / `arch` / `packages` / `browsers_path`）、`PlaywrightDebian12Packages()`、`PlaywrightPythonPackage = "playwright"`；
  - `PlaywrightBrowserDownloadApplies(packageName) bool`、`NewPlaywrightBrowserInstallCommand(pythonVersion) (*exec.Cmd, browsersPath string, error)`；
  - `PlaywrightDownloadStartPrefix` / `PlaywrightDownloadStartLine(path)` / `PlaywrightDownloadReadyLine`；
    v3.3.2 起 `PlaywrightDownloadStartLine` 的返回值**带上体量与耗时说明**（约 150-300MB、下载期间日志不刷新属正常），
    两处测试按**精确相等**断言它，改文案必须同步改断言；
  - `BuildPlaywrightEnvironmentHint(output)`、`BuildRuntimeFailureHint(output)`。
- `service`（`linux_packages.go`）：`LockLinuxPackageOperation() func()`、`aptLockTimeoutOption = "DPkg::Lock::Timeout=300"`。
- `handler`（/deps 组，`JWTAuth()` + `RequireAdmin()`；/deps 不在开放 API 的 scope 里，MCP 不用同步）：
  - `GET /api/v1/deps/playwright` → `PlaywrightRuntimePlan` 加 `python_installed` / `browsers_installed` / `linux_installed` / `linux_total`，
    与前端 `web/src/api/deps.ts` 的 `PlaywrightStatus` 逐字段对应；
  - `POST /api/v1/deps/playwright/install` → 201 `{ message, data: Dependency[], packages, browsers_path }`；
  - `depFollowUpStep{ build, doneLine, acquire }`、`runCmdWithSSEThen(cmd, id, successStatus, deleteOnSuccess, followUps)`；
  - `playwrightInstallMu`、`playwrightBrowserDownloadSem`（容量 1 的 channel）。

### 3. Contracts

**`PLAYWRIGHT_BROWSERS_PATH`：Go 侧是唯一真源**（`entrypoint.sh` 不另写一套公式，免得 `DATA_DIR` 与 config.yaml 的 `data.dir` 两套算法分叉）

- 默认值 `DefaultPlaywrightBrowsersPath()` **只在容器部署时**返回 `<data.dir>/deps/ms-playwright`（绝对路径），其余一律空串：
  - 容器判定 `runningInContainer()`：`/.dockerenv`、`/run/.containerenv`，兜底看 `/proc/1/cgroup` 里有没有 docker / containerd / kubepods / lxc / podman。
    **不含 Magisk 分支**：青龙兼容层把 Magisk 的 ruri chroot 也当成可以动 `/` 的环境，这里要把它排除，两边口径相反，各自在调用方判断。
  - Windows 桌面版、裸机不设：浏览器本来就在 `%LOCALAPPDATA%\ms-playwright` / `~/.cache/ms-playwright`，重建不会丢，改默认目录反而让装好的浏览器「消失」。
  - Magisk 模块版不设：`deps/` 会被快照整体 `cp -rf`、每 10 分钟同步、开机回填，几百 MB 的浏览器放进去会被反复拷贝。
- 优先级（`ResolvePlaywrightBrowsersPath`，与任务环境一致）：**环境变量页里启用的同名变量 > config.sh > 进程环境 > 默认值**。
  用户在面板里设过就算设过，**哪怕是空串**（任务里拿到的就是空串）；进程环境里的空串视同没设。非空的用户值原样尊重，包括 `0`（Node 版里表示装进 node_modules）。
- 注入点：
  - **进程级**：`main.go` 在 `appboot.InitWithConfig` 之后、`verifyInstalledDeps()` 之前调 `ApplyPlaywrightBrowsersPathProcessEnv()`：
    进程环境没设、且默认值非空时 `MkdirAll` + `os.Setenv`。系统命令行、依赖安装（pip / apt）、任务缺包时的自动安装都直接继承 `os.Environ`，只有它们靠这一步。
    必须在数据库就绪之后（要查环境变量页决定是否搬迁），并赶在启动校验排队重装之前。全程 best-effort，失败只打日志。
  - **任务级**：`BuildManagedRuntimeEnvMapWithScriptToken` 在合并完环境变量页与 config.sh 之后调 `applyPlaywrightBrowsersPathDefault`：
    **键不存在才写**（语义同 QL_DIR 那批青龙兼容变量，不像 TZ 那样强制覆盖），值取进程环境、为空再取默认值。
    任务、调试运行、run-code、ddp python / shell 的子进程都是白名单环境，只靠进程环境传不进去。
  - **订阅钩子**：`subscription_hook.go` 键不存在时写 `ResolvePlaywrightBrowsersPath()`（钩子环境不含环境变量页的值，所以取 Resolve 而不是只补默认值），取到空串就不写。
  - **浏览器下载子进程**：显式设 `PLAYWRIGHT_BROWSERS_PATH=ResolvePlaywrightBrowsersPath()`，并带上环境变量页里的 `PLAYWRIGHT_DOWNLOAD_HOST`、面板代理和可写 HOME。
    依赖安装子进程只继承 `os.Environ`，看不到环境变量页，不显式传就会下到与任务不一样的目录。

**`PLAYWRIGHT_DOWNLOAD_HOST` 的默认值按架构分流**（v3.3.2 / #146；这个变量在此之前是纯透传，全仓没有默认值）

- `DefaultPlaywrightDownloadHost()` 只在 **arm64** 返回 `DefaultPlaywrightDownloadHostARM64`（npmmirror），其余架构一律返回空串、不设默认。
  **amd64 刻意不设**：npmmirror 近期几个 revision 下只有 arm64 的包，没有 x64 的 `chromium-linux.zip`，
  强推会把「慢」变成「直接 404」，比不设更糟。面板的 Magisk / Android 部署全是 arm64，正好被这条默认值覆盖。
  想加新镜像时的验证口径：**不能只看状态码**——必须看 `content-type` 和文件头（有的站点任何路径都回 200，内容却是门户页 HTML）。
- 🔴 **顺序契约：先写面板默认值，再叠用户值；用户值必须先滤空。**
  `withEnvEntry` 是「先剔同名再追加」，两次调用的先后天然实现「用户值优先」；
  用户值要过 `nonEmptyEnvValues`，否则环境变量页里一条 enabled 但 Value 为空的同名记录会把默认镜像覆盖成空串，**镜像静默失效**。
- ⚠️ 滤空刻意**不**下沉到 `panelUserEnvValues`：`PLAYWRIGHT_BROWSERS_PATH` 那边「设成空串」是有意义的
  （表示让 Playwright 用它自己的默认目录，`PlaywrightDownloadStartLine` 专门为此写了一个分支），
  在公共函数里滤空会把那条语义一并改掉。
- **PUID 存量搬迁**：降权部署的 HOME 被 entrypoint 钉成 `<data.dir>/.home`，老浏览器在 `<data.dir>/.home/.cache/ms-playwright`。
  用户没在面板里自己设这个变量、旧目录非空、新目录不存在或为空时，`os.Rename` 过去（同一个卷，瞬时完成）；
  新目录已有内容时不合并、不覆盖；失败只打日志，不阻塞启动。

**一键安装的前置判定**（`planPlaywrightRuntime`，顺序是契约：越靠前的原因越根本，Alpine 用户该看到的是换镜像，而不是「架构不对」）

| 顺序 | 条件 | `reason` |
|---|---|---|
| 1 | 非 Linux | 一键安装只支持 Linux 上的 Debian 12 版 Docker 镜像，当前系统是 %s |
| 2 | Alpine / apk | Alpine 镜像（musl）跑不了 Playwright 官方的 Chromium（glibc 构建），请把镜像标签里的 latest 换成 debian（如 latest → debian、latest-full → debian-full） |
| 3 | 非 apt | 一键安装只支持 apt 系统（Debian 12），当前包管理器：%s |
| 4 | 架构不是 amd64 / arm64 | Playwright 的 Chromium 只有 amd64 / arm64 构建，当前架构是 %s |
| 5 | 不是 Debian 12 | 一键安装目前只内置 Debian 12（bookworm）的系统库清单，当前系统：ID=… VERSION_ID=… VERSION_CODENAME=… |
| 6 | 浏览器目录不归面板管（非容器部署 / 面具模块版） | 一键安装只在 Docker 部署下可用：%s的浏览器目录不由面板托管，请在终端执行 python3 -m playwright install --with-deps chromium |
| — | 以上都过 | `supported=true`，`packages` 为 26 个 |

- root 判定不在 plan 里（文案要按部署形态分岔，复用 `EnsureLinuxPackageManagerPrivilege`）：
  GET 把它的失败并进 `supported=false` + `reason`，前端按钮直接置灰、旁边写原因；POST 同样在建任何记录之前 400。
- **包清单**：`playwrightDebian12Packages` 是 Debian 12 bookworm 的常量，amd64 与 arm64 共用，共 26 个：
  21 个 chromium 必需库（来自 Playwright `nativeDeps.ts` 里 debian12-x64 的 chromium 组）+ 5 个字体相关
  （fonts-liberation、fonts-wqy-zenhei、fonts-noto-color-emoji、libfontconfig1、libfreetype6）。
  不装 xvfb（会拉进上百 MB，只有有头模式才需要）。Playwright 升级或镜像换代（bookworm → trixie，多数包名会变成 `*t64`）时要重新核对；
  其它发行版在 plan 里明确拒绝，不套用这份清单。

**POST `/deps/playwright/install`**

- 两道前置检查（plan、root）都排在建任何记录之前：原来 Alpine / 非 root 下会先建出一批记录再全部 failed，白白多出一串失败记录和侧栏角标。
- `playwrightInstallMu` 把「查重 → 复用 / 新建 → 置为排队」整段串行：连点两次时，第二次必须看到第一次已经排上的记录。
- 系统包（`resolvePlaywrightLinuxRecord`，按清单顺序）：queued / installing / removing 的不动（容器重建后启动校验正在重装的就属于这类，再排一次等于装两遍）；
  登记为 installed 且确实装着的跳过；登记为 installed、实际却不在的复用原记录重装；failed / cancelled 的复用原记录（不新建，免得失败记录和角标越积越多）；
  没登记过的新建为 queued。查重口径与 POST /deps 共用 `findExistingDependency`，建记录共用 `createDependencyRecord`。
- Python `playwright`（默认 Python 版本，`resolvePlaywrightPythonRecord`）：只有处理中的不动，**已安装的也重新入队**——重装会在 pip 之后接着下载浏览器，
  这正是补浏览器的途径（老版本升级上来的用户，浏览器原本在 `/root/.cache`，pip 记录是已安装，浏览器早就丢了）。
- 全部置为 queued，日志追加「[Playwright 一键安装] 已加入顺序队列（i/n）」；单个协程按「先系统库、后 Python」依次执行，
  每条开始前确认它仍是 queued，再写「开始执行（i/n）」，写法照 BatchReinstall。Python 放最后：它后面接的 Chromium 下载与启动自检要用到这些系统库。
- 201 的 `message`：「已加入安装队列，共 N 项」，有跳过时追加「，已就绪或正在处理的 M 项已跳过」。没有要入队的项时仍回 201，`data` 为 `[]`。
- 🔴 **建记录中途失败要回滚**：`collectPlaywrightInstallQueue` 记下本次新建的 id，任何一步出错就先删掉它们再返回，响应 500「登记 Playwright 依赖失败，请稍后重试」。
  新记录一出生就是 queued，而出错时不会起安装协程，留下来就是没人接手的 queued。复用的旧记录这时还没被改过，原样保留。
  删除本身失败时，只能留给下次启动的 `ReconcileDependenciesAfterRestart` 收口（见下）。

**GET `/deps/playwright` 的就绪状态**

- `python_installed`：默认 Python 版本的 playwright **登记为 installed，且 pip 里确实装着**。先查库，库里是已安装才跑 `pip show`，免得每次打开依赖页都起一个子进程。
  面板外手动 pip 装的（没登记）、正在排队 / 安装的，都返回 false。
- `browsers_installed`：`browsers_path` 是绝对路径，且下面有 `chromium` 开头的目录（`chromium-*` 或 `chromium_headless_shell-*`）。
  目录为空或不是绝对路径（例如用户设成了 `0`）时查不了，按未就绪处理。
- `linux_installed` / `linux_total`：清单里「登记为 installed 且确实装着」的个数 / 清单长度。只登记不算（重建后 dpkg 里已经没了），只装着也不算（没登记，重建就丢）。

**pip 之后链式下载 Chromium**

- `PlaywrightBrowserDownloadApplies(name)` 为真的条件：名字按 PEP 503 归一化后等于 `playwright`（`playwright==x` 这类写法也算）、
  `DefaultPlaywrightBrowsersPath() != ""`（容器）、且不是 Alpine。Windows / 裸机 / 面具版用户自己管理浏览器，
  给每个手动装 playwright 的人悄悄多下 150-300MB 不可接受；Alpine 上官方 Chromium 跑不起来，下了也白下。
- 为真时 `installDependency` 的 Python 分支给 `runCmdWithSSEThen` 追加一个 `depFollowUpStep`：pip 成功后，
  在**同一条记录、同一个 SSE 广播、同一份日志、同一个超时 / 取消 ctx** 里执行 `<托管 venv 的 python> -m playwright install chromium`。
  - 前后各一行日志：「[Playwright] 正在下载 Chromium 到 <path>」「[Playwright] 浏览器已就绪」。失败提示靠这两行判断失败发生在哪个阶段，
    所以写日志和判定引用同一组常量。
  - 下载失败 → 整条记录 failed；取消 / 超时同样覆盖第二段（整个进程组被杀）。主命令恰好在 ctx 结束的同一瞬间成功时，后续步骤不再启动，
    记「[依赖任务已超时，后续步骤未执行]」或「[依赖任务已取消，后续步骤未执行]」，否则它会脱离超时与取消的管控。
  - `followUps` 为空时，`runCmdWithSSEThen` 与 `runCmdWithSSE` 完全相同（卸载走这条）。强制卸载（删除、批量删除）**不走** `runCmdWithSSEThen`：
    记录事先就删了，用的是 `forceUninstallDependency` 自己的 `bytes.Buffer` + `WaitDelay`。主命令与后续步骤的输出接法见「场景：依赖命令的输出管道（契约 P1，v3.3.5）」。
  - 网页安装、重装、一键安装都经过 `installDependency`；**重启后的自动重装（`reinstallDependency`）不追加下载**：浏览器在数据卷里，重建不丢。
- **下载全进程串行**：step 的 `acquire` 是 `acquirePlaywrightBrowserDownloadSlot`，先试一次拿 `playwrightBrowserDownloadSem`；
  拿不到就写一行「[Playwright] 另一条记录正在下载 Chromium，排队等待……」并立刻落库，再 `select` 等槽位或 `ctx.Done()`。
  等待中被取消 / 超时立刻返回，按「后续步骤未执行」收尾。槽位在这一步命令退出之后才释放（`defer release()`），build / start 失败也会释放。
  - 为什么：all 镜像上 POST /deps 装 playwright 会按 Python 版本各建一条记录、各起一个协程，pip 装完几乎同时进入下载，下到同一个目录；
    Playwright 自己的 `<浏览器目录>/__dirlock` 抢不到时只重试约 8 分钟，慢网下其余几条全部 failed。连点几次重装也是同样的局面。
  - 不能用 `sync.Mutex`：排队中的记录点取消、到超时都停不下来。

**失败提示的顺序契约**（`buildDependencyFailureHint`）

这是一条 `switch`，**分支顺序本身就是契约**：一次故障的日志里往往同时出现好几类关键词，排错位就是误诊。
当前顺序（改动时整张表一起核对，不要只看自己那一条）：

| 次序 | 分支 | 判据 | 为什么在这个位置 |
|---|---|---|---|
| 1 | Playwright 浏览器下载失败 | 结构性事实：有下载开始行、无就绪行 | 判据最硬，其余分支都在猜关键词；详见下一条 |
| 2 | 包管理器锁冲突 | `could not get lock` / `unable to acquire the dpkg frontend lock` / `unable to lock database` / `another app is currently holding the yum lock` | 只要撞锁，本次结论一定是「稍后重试」 |
| 3 | 容器内 DNS 解析失败 | `temporary failure resolving` / `temporary failure in name resolution` / `could not resolve` / `name or service not known` | 与下一条排查方向完全相反，必须分开报：解析失败时宿主机往往一切正常 |
| 4 | 镜像源不可达 / 网络中断 | `connection timed out` / `connection refused` / `failed to fetch` | 域名能解析但连不上 |
| 5 | **apt 索引与镜像源对不上** | `e: unable to locate package` | 🔴 **必须排在第 3、4 之后**，理由见下 |
| 6 | 缺编译工具链 | `isMissingBuildToolchain` | 夹在镜像源与 Alpine 之间：前三类是更靠前的次生故障 |
| 7 | Alpine glibc 不兼容 | `isAlpineGlibcIncompatible` | 关键词（`failed to build installable wheels`、`manylinux`）太宽，放最后才不会盖掉第 6 条那个更具体的真因 |

- 🔴 **第 5 条（`e: unable to locate package` → apt 索引与镜像源对不上）必须排在 DNS 与 `Failed to fetch` 之后。**
  安装脚本用 `;` 串联 `apt-get update` 与 `apt-get install`（见 `linux_packages.go` 的 `LinuxInstallCommandSpec`），
  update 因网络失败后 install 照跑、照样报 `E: Unable to locate package`——
  也就是说一次**纯网络故障**的日志里两者会**同时出现**。排在网络分支前面，就会把「网都不通」误诊成「索引过期」，
  让用户去换源、刷索引，白折腾一圈。反过来排在后面不会漏报：索引真过期时日志里只有 `Unable to locate package`，网络分支不命中。
  文案（`buildAptStaleIndexHint`）要给两条出路：容器内 `apt-get update` 后重装；或到「依赖管理 → 镜像源设置」重新保存一次 Linux 镜像源（面板会自动作废旧索引）。
- **Playwright 浏览器下载失败的分支排在最前面，连 dpkg 锁冲突都要让它。** 它的判据是结构性事实：本次运行的日志段（最后一个 `dependencyRunStartMarker` 之后）
  里有下载开始行、没有就绪行，说明 pip 已经成功，失败只可能发生在下载阶段。其余分支都是在整段日志里猜关键词：
  pip 阶段中途重试成功时留下的 `Temporary failure in name resolution` 之类的行，会被 DNS / 镜像源分支抢走，把用户引去查一个没问题的 pip 镜像。
  反过来它不会误伤别的分支：没有下载开始行（pip 就失败了，或者根本不是 playwright）时一律不命中。
- 下载阶段里再细分一种：下载开始行之后的输出含 `lockfile` / `__dirlock` / `lock file is already being held` → 浏览器目录正被**面板管不到的**
  `playwright install` 进程占用（面板内的下载已经串行，不可能是列表里的其它记录）。与网络无关，不能引去配代理。
- 其余下载失败：讲清楚「pip 包已经装好，失败的是随后下载 Chromium 这一步（走 Playwright 官方源，与 pip 镜像无关）」，给两条出路：
  到「系统设置 → 代理设置」配代理后重装；或在「环境变量」页添加 `PLAYWRIGHT_DOWNLOAD_HOST` 指向可用的下载镜像后重装。

**任务失败提示**（`BuildPlaywrightEnvironmentHint`，经 `BuildRuntimeFailureHint` 接进 4 个调用点：`task_executor.go` 两处、`script_debug.go`、`script_run_code.go`；
先认 ESM 兼容提示，两者关键词不相交）

- 触发词共三类：缺模块（v3.3.4 / #154 新增：Python 的 `No module named 'playwright'`；Node 用 require 报 `Cannot find module 'playwright'`，
  用 ESM 的 import 报 `Cannot find package 'playwright'`，后者不含前一句）、缺浏览器、缺系统库。
  一个都不命中时连环境都不读（`playwrightHintEnvFunc` 要查库、读 os-release），任务每次失败都会调到这里。
- 🔴 **顺序契约：Alpine 分支排在最前面，先于缺浏览器、缺库**（v3.3.4 / #154）。后两段会引人去点「安装 Playwright 运行环境」，
  而一键安装在 Alpine 上一定被 `planPlaywrightRuntime` 拒绝；排在后面，Alpine 用户要多走一圈才知道得换镜像。
  进 Alpine 分支的条件：`playwrightHintEnv.Alpine`（os-release 的 `ID=alpine`），且报错提到 `playwright` 或含 `Host system is missing dependencies`
  （缺模块、缺浏览器、Playwright 自己报的缺库都满足）。不提 playwright 的通用缺库报错（`error while loading shared libraries`，以及 musl 的 `error loading shared library` / `error relocating`）
  不进，交给原逻辑（口径见下面缺库那一条：没提到 playwright 时宁可不给提示）。
- **不是 Alpine、只命中缺模块 → 返回空串**，交给任务的自动安装（开关关着时由用户自己装）。缺模块的提示只在 Alpine 上给。
- Alpine 文案（`buildPlaywrightAlpineHint`）按「语言 × 部署 × 架构」拼，每种组合都必须单行、`[提示] ` 开头、≤320 字符，
  `TestBuildPlaywrightEnvironmentHintAlpineCoversEveryVariant` 把全部组合跑一遍兜底：
  - 语言只能从报错文本认：Python（`no module named 'playwright'` / traceback / `playwright._impl`）讲「PyPI 只发 manylinux 预编译包，pip 装不上（from versions: none）」；
    Node（`cannot find module 'playwright'` / `cannot find package 'playwright'` / `npx playwright` / `node:internal`）讲「npm 包能装上，但 Playwright 下载的 Chromium 是 glibc 构建、起不来」，
    并给出 `connect` / `connectOverCDP` 连接别处浏览器这条路；认不出时只讲两边都成立的结论。
    **Python 的说法（pip、manylinux）不能出现在 Node 文案里，Node 文案也不能说成「跑不了 Playwright」。**
  - 部署决定出路：容器（`OneClick`）→ 把镜像标签里的 latest 换成 debian（`latest` → `debian`、`latest-full` → `debian-full`，`-3.10` / `-3.11` / `-all` 同理；
    写死 `:debian` 的话，`latest-full` 的用户照做会丢掉 Go 与编译链），数据卷可直接沿用，再点一键安装；
    面具（`Magisk`）→ 不提镜像标签、也不提一键安装按钮，改刷 Debian 版模块再在终端执行 `python3 -m playwright install --with-deps chromium`
    （Node 是 `npx playwright install --with-deps chromium`），并写明安卓上能否跑起 Chromium 尚未验证；其它部署 → 换 Debian / Ubuntu 等 glibc 系统。
  - 架构（`Arch`，即 `playwrightGOARCH`）不是 amd64 / arm64：Playwright 的浏览器与 Debian 版镜像都只有这两种架构，换什么都没用，所以这一段排在部署出路之前、直接收尾，哪种部署都不叫人换镜像、改刷模块或换系统。结尾按语言分三种：Python 说「这台机器上跑不了 Playwright」；Node 说「这台机器上起不了浏览器，只能让脚本用 connect / connectOverCDP 连接别处的浏览器」（它还能连别处的浏览器，所以同样不能说成「跑不了 Playwright」）；认不出语言时说「这台机器上起不了 Playwright 的浏览器」。
- 报错含 `Executable doesn't exist` 且含 `ms-playwright` → 写出当前生效的 `PLAYWRIGHT_BROWSERS_PATH`；容器部署引导去「依赖管理 → Linux」点「安装 Playwright 运行环境」，否则给命令。
- 含 `Host system is missing dependencies`、`error while loading shared libraries`，或 musl 的 `error loading shared library` / `error relocating`
  → 有 N 个 Linux 依赖在 installing / queued 时，
  提示「容器重建后正在后台自动重装 N 个系统依赖，完成后重试即可」：这段时间里该让用户等，而不是再去点一次安装。
  否则容器部署引导去点一键安装，其它部署给 `python3 -m playwright install-deps chromium`（以 root 执行）。
  `error while loading shared libraries` 是任何原生程序缺库都会报的通用错误，报错里没提到 playwright 时宁可不给提示。
  musl 的两种说法是 v3.3.4 / #154 补的，口径同上：Alpine 镜像靠 gcompat 让 musl 加载器拉起 glibc 版 Chromium，缺库时报的是 musl 措辞，glibc 不会打这两句。
- 正在重装的依赖数通过可注入的 `playwrightHintEnvFunc` 取，只在命中缺库关键词时才查库。
- 提示要短：失败摘要会截断到 320 字符。

**Linux 包操作锁与 apt**

- `service.LockLinuxPackageOperation()`（v3.3.1 从 handler 的 `linuxPackageOperationMu` 搬到 service，写法同 `LockNodePackageOperation`），
  由网页端的 `installDependency` / `uninstallDependency` / `forceUninstallDependency` 与重启重装 `reinstallDependency` 的 Linux 分支**共用**。
- 调用方要在**构造命令之前**拿锁，一直持有到命令结束：`BuildLinuxPackageCommand` 在构造时**先写镜像源、再判断 apt 索引要不要刷新**，这两步同样不能与另一条 apt 交错。
  锁不可重入：拿着它的代码路径里不能再调用会拿它的函数。
- 🔴 **顺序契约：`refreshApt` 必须在 `ensureMirror` 之后求值，并 OR 上「源是否被改写」**（v3.3.2 / issue #146 的根因）：

  ```go
  refreshApt := manager.Name == "apt" && (mirrorChanged || ShouldRefreshAptPackageLists())
  ```

  v3.3.2 之前是先算 `refreshApt` 再换源，于是「换源」这个动作**永远影响不到本次要不要 update**——
  索引还在 6 小时 TTL 内时，换完源直接拿旧源的索引装包，报 `E: Unable to locate package`。
  两步顺序一旦写反，症状是「换了源却还是装不上」，而日志里看不出任何异常。
- 为什么：重建后 apt 索引是空的（`Dockerfile.debian` 构建时删了 `/var/lib/apt/lists/*`），两边都会先跑 `apt-get update`，
  而 **`apt-get update` 拿的 lists 锁不等待**，撞上立刻失败；安装脚本用 `;` 串联，update 失败后 install 照跑，读着空索引报 Unable to locate package。
- `-o DPkg::Lock::Timeout=300` 加在 apt 的 install 与 remove 上（`LinuxInstallCommandSpec` / `LinuxRemoveCommandSpec`，网页端与重启路径都走它们）。
  它**只覆盖 dpkg 锁**，面板内部靠上面那把进程内锁串行，这个选项留着挡面板管不到的 apt（比如用户在系统命令行里手动装包）。apt 1.9.11 以下会忽略未知的 `-o`，不会报错。
- 重启重装的 Linux 分支接上了与网页安装同一个换源（`EnsureDefaultLinuxMirror`，原来传 nil，重建后一律走 deb.debian.org）；权限检查仍排在换源之前。
- **换源这一族函数统一返回 `(changed bool, err error)`**（v3.3.2 / #146）：
  `SetLinuxMirror` / `EnsureDefaultLinuxMirror` / `writeAPKMirror` / `writeAPTMirror`，
  以及 `BuildLinuxPackageCommand` 的 `ensureMirror` 形参 `func(LinuxPackageManager, string) (bool, error)`。
  `changed` 表示**磁盘上的源文件真的被改写了**，一路透出来是为了让上面那条 `refreshApt` 契约成立；
  `writeAPKMirror` 原来是无条件整文件覆写、拿不到这个信号，对齐之后调用方不用再区分包管理器。
  新增换源函数照此签名写。`backup_runtime.go` 的 `linuxDependencyEnsureMirrorFunc` 没写显式类型、由类型推断跟随，加签名时不用动它。
- apt 源改写的 security 段：URI 路径以 `-security` 结尾，或（仅 Debian）Suites 里有 `*-security` / `*/updates` 时，目标改为 `<mirror>-security`；
  Ubuntu 的 `noble-security` 就在 `/ubuntu` 下，加后缀反而指到不存在的路径。

**依赖镜像源默认值与旧默认源迁移**（v3.3.2 / #146）

- 默认源：pip、apk、debian、ubuntu **全部是腾讯云**（`DefaultPipMirror`、`defaultLinuxMirror`）。
  阿里云没有从候选清单里删掉，只是不再是默认值（阿里云 ECS 内网走 aliyun 反而最快）。
  改默认常量时前端的「(默认)」标记要一起改，见 `.trellis/spec/frontend/index.md`。
- 用户在面板里换过 apt 源之后必须调 `service.InvalidateAptPackageIndex()` 作废索引：
  索引文件名按仓库 URL 编码，`ShouldRefreshAptPackageLists` 只看 mtime 不看来源，6 小时内会判「索引还新」而跳过 update。
  选「删文件」而不是内存脏标记，是因为它**跨进程有效**（二进制部署重启后内存标记就丢了）。
  这条路没有权限闸，降权部署下会 EACCES：**只记日志不阻断**，不能让「镜像源设置成功」变成失败。
  它跳过的文件（`lock` / `*.lock` / 子目录）必须与 `ShouldRefreshAptPackageListsFromDir` 认的是同一组，
  否则会出现「删完了它还说索引是新的」。
- 🔴 **旧默认源（阿里云）的一次性迁移，只作用于「读生效值」，不作用于「写盘与显示」。**
  存量用户的 `pip.conf` / `sources.list` 里存的是上一版默认的阿里云，而阿里云不是官方源，
  原来的判定会把它当成「用户自己选的」而永远不动——光改默认常量对这批人等于没改。
  所以迁移只挂在 `EffectivePipMirror`（读生效值）与 `EnsureDefaultLinuxMirror` 上，
  **不**挂 `SetPipMirror`（存盘）和 `EffectiveLinuxMirror`（存盘前归一化 + 页面显示）——
  挂上去用户就**永远选不回阿里云**（存进去立刻被改成腾讯云），还会出现「磁盘写着阿里云、页面显示腾讯云」的读写不对称。
- 迁移必须是**一次性**的，否则用户日后主动选回阿里云又会被静默改走。
  一次性的标记是数据目录下的 `dependency-mirror-choice.saved`（`MarkDependencyMirrorChoiceSaved()`，
  在 `PUT /deps/mirrors` 成功后落），语义是「用户已经在本面板里显式保存过依赖镜像源设置」；
  落了标记之后阿里云就只是一个普通的用户选择，`legacyDefaultMirrorMigrationPending()` 整体返回 false。
  ⚠️ 这个标记**刻意不进 `system_config_registry.go`**：那张表里每一条都是用户可见的设置项（带 label / 分组、会渲染到设置页），
  塞一个 `dependency_mirror_migrated` 进去会冒出一个没人看得懂的条目；而镜像源本身就是磁盘态，标记放数据目录语义更一致。
  数据目录还没就绪（启动早期）时宁可不搬——搬了却关不掉比不搬糟得多。
- 🔴 **pip / npm 只有「留空」才回落默认加速源，官方源原样保存、原样生效**（v3.3.9 / #165）。
  以前 `normalizePipMirrorForWrite` / `EffectiveNpmMirror` 把 pypi.org、pypi.python.org、registry.npmjs.org 和留空一样换成默认源，
  用户选了官方源，存进 pip.conf / .npmrc 的却是腾讯云 / npmmirror——官方源这个选项永远选不上。`isOfficialPipMirror` / `isOfficialNpmMirror` 已删。
  **Linux 源不同，`isOfficialLinuxMirror` 必须保留**：容器出厂的 sources.list 就是官方源，`EnsureDefaultLinuxMirror` 靠「官方源 = 出厂态」做启动替换；
  照搬 pip 的口径，所有新容器都会停在 deb.debian.org。
  测试：`TestEffectivePipMirrorFallsBackToDefaultAcceleratedMirror` / `TestEffectiveNpmMirrorFallsBackToDefaultAcceleratedMirror` 的 `official preserved`、
  `TestSetPipMirrorKeepsOfficialMirror`（存盘、生效值、`PIP_INDEX_URL` 都是官方源）、`TestSetNpmMirrorKeepsOfficialMirror`。

**启动收口 queued**：`ReconcileDependenciesAfterRestart`（`main.go` 的 `verifyInstalledDeps()`）把 `queued` 与 installing / removing 一起纳入：

- 包已经装着 → installed（「[启动校验] 检测到依赖已安装，已同步状态为已安装」）；
- 否则 → failed（「[启动校验] 排队中的任务因服务重启而中断，未执行，可重新安装」），**不自动续装**：保守，也不和别的恢复逻辑抢着装。
- 为什么：排队只活在进程内存里的那个协程中，面板一重启协程就没了。剩下的 queued 以前会永远卡住：删除、重装、取消、再点一键安装都把 queued 当「正在处理」拒掉，
  侧栏角标一直亮着，缺库提示还会一直说「正在后台自动重装」。收口成 failed 后，再点一次一键安装（它复用 failed 记录）就能恢复。
- 恢复备份续装那条分支只认 installing（`shouldResumeRestoredDependency`），不受影响。

### 4. Validation & Error Matrix

| 接口 / 情形 | 结果 |
|---|---|
| POST：非 Linux / Alpine / 非 apt / 非 amd64、arm64 / 非 Debian 12 / 非容器部署 | 400，`error` 是上表对应的 `reason`，不建任何记录 |
| POST：面板进程不是 root（如 PUID 降权） | 400，`EnsureLinuxPackageManagerPrivilege` 的说明（按容器 / systemd / Magisk 给出路），不建任何记录 |
| POST：建记录中途数据库出错 | 500「登记 Playwright 依赖失败，请稍后重试」，本次新建的记录已删掉，不起安装协程 |
| POST：全部已就绪或正在处理 | 201，`data` 为 `[]`，`message` 写明跳过几项 |
| GET：以上任一不支持的情形 | 200，`supported=false` + `reason`。plan 判定不支持时 `packages` 为 `[]`；只有 root 判定失败时清单照常下发，前端仍能显示「系统库 x/y 已安装」 |
| pip 成功、Chromium 下载失败 | 记录 failed，日志末尾是下载失败提示（代理 / `PLAYWRIGHT_DOWNLOAD_HOST`） |
| 下载撞上 `__dirlock` | 记录 failed，提示是「目录被面板外的 playwright install 占用」，不提代理 |
| 下载排队中点取消 / 超时 | cancelled / failed，日志「后续步骤未执行」，不会启动下载 |
| 一键安装跑到一半面板重启 | 没轮到的 queued 在下次启动置为 failed（已装着的置为 installed） |
| 重建后网页端装包与启动校验重装同时发生 | 两条 apt 串行执行，不再出现 `Could not get lock /var/lib/apt/lists/lock` |

### 5. Good/Base/Bad Cases

- Good：Debian 12 容器（root）里点一键安装 → 26 个系统库依次装完，最后 pip 装 playwright 并把 Chromium 下到 `<data.dir>/deps/ms-playwright`；
  容器重建后系统库由启动校验在后台重装，浏览器与 pip 包原地可用，重装期间跑的任务得到「正在后台自动重装 N 个系统依赖」的提示。
- Base：Windows / 裸机 / 面具版 → 不设默认目录、不注入、不链式下载，行为与 v3.3.0 一致；用户在环境变量页设了 `PLAYWRIGHT_BROWSERS_PATH=0` → 原样生效。
- Bad：在 entrypoint.sh 里再算一遍默认目录；任务环境里无条件覆盖用户设的值；用 `sync.Mutex` 串行下载；把下载失败的提示排到 DNS 分支后面；
  重启重装的 Linux 分支不拿包锁，或拿锁晚于构造命令；启动校验不管 queued。

### 6. Tests Required

- `server/service/playwright_env_test.go`：
  `TestRunningInContainerDetectsMarkersAndCgroup`、`TestDefaultPlaywrightBrowsersPathOnlyForContainers`（非容器 / Magisk / Windows 不设）、
  `TestManagedRuntimeEnvInjectsPlaywrightBrowsersPath`（进程环境优先于默认值）、`TestManagedRuntimeEnvKeepsUserPlaywrightBrowsersPath`（envMap 已有同名键不覆盖）、
  `TestResolvePlaywrightBrowsersPathMatchesTaskPriority`、`TestSubscriptionHookEnvCarriesPlaywrightBrowsersPath`、
  `TestApplyPlaywrightBrowsersPathProcessEnv`、`TestMigrateLegacyPlaywrightBrowsers`、`TestWithEnvEntryKeepsSingleValue`。
- `server/service/playwright_runtime_test.go`：
  `TestParseLinuxOSRelease`、`TestDetectLinuxOSReleaseFallsBackToUsrLib`、`TestPlaywrightDebian12PackagesList`、`TestPlanPlaywrightRuntime`（判定顺序与各分支，含 Debian 12 裸机、面具模块版）、
  `TestPlaywrightBrowserDownloadApplies`、`TestNewPlaywrightBrowserInstallCommand`、`TestPlaywrightDownloadStartLine`、
  `TestBuildPlaywrightEnvironmentHint`、`TestBuildPlaywrightEnvironmentHintSkipsEnvLookupWhenUnrelated`、`TestCountPendingLinuxDependencies`、
  `TestBuildRuntimeFailureHintCombinesModuleAndPlaywrightHints`。
- `server/handler/deps_playwright_test.go`：
  `TestPlaywrightStatusReportsReadiness`、`TestPlaywrightStatusFoldsPrivilegeIntoSupported`、`TestInstallPlaywrightRejectsBeforeCreatingRecords`、
  `TestInstallPlaywrightQueuesLinuxThenPython`、`TestInstallPlaywrightTwiceDoesNotDuplicate`、`TestInstallPlaywrightRollsBackCreatedRecordsOnError`、
  `TestDependencyCreateStillAllowsResubmittingFailedName`、
  `TestRunCmdWithSSEThenRunsFollowUpInSameRecord`、`TestRunCmdWithSSEThenFailsRecordWhenFollowUpFails`、`TestRunCmdWithSSEThenSkipsFollowUpWhenMainFails`、
  `TestRunCmdWithSSEThenRecordsBuildError`、`TestRunCmdWithSSEThenCancelCoversFollowUp`、`TestRunCmdWithSSEWithoutFollowUpsKeepsBehavior`、
  `TestNewPlaywrightBrowserDownloadStepUsesInjectedCommand`、`TestPlaywrightBrowserDownloadRunsOneAtATime`、`TestPlaywrightBrowserDownloadWaitIsCancellable`
  （后两条去掉信号量后都会失败）、`TestBuildDependencyFailureHintPlaywrightDownload`、`TestBuildDependencyFailureHintPlaywrightDirLock`。
- 抽出 `findExistingDependency` / `createDependencyRecord` 的回归：`deps_duplicate_skip_test.go`（`TestNodeAndLinuxDependencyCreateSkipsExistingName`、
  `TestNodeDependencyCreateStillAddsDifferentName`）、`deps_regression_test.go`（`TestBatchReinstallRunsSequentially`、`TestPythonDependencyCreateInstallsAllPythonVersions` 等）。
- `server/service/linux_packages_test.go`：`TestAptCommandsWaitForDpkgLock`、`TestRestartLinuxReinstallEnsuresMirror`、`TestBuildLinuxPackageCommandChecksPrivilegeBeforeTouchingMirror`。
- apt 索引与网络的归因边界（v3.3.2 / #146）落在 `handler/deps_failure_hint_test.go` 已有的
  `TestBuildDependencyFailureHintClassifiesFailureCause` 这张表里，共 3 条：
  「只有 `Unable to locate package` → 索引过期」「同时出现 `Failed to fetch` → 按网络归类」「同时出现解析失败 → 按 DNS 归类」。
  后两条正是顺序契约的守门人——把索引分支挪到网络分支前面，它们立刻变红。
- ⚠️ 已知缺口：旧默认源的一次性迁移（`legacyDefaultMirrorMigrationPending` / `MarkDependencyMirrorChoiceSaved`）
  与 `InvalidateAptPackageIndex` **目前没有直接用例**，下面那两条 run 过滤器也扫不到它们。
  再动这两处时要顺手补上——「只搬一次」和「标记落盘后整体失效」是纯靠约定撑着的，回归了不会有任何红灯。
- `server/service/linux_mirror_test.go`：`TestRewriteAPTSourcesKeepsDebianSecuritySuffix`、`TestRewriteAPTSourcesSecurityFollowsRequestedMirror`、
  `TestRewriteAPTListLineKeepsDebianSecuritySuffix`、`TestRewriteAPTSourcesLeavesUbuntuSecurityUnderUbuntu`。
- `server/service/backup_restore_regression_test.go`：`TestReconcileDependenciesAfterRestartSettlesQueuedRecords`、`TestReinstallDependencyLinuxWaitsForSharedPackageLock`。
- `server/service/startup_wiring_test.go`：`TestMainWiresPlaywrightBrowsersPathBeforeDependencyVerification`（静态断言 `main.go` 的调用顺序）。
- 修改后至少运行：

```bash
cd server
go test ./service -run "Playwright|RunningInContainer|WithEnvEntry|LinuxOSRelease|CountPendingLinux|AptCommands|RestartLinuxReinstall|BuildLinuxPackageCommand|RewriteAPT|ReconcileDependencies|ReinstallDependency" -count=1
go test ./handler -run "Playwright|RunCmdWithSSE|BuildDependencyFailureHint|DependencyCreate|BatchReinstall" -count=1
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误一：任务环境里无条件覆盖。用户在环境变量页看到自己设的值，脚本里实际却是另一个目录。
envMap[PlaywrightBrowsersPathEnv] = DefaultPlaywrightBrowsersPath()

// 错误二：下载排队用 Mutex。排在后面的记录点取消、到超时都停不下来。
playwrightDownloadMu.Lock()
defer playwrightDownloadMu.Unlock()

// 错误三：重启重装的 Linux 分支不拿包锁（或构造完命令才拿），重建后与网页端同时 apt-get update，lists 锁不等待、直接失败。
cmd, err = buildLinuxDependencyInstallCommandFunc(dep.Name)
```

#### Correct

```go
// 用户没配才补默认值（同 QL_DIR）
if _, exists := envMap[PlaywrightBrowsersPathEnv]; !exists {
    if value := managedPlaywrightBrowsersPath(); value != "" {
        envMap[PlaywrightBrowsersPathEnv] = value
    }
}

// 容量 1 的 channel：拿不到先写排队提示，再同时等槽位与 ctx
select {
case playwrightBrowserDownloadSem <- struct{}{}:
case <-ctx.Done():
    return nil, ctx.Err()
}

// 构造命令之前拿锁，defer 持有到 CombinedOutput 结束
unlock := LockLinuxPackageOperation()
defer unlock()
cmd, err = buildLinuxDependencyInstallCommandFunc(dep.Name)
```

---

## 场景：启动依赖校验的已装包快照（契约 S1，v3.3.5 #156）

### 1. Scope / Trigger

- 触发：修改 `server/service/dependency_reconcile.go`（`ReconcileDependenciesAfterRestart`、`listPythonInstalledPackages`）、
  `dependency_state.go` 的 `DependencyInstalledForPythonVersion`、`python_package_name.go` 的 `CanonicalizePythonPackageName`，
  `runtime_exec.go` 的 `ensureManagedPythonVenvForVersion`、`ResolveManagedPipBinaryForPythonVersion` / `ResolveManagedPythonBinaryForPythonVersion`、`createManagedPythonCommand` / `createManagedPythonModuleCommand`（后四个靠下面的 I7 不再自查），
  或 `main.go` 里 `verifyInstalledDeps()` 的位置时必须看本节。
- 背景（#156）：启动校验在 HTTP 监听**之前**同步执行。以前对每条「已安装」依赖串行起子进程：装着的 Python 依赖每条 5 个、判缺的 12 个（Linux 1 个 dpkg-query，Node 0 个），
  慢 NAS 上整轮 3 分 35 秒，这段时间面板打不开；带版本号 / extras 的依赖逐条 `pip show` 永远判缺，每次重启都排进后台重装。
  改成快照后 #156 实测整轮从约 100 个子进程 / 14 秒降到 6 个 / 0.7 秒（WSL 真实 venv，10 条 Python + 2 条 Linux + 2 条 Node + 1 条排队：
  改前 93~100 个子进程、7.7~20.5 秒，改后 6 个、0.43~0.70 秒；单条判缺从 12 个子进程降到 5 个）。
  v3.3.6（#158，日志里 Python 校验 18 秒）再去掉重复的 venv 健康检查：托管 venv 健康时快照每个版本只起 1 个子进程，单条判缺降到 3 个。

### 2. Signatures

- `func ReconcileDependenciesAfterRestart()`：流程顺序不变（第一轮 installed → stale 轮 queued / installing / removing），两轮共用闭包
  `dependencyStillInstalled(dep model.Dependency, staleRound bool) bool`（第一轮传 false、stale 轮传 true，见契约里的 stale 轮例外）。
- `func listPythonInstalledPackages(pythonVersion string) (map[string]bool, error)`：键是 PEP 503 规范名；返回 nil 表示列举失败。
- 包级变量（测试替身 / 可调参数，用例里改了必须 `t.Cleanup` 还原）：

| 变量 | 默认值 | 作用 |
|---|---|---|
| `pythonInstalledPackagesFunc` | `listPythonInstalledPackages` | 列举替身，返回 nil = 列举失败、这个版本走逐条 |
| `pythonInstalledPackagesTimeout` | `60 * time.Second` | 只管 pip list 这一条命令 |
| `dependencyReconcileSlowLogThreshold` | `3 * time.Second` | 整轮超过它才打分类型耗时 |
| `pipListHiddenPackageNames` | `python` / `wsgiref` / `argparse` | pip list 写死不列（pip 的 `stdlib_pkgs`）、pip show 却查得到的名字 |
| `dependencyInstalledFunc`（既有） | `DependencyInstalledForPythonVersion` | 逐条判定 |

- 命令（v3.3.6 起最多两次尝试，参数都是 `list --format=json --disable-pip-version-check`）：
  1. 直接列：`PythonVersionSupportedByCurrentRuntime(version)` 且 `resolveManagedPipBinaryInVenv(ManagedPythonVenvDir(version))` 非空（只看文件在不在，`pip3` 优先于 `pip`，与老路里 Resolve 用的是同一个函数）时，
     `exec.Command(pipBin, args...)`，成功就返回；
  2. 没有托管 pip，或第 1 步有任何失败（起不来、非 0 退出、超时、JSON 不对）：**不打日志**，走老路 `NewPipCommandForPythonVersion(version, args)` 再列一次。
  两次都经函数内的闭包 `runPipList(cmd)`：`cmd.Env = SanitizePipEnv(os.Environ())`、stdout / stderr 分开收，经 `runNodeABICommand(cmd, pythonInstalledPackagesTimeout)` 执行
  （`SetPgid` + 超时 `KillProcessGroup` + `WaitDelay` 兜住攥着管道的孙进程）。
- 名称规范化：`CanonicalizePythonPackageName(spec)`，与 `POST /deps` 查重是同一个函数，不另写一套。

### 3. Contracts

- **懒加载、每个 Python 版本一次**：键是 `NormalizeDependencyPythonVersion(dep.PythonVersion)`（`python_version` 为空的老记录并入默认版本），第一轮与 stale 轮共用；没有 Python 依赖时一次都不跑。
- **判定**：`snapshot[CanonicalizePythonPackageName(dep.Name)]` 为真即已装。基础包名先去掉环境标记、extras、版本算符，再做 PEP 503（小写 + 折叠 `-_.`）：
  `requests==2.31.0`、`httpx[http2]`、`aiohttp>=3.9`、`name @ https://…` → `requests` / `httpx` / `aiohttp` / `name`。
  ⚠️ `@ url` 能还原靠的是 `@` 前有空白（函数在空白处截断，不认 `@`）；`name@https://…` 不带空白时还原不出主包名、按判缺处理——与 `POST /deps` 查重同口径，要改两处一起改。
  快照里没有、但规范名在 `pipListHiddenPackageNames` 里 → 退回逐条 `dependencyInstalledFunc` 问一次；其余判缺。
- 🔴 **stale 轮例外**（v3.3.5 回归复核后补）：stale 轮（queued / installing / removing）里，名字含 `= > < ~ ! [ @ ;` 任一字符（版本约束 / extras / url / marker）的 Python 记录
  **不查快照**，照旧逐条 `dependencyInstalledFunc` 精确判定（`staleRound && strings.ContainsAny(dep.Name, "=><~![@;")`），结局与改快照之前一致：
  逐条 `pip show` 认不出这类写法、判为未装，于是带 `[恢复备份]` 的 installing 续装，queued 置 failed（「排队中的任务因服务重启而中断」），其余 installing 与 removing 置 failed（「操作因服务重启而中断」）。
  - 为什么：这些记录的操作还没做完，只看主包会误判。恢复备份时 `requests==2.31.0` 装到一半重启、环境里留着旧版 `requests`，按主包判就直接记成已安装，续装不再发生、钉住的版本永远装不上；
    排队中的 `PyYAML==6.0` 一步都没执行，也会被记成已安装。复核用真 venv、真 pip 实测：第二波 stale 轮也查快照时，8 条 stale 记录里带版本号 / extras / marker 的 6 条全被误记成已安装；改后 8 条结局与 v3.3.4 完全一致。
  - 不含这些字符的裸包名：主包就是这条记录本身，查快照与逐条问结论相同，照旧查快照。**第一轮（installed）不受影响**，照旧按主包查快照（`idna==3.7` 保持已安装，v3.3.4 会判缺重装，这是有意的差别）。
- **规范名只用在启动校验**，不下沉到 `DependencyInstalledForPythonVersion`（恢复备份续装、Playwright 状态卡片的判定口径不变）。
  语义变化（要写进发布说明）：**已安装**（第一轮）里带版本号 / extras / `@ url` 的依赖改为「主包在就算已装」，重启不再强制装回钉死的版本、不再补缺的 extra；stale 轮的语义不变（见上一条）。
- **pip list 的三个坑**（缺一个都会让快照静默失效或拖住启动）：
  1. 必须带 `--disable-pip-version-check`：pip ≤ 24.0 的普通 `pip list` 会联网自检版本，venv 里的 pip 是建 venv 时带进来的，不随镜像升级；
  2. **只解析 stdout**，stderr 单独收：中断安装留下的 `~xxx` 残目录会让 pip 往 stderr 打 `Ignoring invalid distribution`，混进来 JSON 就解析失败；
  3. 超时 ≥ 60 秒、超时按进程组杀。它只管 pip list 这一条：直接列时没有健康检查；走老路时 `NewPipCommandForPythonVersion` 返回前那遍 venv 健康检查（2 个子进程）不归它管；
     设得太短，慢机器会悄悄退回逐条校验，等于没修。
  两次尝试都遵守这三条。
- **失败回退**：版本不支持、pip 坏了、超时、JSON 解析失败 → 这个版本整体退回逐条 `dependencyInstalledFunc`，语义与改动前一致；
  每个版本**最多跑两次 pip list**（直接列一次；失败再经健康检查列一次），调用方仍**每个版本只调一次 `pythonInstalledPackagesFunc`、只打一行原因**（原因取老路的错误），不按依赖重试。
  不区分「直接列超时」与「直接列失败」：极慢环境里最多多等一个超时窗口，那种环境退回逐条校验本来就慢得多。
- **venv 损坏时照样能发现并重建**（快路径不能丢掉这条，v3.3.6）：直接列失败 → 老路 `NewPipCommandForPythonVersion` → `ResolveManagedPipBinaryForPythonVersion` → `EnsureManagedPythonVenvForVersion` 做健康检查，
  坏了先修 pip，修不好挪成 `<venv>.broken-<时间>` 再重建（重建时 Ensure 自己打 `managed python … venv created …` 一类日志，这里不另打）→ 在新 venv 上再列一次。
  快路径只要求「pip 跑得起来」、不核对 venv 里 python 的小版本：小版本对不上时 pip 装在带版本号的 `lib/pythonX.Y/site-packages` 里会导入失败，直接列失败，照样走到这一步。
- 🔴 **I7：`ensureManagedPythonVenvForVersion` 每条返回 true 的路径，结尾都是一次通过的 `managedPythonVenvHealthyForVersion`**（v3.3.6，`runtime_exec.go` 函数头有同义注释）：
  首查通过、加锁后复查通过、修好 pip 后查过、新建后查过、新建并修好 pip 后查过，五条都是；返回 false 的是版本不支持、没有数据目录、`syncCreate=false`（只起后台协程去建）、建不出也修不好。
  - 两个 `ResolveManaged{Pip,Python}BinaryForPythonVersion` 靠它不再自己查第二遍（每查一遍 2 个子进程：`python -c` 核对版本 + `pip --version`）：Ensure 为 true 时直接返回 `resolveManaged{Pip,Python}BinaryInVenv(ManagedPythonVenvDir(v))`，为 false 时返回空串，由调用方走各自原有的回退。
  - 两个 `createManagedPython*` 同理：Ensure 为 true 时直接用 `resolveManagedPythonBinaryInVenv(...)`（健康检查核对版本用的就是这个文件），不再进候选循环、不再起那次 `managedPythonBinaryMatchesVersion`；
    为 false 或取不到时，原候选循环（托管 venv、Windows 常见安装目录、PATH，逐个核对版本）一字不改。每次起 Python 任务的辅助子进程因此 4 → 3 个。
  - 以后给 Ensure 加缓存、「刚查过就直接返回 true」之类的捷径，**必须同时改这四处**，否则会把坏掉的 venv 里的 pip / python 交出去。
- **Node.js / Linux** 照旧逐条 `dependencyInstalledFunc`（只 stat 目录 / 起一个 dpkg-query 或 apk，本来就便宜）。
- **两类 `[启动校验]` 日志行**（标准库 `log`，进面板日志）：
  - 列举失败，每个版本一行：`[启动校验] 列举 Python <版本> 已安装的包失败，该版本依赖改为逐条校验：<原因>`。
    原因：`NewPipCommandForPythonVersion` 的错误原样（版本不支持、venv 坏了）；pip 非 0 退出或超时是 `pip list 执行失败：<err> <stderr 最后一行>`
    （超时的 err 是「执行超过 1m0s 仍未结束，已终止」）；解析失败是 `pip list 的输出不是预期的 JSON：<err>`。
  - 整轮慢日志，只在 ≥ `dependencyReconcileSlowLogThreshold`（3 秒）时打一行：`[启动校验] 校验 N 条已安装依赖耗时 X（Python a / Node.js b / Linux c）`。
    N 是两轮实际判定过的记录数（stale 轮通常为 0）；X 是整轮总耗时（含写库）；a / b / c 只算判定本身（Python 含建快照那次 pip list），都按毫秒取整。没超阈值不打，正常启动不刷日志。
- **`DependencyInstalledForPythonVersion` 去重**：`ResolveManagedPipBinaryForPythonVersion` 拿到托管 pip 就只跑它一次 `pip show`；
  拿不到（版本不支持、venv 坏了）才按原顺序试 venv 里残留的 `bin/pip`、`bin/pip3`、`Scripts/pip.exe` 与系统 pip。语义零变化，判缺 12 → 5 个子进程
  （v3.3.6 起 → 3 个：解析托管 pip 时一遍健康检查的 2 个 + 1 次 `pip show`），恢复备份、Playwright 状态卡片跟着变快。

### 4. Validation & Error Matrix

| 情形 | 结果 |
|---|---|
| 快照可用，主包在 | 已装，不再起子进程（stale 轮带版本约束 / extras / url / marker 的除外，见下两行） |
| 快照可用，主包不在 | 判缺，走原有重装（第一轮）/ 收口（stale 轮）逻辑 |
| stale 轮，名字含 `= > < ~ ! [ @ ;` | 不查快照，逐条 `dependencyInstalledFunc`：带 `[恢复备份]` 的 installing 续装，queued 置 failed，其余 installing / removing 置 failed（与改快照之前一致） |
| stale 轮，裸包名 | 查快照，主包在 → 同步成已安装 |
| 快照里没有 `python` / `wsgiref` / `argparse` | 逐条 `pip show` 问一次 |
| 托管 pip 在、健康 | 直接 `pip list`，整个版本只起这 1 个子进程，不做健康检查 |
| 托管 pip 在、但跑不起来（venv 坏了、换镜像后 Python 小版本对不上、pip 只剩半截、输出不是 JSON） | 直接列失败不打日志，走老路：健康检查发现坏了就修 / 重建 venv 后再列；重建成功则快照照常生效、日志里没有「列举失败」 |
| 镜像不支持该 Python 版本 / venv 坏了且修不好、重建不出来 | 该版本逐条校验 + 一行原因日志 |
| pip list 超过 60 秒 | 整个进程组被杀，走老路再列一次（最坏多等一个 60 秒窗口）；还不行就该版本逐条校验 + 一行原因日志 |
| stdout 不是 JSON | 该版本逐条校验 + 一行原因日志 |
| stderr 有 `Ignoring invalid distribution ~xxx` | 不影响，快照照常生效 |
| venv 里是 pip ≤ 24.0、开机网络没通 | 不联网，快照照常生效（靠 `--disable-pip-version-check`） |
| 整轮 ≥ 3 秒 | 打一行分类型耗时 |

### 5. Good/Base/Bad Cases

- Good：25 条 Python + 26 条 Linux + 10 条 Node 的面板重启时，每个 Python 版本只起一次 pip list（托管 venv 健康时整个版本只起这 1 个子进程）；`requests==2.31.0` 判已装，不再每次重装。
- Base：只有 Node / Linux 依赖 → 一次 pip list 都不跑，行为与改动前一致。
- Bad：合并 stdout / stderr；不带 `--disable-pip-version-check`；超时只给几秒；列举失败时每条依赖都重试一次 pip list；
  把规范名判定塞进 `DependencyInstalledForPythonVersion`（恢复备份跟着改语义）；用例建了 Python 记录却不给 `pythonInstalledPackagesFunc` 打桩。
- Bad：stale 轮也一律按主包查快照（v3.3.5 第二波的写法）—— 恢复备份装到一半的 `requests==2.31.0` 被记成已安装、不再续装，排队中一步没执行的 `PyYAML==6.0` 也被记成已安装。
- Bad：直接列失败就返回、不回退老路 —— 坏掉的 venv 不会被健康检查发现、也不会重建，这个版本白白退回逐条校验。
- Bad：给 `ensureManagedPythonVenvForVersion` 加「刚查过就返回 true」的捷径，却不改两个 Resolve 与两个 `createManagedPython*`（违反 I7）—— 坏 venv 里的 pip / python 被直接交出去。

### 6. Tests Required

- `server/service/dependency_reconcile_snapshot_test.go`（`stubReconcileFuncs` 一次换掉四个包级替身）：
  - `TestReconcileDependenciesAfterRestartJudgesPythonDepsBySnapshot`：每个版本整轮只列举一次（两轮共用）；第一轮带版本号 / extras / `@ url` 按主包判已装；
    `python_version` 为空的老记录并入 3.12；逐条判定的只有 Node / Linux 与 stale 轮那条排队中的 `PyYAML==6.0`（逐条判未装 → 置失败，不记成已安装）。
  - `TestReconcileDependenciesAfterRestartJudgesStaleSpecifiersExactly`（stale 轮例外）：逐条判定的恰好是 stale 轮带版本约束 / extras 的 5 条，裸包名与第一轮的 `idna==3.7` 不逐条问；
    带 `[恢复备份]` 的 installing（`requests==2.31.0`、`httpx[http2]`）续装并保持安装中；queued 的 `PyYAML==6.0` 置失败；installing 的 `aiohttp>=3.9`、removing 的 `urllib3<2` 置失败；
    queued 的裸包名 `certifi` 查快照同步成已安装；第一轮的 `idna==3.7` 保持已安装、不排重装。突变：去掉 stale 轮的字符判断，这条与上一条都红。
  - `TestReconcileDependenciesAfterRestartFallsBackPerVersionWhenSnapshotFails`：只有列举失败的版本逐条判定；该版本**恰好一行**原因日志且带原因；成功的版本不打。
  - `TestReconcileDependenciesAfterRestartSkipsSnapshotWithoutPythonDeps`、`TestReconcileDependenciesAfterRestartAsksPipShowForNamesPipListHides`、
    `TestReconcileDependenciesAfterRestartLogsSlowRoundByType`（默认阈值下不打；阈值调成 0 时断言「校验 3 条已安装依赖耗时」与「（Python … / Node.js … / Linux …」）。
  - `TestReconcileDependenciesAfterRestartListsPythonPackagesOnceWithManagedPip`：冒充的托管 venv 端到端，4 条 Python 依赖**恰好 1 个子进程**，就是 `pip3 list --format=json --disable-pip-version-check`（v3.3.6 起不做健康检查、不出现 `pip show`）。
  - `TestReconcileDependenciesAfterRestartRebuildsBrokenManagedVenv`（v3.3.6）：托管 venv 的 `pip` / `pip3` 不管什么子命令都报 `ModuleNotFoundError: No module named 'pip'` 并退 1；
    PATH **前置**一个假的 `python3.12`（`--version` 报 3.12.13，`-m venv DIR` 时 `mkdir -p` + `cp -R` 拷入健康模板，模板的 `pip3 list` 输出 `[]`、日志带 `new-` 前缀；要拷文件，所以不能用 `isolatePythonProbePath` 整体替换 PATH）：
    第一个子进程是旧 venv 的 `pip3 list …`（失败）、中间有 `python3.12 -m venv <venv>`、最后一个是 `new-pip3 list …`；`<venv>.broken-*` 恰好一个；两条依赖都排进重装；日志里没有「列举 Python 3.12 已安装的包失败」。
    突变：直接列失败后不回退 → 这条红（逐条判定那条路照样会重建，但最后一个子进程变成 `pip3 show`，还会打出回退日志）。
  - `TestListPythonInstalledPackagesReadsOnlyStdout`（冒充的 pip 往 stderr 打告警，且不带 `--disable-pip-version-check` 就报错）、`TestListPythonInstalledPackagesRejectsNonJSONOutput`（两次尝试都拿到非 JSON，错误里仍含「JSON」）、
    `TestListPythonInstalledPackagesTimesOutAndKillsProcessGroup`（超时调成 1 秒，孙进程攥着 stdout 时 6 秒内返回；v3.3.6 起直接列、走老路各超时一次，WSL 实测约 2.2–2.4 秒，不整组杀的话每次多等 10 秒 WaitDelay，照样红）。
- `server/service/dependency_state_test.go`：
  - `TestDependencyInstalledForPythonVersionAsksManagedPipOnce`：装着的、判缺的都**恰好 3 个子进程**：`python -c …`、`pip3 --version`、`pip3 show NAME`。
  - `TestResolveManagedBinariesCheckVenvHealthOnce`（v3.3.6）：健康的假 venv 上 `ResolveManagedPipBinaryForPythonVersion("3.12")` 得到 `<venv>/bin/pip3`、`ResolveManagedPythonBinaryForPythonVersion("3.12")` 得到 `<venv>/bin/python`，
    各恰好 2 个子进程：第一个以 `python -c ` 开头，第二个是 `pip3 --version`。
  - `TestCreateManagedPythonCommandsReuseEnsureVersionCheck`（v3.3.6）：先 `isolatePythonProbePath`；`createManagedPythonCommand` 与 `createManagedPythonModuleCommand`（`python3 -m pip`）的 `cmd.Path` 都是 `<venv>/bin/python`，
    各 2 个子进程、`python -c` 只出现一次；cleanup 照常调用。
  - 冒充的 venv（`writeFakeManagedVenv`）是 sh 脚本，Windows 上 `t.Skip`：要交叉编译到 Linux / WSL 跑才算数。
- 🔴 **任何会走到 `ReconcileDependenciesAfterRestart` 且建了 Python 记录的用例都必须给 `pythonInstalledPackagesFunc` 打桩**
  （既有的如 `backup_restore_regression_test.go` 的 `TestReconcileDependenciesAfterRestartSettlesQueuedRecords`），否则 CI 会真去建 venv、跑 pip。
- 修改后至少运行：

```bash
cd server
go test ./service -run "ReconcileDependencies|ListPythonInstalledPackages|DependencyInstalled|ResolveManagedBinaries|CreateManagedPythonCommands" -count=1
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误一：合并 stdout / stderr —— 有 ~xxx 残目录时 stderr 的告警混进 JSON，快照永远解析失败、静默退回逐条
out, err := exec.Command(pip, "list", "--format=json", "--disable-pip-version-check").CombinedOutput()

// 错误二：不带 --disable-pip-version-check —— pip ≤ 24.0 每次都联网自检，开机网络没通时一直拖到超时
cmd, err := NewPipCommandForPythonVersion(version, []string{"list", "--format=json"})

// 错误三：按原名查表 —— requests==2.31.0、httpx[http2] 永远判缺，每次重启都重装
installed := snapshot[strings.ToLower(dep.Name)]

// 错误四：stale 轮也按主包查快照 —— 恢复备份装到一半的 requests==2.31.0、排队中的 PyYAML==6.0 都被记成已安装，续装不再发生
dependencyStillInstalled := func(dep model.Dependency) bool {
    return snapshot[CanonicalizePythonPackageName(dep.Name)]
}

// 错误五：直接列失败就返回、不回退老路 —— 坏掉的 venv（换镜像后 Python 小版本变了）不会被健康检查发现，也不会重建
if pipBin := resolveManagedPipBinaryInVenv(ManagedPythonVenvDir(version)); pipBin != "" {
    return runPipList(exec.Command(pipBin, args...))
}
```

#### Correct

```go
args := []string{"list", "--format=json", "--disable-pip-version-check"}
runPipList := func(cmd *exec.Cmd) (map[string]bool, error) { // 直接列与老路共用，两次都遵守三个坑
    cmd.Env = SanitizePipEnv(os.Environ())
    var stdout, stderr bytes.Buffer
    cmd.Stdout, cmd.Stderr = &stdout, &stderr // 只解析 stdout
    if err := runNodeABICommand(cmd, pythonInstalledPackagesTimeout); err != nil { // 超时按进程组杀
        return nil, fmt.Errorf("pip list 执行失败：%v %s", err, lastLine) // lastLine：stderr 最后一行
    }
    // json.Unmarshal(stdout.Bytes(), &rows)，键一律过 CanonicalizePythonPackageName
}
// 先直接用托管 venv 里的 pip 列：健康时整个版本只起这 1 个子进程；失败不打日志
if PythonVersionSupportedByCurrentRuntime(version) {
    if pipBin := resolveManagedPipBinaryInVenv(ManagedPythonVenvDir(version)); pipBin != "" {
        if installed, err := runPipList(exec.Command(pipBin, args...)); err == nil {
            return installed, nil
        }
    }
}
// 老路：健康检查，坏了就修 / 重建 venv，再列一次（「venv 损坏时发现并重建」靠这一步）
cmd, err := NewPipCommandForPythonVersion(version, args)
if err != nil {
    return nil, err
}
return runPipList(cmd)

// dependencyStillInstalled(dep, staleRound) 里，查快照之前先放过 stale 轮的例外：
// 带版本约束 / extras / url / marker 的记录逐条精确判定（恢复备份续装、排队中置失败与改前一致）
if staleRound && strings.ContainsAny(dep.Name, "=><~![@;") {
    return dependencyInstalledFunc(dep.Type, dep.Name, dep.PythonVersion)
}
installed := snapshot[CanonicalizePythonPackageName(dep.Name)] // 基础包名 + PEP 503
```

---

## 场景：Python 依赖的 extras 与原地升级（v3.3.9，#166）

### 1. Scope / Trigger

- 用户报：脚本提示 `pip install httpx[http2]`，自动安装只装了 `httpx`，h2 一直缺；去依赖管理手动装 `httpx[http2]` 又被「已存在跳过」。
- 三处都栽在同一件事上：**查重键剥掉了 extras，但装包和登记必须带着 extras**。

### 2. Signatures

- `service.PythonExtrasCovered(existingSpec, requestedSpec string) bool`：旧写法的 extras 集合 ⊇ 新写法的（小写 + PEP 503 分隔符折叠；只看 `;` 之前第一对方括号，没闭合按「没有 extras」）。
- `service.MergePythonSpecExtras(existingSpec, requestedSpec string) string`：只在 `PythonExtrasCovered == false` 时用。
- `autoInstallPyHintRe`：`pip3?\s+install\s+["']?([a-zA-Z][a-zA-Z0-9_.@-]*(?:\[[a-zA-Z0-9_.,\s-]*\])?)`。

### 3. Contracts

- 查重键不变：`CanonicalizePythonPackageName`（剥 marker / extras / 版本后 PEP 503），**一包一条**，启动期 `MergeDuplicatePythonDependencies` 也不变。
- `POST /deps`（`handler.Create`）命中同名记录时：
  - 命中 **installed** 且 extras 没被覆盖 → **原地升级**：同一条记录改名为 `MergePythonSpecExtras(旧, 新)`、状态置 installing、清空日志、计入 `data`，`dependencyInstallRunner` 装的也是合并后的写法；
  - 命中 installing / queued，或 extras 已覆盖 → 照旧 `skipped++`。
- 合并规则：基础名取新写法；extras = 旧的（原顺序原拼写）+ 新写法里旧的没有的；extras 之后那段（`==0.27.0`、`>=1,<2`、`@ url`、`; marker`）新写法有就用新的，没有就沿用旧的。
  **为什么合并**：记录名就是重建容器后自动重装的写法，整串换成新写法会丢掉旧的版本锁定和已有 extras。
- 自动安装：提示正则连 extras 一起捕获；`RecordAutoInstalledDepForPythonVersion` 命中已有记录且 extras 没覆盖时，登记名同样改成合并写法（pip 实际执行的仍是候选写法）。
- 新建依赖时切名字（网页 `parseNames`、APP `splitDependencyNames`）：**换行无条件切**，行内逗号 / 空白落在方括号里不切。

### 4. Validation & Error Matrix

| 已有记录 | 本次提交 | 结果 |
|---|---|---|
| `httpx`（installed） | `httpx[http2]` | 原地升级为 `httpx[http2]`，重装 |
| `httpx==0.27.0`（installed） | `httpx[http2]` | 原地升级为 `httpx[http2]==0.27.0` |
| `httpx[socks]`（installed） | `httpx[http2]` | 原地升级为 `httpx[socks,http2]` |
| `httpx[http2]`（installed） | `httpx` / `httpx[HTTP_2]` | 跳过（已覆盖） |
| `httpx`（installing / queued） | `httpx[http2]` | 跳过（不抢正在装的记录） |
| `httpx`（failed） | `httpx[http2]` | 不命中（查重只认 installed / installing / queued），新建一条 |

### 5. Good/Base/Bad Cases

- Good：已装 `httpx==0.27.0`，脚本报缺 h2 → 自动安装 `httpx[http2]` → 登记名变 `httpx[http2]==0.27.0`，重建容器后重装仍带 h2、仍锁 0.27.0。
- Base：`requests` 再提交 `Requests` → 照旧按 PEP 503 判重跳过。
- Bad：为了让 extras 能装上，把 extras 加进查重键 —— 同一个包会出现两条记录，启动期合并又按基础名删掉其中一条，删哪条看更新时间。

### 6. Tests Required

- `service/python_package_name_test.go`：`TestPythonExtrasCovered`、`TestMergePythonSpecExtras`（上表的合并例子 + 方括号没闭合 + 空白）。
- `service/dependency_auto_install_test.go`：`TestDetectAutoInstallCandidate` 的 `python hint keeps extras from httpx h2 error`（httpx 原文报错）、`python hint keeps quoted extras`。
- `service`：`TestRecordAutoInstalledDepRenamesWhenExtrasNotCovered`（断言合并名 `httpx[http2]==0.27.0`）、`TestRecordAutoInstalledDepKeepsNameWhenExtrasCovered`。
- `handler/deps_duplicate_skip_test.go`：`TestPythonDependencyCreateUpgradesInstalledRecordForNewExtras`（记录名与 runner 收到的都是合并名）、
  `TestPythonDependencyCreateSkipsInstallingRecordEvenWithNewExtras`、`TestPythonDependencyCreateSkipsInstalledRecordWhenExtrasCovered`。

### 7. Wrong vs Correct

```go
// Wrong：查重命中就跳过 —— httpx[http2] 永远装不上
if _, exists := findExistingDependency(req.Type, name, pythonVersion, dependencyActiveStatuses...); exists {
    skipped++
    continue
}

// Wrong：原地升级但整串换名 —— httpx==0.27.0 的锁定在重建容器后丢失
updates["name"] = name

// Correct：只在 installed 且 extras 没覆盖时原地升级，名字用合并写法，循环里的 name 不覆盖（下一个 Python 版本还要用原始写法查重）
if req.Type == model.DepTypePython && existing.Status == model.DepStatusInstalled && !service.PythonExtrasCovered(existing.Name, name) {
    upgradedName := service.MergePythonSpecExtras(existing.Name, name)
    // 更新 name / status=installing / log="" 后 go dependencyInstallRunner(existing.ID, req.Type, upgradedName)
}
```

---

## 场景：依赖命令的输出管道（契约 P1，v3.3.5）

### 1. Scope / Trigger

- 触发：修改 `server/handler/deps.go` 的 `runCmdWithSSEThen`（含内部的 `waitCommand` 与后续步骤 `depFollowUpStep` 的启动），
  或新写「起子进程、边读输出边广播 SSE、再等它退出」的代码时必须看本节。
- 走这里的：`installDependency`（网页安装、重装、批量重装、Playwright 一键安装）与 `uninstallDependency`（卸载）。
  强制卸载（删除、批量删除）**不走**这里，用的是 `forceUninstallDependency` 自己的 `bytes.Buffer` + `WaitDelay`。
- 背景：v1.8.0 起读协程与 `Wait` 并发读 `cmd.StdoutPipe()`，`Wait` 看到进程退出就关读端：秒退的命令偶尔整段输出只剩一行
  「[读取安装输出失败] read |0: file already closed」，输出多的丢末尾一截；单行超过 256KB 时读协程退出、没人读管道，命令写满就卡到 20 分钟超时。
  v3.3.4 发版时 CI 与 Release 的第一次尝试都挂在 `TestRunCmdWithSSEThenRunsFollowUpInSameRecord` 上。

### 2. Signatures

- `func runCmdWithSSEThen(cmd *exec.Cmd, id uint, successStatus string, deleteOnSuccess bool, followUps []depFollowUpStep)`；`runCmdWithSSE` = 它 + `followUps=nil`。
- `var dependencyOutputWaitDelay = 10 * time.Second`：与强制卸载、Node ABI 重建取同一个值，抽成变量只为测试能调短。
- 内部：`waitCommand(running *exec.Cmd, output *io.PipeReader, outputWriter *io.PipeWriter) (error, string)`，主命令与每个后续步骤共用；返回退出错误与被 ctx 打断时的终态。

### 3. Contracts

1. **输出接 `io.Pipe` 的写端**：`pipeReader, pipeWriter := io.Pipe(); cmd.Stdout = pipeWriter; cmd.Stderr = pipeWriter`，后续步骤同一套接法。
   不是 `*os.File` 的 Writer 会让 exec 起一个内部拷贝协程，`Wait` 要等它把输出全部交给读端才返回；stdout / stderr 给同一个写端，exec 只建一根 OS 管道，两路输出照旧按到达顺序混排。
2. **`cmd.WaitDelay = dependencyOutputWaitDelay`，必须在 `Start` 之前设**：命令拉起的后台进程（守护进程、postinst 起的服务、`xxx &`）会继承这根管道，
   不设上限时这条依赖一直停在「安装中」，还攥着 apt / npm 的包操作锁。
3. **`Wait` 返回之后才关写端**：Wait 协程写成 `err := running.Wait(); outputWriter.Close(); …; waitCh <- err`，读协程这才读到 EOF 结束。
4. 🔴 **读协程任何退出路径都必须把管道读到 EOF**：开头 `defer close(scanDone)` 之后紧跟 `defer func() { _, _ = io.Copy(io.Discard, output) }()`（defer 后进先出：先读空、再关 `scanDone`）。
   io.Pipe 的 Write 要等读端读走才返回：读协程一停读，exec 的拷贝协程就永久卡在 Write 上，`Wait` 永不返回——卡住的是面板进程自己，
   杀进程组、超时、WaitDelay 都救不回来，还一直攥着包锁。目前唯一的提前退出是单行超过 256KB（`bufio.Scanner` 上限）：
   记一行「[读取安装输出失败] bufio.Scanner: token too long」，之后的输出读空丢弃、不进日志；将来在读循环里加 `return`（比如给日志限长）也由这个 defer 兜住。
   **不能**只在 `scanner.Err()` 分支里排空。
5. **`exec.ErrWaitDelay` 归一成成功**：只有命令本体以 0 退出、但输出管道到点还没关时 `Wait` 才报它（意味着命令成功、只是后台进程还攥着管道）。
   `err = nil`、记 `backgroundHeld`，读完输出后追加一行「[命令已结束；仍有它拉起的后台进程持有输出管道，其后续输出不再收集]」（口径同系统命令行的 `resolveConsoleRunOutcome`）。
6. **ctx 分支（超时 / 取消）**：`KillProcessGroup` → 等 `waitCh` → **先 `<-scanDone` 等读协程写完剩余输出**，
   再写「[依赖任务已超时，进程已终止]」/「[依赖任务已取消]」/「[依赖任务在超时/取消触发的同时已正常结束，按成功处理]」，收尾行不会插进输出中间。
   结果记 cancelled / failed、「同一瞬间正常结束按成功」、ctx 结束后不再启动后续步骤、包锁与下载槽位的释放时机都不变。
7. 🔴 **禁止 `cmd.StdoutPipe()` 与 `Wait` 并发**。Go 文档（`os/exec` 的 `StdoutPipe`）原文：
   「Wait will close the pipe after seeing the command exit, so most callers need not close the pipe themselves. It is thus incorrect to call Wait before all reads from the pipe have completed.」
- 代价（已接受，写进 PR 描述）：后台进程攥着管道，或取消 / 超时时有逃出进程组的孙进程（Linux 的 `setsid`、Windows 上 `npm.cmd` 包着的 node.exe），
  最多多等 10 秒才落终态；这 10 秒里包锁与 Chromium 下载槽位仍攥着。

### 4. Validation & Error Matrix

| 情形 | 结果 |
|---|---|
| 命令写一大段输出后立刻 0 退出 | 一行不少，记 successStatus，没有「读取安装输出失败」 |
| 命令 0 退出，后台进程攥着管道 | WaitDelay 到点收尾，记 successStatus，追加「仍有它拉起的后台进程持有输出管道」 |
| 单行 > 256KB，之后还有大量输出 | 立即结束，按退出码记，日志有「[读取安装输出失败] bufio.Scanner: token too long」，之后的输出丢弃 |
| 取消 / 超时，有逃出进程组的孙进程 | 杀组后最多再等 WaitDelay，记 cancelled / failed，收尾行在全部输出之后 |
| 命令非 0 退出 | 照旧 failed + `buildDependencyFailureHint`（`ErrWaitDelay` 只在 0 退出时出现，不会把失败洗成成功） |

### 5. Good/Base/Bad Cases

- Good：pip 秒退时日志完整；`apt install` 拉起的服务攥着管道时 10 秒后按成功收尾并写明原因。
- Base：普通 pip / npm / apk 安装，行为与改动前一致，只是不再随机丢尾巴、不再冒出误报的「读取安装输出失败」。
- Bad：回退成 `StdoutPipe`；只在 `scanner.Err()` 分支里排空；`Wait` 返回之前就关写端；不设 `WaitDelay`；ctx 分支不等读协程就写收尾行。

### 6. Tests Required

- `server/handler/deps_output_pipe_test.go`（把测试二进制自己当子进程 `TestDependencyOutputHelperProcess`，不依赖 sh / cmd 的行为）：
  - `TestRunCmdWithSSEKeepsTailOfFastExitingCommand`：30000 行（约 470KB）后立刻 `os.Exit(0)`，连跑 5 轮；每轮 installed、没有读取失败行、30000 行齐全且最后一行 `dep-line-029999` 在。
  - `TestRunCmdWithSSEDoesNotWaitForBackgroundProcessHoldingOutput`：`dependencyOutputWaitDelay` 调到 300ms，`echo main-ok; sleep 20 & echo bg-started`（Windows 用 `start /b ping`）；
    10 秒内收尾、installed、有说明行、没有读取失败行。
  - `TestRunCmdWithSSEDrainsOutputAfterOverlongLine`：先一行 300KB 再 30000 行；20 秒内结束、installed、有「bufio.Scanner: token too long」。
- 突变验证（改这段代码后要复跑）：去掉 `WaitDelay` → 第 2 条红；去掉排空 → 第 3 条红（永久卡死）；回退到 `StdoutPipe` → Linux 上三条都红。
- 既有用例修后变成确定性的：`deps_playwright_test.go` 的 `TestRunCmdWithSSEThenRunsFollowUpInSameRecord`、`TestRunCmdWithSSEWithoutFollowUpsKeepsBehavior`
  （修前 WSL 300 次分别红 10~33 次、13 次，修后 0 次）。
- 竞态只在 Linux 复现，Windows 上过不代表修好了：交叉编译测试二进制到 WSL 跑（`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./handler`）。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误一：StdoutPipe + 与读协程并发的 Wait —— Wait 看到进程退出就关读端，秒退的命令丢尾巴
pipe, _ := cmd.StdoutPipe()
cmd.Stderr = cmd.Stdout
go scan(pipe)
waitCh <- cmd.Wait()

// 错误二：只在读出错时排空 —— 将来读循环里多一个 return，exec 的拷贝协程就永久卡在 io.Pipe 的 Write 上
for scanner.Scan() { appendLine(scanner.Text(), true) }
if err := scanner.Err(); err != nil {
    appendLine("[读取安装输出失败] "+err.Error(), true)
    _, _ = io.Copy(io.Discard, output)
}
```

#### Correct

```go
pipeReader, pipeWriter := io.Pipe()
cmd.Stdout, cmd.Stderr = pipeWriter, pipeWriter
cmd.WaitDelay = dependencyOutputWaitDelay // Start 之前设

// 以下在 waitCommand(running, output, outputWriter) 里：output / outputWriter 就是上面那根 io.Pipe 的两端
go func() {
    defer close(scanDone)
    defer func() { _, _ = io.Copy(io.Discard, output) }() // 任何退出路径都先读到 EOF
    scanner := bufio.NewScanner(output)
    scanner.Buffer(make([]byte, 64*1024), 256*1024)
    for scanner.Scan() { appendLine(scanner.Text(), true) }
    if err := scanner.Err(); err != nil { appendLine("[读取安装输出失败] "+err.Error(), true) }
}()
go func() {
    err := running.Wait()
    outputWriter.Close() // Wait 返回之后才关写端
    if errors.Is(err, exec.ErrWaitDelay) { backgroundHeld = true; err = nil }
    waitCh <- err
}()
```

---

## 场景：停机收尾（v3.3.5，#156 顺带）

### 1. Scope / Trigger

- 触发：改下面任何一处都必须看本节。
  - `server/main.go`：`main` 里的信号接管、`SetPanelExitRequester`、`http.Server` 构造、启动清理的位置；`shutdownPanel` 本身。
  - `service/scheduler_manager.go` 的 `HaltSchedulerV2` / `ShutdownSchedulerV2`、`scheduler_v2.go` 的 `SignalStop`、`backup_schedule.go` / `subscription_scheduler.go` 的关停、`database.Close`。
  - `handler/system.go` 的 `panelProcessExit` / `SetPanelExitRequester`，或新增「面板自己退出」的入口。
  - 三条 SSE（`log.go` 的 `Stream`、`deps.go` 的 `LogStream`、`subscription.go` 的 `PullStream`），或新增长连接。
  - `docker/entrypoint.sh` 的 trap / `shutdown()` / `log()`；`service/task_temp_cleanup.go`，或任务临时文件创建处的前缀与形状。
  - 任何调用 `GetSchedulerV2()` / `GetTaskExecutor()` 的地方，或给 `*SchedulerV2` 新增方法：关停第 3 步之后它们是 nil，见下文「关停后的全局调度器」。
  - 被关停打断的那次执行怎么结算，见「执行器的「执行窗口」与 per-run 停止」；任务进程组怎么终止（先 TERM、共享 2 秒宽限、Magisk 立即 KILL），见「任务进程组的清理与终止」。
- 背景（#156 专项调研在 WSL 里模拟 PID 1 实测，v3.3.4 及以前）：
  - 停容器时 entrypoint 转手 `kill` 就 `exit 0`，PID 1 几毫秒内退出，内核把面板整组 SIGKILL：任务停在「运行中」、执行日志正文为空，`node_modules` 软链残留，
    `daidai.db` 只有 4 KB、数据全在 1.8 MB 的 `-wal` 里，`/tmp` 漏下装着全部环境变量与 7 天有效脚本凭据的任务临时文件（jwt 密钥存在数据目录，重启后凭据照样能用）。
  - 只改 entrypoint 不够：开着实时日志时 `Shutdown(15s)` 被 SSE 拖满 15 秒，而 Docker 默认只给 10 秒；关停时照跑后置脚本（一个 `sleep 31` 的 `task_after.sh` 就拖到被 SIGKILL，二进制部署还留下孤儿）；
    失败通知时有时无；「重启面板」是裸 `os.Exit(1)`，不输出的任务成孤儿继续跑，新进程却把它标成「上次异常退出」。
- 目标：停容器、`ddp restart`、systemd stop、Ctrl+C、关 Windows 控制台窗口，以及面板里「重启 / 停止 / 在线升级」，都在 **8 秒内**收完尾。正常路径实测几十毫秒（PID 1 约 20~30ms 退出）。

### 2. Signatures

- `server/main.go`：
  - `func shutdownPanel(server *http.Server)`：唯一的关停流程。预算常量 `shutdownHardLimit = 8s`、`shutdownHTTPWait = 5s`、`shutdownSchedulersDeadline = 6500ms`（从关停开始算）、`shutdownDBWait = 1s`。
  - `http.Server{Handler: engine, BaseContext: func(net.Listener) context.Context { return requestsCtx }}` + `server.RegisterOnShutdown(cancelRequests)`。
  - 进入关停的三个入口：`serverErr`（`Serve` 意外返回，之后以 1 退出）、`shutdownSignals`（`os.Interrupt` / `syscall.SIGTERM`）、`panelExitRequests chan int`（容量 1）。
  - `func logSlowStartupStep(step string, started time.Time)` + `startupStepSlowThreshold = 3s`（日志口径见 `logging-guidelines.md`「约定：启动耗时与关停日志」）。
- `service`：
  - `func HaltSchedulerV2()`：`SignalStop` + `StopAllRunningTasks`，可重复调用。
  - `func ShutdownSchedulerV2()`：**保持无参**（二十多个测试 `t.Cleanup(ShutdownSchedulerV2)`）；`schedulerShutdownWait = 3s`（v3.3.6 前 4s）；`SignalStop` 里的 `schedulerCronCallbackWait = 1s`；
    `StopAllRunningTasks` 的共享 TERM 宽限 `shutdownTermGrace = 2s`（`task_executor.go` 的 `var`，测试可调短）。
  - `func ShutdownBackupScheduler(deadline time.Time)` / `func ShutdownSubscriptionScheduler(deadline time.Time)`：只有 `shutdownPanel` 调。
  - `func CleanupLeakedTaskTempEntriesOnStartup()`（`task_temp_cleanup.go`）；内部 `cleanupLeakedTaskTempEntries(dir string) int`、`isLeakedTaskTempEntry(entry os.DirEntry) bool`、表 `leakedTaskTempPatterns`。
  - 执行器侧的 `halted` / `hookProcesses` / `revokeUnsettledScriptTokens` 见「执行窗口」一节。
- `database`：`func Close(timeout time.Duration) error`。
- `handler`：`func SetPanelExitRequester(fn func(code int))`（传 nil 不替换）；`var panelProcessExit = func(code int) { os.Exit(code) }`（面板主程序之外的进程不接线）。
- `docker/entrypoint.sh`：脚本开头 `trap 'exit 143' TERM INT`；尾段 `shutdown()` + `trap shutdown TERM INT`；`log()` / `fail()`。

### 3. Contracts

**关停顺序与时间预算**（`shutdownPanel`；改任何一步都要重算这笔账）：

| 步 | 做什么 | 上限 |
|---|---|---|
| 兜底 | 最先装 `time.AfterFunc(8s)`：打「面板关停超过 8s 仍未完成，强制退出」后 `os.Exit(1)` | 8 秒 |
| 2 | **先起协程** `server.Shutdown(5s)`：关监听 → 调 `RegisterOnShutdown` 注册的 `cancelRequests` → 所有请求 ctx 取消、SSE 收流；超时就 `Close` | 5 秒，与 1 / 3 / 4 并行 |
| 1 | `HaltSchedulerV2`：`SignalStop`（不再触发、不再入队；等正在跑的 cron 回调 ≤ 1 秒）+ `StopAllRunningTasks`（标 `runStopHalt`、置 `halted`；任务进程与钩子的进程组一起 SIGTERM，共用 `shutdownTermGrace` 截止逐组等，到点还在的统一 SIGKILL；Magisk 部署宽限为 0、立即整组 KILL） | ≈ 1 + 2 秒 |
| 3 | `ShutdownSchedulerV2`：`WaitWorkers` 与 `executor.Wait` 共用一个 3 秒截止时间（剩余至少 1ms）→ `revokeUnsettledScriptTokens` → `MarkActiveTasksInterrupted` → 把 `globalScheduler` / `globalExecutor` 置 nil（之后的调用点见「关停后的全局调度器」） | 3 秒 |
| 4 | 关自动更新、日志清理、资源监控的通道；备份调度、订阅调度**同时**（各一个协程）`cron.Stop`，正在跑的作业共用截止「关停开始后 6.5 秒」，**不取消**作业 | 到 6.5 秒 |
| 5 | `<-httpDone` | 通常立刻 |
| 6 | `database.Close(1s)`，**必须最后**：之后任何读写都报 `sql: database is closed` | 1 秒 |
| 收尾 | 回到 main：`cleanupPIDFile()`；退出码非 0 才 `os.Exit(code)`，0 时 main 正常返回 | — |

- 总账：max(1 + 2 + 3, 6.5, 5) + 1 = 7.5 秒 < 8 秒 < Docker 默认的 10 秒。总兜底管住其余意外，例如唯一的数据库连接被恢复备份的长事务占着，`MarkActiveTasksInterrupted` 拿不到连接。
  v3.3.6（#159）第 1 步多了最多 2 秒的 TERM 宽限，第 3 步同时从 4 秒缩到 3 秒：TERM 宽限本身就是结算等待的前半段，进程被 KILL 后结算通常只要几十毫秒；
  不缩的话 max(1 + 2 + 4, 6.5, 5) + 1 = 8 秒，正好撞上总兜底。Magisk 部署第 1 步不给宽限，仍约 1 秒。
- HTTP 关停必须与第 1 步**同时开始**：第 1 步可能卡住（cron 回调要读写库），串行写在后面 SSE 就断不开。
- 两个 cron 不各写死 1 秒：走到这一步任务已经结算完，多等不影响别的，能让本来来得及跑完的定时拉取跑完（git 在 stash push 与 pop 之间被杀会把用户改动留在 stash 里）。

**请求 ctx 与三条 SSE**：

- `http.Server.Shutdown` 本身**不取消**请求 ctx（只关监听、等连接空闲）。不接 `BaseContext` + `RegisterOnShutdown(cancel)` 的话，SSE 永远等不到 ctx，`Shutdown` 等满超时。
- 🔴 ctx 取消分支 return 之前**必须先写终止事件再 Flush**。干净结束的响应不会触发网页 `sse.ts` 的任何回调，日志弹窗、日志管理、依赖页只认 `done` 与 `onError`，会静默卡在「运行中」。
  原来被 SIGKILL 是异常断开、会走 `onError`，所以漏发是新设计才会引入的回退。客户端自己断开时这次写入只是写失败，无副作用。
  - 实时日志 `GET /logs/:id/stream`：`event: done` + `data: reconnect`（与会话撤销同口径；网页与 APP 去重连，面板没起来就走各自的出错收口）。三个分支都要：
    TinyLog 实时分支的 `ctx.Done`、排队中短轮询的 `ctx.Done`、运行中却没有 TinyLog 的 `c.Stream` 轮询分支（典型是 conc 模式 / 抑制实时输出的任务）。
    `c.Stream` 只认客户端断开、不认 ctx：**每轮开头**判 `c.Request.Context().Err() != nil`，两轮之间的 500ms 等待也 `select` ctx，否则要轮询满 60 秒。
  - 依赖安装输出 `GET /deps/:id/log-stream`、订阅拉取输出 `GET /subscriptions/:id/pull-stream`：**同样**是 `event: done` + `data: reconnect`，三条流同值。
    🔴 不能发 `timeout`：APP 的依赖页、订阅页（从仓库最早的 tag v1.0.2 起）把 `reconnect` 以外的 `done` 都当成完成，收到 `timeout` 会显示绿色「安装完成 / 拉取完成」，是假成功；
    `reconnect` 则让所有版本的 APP 1 秒后重连一次，面板没起来就走 `onError`，显示红色「安装日志连接已断开 / 拉取日志连接已断开」，与 v3.3.4 被强杀时同一终态（只是正文末尾多一行字面量 `reconnect`）。
    网页依赖页对 `timeout` 与 `reconnect` 显示同一句「日志流已断开，任务可能仍在进行」（`web/src/views/deps/index.vue` 的 done 分支）；网页订阅页把 `finished` / `not_running` / `closed` 以外的值都当「连接中断」，不用改。
    v3.3.5 第二波这里发的是 `timeout`，APP 侧 spec 复核发现后改成 `reconnect`；APP 各页的表现与约束见 APP 仓库 `.trellis/spec/frontend/panel-contract.md`「面板关停时的结束事件」。
  - 只改 ctx 取消分支：依赖流的硬上限（`hardDeadline` = 依赖操作超时 + 5 分钟）、订阅流 5 分钟无输出仍发 `timeout`（APP 依赖 / 订阅页对它同样显示「…完成」，是 APP 既有问题，待 APP 修）。
- 取消只影响「看输出」的那条流：依赖安装与订阅拉取本身的 ctx 派生自 `context.Background()`，GORM 调用不带请求 ctx；MCP、企业微信回调正在进行的调用会收到取消，停机时可接受。

**关停后的全局调度器**（`GetSchedulerV2()` / `GetTaskExecutor()`）：

- 第 3 步 `ShutdownSchedulerV2` 末尾把 `globalScheduler` / `globalExecutor` 置 nil，之后**仍有**代码会去拿它：第 4 步还在等的定时拉取（最多到关停开始后 6.5 秒）、
  handler 起的手动拉取协程（不随关停取消、没有 `recover`）、仍在处理的 HTTP 请求；`ddp` 进程里调度器本来就没起（`ddp sub pull` 走同一段订阅同步代码）。
- 🔴 调用点一律先判空，只读一次：`if scheduler := GetSchedulerV2(); scheduler != nil { … }`。连读两次（`GetSchedulerV2() != nil && GetSchedulerV2().X()`）两次之间可能变 nil，
  `handler/task_query.go` 的 `schedule_hint` 那处不崩只因为 `ScheduledEntryCount` 对 nil 返回 -1，新代码别这么写。
- `*SchedulerV2` 上对 nil 接收者安全的方法：`AddJob`（返回 nil；`UpdateJob` 转调它）、`RemoveJob`、`HasJob`（false）、`ScheduledEntryCount`（-1）、`Enqueue`（返回 `scheduler stopped`，
  `RunNow` 经它也只返回错误）、`SignalStop`。新增会在关停期间被调到的方法，照此开头判 `s == nil`。
- 调度器为 nil 时，订阅同步自动添加的任务**不注册、不算失败**：任务行已经落库，面板下次启动 `InitSchedulerV2` 按库注册（与同文件摘任务的 `deleteSubscriptionTaskIfUnchanged` 同一写法）。
- 教训（v3.3.5 回归复核实测）：第二波的订阅同步自动添加任务直接 `GetSchedulerV2().AddJob(&task)`。关停时还在跑的手动拉取走到这里空指针，面板以退出码 2 崩溃，
  关库、删 PID 文件都没走，`-wal` / `-shm` 残留，同时在跑的定时拉取也什么都没落库；单独的定时拉取被 cron 的 `Recover` 兜住，但这次拉取的收尾丢了；`ddp sub pull` 需要自动加任务时同样崩。

**面板自己发起的退出**：

- 「重启面板」（`POST /system/restart`，退出码 1）、「停止面板服务」（`POST /system/stop`，0）、二进制与 Magisk 在线升级让位（0）都只调 `panelProcessExit(code)`。
  main 用 `SetPanelExitRequester` 把它接到 `panelExitRequests`，走完 `shutdownPanel` 再按原退出码退出。退出码不变：1 让 Docker 重启循环、systemd `Restart=on-failure`、Magisk 存活守护照旧拉起。
- 🔴 `SetPanelExitRequester` 必须早于 `handler.StartPanelAutoUpdateWatcher` 与 `server.Serve`（main 里放在 `InitSchedulerV2` 之前）：之后再接，会与这些可能触发退出的协程产生数据竞争。
- 通道容量 1：已有请求在排队时，后来的直接丢弃（以第一个为准，例如连点两次重启）。
- ddp 进程不接线：`ddp update` 走二进制分支时仍是默认的 `os.Exit`，行为不变。
- `Serve` 意外返回（不是 `http.ErrServerClosed`）也走 `shutdownPanel`，再以 1 退出（原来是 `log.Fatalf`，什么都不收尾）。

**信号与 defer**：

- `signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)` 必须在 `service.InitSchedulerV2` **之前**：调度器一起来就可能有开机任务在跑，之后到的信号先存进通道，HTTP 起来后立刻处理。
  更早的启动步骤（启动校验可能很慢，#156）里收到信号，仍按默认方式立即退出：那时还没有任务要收尾。
- main **不再 defer** 任何收尾函数，也不直接调用它们（`HaltSchedulerV2`、`ShutdownSchedulerV2`、两个调度器、资源监控、日志清理、自动更新、`database.Close`；`cleanupPIDFile` 也不 defer）。
  顺序只由 `shutdownPanel` 一处决定：v3.3.4 的 defer 逆序让 HTTP 先关（还要等满 15 秒）、任务最后才杀。

**启动时清理上次留下的任务临时文件**（`CleanupLeakedTaskTempEntriesOnStartup`）：

- 位置：`InitSchedulerV2` 之前。这时本进程还没有任务在跑，命中前缀的通常是上次留下的（SIGKILL、`os.Exit`、8 秒兜底强退，或执行卡在读输出里走不到清理）；
  但同一个临时目录里别的进程正在用的也长这样：`ddp task run`（Docker 里 `docker exec` 起的、Magisk 里进容器执行的，与面板共用容器的 `/tmp`）、共用 `TMPDIR` 的另一个二进制面板。
  它们的文件同样会被删，那次执行的日志正文丢失（已知限制，backlog，见错误矩阵）。
- 只扫 `os.TempDir()`（Unix 上遵从 `TMPDIR`，与创建处同一位置），只删三类，形状必须对得上：

| 前缀 / 形状 | 创建处 |
|---|---|
| `daidai-runtime-*`，目录 | `runtime_exec.go` 的 `writeManagedRuntimeEnvFile` / `writeManagedRuntimeShellEnvFile` |
| `daidai-hook-env-*`，目录 | `task_hook_env.go` 的 `newHookEnvCapture` |
| `daidai-log-*.log`，普通文件 | `tiny_log.go` 的 `NewTinyLog` |

- 软链（`DirEntry.Type()` 不跟随）、形状不对的、前缀不在开头的、别的程序的文件，一律不动。真删了才打一行 `removed N leftover task temp entries from <dir>`。
- **不区分实例**：只认前缀与形状，不认是哪个面板进程建的（代价见错误矩阵）。
- 改了创建处的前缀或形状，必须同步 `leakedTaskTempPatterns`（`TestLeakedTaskTempPatternsMatchCreators` 守着）。
- 测试里只对 `t.TempDir()` 调 `cleanupLeakedTaskTempEntries`，**不要**调 `CleanupLeakedTaskTempEntriesOnStartup`：并行的其它测试包也在系统临时目录里建同前缀的文件。

**关库**：`database.Close(timeout)` 把 `sqlDB.Close()` 放进协程，到点就返回错误、不干等（SQLite 本身崩溃安全，没提交的事务下次打开时自动回滚）。
SQLite 关最后一个连接时会自动 checkpoint 并删掉 `-wal` / `-shm`，数据目录只剩 `daidai.db`。还没结束的协程（依赖安装、慢请求）之后读写会报 `database is closed`，只多几行日志。

**entrypoint**（`docker/entrypoint.sh`，dash 与 busybox ash 都要成立）：

- 脚本第一件事（PUID 的 `chown -R`、`find` 之前）装 `trap 'exit 143' TERM INT`：没装 trap 的 PID 1 收到的 SIGTERM 会被内核丢掉，`docker stop` 要白等满 10 秒。
  trap 要等手上那条前台命令跑完才执行。面板起来之前换成 `trap shutdown TERM INT`。
- `shutdown()` 的顺序：
  1. `trap '' TERM INT`，防重入；
  2. `kill -TERM "$SERVER_PID"` 成功才 `log` 一行，并 `wait "$SERVER_PID" 2>/dev/null || true`（`|| true` 不能省：面板非 0 退出时会被 `set -e` 带出）；
  3. `nginx -s quit 2>/dev/null || true`，不等它；
  4. `rm -f` PID 文件；
  5. **在 trap 里 `exit 0`**，不回 `while true` 重启循环。
- **不自设超时**：兜底交给 Go 的 8 秒与 Docker 宽限；内部计时器还会截断用户自己配的更长宽限。
- `log()` / `fail()` 每行带 `date '+%Y/%m/%d %H:%M:%S'` 时间戳，与 Go `log` 的默认格式一致。
- su-exec / gosu / `/usr/bin/env` 那几行启动命令原样保留：exec 链上 PID 不变，`SERVER_PID` 就是降权后的面板本身（`docker_entrypoint_assets_test.go` 断言这几行）。
- 旧 docker.sock 一键更新的辅助脚本（`handler/system_update.go` 的 `buildPanelUpdateHelperScript`）：`docker rm -f` 之前先 `docker stop -t 10 <容器> >/dev/null 2>&1 || true`。
  `rm -f` 等于直接 SIGKILL；stop 失败时 `rm -f` 照样兜底。
- **不加**：compose `stop_grace_period`、Dockerfile `STOPSIGNAL` / `init`、entrypoint 内部超时。理由：面板 8 秒内必退；tini 只转发给直接子进程，entrypoint 本来就是那个子进程；用 NAS 图形界面部署的用户拿不到 compose 配置。
  `packaging/linux/daidai-panel.service` 的 `TimeoutStopSec=30s` 只是余量。Magisk 动作按钮「TERM 后等 2 秒再 KILL」（`Magisk/action.sh`）不改：它是模块外壳，在线升级改不到，
  改了只惠及重刷 zip 的用户（改成轮询等待、升外壳版本进 backlog）。作为让步，Magisk 部署下面板关停不给 TERM 宽限、立即整组 KILL（v3.3.6，见「任务进程组的清理与终止」）。

### 4. Validation & Error Matrix

| 情形 | 结果 |
|---|---|
| 正常停容器 / Ctrl+C，开着实时日志 | 面板日志 `panel shutdown finished in` 几十毫秒；SSE 以 `done` 事件结束；任务按失败结算并写两行「[面板正在关闭，…]」；软链清掉；只剩 `daidai.db`；`/tmp` 无残留；下次启动没有 `recovered` |
| 第 1 步卡住（cron 回调拿不到库连接，例如恢复备份的长事务占着唯一连接） | `SignalStop` 1 秒后放手、接着杀任务；HTTP 照样关、SSE 照样以结束事件断开；后面的写库也拿不到连接时，8 秒兜底 `os.Exit(1)`（不删 PID 文件，systemd 显示 failed） |
| 任务派生了逃出进程组、攥着输出管道的孙进程（`pumpAndWait` 等不到 EOF，`runTask` 卡住） | 3 秒截止：凭据吊销、`MarkActiveTasksInterrupted` 标中断，这次输出丢失。`node_modules` 软链留着（不悬空，与面板现建的等价、无害；之后托管目录没了、变悬空时，由下次启动的 `removeLeftoverManagedNodeModulesLinks` 清），临时文件留给下次启动的 `CleanupLeakedTaskTempEntriesOnStartup`。孙进程本身**不保证**被清理：Docker 里随 PID 1 退出被内核收走，二进制 / Magisk 下成孤儿。spec 与发布说明都**不承诺**「一定清理」 |
| Windows | `killGroup` 是空操作、`signalGroupTerm` 恒为 false（不等 TERM 宽限，立即 `p.Kill()`），只杀直接子进程：孙进程攥管道更常见，走上一行的 3 秒路径（关控制台窗口约 5 秒上限） |
| 组里有忽略 TERM 的进程（`trap '' TERM` 的脚本、继承了忽略的子进程） | 共用的 2 秒宽限到点统一 SIGKILL，面板日志多一行 `N task process group(s) still running 2s after SIGTERM, killed`（`StopAllRunningTasks` 自己打，所以排在 `HaltSchedulerV2` 那行 `interrupted N running task process(es) during panel shutdown` 之前）；关停最多慢 2 秒 |
| 脚本 trap 了 TERM、收完尾以 0 退出 | 仍按失败结算（`runStopHalt`），写「任务已中断」「跳过后置脚本」，成功、失败通知都不发（见「执行窗口」一节） |
| Magisk 部署 | 不发 TERM，所有进程组立即整组 SIGKILL（与 v3.3.5 一致）：动作按钮 TERM 后只等 2 秒就 KILL 面板，装不下 1 秒 + 2 秒宽限 |
| 对忽略 TERM 的任务点了停止，5 秒内又重启面板 | `StopTask` 已把进程摘出进程表，关停时看不到它；补刀协程随旧进程退出：这个进程成为孤儿（已接受，改前停止是立即 SIGKILL，没有这个窗口） |
| 定时备份 / 定时拉取正在跑 | 等到关停开始后 6.5 秒，不取消；到点不再等（Docker 里随后被 SIGKILL；备份非原子写盘、git stash 中途被杀见 backlog） |
| 正在装依赖 | 不取消；记录停在 installing，下次启动的对账改成已安装 / 续装 / 「操作因服务重启而中断」，不会卡住 |
| 关停开始后才轮到的钩子 | `halting()` 拦住不启动；正好撞上的那一下由 `registerHookProcess` 立刻杀掉 |
| 两个二进制面板共用一个 `TMPDIR`，或面板重启时正有 `ddp task run` 在跑——包括 Docker 里 `docker exec` 起的、Magisk 里进容器手动执行的（它们与面板共用容器里同一个 `/tmp`） | 启动清理不区分实例，会删掉对方正在用的 TinyLog（`daidai-log-*.log`）与运行时目录，后果是那次执行的日志正文丢失（`TinyLog.Close` 按路径回读，文件没了就读不到正文）。Docker / Magisk 的独立 `/tmp` 只隔开宿主机，挡不住容器里的 `ddp`。已知限制（backlog） |
| `ddp restart` / `ddp backup restore`（Docker 里） | 给面板发 TERM、面板以 0 退出，entrypoint 随之退出、整个容器停掉，靠 compose 的 `unless-stopped` 拉起（既有问题，未改，backlog） |
| `net.Listen` 失败（端口被占） | 仍是 `log.Fatalf` 直接退出、不走 `shutdownPanel`，而调度器此时已经起来（既有行为，未改） |
| `ShutdownSchedulerV2` 末尾把 `globalScheduler` / `globalExecutor` 置 nil 之后，还有代码调 `GetSchedulerV2()`（第 4 步还在等的定时拉取、handler 起的手动拉取、仍在处理的请求；`ddp` 进程里本来就是 nil） | 调用点都判空、`AddJob` / `RemoveJob` 对 nil 安全：不 panic，自动添加的任务照常落库、下次启动注册（见「关停后的全局调度器」）。置 nil 与这些协程的读并发，`-race` 下会报（backlog） |
| 关停时订阅拉取还在跑、要自动添加任务（手动拉取 + 定时拉取） | 面板照常以 0 退出；订阅日志、`last_pull_at`、新任务行都在 SIGTERM 之后落库，重启后任务注册进调度器（回归复核 WSL 实测，2.5 秒走完关停）。第二波漏判空时：手动拉取空指针、面板以退出码 2 崩溃，什么都没落库 |
| 面板卡死、无视 TERM | entrypoint 不自设超时，Docker 宽限到点 SIGKILL，结果同 v3.3.4 |
| 启动前半段（面板还没起）收到 TERM | 手上那条前台命令跑完就以 143 退出，不启动 nginx 与面板 |
| 收尾期间再来 TERM / INT | 忽略，收尾照常完成 |
| 重启间隙（`sleep 2`）收到 TERM | 不再拉起；dash 要等外部 `sleep` 跑完，busybox 的内建 `sleep` 立即被打断 |
| 新 entrypoint 配旧 Go | 开着实时日志停容器要等满 10 秒被 SIGKILL，比 v3.3.4 还慢：两侧必须同版发（镜像同版构建，不会错配） |

### 5. Good/Base/Bad Cases

- Good：开着实时日志停容器 → PID 1 约 20~30ms 退出；网页日志弹窗收到 `done: reconnect` 后走重连 / 出错收口，不卡在「运行中」；面板回来后重新打开弹窗，能看到「[面板正在关闭，任务已中断]」。
- Good：停调度那一步卡住 → HTTP 照样十几毫秒关停、SSE 照样以结束事件断开，8 秒兜底准时退出，等不到 Docker 10 秒强杀。
- Base：点「重启面板」→ 旧进程收完尾以 1 退出、被原机制拉起，新进程约 4 秒可用（与改前相当），不再留下孤儿任务。
- Base：没有任务在跑时停容器 → 只多几行关停日志。
- Bad：entrypoint `kill` 完立刻 `exit`；`Shutdown(15s)` 排在杀任务之前、请求 ctx 取消不了；两个 cron 各写死 1 秒；SSE 被取消时直接 return；
  `SetPanelExitRequester` 挪到 `Serve` 之后；main 里重新 defer 收尾函数；删掉 `wait` 后面的 `|| true`。
- Bad：依赖安装 / 订阅拉取流在关停时发 `timeout`（APP 依赖页、订阅页显示绿色「安装完成 / 拉取完成」）；
  关停后还可能跑的代码里直接 `GetSchedulerV2().AddJob(...)`（订阅拉取自动加任务时面板 panic、以退出码 2 崩溃）。

### 6. Tests Required

计时一律只断言上界（WSL 墙钟会跳；shell 侧用 `/proc/uptime`）。

- `server/service/panel_shutdown_test.go`：
  - `TestShutdownSchedulerV2IsBoundedWhenRunIsStuck`：`runCommandWithPlanFunc` 卡住、不登记进程、不理停止 → `ShutdownSchedulerV2` 5 秒内返回（等待共用 3 秒截止；两段各等 3 秒共 6 秒，或剩余时间算成 0 变成无限等，都超过这个上界；不登记进程，不受 TERM 宽限影响）；
    任务不再是运行中 / 排队中，`last_run_status == RunFailed`，日志含「面板正在关闭或重启」；该次执行注入脚本的凭据按 jti 进了 `TokenBlocklist`。
  - `TestHaltedRunSkipsPostHooksAndFailureNotify`：先跑不关停的对照（`[执行后置脚本]` 出现、webhook 探针收到失败通知；Linux 上三个标记文件写出），证明链路是通的。
    关停那条：`[执行后置脚本]` 不出现；任务后置脚本、`task_after.sh`、`extra.sh` 的标记文件都不存在；`[面板正在关闭，任务已中断]` 与 `[面板正在关闭，跳过后置脚本]` 各自整行出现且 `isPanelMetaLine` 认得；
    等 500ms 探针仍是 0 次。两条都 `RunFailed`。
  - `TestManualStopStillRunsPostHooks`（回归）：手动停止照跑后置脚本，日志里没有 `[面板正在关闭，`，结算 `RunAborted`。
  - `TestHaltKillsRunningPreHookAndSkipsNewOnes`：正在 `sleep 30` 的前置钩子 5 秒内被杀（结束标记不存在）、主进程一次没起、「取消后续执行」「任务已中断」「跳过后置脚本」三行整行出现、`RunFailed`；
    关停之后才开窗的执行连 `[执行前置脚本]` 都不出现，任务前置脚本与 `task_before.sh` 都没跑。
  - `TestHaltKillsRunningPostHook`：关停前已经开始的后置钩子（`sleep 30 &` + `wait`）整组被杀，后台 `sleep` 3 秒内消失（`/proc/<pid>/stat` 读不到或是僵尸）；
    这次执行在 `runTask` 里正常结算，日志保留主进程输出、`[执行后置脚本]`、`=== 执行结束`。
  - `TestShutdownInterruptNoticesAreStandalonePanelMetaLines`：真实 bash 进程被关停整组杀掉，日志有「[脚本进程被信号终止：」，两行提示整行出现；
    逐行检查：`[面板正在关闭，` 只出现在行首，以它或「[脚本进程被信号终止：」开头的行都被 `isPanelMetaLine` 认出。
  - `TestSchedulerV2SignalStopDoesNotWaitForStuckCronCallback`：cron 回调卡住时 `SignalStop` 1.5 秒内返回，之后 `Enqueue` 报错。
  - `TestCronSchedulersShutdownWithinDeadline`（备份、订阅各两条）：作业在跑时，截止 100ms 的关停 500ms 内返回，作业既没被打断也还没跑完，放行后照常跑完；没有作业时截止给 5 秒也在 1 秒内返回。
  - `TestSubscriptionSyncAfterSchedulerShutdownStillCreatesTasks`：按 `shutdownPanel` 的顺序 `InitSchedulerV2` → `HaltSchedulerV2` → `ShutdownSchedulerV2`（先断言 `GetSchedulerV2()` 已是 nil），
    再对开着自动添加的订阅调 `syncSubscriptionTasks`：不 panic；任务行落库（命令、cron 正确）；同步日志有「[自动添加任务] 」。
  - `TestSchedulerV2NilReceiverAddAndRemoveJob`：nil 调度器的 `AddJob` / `UpdateJob` 返回 nil、`RemoveJob` 不 panic、`HasJob` 为 false、`ScheduledEntryCount` 为 -1。
  - 突变：去掉 `AddJob` / `RemoveJob` 开头的 nil 判断，第二条 panic；第一条要 `syncSubscriptionTasks` 的判空与 `AddJob` 的 nil 判断**同时**去掉才红（两处互为兜底），两处都要留。
- `server/service/task_process_cleanup_linux_test.go`（`//go:build linux`，v3.3.6 先 TERM 的关停，断言点与跑法见「场景：任务进程组的清理与终止」）：`TestShutdownTermsAllGroupsWithinSharedGrace`、
  `TestShutdownSettlesTermTrappedZeroExitAsInterrupted`、`TestShutdownAfterMainCommandSucceededStillSettlesAsSuccess`、`TestShutdownKillsImmediatelyOnMagisk`、`TestTermLetsParentCloseDetachedChild/面板关停`。
- `server/service/shutdown_wiring_test.go` 的 `TestMainShutdownOrder`（AST 读 `../main.go`，注释里出现函数名不算）：
  - `shutdownPanel`：`time.AfterFunc` → `service.HaltSchedulerV2` → `service.ShutdownSchedulerV2` → `service.ShutdownBackupScheduler` → `service.ShutdownSubscriptionScheduler` → `database.Close`；
    `server.Shutdown` 在 `go` 语句里、写在 `HaltSchedulerV2` 之前，两者之间没有任何 `<-` 接收。
  - `main`：`handler.SetPanelExitRequester` 早于 `handler.StartPanelAutoUpdateWatcher`，也早于 `server.Serve`、`shutdownPanel`；`server.RegisterOnShutdown` 早于 `server.Serve`；
    `service.CleanupLeakedTaskTempEntriesOnStartup` 与 `signal.Notify` 都早于 `service.InitSchedulerV2`；`http.Server` 字面量带 `BaseContext`；
    不直接调用、也不 defer 那八个收尾函数，也不 defer `cleanupPIDFile`。
- `server/service/task_temp_cleanup_test.go`：
  - `TestCleanupLeakedTaskTempEntriesRemovesOnlyPanelPrefixes`（只在 `t.TempDir()` 里）：恰好删 3 条；`daidai-runtime-` 开头的普通文件、名为 `daidai-log-*.log` 的目录、`.txt` 后缀、
    `daidai-restore-*`、`my-daidai-runtime-*`、别的文件、同前缀软链及其指向都保留；目录不存在时删 0 条。
  - `TestLeakedTaskTempPatternsMatchCreators`：用真实的创建函数各建一个，判定必须认得，并且都建在 `os.TempDir()` 下。
- `server/handler/panel_shutdown_test.go`：
  - `TestShutdownEndsSSEStreamsWithDoneEvent`（表驱动 5 行：实时日志的 TinyLog 分支、排队中短轮询、运行中无 TinyLog 的轮询分支、依赖安装输出、订阅拉取输出；服务按 main 的方式接 `BaseContext` + `RegisterOnShutdown`）：
    `Shutdown` 后 1 秒内收流，五行的最后两行都是 `event: done` 与 `data: reconnect`，`Shutdown` 2 秒内无错返回。
    突变：删掉任一 ctx 分支的结束事件，对应那行变红；依赖 / 订阅改回 `data: timeout`，那两行变红；删掉轮询分支开头的 ctx 判断，「轮询分支」那行变红。
  - `TestRestartAndStopRouteThroughPanelExitRequester`：`/system/restart` 请求退出码 1、`/system/stop` 请求 0；`SetPanelExitRequester(nil)` 不替换已接好的出口。
  - `TestPanelSelfUpdateExitsThroughPanelExitRequester`（AST）：`system_update_binary.go`、`system_update_magisk.go` 不直接调 `os.Exit`，并且都调了 `panelProcessExit`。
  - `TestPanelUpdateHelperStopsContainerBeforeRemoving`：`docker stop -t 10 '<容器>' >/dev/null 2>&1 || true` 在 `docker rm -f` 之前，`docker 'run'` 在最后。
- `server/database/close_test.go` 的 `TestCloseCheckpointsWALAndRemovesSidecarFiles`：前提是 Close 之前 `-wal` 存在（否则断言没有区分度）；`Close(1s)` 后 `-wal`、`-shm` 都不存在；全新的连接读得到刚写的行。
- `docker/test-entrypoint-shutdown.sh`（需要 root）：`unshare --mount` + tmpfs 隔离 `/tmp`、`/app`、`/ql`；`unshare --pid --fork --mount-proc` 让**整份** entrypoint 当 PID 1；
  面板换成「收到 TERM 后收尾 1 秒」的 shell 桩；dash 跑一遍，有 busybox 再用 `busybox sh` 跑一遍。8 个用例：

| # | 用例 | 断言点 |
|---|---|---|
| 1 | TERM 转发并等待收尾 | rc 0、没被强杀、收尾标记写出、只拉起 1 次、< 5000ms、`nginx -s quit`、PID 文件已删、entrypoint 日志带 `YYYY/MM/DD HH:MM:SS` 时间戳 |
| 2 | 面板收尾后以 3 退出 | PID 1 仍以 0 退出、没被重新拉起（dash 下走降权分支，用 chown 桩把 TERM 稳稳落在 `set -e` 还开着的那一小段） |
| 3 | 收尾期间再来 TERM + INT | rc 0；被忽略，收尾完成，只拉起 1 次 |
| 4 | 崩溃后照常重启 | 桩以 1 退出后第 2 次拉起；之后 TERM → rc 0，不再拉起第 3 次 |
| 5 | 重启间隙收到 TERM | rc 0，不再拉起，< 4000ms |
| 6 | 面板自己以 0 退出 | entrypoint 以 0 退出、不是被强杀，只拉起 1 次 |
| 7 | 降权链路（PUID=65534，本机没有 65534 就 SKIP） | 桩以 uid 65534 运行，PID 文件里的 `SERVER_PID` 就是桩自己；TERM → rc 0，降权后的面板完成收尾 |
| 8 | 启动前半段收到 TERM | 以 143 退出、< 4000ms、没被强杀、面板与 nginx 都没启动 |

  - 已实测的三个突变（`ENTRYPOINT_SRC=<改坏的文件> sudo -E bash docker/test-entrypoint-shutdown.sh`）：`shutdown()` 换回「kill 完立刻 exit 0」→ 第 1 条红；删掉 `wait` 后面的 `|| true` → 第 2 条红；删掉开头的 `trap 'exit 143'` → 第 8 条红。
  - 接在 `.github/workflows/checks.yml` 的 PUID 那步之后：先装 busybox，装不上只跑 dash 并打 warning。
- 修改后至少运行：

```bash
cd server
go test ./service -run "Shutdown|Halt|SignalStop|LeakedTaskTemp|ManualStopStillRunsPostHooks|NilReceiver" -count=1
go test ./handler -run "Shutdown|PanelExitRequester|PanelUpdateHelperStops" -count=1
go test ./database -run "TestCloseCheckpointsWAL" -count=1
go test ./...
# Linux / WSL，需要 root：
sudo bash docker/test-entrypoint-shutdown.sh
```

### 7. Wrong vs Correct

#### Wrong

```sh
# 错误：转手 kill 就退。PID 1 一退出，内核把容器里剩下的进程整组 SIGKILL，面板一个收尾动作都做不了
shutdown() {
  if [ -n "${SERVER_PID:-}" ]; then
    kill "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -f "${SERVER_PID_FILE}"
  exit 0
}
```

```go
// 错误：Shutdown(15s) 排在杀任务之前（杀任务靠 defer 逆序，最后才轮到），请求 ctx 又取消不了。
// 开着实时日志时 SSE 把 Shutdown 拖满 15 秒，任务这期间照常跑，Docker 10 秒到点 SIGKILL。
service.InitSchedulerV2()
defer service.ShutdownSchedulerV2()
server := &http.Server{Handler: engine}
// ……
case sig := <-shutdownSignals:
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    _ = server.Shutdown(ctx)
```

```go
// 错误：两个 cron 各写死 1 秒、串行。主动放弃本来来得及跑完的定时拉取（git 在 stash push 与 pop 之间被杀）；
// 串行时后一个 cron 还没 Stop，等前一个的这段时间里还可能再触发一次新作业。
service.ShutdownBackupScheduler(time.Now().Add(time.Second))
service.ShutdownSubscriptionScheduler(time.Now().Add(time.Second))
```

```go
// 错误：ctx 取消时直接 return。响应是干净结束的，网页 sse.ts 一个回调都不触发，弹窗静默卡在「运行中」
case <-ctx.Done():
    return
```

```go
// 错误：依赖安装 / 订阅拉取流在关停时发 timeout（v3.3.5 第二波）。
// APP 的依赖页、订阅页（v1.0.2 起）把 reconnect 以外的 done 都当成完成，显示绿色「安装完成 / 拉取完成」——假成功。
case <-ctx.Done():
    fmt.Fprintf(c.Writer, "event: done\ndata: timeout\n\n")
    c.Writer.Flush()
    return
```

```go
// 错误：关停第 3 步之后 globalScheduler 已是 nil，还在跑的订阅拉取走到这里空指针。
// 手动拉取的协程没有 recover，面板以退出码 2 崩溃，关库、删 PID 文件都没走（v3.3.5 第二波）。
if err := GetSchedulerV2().AddJob(&task); err != nil {
    log.Printf("任务 %d 注册调度失败（它不会自动触发）: %v", task.ID, err)
}
```

#### Correct

```sh
trap 'exit 143' TERM INT # 脚本第一件事：面板还没起，收到就退
# ……
shutdown() {
  trap '' TERM INT
  if [ -n "${SERVER_PID:-}" ] && kill -TERM "${SERVER_PID}" 2>/dev/null; then
    log "收到停止信号，等待面板收尾..."
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  nginx -s quit 2>/dev/null || true
  rm -f "${SERVER_PID_FILE}"
  exit 0 # 在 trap 里退，不回重启循环
}
trap shutdown TERM INT
```

```go
requestsCtx, cancelRequests := context.WithCancel(context.Background())
server := &http.Server{
    Handler:     engine,
    BaseContext: func(net.Listener) context.Context { return requestsCtx },
}
server.RegisterOnShutdown(cancelRequests)

// shutdownPanel：HTTP 关停先起协程，与停调度、杀任务同时开始
go func() {
    defer close(httpDone)
    ctx, cancel := context.WithTimeout(context.Background(), shutdownHTTPWait)
    defer cancel()
    if err := server.Shutdown(ctx); err != nil {
        _ = server.Close()
    }
}()
service.HaltSchedulerV2()
service.ShutdownSchedulerV2()
// 两个 cron 各起一个协程同时停，共用截止 started.Add(shutdownSchedulersDeadline)（关停开始后 6.5 秒），再 Wait
<-httpDone
if err := database.Close(shutdownDBWait); err != nil {
    log.Printf("close database failed: %v", err)
}
```

```go
case <-ctx.Done():
    // 三条流（实时日志、依赖安装、订阅拉取）都发 reconnect；非关停的硬上限 / 静默超时才发 timeout
    fmt.Fprintf(c.Writer, "event: done\ndata: reconnect\n\n")
    c.Writer.Flush()
    return
```

```go
// 正确：只读一次、先判空。调度器为 nil 时不注册也不算失败：任务行已落库，下次启动 InitSchedulerV2 按库注册
if scheduler := GetSchedulerV2(); scheduler != nil {
    if err := scheduler.AddJob(&task); err != nil {
        log.Printf("任务 %d 注册调度失败（它不会自动触发）: %v", task.ID, err)
    }
}

// 正确：会在关停期间被调到的方法，对 nil 接收者直接返回（与 HasJob / ScheduledEntryCount / Enqueue 一致）
func (s *SchedulerV2) AddJob(task *model.Task) error {
    if s == nil {
        return nil
    }
    // ……
}
```

---

## 场景：任务进程组的清理与终止（v3.3.6，#159 修复 A / B）

### 1. Scope / Trigger

- 触发：改下面任何一处都必须看本节。
  - 信号原语：`service/process_unix.go` / `process_windows.go` 的 `killGroup` / `killGroupByPid` / `signalGroupTerm` / `processGroupAlive`；
    `task_executor.go` 的 `KillProcessGroup` / `KillProcessByPid` / `TerminateProcessGroup` / `TerminateProcessByPid` / `waitProcessGroupGone` / `groupTermGrace` / `shutdownTermGrace`。
  - 任何「停止 / 终止任务进程」的入口：`StopTask`、`StopAllRunningTasks`、`script_runner.go` 的 `pumpAndWait` 超时分支、handler `task_control.go` 的 `Stop`、`task_batch.go` 的 `case "stop"`、
    `scheduler_v2.go` 的 `stopTaskBySchedule`、`handler/script_runtime.go` 的 `debugRun.stop`。
  - 修复 A：`runSingleCommand` 末尾的清理段、`runTask` 里挂 `plan.ShouldCleanupProcessGroup` 的那几行、常量 `leftoverProcessCleanupNotice`、系统配置 `cleanup_leftover_processes`。
- 背景（#159，research 实测）：
  - 修复 A：主命令正常结束后不清理进程组。脚本放到后台、不占输出管道的子孙进程（`nohup … >/dev/null &`、`Popen(DEVNULL)`、没 `quit` 的浏览器 / driver）被 PID 1 收养，
    活到重启容器为止，「重启面板」也释放不了。留 20MB 子进程的任务跑 20 次，容器内存 52.7→516MB，面板 RSS 只 35→38MB。
  - 修复 B：超时 / 停止 / 关停一律直接对进程组 SIGKILL。Puppeteer 默认 `detached:true` 把浏览器放进独立进程组、收尾挂在 SIGTERM 上，每被打断一次留下约 125–140MB 的进程树。
- 进程组只在 Linux（含 Magisk 的 chroot）上成立；Windows 二进制的行为与改动前一致。

### 2. Signatures

| 符号 | 位置 | 语义 |
|---|---|---|
| `signalGroupTerm(pid int) bool` | `process_unix.go` / `process_windows.go` | `syscall.Kill(-pid, SIGTERM) == nil`；false = 组已经不在（ESRCH）或信号发不出去，调用方改走立即 KILL。pid <= 1 恒为 false；Windows 恒为 false |
| `processGroupAlive(pid int) bool` | 同上 | `syscall.Kill(-pid, 0) == nil`，只检查不发信号；还没被回收的僵尸也算「还在」。pid <= 1 恒为 false；Windows 恒为 false |
| `waitProcessGroupGone(pid int, limit time.Duration) bool` | `task_executor.go` | 每 50ms 查一次 `processGroupAlive`，组空了返回 true，等满 `limit` 还有进程返回 false；`limit <= 0` 只查一次 |
| `TerminateProcessGroup(p *os.Process)` | 同上 | **不阻塞**：`signalGroupTerm` 失败 → 直接 `KillProcessGroup(p)`；成功 → 起协程 `if !waitProcessGroupGone(p.Pid, grace) { KillProcessGroup(p) }`。`grace := groupTermGrace` 在起协程**之前**读（测试会临时调短它，协程里再读全局变量会和收尾时的还原互相踩）；`p == nil` 直接返回 |
| `TerminateProcessByPid(pid int)` | 同上 | 同上，补刀用 `KillProcessByPid(pid)`；只给「执行器不认识这次执行、只能按库里的 PID 兜底」的停止路径用 |
| `KillProcessGroup(p)` / `KillProcessByPid(pid)` | 同上 | 语义不变：立即整组 SIGKILL（`killGroup` / `killGroupByPid`）再补一个 `p.Kill()`。`KillProcessByPid` 与 `killGroupByPid` 对 pid <= 1 直接返回 |
| `var groupTermGrace = 5 * time.Second` | 同上 | 超时、手动 / 批量 / 定时停止、调试运行停止、修复 A 补刀的宽限。用 `var` 只为测试能调短（先例：`handler/deps.go` 的 `dependencyOutputWaitDelay`） |
| `var shutdownTermGrace = 2 * time.Second` | 同上 | 面板关停时所有进程组共用的宽限；Magisk 部署下关停改用 0 |
| `CommandExecutionPlan.ShouldCleanupProcessGroup func() bool` | `script_runner.go` | 修复 A 的判定，nil = 不清。用字段而不是给函数加参数：`runCommandWithPlanFunc` 的签名被约 40 个测试桩原样照抄 |
| `const leftoverProcessCleanupNotice` | 同上 | 修复 A 写进任务日志的提示行 |
| 配置 `cleanup_leftover_processes` | `model/system_config_registry.go` | `newBoolConfig("cleanup_leftover_processes", "任务结束后清理残留进程", "true", <说明>, "tasks")`，紧跟在 `detect_silent_exit` 后面 |

### 3. Contracts

**调用点矩阵**（新增停止入口照这张表选原语）：

| 路径 | 写法 | 阻塞 |
|---|---|---|
| 超时：`pumpAndWait` 的 `timerC` 分支（任务主命令、任务钩子 60 秒、订阅拉取前指令 / 拉取后钩子 900 秒共用） | `if !signalGroupTerm(pid) \|\| !waitProcessGroupGone(pid, groupTermGrace) { KillProcessGroup(cmd.Process) }`，最后照旧 `<-outcomeCh` | 是，最多 5 秒 |
| 手动停止（网页、开放 API、MCP `stop_task`）/ 批量停止 / 定时停止 | `StopTask` 出锁后对每个 victim `TerminateProcessGroup`；返回 false 才 `MarkManualStop` + `TerminateProcessByPid(库里的 pid)` | 否，接口立即返回 |
| 调试运行 / 运行代码停止（`debugRun.stop`） | `service.TerminateProcessGroup(run.Process)`，在 `run.mu` 里调 | 否 |
| 修复 A 的残留清理 | `TerminateProcessGroup(process)` | 否，不推迟结算 |
| 面板关停（`StopAllRunningTasks`） | 任务进程与正在跑的钩子一起 `signalGroupTerm`；共用 `deadline := time.Now().Add(termGrace)`，逐组 `waitProcessGroupGone(pid, time.Until(deadline))`，到点还在的 `KillProcessGroup` | 是，最多 2 秒（Magisk 0） |
| 保持立即 KILL：`killRunningProcess`（onStart 发现停止早于进程登记）、`registerHookProcess` 里关停之后才起来的钩子、`ddp task stop`、系统命令行的停止 / 超时 / 清除、依赖安装取消、`runNodeABICommand` 的超时（Node ABI 重建、启动校验的 pip list）、`DebugClear` 的 `killIfRunning`、v1 调度器 | `KillProcessGroup` / `KillProcessByPid` | — |

- 保持立即 KILL 的理由：刚 exec 的进程来不及装收尾逻辑，关停路径上也不能再起一个 2 秒后才补刀的协程；`ddp` 是一次性进程，异步补刀协程活不到 5 秒后，要改就得阻塞 5 秒；系统命令行跑的是管理员临时敲的命令，不是任务。

**修复 A：主命令自己结束后清理它的进程组**

- 清理点只有一处，在 `runSingleCommand` 末尾、`return` 之前：
  `if !timedOut && plan.ShouldCleanupProcessGroup != nil && plan.ShouldCleanupProcessGroup() && processGroupAlive(process.Pid) { TerminateProcessGroup(process); onOutput(leftoverProcessCleanupNotice) }`。
  - 只覆盖任务主命令：conc 每个账号各走一次 `runSingleCommand`，desi 经 `RunCommandWithPlan` 也走它。钩子（任务前置 / 后置脚本、`task_before.sh`、`task_after.sh`、`extra.sh`）走 `runInlineScript` / `runHookScript`，**永远不清**（把钩子也纳入进来在 backlog）。
  - 超时分支已经对整组 TERM→KILL 过，所以要 `!timedOut`。
- 判定由 `runTask` 挂在 plan 上，每次执行读一次配置（保存后对之后开始的执行生效，不需要进 `reloadRuntimeConfigKeys`）：

  ```go
  plan.ShouldCleanupProcessGroup = nil // plan 挂在请求上，万一被复用，不能沿用上一次执行的判定
  if task.GetTaskType() != model.TaskTypeStartup && model.GetRegisteredConfigBool("cleanup_leftover_processes") {
      plan.ShouldCleanupProcessGroup = func() bool { return e.runStopKind(run) == runStopNone }
  }
  ```

  - 开机任务按**任务类型**豁免，不看触发方式（开机自动触发、手动点运行都不清）：这类任务常用来拉起常驻服务，每次开机只跑一次、不会累积。`GetTaskType()` 把空值归成 cron、非法值归成空串，都按非开机任务处理（与调度器口径一致）。
  - 闭包推迟到主命令结束那一刻才读停止种类：已被手动停止 / 面板正在关停的执行，组由停止路径负责（已经发过 TERM、安排了 KILL），这里再发会把第二个信号打进脚本的收尾，也不该写提示行。
  - 其它调用方（`ParseCommandExecutionPlan` 直接产出的 plan）是 nil，等于不清理。
- **不改变执行结果、不推迟结算**：退出码、成功 / 失败判定、重试、通知、日志正文都不变（只多一行提示）；补刀在协程里。
  推迟的话，「每分钟一次、还留了个忽略 TERM 的后台进程」的任务每次都会多占 5 秒并发槽位、拖住单实例闸门（下一次触发被跳过）、虚增 5 秒耗时。
- `setsid`、`start_new_session=True`、`os.setsid()` 起的进程不在这个组里，修复 A、停止、超时、关停都不碰它们——这是给用户的常驻出口。
- 总开关 `cleanup_leftover_processes`：tasks 组、默认 true。库里没有这一行时 `GetRegisteredConfigBool` 回落到 true，存量用户升级后默认生效；回退 v3.3.5 后这一行是未注册的键，网页兜底区与 APP 都不渲染、不回写。
  网页在「系统设置 → 任务运行」卡（`TaskExecutionCard.vue`，「检测脚本半路静默结束」之后），标题与说明取服务端 schema。
  🔴 **APP 的系统设置页按 schema 渲染，这个开关会自动出现在 APP 里并能保存**，所以说明文字必须对两端都适用（不能写「仅网页生效」之类）。改说明要重新生成演示站 fixtures（`go run ./cmd/gen-demo-fixtures`）。
- 提示行（常量 `leftoverProcessCleanupNotice`）：
  - 前缀 `[已结束残留的后台进程：`，已登记进 `panelMetaLinePrefixes`（成功的任务最容易出现这一行）；失败摘要只在 `normalizeTaskFailureLines` 里丢这一个前缀，不整体套 `isPanelMetaLine`（规则见「任务前后置钩子的环境变量回传」里那条登记约定）。
  - 前后各一个 `\n`：开头的让 conc 模式的 `[ENV#N] ` 前缀单独占一行、提示行本身从行首开始（`isPanelMetaLine` 按行首认）；结尾的免得后面的「=== 执行结束」粘在这一行末尾。
  - 只在判定为真**且** `processGroupAlive` 为真时写，组本来就空就不写；每次 `runSingleCommand` 最多一行（conc 下每个账号最多一行）；
    经 `onOutput` 进任务日志（TinyLog、日志文件、落库正文），不进面板日志；写 `onOutput` 而不是 `emitChunk`，日志被截断后这一行照样会出现。
  - 文案写「已发送结束信号，5 秒内不退出会被强制结束」而不是「已结束」：补刀协程随所在进程退出而消失。

**修复 B：先 SIGTERM，宽限期过后还在才 SIGKILL**

- 超时等的是「**整组退空**」，不是 outcome：outcome 到了只说明主进程和攥着输出管道的进程退了；只等 outcome 会放过不攥管道、又忽略 TERM 的后台进程，而超时之后修复 A 不再清理（`!timedOut`），「超时杀整组」就失效了。
  宽限对所有超时一视同仁，与任务类型、总开关都无关（开机任务超时同样整组终止）。
  组长被 TERM 杀掉时 waitErr 同样是「被信号终止」，`runSingleCommand` 里「必须先排除 timedOut」那条理由不变；被停止 / 超时的任务，「被信号终止」那行多数从 `killed(9)` 变成 `terminated(15)`。
- 🔴 **执行器认领了停止，就不再按库里的 PID 兜底**：`StopTask` 返回 true 表示执行器已经对登记的每个进程组发了 TERM、安排了 KILL（库里的 `pid` 是 onStart 写的，就是其中之一）。
  三个入口一律 `stopped := executor.StopTask(id)`，`if !stopped && pid > 0 { MarkManualStop(id); TerminateProcessByPid(pid) }`（Wrong / Correct 见「主动停止任务的 Aborted 独立状态」）。
  - 紧跟一刀立即 SIGKILL 会让宽限形同虚设；几毫秒内连发两次 TERM 会打断 trap 的收尾（Node 的 Puppeteer 处理函数会先清掉信号监听，第二个 TERM 按默认动作直接杀掉 node）。
  - 唯一的行为差异：允许多实例的任务一份在面板执行器里跑、另一份同时由 `ddp task run` 跑着时，在面板上点停止只停面板那份（以前两份都杀）。
- 面板关停：
  - **必须阻塞到 KILL 落地再返回**：Docker 停容器时 PID 1 一退出，内核会收走整个命名空间；但「重启面板」（退出码 1、容器不停）之后就没人再管这些进程组了，不等的话忽略 TERM 的进程成为孤儿。
  - 所有组**共用一个截止时间**：先退出的不占后面的时间；每组各等一遍会变成「忽略 TERM 的组数 × 2 秒」。
  - 只有真有组被 KILL 时才打 `N task process group(s) still running 2s after SIGTERM, killed`（`StopAllRunningTasks` 自己打，排在 `HaltSchedulerV2` 的 `interrupted N running task process(es) during panel shutdown` 之前），用来事后判断「关停为什么慢了 2 秒」。
  - 第二次调用（`ShutdownSchedulerV2` 开头会再走一遍 `HaltSchedulerV2`）时进程表已经空了，立刻返回。
  - 🔴 **Magisk 部署下关停宽限为 0**：`playwrightMagiskRuntime()`（认 `DAIDAI_MAGISK_SHELL_VERSION`，否则看 `IsMagiskModuleRuntime()`；`service.sh` 拉起面板时两个都会命中）为真时 `termGrace = 0`，
    `termGrace > 0 && signalGroupTerm(pid)` 短路，**一个 TERM 都不发**，所有组立即 `KillProcessGroup`（与 v3.3.5 一致），那行 `… killed` 日志也不会出现。
    原因：模块管理器动作按钮「停止」是 `kill -TERM` 面板、`sleep 2`、还在就 `kill -KILL`（`Magisk/action.sh`），`SignalStop` 最多 1 秒加 2 秒宽限装不下；面板先被 KILL 的话，忽略 TERM 的组没人补刀、成了孤儿。
    `action.sh` 是模块外壳，在线升级改不到，只能面板这边让步。手动停止、超时、调试运行停止用 `groupTermGrace`、不读这个 `termGrace`，在 Magisk 上照旧先 TERM、5 秒后 KILL。
  - 🔴 **被关停打断的执行一律按失败结算**：先 TERM 之后，trap 了 TERM、以 0 退出的脚本不能被记成成功，判据是主命令返回那一刻的 `runStopHalt`，见「执行器的「执行窗口」与 per-run 停止」。
  - 时间预算：`HaltSchedulerV2` ≈ 1 秒（`SignalStop`）+ 2 秒（宽限），`ShutdownSchedulerV2` 的结算等待从 4 秒缩到 3 秒，总账仍是 7.5 秒，见「停机收尾」。

**pid <= 1 保护**

- `signalGroupTerm` / `processGroupAlive` 对 pid <= 1 一律视为「组不在」；`KillProcessByPid` 与 `killGroupByPid` 对 pid <= 1 直接返回（`TerminateProcessByPid(1)` 发不出 TERM、转到 `KillProcessByPid(1)`，同样什么都不做）。
- 原因：按库里 PID 兜底的调用方只挡了 `<= 0`，而库里的 pid 来自任务记录，恢复备份、脏数据都可能让它变成 0 或 1。
  pid 0 → `Kill(0, …)` 打的是面板自己的进程组；pid 1 → `kill(-1, SIGKILL)` 打到面板有权限打的所有进程（容器里以 root 跑时连 nginx 和全部任务一起被杀，网页打不开、要重启容器才恢复；Magisk / 裸机上是整台机器）。
  `kill(-1)` 不打 PID 1 与调用者自己：面板进程本身还活着，它有权限打的其余进程全没了，所以很难一眼看出是面板干的。
- 🔴 这条保护**不能写用例验证**：去掉保护的突变下，用例自己就会执行 `kill(-1, SIGKILL)`，把跑测试那台机器上能杀的进程全杀掉。改这几个函数时只能靠代码评审守住，别删。

**Windows**

- `signalGroupTerm` / `processGroupAlive` 恒为 false，`killGroup` / `killGroupByPid` 是空实现：所有路径立即 `p.Kill()`（只结束直接子进程），宽限等待立刻结束，修复 A 永远不会触发。停止、超时、关停的行为与改动前一致。

### 4. Validation & Error Matrix

| 情形 | 结果 |
|---|---|
| 定时 / 手动任务的主命令退出，组里还有后台进程，开关开着 | 整组 TERM，5 秒后还在才 KILL；日志多一行 `[已结束残留的后台进程：…]`；退出码、成功判定、结束行都不变，结算不等宽限 |
| 组里本来就空 | 不清理、不写提示 |
| 开机任务（自动触发、手动点运行都算）/ 总开关关闭 | 不清理、不写提示 |
| `setsid` 起的后台进程 | 不在组里，任何路径都不碰 |
| 前置 / 后置脚本、`task_before.sh` 等钩子留下的后台进程 | 不清理（只覆盖主命令） |
| conc 模式两个账号各留一个后台进程 | 各清各的组；两行提示，各自前面有单独一行 `[ENV#N] ` |
| 已被手动停止 / 面板关停的执行，主命令收完尾正常退出 | 修复 A 不发第二个信号、不写提示（组归停止路径负责）；手动停止照旧结算为已终止 |
| 超时，脚本 trap 了 TERM | 在宽限内完成收尾；`[任务超时，已在 N 秒后终止]` 照旧；结算为失败 |
| 超时，组里有忽略 TERM 的进程（攥不攥输出管道都算） | 宽限到点整组 KILL；耗时 ≥ 超时 + 宽限；能正常结算 |
| 开机任务超时，组里有后台进程 | 同样整组终止（开机豁免只管修复 A） |
| 手动 / 批量 / 定时停止、调试运行停止 | 接口立即返回；组先收到 TERM，5 秒后还在才 KILL；手动停止结算 Aborted |
| `StopTask` 返回 false 且库里有 pid（例如 `ddp task run` 在别的进程里起的） | 打停止标记 + `TerminateProcessByPid`，同样先 TERM |
| 库里的 pid 是 0 或 1 | 不发任何信号 |
| 关停，组里有忽略 TERM 的进程 | 共用的 2 秒截止后统一 KILL，打一行 `N task process group(s) … killed` |
| 父进程 trap TERM 去关掉自己用 `setsid` 起的子进程（Puppeteer 默认 detached 的模式） | 超时、手动停止、关停三条路径都 0 残留（直接整组 KILL 的话父进程来不及，独立组里的子进程成孤儿） |
| Magisk 部署关停 | 不发 TERM，所有组立即整组 KILL |
| Windows | 全部立即 `p.Kill()`，修复 A 不触发 |
| 已接受：对忽略 TERM 的任务点了停止，5 秒内又重启面板 | `StopTask` 已把进程摘出进程表，关停时看不到它；补刀协程随旧进程退出：这个进程成孤儿（改前停止是立即 KILL，没有这个窗口） |
| 已接受：修复 A、各类停止起的补刀协程还没到点，面板或 `ddp task run` 就退出了 | 补刀随进程消失，忽略 TERM 的残留留下来（与改动前一样）；`ddp task run` 结算后几百毫秒就退出，这种情况几乎必然 |
| 已接受：主命令结束之后、结算之前（后置钩子阶段，每个最长 60 秒）点停止 | `StopTask` 对已经清理过的组再发一次 TERM；仍比改前的直接 SIGKILL 温和 |
| 已接受：组里只剩一个还没被回收的僵尸 | `processGroupAlive` 算「还在」：偶尔多写一行提示，或让宽限多等一小会儿，无害 |
| 管不到：`setsid` 逃出去的进程、Node 被 OOM 直接杀掉时留下的浏览器 | 修复 A / B 都碰不到（backlog） |

### 5. Good/Base/Bad Cases

- Good：每分钟一次、脚本里 `nohup … &` 留了个 20MB 子进程的任务，跑几天容器内存不再上涨；日志里每次一行提示，用户据此改成 `setsid nohup … &`、改成开机任务，或关掉开关。
- Good：超时、点停止、停容器时，Puppeteer 脚本收到 TERM 自己 `browser.close()`，独立进程组里的浏览器 0 残留。
- Base：没有后台进程的普通脚本，日志、退出码、耗时与改动前一致；Windows 上一切照旧。
- Bad：把清理放进 `pumpAndWait` 的正常分支（research 原型）—— 钩子也被清（违反「只覆盖主命令」）；不分任务类型、不看停止状态、直接 KILL、不写日志。
- Bad：把「TERM → 轮询 → KILL」塞进 `killGroup` / `KillProcessByPid`（research 原型）—— HTTP 停止接口卡 5 秒、关停每组串行各等 5 秒，依赖安装取消、系统命令行、onStart 竞态这些不该变的路径一起变了。
- Bad：`StopTask` 之后照旧无条件 `KillProcessByPid` 补刀；超时分支 TERM 后只等 outcome；修复 A 同步等宽限；关停对每个组各等一遍宽限；Magisk 上关停照样等 2 秒宽限。

### 6. Tests Required

**跑法**

- 进程组断言只在 Linux 成立：带 `//go:build linux` 的用例交叉编译到 WSL / CI 跑才算数，Windows 本机全绿不算。

```powershell
$env:PATH = "D:\软件目录\Go\bin;" + $env:PATH; cd server
$env:GOOS="linux"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
go test -c -o <scratch>\service.test ./service; go test -c -o <scratch>\handler.test ./handler
```

- WSL 里的命令写进 `.sh` 再 `wsl -u root -e sh <脚本>` 跑：从 PowerShell 直接传 `-test.v` 会被拆成 `-test` 与 `.v`。这组新用例不读仓库里的文件，二进制可以单独拷到 `/root` 下跑（WSL 默认用户不是 root）。
  和时序有关的用例至少 `-test.count=20` 跑一遍，看结果分布。
- 回归正则：`-test.run 'Leftover|Sigterm|Grace|Detached|Timeout|Shutdown|Halt|Stop|Executing|Settle|Prepared|Panic|PumpAndWait|DescribeTermination|Magisk'`。
  既有用例必须保持全绿：`TestShutdownInterruptNoticesAreStandalonePanelMetaLines`、`TestHaltKillsRunningPreHookAndSkipsNewOnes`、`TestHaltKillsRunningPostHook`、`TestShutdownSchedulerV2IsBoundedWhenRunIsStuck`、
  `TestManualStopStillRunsPostHooks`、`manual_stop_executing_window*`、`TestPumpAndWaitKeepsTailOutput`、`TestDescribeTerminationSignal*`
  （其中起的辅助子进程没有 `Setpgid`，`signalGroupTerm` 返回 false 后立即 KILL，行为与改前一致，但仍要实跑）。

**用例写法约定**（`service/task_process_cleanup_linux_test.go` 开头有同义注释）

- 脚本写进 `config.C.Data.ScriptsDir`，调 `executor.runTask`（异步的用 `runTaskAsync`，收尾时会 `StopAllRunningTasks` 并等结算）；每条用例一个独一无二的 `sleep 8641xx` 当标记，
  扫 `/proc/*/cmdline` 数存活的标记进程（`/proc/<pid>/stat` 状态 Z / X 不算）；断言一律「轮询到期限为止」（`cleanupTestPollLimit` = 3 秒），不用固定 sleep；
  `overrideTermGraces(t, group, shutdown)` 调短宽限，必须在 `runTaskAsync` 之前调（`t.Cleanup` 后进先出，还原要排在「等 runTask 跑完」之后）；`killMarkerProcessesOnCleanup` 收掉残留。
- 🔴 靠 trap 的用例，必须等脚本写出就绪标记（trap 装好之后才 `echo ready > xxx.flag`）、且 `HasRunningProcess` 为真才发信号（`waitScriptReady`）。
  只等 `HasRunningProcess` 不够：它在 onStart 时就为真，那时 bash 还在跑 bootstrap、trap 没装上，TERM 按默认动作直接杀掉 bash（以 143 退出，trap 不执行）。
- 🔴 放到后台的标记进程一律重定向输出（`… >/dev/null 2>&1 &`），专测「攥着管道」的除外：否则断言失败或做突变时，攥着管道的孙进程让 `pumpAndWait` 等不到 EOF，用例卡到 `go test` 的 10 分钟超时，而不是干脆地变红。
- 🔴 放到后台之后，要等 `/proc/$!/comm` 变成 `sleep` 才往下走（`waitBackgroundSleepSnippet`，最多约 5 秒）：否则清理可能发生在 fork / `nohup` / `setsid` 生效之前，
  分不清「被清理了」和「还没起来」，setsid 那条还可能在 `setsid()` 生效之前就被当成组内进程误伤。
- `nohup sleep N >/dev/null 2>&1 &` 之后换行再写下一条命令：`…&; echo spawned` 是 bash 语法错误（退出码 2）。
- 非交互 bash 里 `setsid sleep N … &` 的 `$!` 就是 sleep 本身（自成会话与进程组，整组 KILL 打不到它），模拟 Puppeteer 的 detached 子进程靠的就是这一点。

**用例**

| 用例 | 断言 |
|---|---|
| `TestLeftoverProcessCleanupKillsBackgroundSleep`（定时、手动两个子用例） | 结算后 3 秒内标记进程归零；日志恰好一行提示且被 `isPanelMetaLine` 认出；脚本输出与「退出码 0」结束行照旧；`RunSuccess` |
| `TestLeftoverProcessCleanupSparesSetsidProcess` | `setsid nohup sleep …` 起的还活着；没有提示行 |
| `TestLeftoverProcessCleanupSkipsStartupTasks`（触发方式 startup、manual 两个子用例） | 开机任务留下的进程还活着；没有提示行（`runTask` 不看触发方式，这对子用例守的是「将来改成按触发方式判定」时手动那条变红） |
| `TestLeftoverProcessCleanupHonorsSwitch` | 配置写成 false 后进程还活着；没有提示行 |
| `TestLeftoverProcessCleanupSilentWhenGroupEmpty` | 脚本只 `echo clean`：没有提示行；`RunSuccess` |
| `TestLeftoverProcessCleanupCoversConcAccounts` | `task leak.sh conc LEAK_ACC`，两个账号各留一个：都被清掉；两行提示，各自前面是单独一行的 `[LEAK_ACC#N] ` |
| `TestLeftoverProcessCleanupLeavesHooksAlone` | 前置脚本留下的进程还活着；没有提示行 |
| `TestLeftoverProcessCleanupDoesNotDelaySettlement` | 后台进程忽略 TERM、宽限调成 2 秒：`runTask` 一返回，标记进程**还活着**；2 秒 + 3 秒内归零（补刀生效） |
| `TestLeftoverProcessCleanupSkippedAfterManualStop` | 主命令 trap TERM 后以 0 退出、后台进程忽略 TERM，就绪后 `StopTask`：没有提示行；`RunAborted` |
| `TestTimeoutSendsSigtermBeforeSigkill` | Timeout=1，trap 里写收尾标记：就绪标记与收尾标记都在；超时提示照旧；`RunFailed` |
| `TestTimeoutKillsTermIgnoringGroupAfterGrace` | `trap '' TERM; sleep 864111`、宽限 500ms：耗时 ≥ 1.5 秒；进程已不在；正常结算为失败 |
| `TestTimeoutStillKillsWholeGroupForStartupTask` | 开机任务超时：后台标记进程归零 |
| `TestTimeoutKillsTermIgnoringBackgroundOutsideThePipe` | 后台进程重定向输出、忽略 TERM（标记 864112），前台 `sleep 30` 收到 TERM 就退：宽限过后后台进程同样归零。**唯一**能区分「等整组退空」与「只等 outcome」的用例 |
| `TestStopTaskReturnsImmediatelyAndSendsSigterm` | 就绪后 `StopTask` 200ms 内返回；3 秒内出现收尾标记；`RunAborted` |
| `TestStopTaskKillsTermIgnoringGroupAfterGrace` | 停止那一刻进程还活着（只发了 TERM），宽限过后归零；`RunAborted` |
| `TestShutdownTermsAllGroupsWithinSharedGrace` | 三个任务（A trap TERM 写标记，B、C 都忽略 TERM），共享宽限 1 秒：`StopAllRunningTasks` 返回 3、耗时在 [0.8, 1.5) 秒；A 写出了标记；B、C 归零。必须两个忽略 TERM 的组，「每组各等一遍」才会变成约 2 秒 |
| `TestShutdownSettlesTermTrappedZeroExitAsInterrupted` / `TestShutdownAfterMainCommandSucceededStillSettlesAsSuccess` | 断言点见「执行器的「执行窗口」与 per-run 停止」 |
| `TestShutdownKillsImmediatelyOnMagisk`（Magisk、其它部署两个子用例，`t.Setenv` 设 `DAIDAI_MAGISK_SHELL_VERSION`、清空 `DAIDAI_MAGISK_MODULE`） | Magisk：`StopAllRunningTasks` 500ms 内返回，主进程被 `killed(9)` 结束；其它部署：≥ 1.5 秒（等满 2 秒宽限），主进程被 `terminated(15)` 结束；两种最终都 0 残留 |
| `TestTermLetsParentCloseDetachedChild`（超时、手动停止、面板关停三个子用例） | 父进程 `trap 'kill $child; exit 0' TERM` 关掉自己 `setsid` 起的子进程：三条路径都 0 残留 |
| `TestStopTaskEndpointLetsScriptHandleSigterm`（`handler/task_stop_graceful_linux_test.go`） | `InitSchedulerV2` + `RunNow`，库里有 pid 且就绪后 `PUT /tasks/:id/stop`：1 秒内 200；3 秒内出现收尾标记（trap 里先 `sleep 0.3` 再写，否则「TERM 后紧跟 SIGKILL」的突变偶尔测不出来）；再等到注入的脚本凭据被吊销（这次执行彻底收完尾） |
| `TestDebugStopSendsSigtermFirst`（同一文件） | `POST /scripts/run-code`（bash，标记文件用 `t.TempDir()` 下的绝对路径），就绪后 `PUT /scripts/run/:run_id/stop`：1 秒内 200，`done=true`、`status=stopped`；收尾标记出现 |
| `TestSummarizeTaskSuccessOutputDropsBannersAndMeta`、`TestSummarizeTaskFailureOutputCondensesPythonTraceback`（末尾跟提示行的子用例）、`TestSummarizeTaskFailureOutputGenericContextSkipsLeftoverNotice`（跨平台） | 提示行取自真实常量（`strings.Trim(leftoverProcessCleanupNotice, "\n")`）：成功摘要滤掉它；失败摘要仍走 Python 摘要、通用摘要的「上下文」不取它，「被信号终止」那行照旧能当上下文 |
| `go test ./model`、`go test ./cmd/gen-demo-fixtures` | 分组中文名护栏通过；`configs.json` 与注册表逐字一致（只用生成器改，不手改） |

**突变清单**（每条改完跑对应用例必须变红，再改回）

- 删掉 `runSingleCommand` 里的清理段 → `KillsBackgroundSleep`、`CoversConcAccounts` 红；挪进 `pumpAndWait` 的正常分支 → `LeavesHooksAlone` 红。
- 去掉任务类型判断 → `SkipsStartupTasks` 红；去掉开关判断 → `HonorsSwitch` 红；判定闭包恒为 true → `SkippedAfterManualStop` 红；
  去掉 `processGroupAlive` 前置判断 → `SilentWhenGroupEmpty` 红；`TerminateProcessGroup` 换成同步等宽限 → `DoesNotDelaySettlement` 红。
- 超时 TERM 后只等 outcome → 只有 `TimeoutKillsTermIgnoringBackgroundOutsideThePipe` 红；超时、`StopTask`、关停任一路径改回直接 `KillProcessGroup` → `TermLetsParentCloseDetachedChild` 对应子用例红；关停每组各等一遍 → `SharedGrace` 红。
- 恢复 handler 里 `StopTask` 之后无条件的 `KillProcessByPid` → `TestStopTaskEndpointLetsScriptHandleSigterm` 红；去掉 Magisk 判断 → `ShutdownKillsImmediatelyOnMagisk` 的 Magisk 子用例红；
  `normalizeTaskFailureLines` 不丢提示行 → 两条失败摘要用例红。
- 没有用例守着的：批量停止、定时停止里的 `!stopped` 条件（照单个停止的写法核对）；pid <= 1 保护（见上，不能写）。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误（示意，research 原型的做法）：把「TERM、轮询 5 秒、KILL」塞进 killGroup，所有调用方一起阻塞——
// HTTP 停止接口卡 5 秒，关停对每个组串行各等 5 秒，依赖安装取消、系统命令行、onStart 竞态这些不该变的路径也跟着变了
func killGroup(p *os.Process) {
    syscall.Kill(-p.Pid, syscall.SIGTERM)
    for i := 0; i < 100 && syscall.Kill(-p.Pid, 0) == nil; i++ {
        time.Sleep(50 * time.Millisecond)
    }
    syscall.Kill(-p.Pid, syscall.SIGKILL)
}
```

```go
// 错误：超时 TERM 之后只等 outcome —— 不攥管道、又忽略 TERM 的后台进程活下来，而超时之后修复 A 不再清理
case <-timerC:
    signalGroupTerm(cmd.Process.Pid)
    select {
    case oc := <-outcomeCh:
        return oc.waitErr, true, normalizePumpReadError(oc.readErr)
    case <-time.After(groupTermGrace):
        KillProcessGroup(cmd.Process)
    }
```

```go
// 错误：关停对每个组各等一遍宽限（N 个忽略 TERM 的组就是 N × 2 秒）；Magisk 上也照样等宽限
for _, p := range victims {
    if signalGroupTerm(p.Pid) && !waitProcessGroupGone(p.Pid, shutdownTermGrace) {
        KillProcessGroup(p)
    }
}
```

#### Correct

```go
// 正确：超时阻塞到整组退空（最多 groupTermGrace），发不出 TERM 或到点还有进程才整组 KILL，最后照旧等 outcome
case <-timerC:
    if !signalGroupTerm(cmd.Process.Pid) || !waitProcessGroupGone(cmd.Process.Pid, groupTermGrace) {
        KillProcessGroup(cmd.Process)
    }
    oc := <-outcomeCh
    return oc.waitErr, true, normalizePumpReadError(oc.readErr)
```

```go
// 正确：关停时所有组先一起 TERM，共用一个截止时间逐组等，到点还在的统一 KILL；Magisk 部署宽限为 0、一个 TERM 都不发
termGrace := shutdownTermGrace
if playwrightMagiskRuntime() {
    termGrace = 0
}
var termed []*os.Process
for _, p := range victims {
    if termGrace > 0 && signalGroupTerm(p.Pid) {
        termed = append(termed, p)
    } else {
        KillProcessGroup(p)
    }
}
deadline := time.Now().Add(termGrace)
for _, p := range termed {
    if !waitProcessGroupGone(p.Pid, time.Until(deadline)) {
        KillProcessGroup(p)
        killed++
    }
}
```

```go
// 正确：修复 A 只在主命令自己结束、执行器判定要清、组里真有进程时才清；不阻塞，提示行经 onOutput 只进任务日志
if !timedOut && plan.ShouldCleanupProcessGroup != nil && plan.ShouldCleanupProcessGroup() && processGroupAlive(process.Pid) {
    TerminateProcessGroup(process)
    if onOutput != nil {
        onOutput(leftoverProcessCleanupNotice)
    }
}
```

---

## 场景：调试运行记录的上限与任务输出尾部（v3.3.6，#159 修复 C / D）

### 1. Scope / Trigger

- 触发：修改 `handler/script.go` 的 `debugRun` / `ScriptHandler`、`handler/script_runtime.go` 的 `storeRun` / `deleteRun` / `pruneDebugRunsLocked` / `debugRun.finish` / `debugRun.stop`、
  `handler/system_console.go` 的 `stopConsoleRun`；`service/script_runner.go` 的 `ScriptResult` 与 `runSingleCommand` / `runConcurrentCommand` 的输出处理；
  `service/task_executor.go` 里 `runTask` 的 `outputTail` / `appendOutputTail`，或新写「读任务输出做摘要 / 依赖识别」的代码时必须看本节。
- 背景（#159，research 实测）：
  - 修复 C：「调试运行 / 运行代码」的记录 `ScriptHandler.debugRuns` 没有条数上限也不过期，网页和 APP 从不调 DELETE（v1.0.0 起）。50 次调试 RSS 29→216MB，闲置加强制 GC 也不降。
  - 修复 D：常驻大输出的任务在堆里存两份输出：`runSingleCommand` 的 `outputBuilder`（经 `ScriptResult.Output` 返回，没人读）和 `runTask` 的 `outputCollector`（封顶约 97.7MiB，只用于摘要与依赖识别）。

### 2. Signatures

- `debugRun.finishedAt time.Time`：结束时刻，还在跑时为零值；`func (run *debugRun) finishedAtTime() time.Time`（在 `run.mu` 里读）。
- `ScriptHandler.order []string`：`debugRuns` 的插入顺序（照搬 `ConsoleHandler.order`）。
- `const maxDebugRuns = 20`、`const debugRunRetention = 30 * time.Minute`（`handler/script_runtime.go`）。
- `func (h *ScriptHandler) pruneDebugRunsLocked()`：调用方必须已持有 `h.mu`；照搬 `ConsoleHandler.pruneConsoleRunsLocked`，多一条过期规则。
- `type ScriptResult struct { ReturnCode int }`：v3.3.6 删掉了 `Output` / `Truncated`。
- `const outputTailLimit = 1 << 20`、`func appendOutputTail(buf []byte, chunk string) []byte`（`service/task_executor.go`，纯函数，可以单独测）。

### 3. Contracts

**调试运行注册表**

- 只在写入新记录时修剪：`storeRun` 持有 `h.mu`，先 `pruneDebugRunsLocked()`、再写入、再把 runID 追加进 `order`；**不起后台协程**。用户调试完离开后，下次再调试时内存就回到基线。
- 修剪规则：
  1. 先扫一遍 `order`：已经不在 map 里的残留项摘掉；`finishedAt` 非零、且距今**超过** 30 分钟的，从 map 和 `order` 里一起删掉；
  2. 再 `for len(h.debugRuns) >= maxDebugRuns`：从 `order` 最老的一头找第一条 `isDone()` 为真的淘汰；一条都淘汰不掉（全在跑）就停下，宁可暂时超过上限，也不死循环。写入之后最多 20 条（全在跑时除外）。
- 🔴 只淘汰已结束的：还在跑的记录一旦被删，用户正盯着的输出就断了，进程也失去停止入口。淘汰按**插入顺序**，不是结束顺序。
- 🔴 不变式「`Done` 为真 ⇔ `finishedAt` 非零」：`finish()`、`stop()`、`stopConsoleRun` 三处都在置 `Done = true` 的同一处、同一把锁（`run.mu`）里写 `finishedAt = time.Now()`。
  命令行自己的注册表不读这个字段，`stopConsoleRun` 写它只为守住不变式。测试里直接构造的 `Done: true` 记录 `finishedAt` 是零值：不会按时间过期，只会按条数被淘汰，正好符合预期。
- `deleteRun`（`DELETE /scripts/run/:run_id`）同时从 `order` 摘掉（照搬 `deleteConsoleRun`）；`run_id` 按毫秒时间戳生成，同一毫秒撞上同一个 id 时沿用原来的覆盖行为，`order` 不追加第二份。
- 对外语义：被修剪掉（过期或按条数淘汰）的记录再查日志返回 404「运行记录不存在」。修剪只发生在**下一次启动调试运行 / 运行代码时**，所以结束超过 30 分钟不等于一定 404
  （`apiData.ts` 的 scripts-run-logs 写的是「最早启动的已结束记录会先被清掉……清理发生在下一次启动调试运行 / 运行代码时」）。
  网页和 APP 在 done 之后不再轮询；MCP `run_script` 取完结果顺手 DELETE（`mcptools/tools_write.go` 的 `waitScriptRun`）。

**任务输出只留尾部**

- `ScriptResult` 只回报退出码：整段输出没有任何调用方读。`runSingleCommand` / `runConcurrentCommand` 里不再有 `outputBuilder`，`totalSize` / `truncated` 保留（它们决定 `onOutput` 什么时候开始截断）。
  **要读输出一律从 `onOutput` 收**，测试也一样（`TestRunCommandSupportsManagedDependencyCommand` 改成从 onOutput 收集）。
- `runTask` 里只有 `outputTail []byte`：`onOutputWithCollect` 在 `outputCollectorMu` 里 `outputTail = appendOutputTail(outputTail, chunk)`；每轮重试开头 `outputTail = nil`（不加锁：上一轮的进程已结束、读协程已排空）。
- 🔴 尾部不变量：`len(outputTail) <= 1MiB`；一定是完整输出的**后缀**；从某一行的行首开始（窗口起点落在行中间时，切到窗口里第一个换行之后，顺带避开被切开的多字节字符）。
  唯一例外：窗口里唯一的换行就是最后一个字节（一整行就超过 1MiB，例如压成一行的报错、`jq -c` 的输出）时不再往后切，保留这一行的后 1MiB——**不能切成空串**，否则失败摘要与依赖识别什么都拿不到。
  底层数组最多约为上限的 2 倍（append 扩容时只拷贝窗口里的字节）。
- 只读尾部的：失败摘要（`buildTaskFailureOutput` / `lastFailureOutput`）、成功摘要（`lastSuccessOutput`）、依赖自动识别（`detectAndInstallDeps`，取窗口里的第一个匹配）、运行时失败提示（`BuildRuntimeFailureHint`）。完整输出另有 TinyLog 与日志文件。
- 已接受：报错之后又输出了超过 1MiB，依赖识别与失败摘要拿不到那条报错。

### 4. Validation & Error Matrix

| 情形 | 结果 |
|---|---|
| 18 条已结束 + 2 条在跑，再写入 5 条已结束 | 总数保持 20；最老的已结束记录被淘汰；在跑的两条与最新一条都在 |
| 3 条结束于 31 分钟前、1 条 29 分钟前、1 条在跑，再写入一条 | 31 分钟前的 3 条被删；其余保留，`order` 同步只剩 3 条 |
| 23 条全在跑 | 全部保留，不死循环 |
| 结束超过 30 分钟、但没人再启动调试，查日志 | 仍能查到（修剪只在写入新记录时发生） |
| 输出不到 1MiB | 原样保留 |
| 3MiB 长短不一、夹带多字节字符的行 | 结果 ≤ 1MiB、是后缀、从行首开始、最后一行完整、UTF-8 合法 |
| 一次 `onOutput` 就灌进约 1.9MiB | 压到 1MiB 以内，仍从行首开始 |
| 单行 2MiB 加换行 | 非空，是这一行的后缀、以这一行的结尾收尾 |
| 报错之前已经输出约 1.5MiB | 运行时失败提示（如 ESM 兼容提示）照样出现在日志里 |

### 5. Good/Base/Bad Cases

- Good：每天在网页里调试几十次脚本，面板内存回到基线，不再越积越高。
- Base：正在跑的调试、刚结束的调试，查日志与改动前一致；常规大小的任务输出，失败 / 成功摘要、依赖识别都不变。
- Bad：起后台协程按时间清；淘汰运行中的记录；只在 `finish()` 写 `finishedAt`（被停止的记录永不过期）；`deleteRun` 不同步 `order`（`order` 越来越长）。
- Bad：`appendOutputTail` 只判 `i >= 0` 就往后切（设计原稿的写法）—— 一整行超过 1MiB 时窗口被切成空串。
- Bad：给 `ScriptResult` 加回 `Output`，或让摘要读完整输出 —— 又在堆里多存一份。

### 6. Tests Required

- `handler/script_runtime_test.go`（Windows 本机即可）：
  - `TestDebugRunRegistryEvictsOldestFinishedRunsBeyondLimit`、`TestDebugRunRegistryNeverEvictsRunningRuns`：断言点见上表。
  - `TestDebugRunRegistryExpiresRunsFinishedOver30Minutes`：先存进去、再把结束时刻拨回去（`storeRun` 每次写入都会修剪，先拨的话前几条会在后面的写入里提前被删）；断言 map 与 `order` 都剩 3 条。
  - `TestDebugRunFinishAndStopRecordFinishedAt`：`finish()`、`stop()` 都写下不早于调用前的结束时刻；`stop()` 停的对象用测试二进制自己起的子进程（`TestDebugRunStopHelperProcess`，没有 `Setpgid`，TERM 发不出去、立即 KILL），10 秒内退出。
  - 既有的 `TestConsoleStopUsesConsoleWording`、`TestConsoleRunRegistry*`、`TestDebugRunFinishDoesNotOverrideStoppedStatus` 保持全绿。
- `service/task_output_tail_test.go`：
  - `TestAppendOutputTailKeepsLastMiB`：四个子用例（小输入原样拼接；3MiB 多字节行；单块约 1.9MiB；单行 2MiB 加换行），断言点见上表。
  - `TestRunTaskFailureHintSurvivesOutputBeyondTail`：桩 `runCommandWithPlanFunc` 先吐约 1.5MiB 填充行、再吐 ESM 报错、返回 1，关掉 `auto_install_deps`：日志里有含「ESM 模块」的提示；`RunFailed`。
- 突变：去掉 `i < len(buf)-1` → 单行 2MiB 子用例红；改成只留前 1MiB → `FailureHintSurvivesOutputBeyondTail` 红。

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：只要找到换行就往后切。一整行超过 1MiB 时，窗口里唯一的换行就是最后一个字节，切完是空串
buf = buf[len(buf)-outputTailLimit:]
if i := bytes.IndexByte(buf, '\n'); i >= 0 {
    buf = buf[i+1:]
}
```

```go
// 错误：不看是否结束就按条数淘汰 —— 用户正盯着的调试输出断了，进程也失去停止入口
for len(h.debugRuns) >= maxDebugRuns {
    delete(h.debugRuns, h.order[0])
    h.order = h.order[1:]
}
```

#### Correct

```go
buf = append(buf, chunk...)
if len(buf) <= outputTailLimit {
    return buf
}
buf = buf[len(buf)-outputTailLimit:]
// 窗口里唯一的换行就在末尾（一整行超过 1MiB）时不再往后切，保留这一行的后 1MiB
if i := bytes.IndexByte(buf, '\n'); i >= 0 && i < len(buf)-1 {
    buf = buf[i+1:]
}
return buf
```

```go
// 正确：从最老的一头找第一条已结束的淘汰；一条都淘汰不掉就停下，宁可暂时超限
for len(h.debugRuns) >= maxDebugRuns {
    evicted := false
    for i, runID := range h.order {
        if !h.debugRuns[runID].isDone() {
            continue
        }
        delete(h.debugRuns, runID)
        h.order = append(h.order[:i], h.order[i+1:]...)
        evicted = true
        break
    }
    if !evicted {
        return
    }
}
```

---

## 场景：执行日志删除与执行趋势归档（v3.3.6，#158）

### 1. Scope / Trigger

- 触发：任何删除 `task_logs` 行的代码（**含测试代码**）；`service/task_log_archive.go`、`model/task_log_daily_stat.go`；`handler/system.go` 的 `Dashboard`；
  `backup_runtime.go` 的 `buildBackupManifest` / `restoreBackupManifest` 日志段、`backup_types.go` 的 `BackupPayload`；`testutil.SetupTestEnv` 的护栏回调；两份模型清单（`appboot.allModels`、`testutil.SetupTestEnv` 的 AutoMigrate）时必须看本节。
- 背景（#158）：今日 / 昨日 / 按天趋势每次请求都现场 COUNT `task_logs`，而清理是物理删除：日志保留 1 天时，趋势里只剩今天和昨天（昨天还被删掉一部分），更早的随清理消失。
  v3.3.6 改成任何删除都先在同一个事务里把要删的行按天并入新表 `task_log_daily_stats`，趋势 = 现存 + 计数表。状态写入点一处不改、不需要升级回填，时区语义与现算逐行一致。

### 2. Signatures

- `func DeleteTaskLogs(tx *gorm.DB, where string, args ...interface{}) (int64, []string, error)`（`service/task_log_archive.go`）：返回（删掉的行数, 这些行里非空的 `log_path`, 错误）。
- `model.TaskLogDailyStat`：`Day string`（`gorm:"primaryKey;size:10" json:"day"`）、`Success / Failed / Aborted / Other int64`（`gorm:"not null;default:0"`，json 同名小写），表名 `task_log_daily_stats`。
  DDL：``CREATE TABLE `task_log_daily_stats` (`day` text,`success` integer NOT NULL DEFAULT 0,`failed` integer NOT NULL DEFAULT 0,`aborted` integer NOT NULL DEFAULT 0,`other` integer NOT NULL DEFAULT 0,PRIMARY KEY (`day`))``，
  SQLite 自动建 `sqlite_autoindex_task_log_daily_stats_1`，不再加别的索引。两份模型清单都要登记（生产启动路径建出的 DDL 与 testutil 建出的逐字一致）；
  存量库升级只多一条 `CREATE TABLE`、不回填，连跑两次 AutoMigrate 不出现 `__temp` 重建。
- `model.TaskLogDeleteGuardKey = "daidai:task_logs_delete_archived"`：`DeleteTaskLogs` 给自己那条 Delete 打的 GORM Statement Settings 标记。放在 model 包，因为 testutil 不能 import service（成环）。
- 备份：`BackupPayload.TaskLogDailyStats []model.TaskLogDailyStat` `json:"task_log_daily_stats,omitempty"`，跟着「日志」勾选项走；老备份没有这个键 = nil。不加 `BackupSelection` 开关、不改 `Version`。
- 接口不变：`GET /system/dashboard` 的响应字段、类型、`daily_stats[].date`（`Format("01-02")`，APP 依赖）与 `range` 规则（1~90，否则 7）都不变，只是数字包含计数表。

### 3. Contracts

**`DeleteTaskLogs` 是删除 task_logs 行的唯一入口**

- 生产代码删 `task_logs` 一律经它；唯一的例外是 `restoreBackupManifest` 勾「日志」且非青龙时的 `deleteAll(tx, "task_logs")`（计数表整体换成备份里的，不需要并入）。
- 依次三步，任一步出错立即返回、由调用方的事务回滚（计数不会多出来）：
  1. 归档：`INSERT INTO task_log_daily_stats (day, success, failed, aborted, other) SELECT COALESCE(substr(created_at, 1, 10), ''), SUM(status IS 0), SUM(status IS 1), SUM(status IS 3), SUM(status IS NULL OR status NOT IN (0, 1, 3)) FROM task_logs WHERE (<where>) GROUP BY 1 ON CONFLICT(day) DO UPDATE SET …`（四列各自累加）。
     用 `IS` 不用 `=`：`status = 0` 遇到 NULL 得 NULL，SUM 会少算。`GROUP BY` 遇到空集不产出行，一行都没命中时不插全 0 的计数行。`substr` 只在 SELECT / GROUP BY 里，WHERE 仍是调用方的条件、照常走索引。
  2. 取路径：`tx.Model(&model.TaskLog{}).Where(where, args...).Where("log_path IS NOT NULL AND log_path <> ''").Pluck("log_path", &paths)`，必须排在删行之前（行一删 `log_path` 就查不回来了）。
  3. 删行：`tx.Set(model.TaskLogDeleteGuardKey, true).Where(where, args...).Delete(&model.TaskLog{})`，返回 `RowsAffected`。
- 调用方必须遵守：
  1. `tx` 必须是事务（`database.DB.Transaction` 的 tx，或 `database.DB.Begin()` 的 tx）。传 `database.DB` 是违约：三条语句各自提交，中途崩溃就会出现「同一批行既在计数里、又还在 task_logs 里」。
  2. 🔴 **它必须是这个事务里的第一条语句**：WAL 下事务「先读后写」，中间若有别的进程（`ddp`）提交过写，随后的写直接报 `database is locked (517)`，`busy_timeout` 救不了。归档的 `INSERT … SELECT` 是写语句，打头就一开始拿到写锁。
     只有 `DeleteTaskLogs` 内部的语句顺序有用例锁着，各调用点靠代码结构与评审守住。
  3. `where` 只能是代码里写死的 SQL 片段（`?` 占位），**禁止拼用户输入**：它被原样拼进原生 SQL。
  4. 事务提交之后再 `DeleteLogFilesForRecords(paths, logDir)` 删磁盘文件；回滚时文件不动。
- 列含义：`day` = 被删行 `created_at` **存储文本**的前 10 个字符（`YYYY-MM-DD`，写入时面板时区的日期），与仪表板「拿带偏移的文本按字典序比较」落到的是同一天，面板时区改过也一样；
  `success / failed / aborted` = 被删时 status 为 0 / 1 / 3 的行数；`other` = 被删时 status 为 NULL（老数据）、运行中或其它值的行，只计入「今日 / 昨日执行」总数，不进三条折线。
  一天一行，不按任务细分。表名刻意不含 `task_logs` 子串（#153 的执行计划护栏按这个子串截查询）。
- 🔴 不变量：每一行日志要么还在 `task_logs`，要么已经并入计数表**恰好一次**。状态写入点（结算、各类停止、重启恢复）一处不改：它们一律 `Updates` 现存行、不用 `Save`，
  行被删后影响 0 行，不会被「UPDATE 0 行就 INSERT」补回来；被删时还在运行的行计入 other，之后结算的 UPDATE 影响 0 行，不会再算一次。
- **统一不减**：任何删除都先并入，趋势只增不减；误跑出来的尖峰只能靠恢复备份消掉（「重置执行趋势」入口在 backlog）。升级前已经清掉的历史补不回来。

**调用点矩阵**

| # | 入口 | 位置 | `where` | 事务 | 出错时 | 磁盘文件 |
|---|---|---|---|---|---|---|
| D1 | 自动清理 worker（`CleanLogsByRetentionPolicy`） | `log_cleanup.go` 的 `cleanExpiredTaskLogRows` | `started_at < ? AND (status IS NULL OR status <> ?)`，按任务分组时再拼 ` AND task_id = ?` / ` AND task_id NOT IN ?` | 每组新开一个 | 打 `log cleanup: delete TaskLog records failed: …`，返回 (0, 0)，**不按 log_path 删文件** | 提交后按路径删 |
| D2 | `DELETE /logs/clean` → `CleanLogsOlderThan` | 同上 | 同 D1（不分组） | 新开一个 | 同 D1（接口照旧 200，删了 0 条） | 同 D1 |
| D3 | `DELETE /tasks/clean-logs` → `CleanLogsOlderThan` | 同上 | 同 D2 | 新开一个 | 同 D2 | 同 D1 |
| D4 | `DELETE /logs/:id` | `handler/log.go` 的 `Delete` | `id = ?` | 新开 | 500「删除日志失败」；删了 0 行照旧 404「日志不存在」 | 提交后按路径删 |
| D5 | `DELETE /logs/batch`、`POST /logs/batch-delete` | `handler/log.go` 的 `BatchDelete` | `id IN ?` | 新开 | 500「删除日志失败」；0 行照旧 200「已删除 0 条日志」 | 同上 |
| D6 | `DELETE /tasks/:id` | `task_mutate.go` 的 `Delete` | `task_id = ?` | 新开 | 忽略（与改动前一致，不拦流程） | 不取路径，随后 `RemoveTaskLogDirs` 整目录收走 |
| D7 | `PUT /tasks/batch`（`action=delete`）、`DELETE /tasks/batch/delete` | `task_batch.go` 的 `Batch` / `BatchDelete` | `task_id = ?` | 每个任务新开 | 同 D6 | 同 D6 |
| D8 | 订阅同步自动删失效任务 | `subscription.go` 的 `deleteSubscriptionTaskIfUnchanged` | `task_id = ?` | 现有事务的第一条 | `删除历史日志失败: %w`，整体回滚（计数一起回滚） | 不删（与改动前一致） |
| D9a | 恢复备份：勾「日志」且 `manifest.Source != "qinglong"` | `backup_runtime.go` 的 `restoreBackupManifest` | ——（`deleteAll` 两张表，再 upsert 写回计数） | 恢复事务的第一批语句 | 整体回滚 | `restoreLogFiles` 整目录替换（不变） |
| D9b | 恢复备份：只勾「任务」，或青龙导入（`Logs \|\| Tasks`） | 同上 | `1 = 1` | 恢复事务的第一条 | 整体回滚 | 不删（路径不用） |

- 只在出错时才有的行为变化：D4 / D5 数据库出错从「404 日志不存在 / 200 已删除 0 条」变成 500「删除日志失败」；D1~D3 删行失败时不再按 `log_path` 删文件
  （`CleanLogsOlderThan` / `CleanLogsByRetentionPolicy` 随后无条件跑的 `CleanOldLogs` 按 ModTime 扫盘照旧，老文件照样可能被扫走，与改动前一样）。
- 新增删除入口时：照 D4 的写法开事务、调 `DeleteTaskLogs`、提交后删文件，并在 `TestDashboardTrendSurvivesEveryLogDeletionEntry` 里补一个子用例。

**仪表板 `GET /system/dashboard`**

- 🔴 一次请求的**全部读取**（任务三计数、今日四计数、环境变量 / 订阅 / 今天之前的任务数、昨日四计数、最近 10 条、计数表、按天三计数）放进同一个 `database.DB.Transaction(func(tx *gorm.DB) error {…})`，读的是同一个 WAL 快照；
  **事务里一律用 `tx`**：连接池只有 1 个连接，事务里再碰 `database.DB` 会自己等自己。
  原因：删除可能排进任意两条查询之间——落在「读计数表」和「按天现算」之间，被删的行两边都数不到；反过来又会算两次。
  DSN 没有 `_txlock`，这是延迟 BEGIN 的只读事务，不拿写锁、不会 517；代价是请求期间独占唯一的连接（7 天视图约 30 条、90 天约 280 条覆盖索引计数，总工作量不变，只是不再被插队）。
  原有查询的顺序与条件一字不改（#153 的口径锁与执行计划护栏照旧成立）。
- 计数表只读一次，范围 `[min(today - (range-1), yesterday), today]`（`day` 字符串比较）：range=1 时昨天不在趋势里，「昨日执行」卡片照样要加。
- 加法：`today_logs += success + failed + aborted + other`（总数连 other 一起加，与现算口径一致）；`success_logs / failed_logs / aborted_logs` 加对应列；`yesterday_*` 同理；
  `daily_stats` 每天只加三列（other 不进折线）。成功率、较昨日、「近 N 天」合计都由前端从这些字段算，自动对齐，前端不改。
- 只看现存、**不加**计数表：侧栏角标 `logs_failed_today`（`system_badges.go`：与 Dashboard 一致的只是时间边界，数字可能不同——角标点进去要能看到那几条日志）、`/system/stats` 的 `logs.*`、`/tasks/:id/stats`。
- 执行计划护栏（`handler/task_log_query_plan_test.go` 的 `TestTaskLogHotQueriesUseQueryIndexes`）：截获条件是 SQL 含 `task_logs` **或** `task_log_daily_stats`；
  仪表板 7 天视图恰好 29 条 task_logs 计数、1 条最近 10 条、**1 条计数表查询**，后者的计划必须含 `SEARCH task_log_daily_stats USING INDEX sqlite_autoindex_task_log_daily_stats_1`。仪表板上新增查询要先在这里补断言。
- 已接受：未来日期的行被删后按自己的日期归档，不回到今天。

**备份 / 恢复**

- 备份勾「日志」时，计数（`day` 升序）与日志在 `buildBackupManifest` 的**同一个读事务**里取：两次读之间碰上一次清理的话，同一批行会既在日志里、又在计数里，恢复后重复计数。计数为空时这个键被 omitempty 省略。
- 恢复（`restoreBackupManifest`，这一段必须是恢复事务里的第一批语句）：

| 情形 | `task_logs` | `task_log_daily_stats` |
|---|---|---|
| 新备份，勾「日志」 | 清空后写回备份里的（任务对不上的照旧跳过，**不并计**） | 清空后写回备份里的 |
| 老备份（没有这个键），勾「日志」 | 同上 | 清零 |
| 勾了「任务」没勾「日志」（含老 JSON 备份） | 现存日志经 `DeleteTaskLogs(tx, "1 = 1")` 并入计数后清空（任务 id 会重排，外键不允许留着） | 原有 + 并入的 |
| 青龙导入，识别出任务或日志文件 | 同上（青龙勾「日志」时也照旧整表替换日志） | 同上，**不清零**：青龙包里没有呆呆面板的执行历史，清零只会丢信息 |
| 「任务」「日志」都没勾 | 不动 | 不动 |

- 写回计数用 upsert（`INSERT … VALUES (?, ?, ?, ?, ?) ON CONFLICT(day) DO UPDATE SET` 四列累加），不用 `Create`：手工拼过的备份里同一天出现两次时累加，不让整次恢复因主键冲突回滚。
- 青龙按 `manifest.Source == "qinglong"` 判断（只由 `backup_qinglong.go` 写成 qinglong）。青龙清单仍在有日志文件时设 `Logs = true`，它还要驱动 `restoreLogFiles` 拷青龙日志文件，`backup_qinglong.go` 不改。
- 新面板的备份在 v3.3.5 上恢复：未知键被忽略，计数丢失、日志照常。

**护栏**

- 语法扫描 `service/task_log_archive_test.go` 的 `TestTaskLogDeletesOnlyGoThroughDeleteTaskLogs`：按相对路径 `..` 遍历 `server/` 下全部非 `_test.go` 的 `.go` 文件（跳过 `data`、`testdata`、`node_modules` 与以 `.` 开头的目录，`server/data` 里可能放着用户脚本），`go/parser` + `go/ast`：
  1. `X.Delete(&model.TaskLog{...})` 恰好 1 处，且在 `DeleteTaskLogs` 里；
  2. `deleteAll(_, "task_logs")` 恰好 1 处，且在 `restoreBackupManifest` 里；
  3. 字符串字面量先 `strconv.Unquote` 解码（`"…\n…"` 里的 `\n`、`\t` 变回真的空白），再合并空白、去掉引号与反引号、忽略大小写，含 `delete from task_logs` 一律报错（多行原生 SQL、`"DELETE FROM\n task_logs"`、反引号包住的表名都算）；
  4. `X.Table("task_logs")` 调用一律报错（读也不行，用 `Model(&model.TaskLog{})`）。

  「恰好 1 处」防的是扫描规则失灵（目录走错、一个文件都没扫到）之后空跑通过。运行时拼出来的 SQL（`Exec("DELETE FROM " + table)`）扫不到，靠 `deleteAll` 那条规则与代码评审。
  在 WSL 里跑交叉编译的 Linux 测试二进制做全包回归时，二进制要放进整个工作区的副本里跑，否则扫不到文件、「恰好 1 处」会红——这是环境问题，不是回归。
- 测试期 GORM 回调：`testutil.SetupTestEnv` 在每次新建的 `database.DB` 上注册 `Callback().Delete().Before("gorm:delete").Register("testutil:task_logs_delete_guard", …)`：
  `db.Statement.Table == "task_logs"` 且没带 `TaskLogDeleteGuardKey` 时 `AddError("task_logs 只能经 service.DeleteTaskLogs 删除（先并入执行趋势计数，#158）")`，一行不删。
  标记只挂在 `DeleteTaskLogs` 那一条语句上，不串到同一事务里的下一条；别的表不受影响；原生 `Exec("DELETE …")` 不走 Delete 回调链，由扫描第 3 条兜。
- 🔴 **测试代码同样受约束**：测试里要删日志，也走 `DeleteTaskLogs`（自己包一层 `database.DB.Transaction`）或原生 `Exec`；直接 `Delete(&model.TaskLog{})`、`Delete(&logs)`、`Table("task_logs")…Delete` 会被回调拦下、用例红。

**已接受的边角**（发布说明与 issue 回复都不承诺）

- 删掉**运行中**的日志后（D4 / D5 不拦运行中的行）、执行结束之前面板重启：启动恢复（`markTaskLogInterrupted`）找不到运行中的行，会另建一行失败，同一次执行在趋势里算两次（other 1 + 失败 1）。
  用例钉住；修法（删日志接口拒删运行中的行）在 backlog，修的时候要同步改那条用例。
- 外部工具写入的「秒级精度、零点整、偏移串小于当前偏移」的行，可能落到相邻一天。
- 删任务（D6 / D7）删日志失败时照旧被忽略，随后删任务因外键失败也被忽略，接口照样回「删除成功」（既有问题，与计数无关）。
- 性能：批量删 2 万行约 +33%（1.05→1.40 秒），稳态一轮清理（约 360 行）约 +10ms；D6~D8 多一次 `log_path` 取值；恢复「只勾任务」/ 青龙导入多一次全表按天分组。

### 4. Validation & Error Matrix

| 情形 | 结果 |
|---|---|
| 任何入口删掉一批日志 | 计数表各列之和 = 删除数；range=1 / 7 / 30 的 today_* / yesterday_* / daily_stats 删前删后逐字段相等 |
| 两天 × status 0 / 1 / 2 / 3 / NULL | 按 `created_at` 前 10 位分成两行；2 与 NULL 进 other；同一天再删一批，ON CONFLICT 累加 |
| 删不存在的行 | 返回 0，不插计数行 |
| 东八区写入、改成 UTC 后再删 | 键 = 删前 `substr(created_at,1,10)`（`07:30+08:00` 那行在 UTC 下仍记在当天，不是前一天） |
| 删行失败（数据库错误、触发器 `RAISE(ABORT)`） | 整个事务回滚：计数表不变、日志行还在；D4 / D5 回 500「删除日志失败」；D1~D3 返回 (0, 0) 且不按 log_path 删文件 |
| D8 删任务失败或快照过期 | 日志与计数一起回滚 |
| 仪表板读取途中有清理 | 清理拿不到唯一的连接，等仪表板事务提交；前后读到的趋势相等 |
| range=1 | 「昨日执行」同样加上计数表 |
| 恢复：新备份勾日志 / 老备份勾日志 / 只勾任务 / 青龙导入 / 都没勾 | 见上面的恢复矩阵 |
| 手工拼的备份里 day 重复 | upsert 累加，恢复不回滚 |
| 测试里直接 `Delete(&model.TaskLog{})` | 回调报错、一行不删 |
| 生产代码新增 `Delete(&model.TaskLog{})`、含 `DELETE FROM task_logs` 的字符串、`Table("task_logs")` | 语法扫描红 |
| 删掉运行中的那行后、执行结束前重启 | 同一次执行算两次（已接受） |

### 5. Good/Base/Bad Cases

- Good：每分钟一次、日志只保留 1 天的任务，7 天趋势每天都是 1440 左右，不再只剩今天和昨天；删一条日志、删任务、批量清理之后，仪表板的数字都不变。
- Base：从不删日志的用户，计数表一直是空的，仪表板数字与改动前一致。
- Bad：在事务外先 Pluck 再 Delete、错误被吞（改动前 D4 / D5 的写法：失败时误报 404，或回「已删除 0 条」）；自己写 `Delete(&model.TaskLog{})`、不经 `DeleteTaskLogs`——执行趋势跟着变少，测试期护栏也会拦下。
- Bad：`DeleteTaskLogs` 之前先在同一事务里跑一条 SELECT（先读后写，撞上 ddp 的写就 517）；传 `database.DB` 而不是 tx。
- Bad：仪表板不开事务，或在事务里用 `database.DB`（自己等自己）；计数表下界只取趋势第一天（range=1 时昨日漏加）；今日 / 昨日总数漏加 other。
- Bad：恢复写回计数用 `Create`（重复 day 主键冲突、整次恢复回滚）；青龙导入清零计数；角标、`/system/stats` 也加计数表。

### 6. Tests Required

跑法：Windows 本机即可（没有进程组断言），CI 跑同一套。时区相关用例先 `service.ApplyPanelTimezone(model.DefaultPanelTimezone)` 并在 Cleanup 还原，跨零点照 `settledLocalToday`。

| 用例 | 文件 | 断言 |
|---|---|---|
| `TestDeleteTaskLogsArchivesEachStatusIntoItsStoredDay` | `service/task_log_archive_test.go` | 两天 × 0/1/2/3/NULL：返回行数 = 删除数、路径 = 非空那些；计数两行逐列正确；同一天再删累加；删不存在的行不插计数行 |
| `TestDeleteTaskLogsKeysByStoredPrefixAfterTimezoneChange` | 同上 | 归档键 = 删前 `substr(created_at,1,10)` |
| `TestDeleteTaskLogsRollsBackArchiveWhenDeleteFails` | 同上 | BEFORE DELETE 触发器：事务返回错误、计数 0 行、日志还在；同条件下直接调 `cleanExpiredTaskLogRows` 返回 (0, 0) 且按 log_path 那份文件还在（断言对象不用 `CleanLogsOlderThan`：它随后的 ModTime 扫盘会干扰） |
| `TestDeleteTaskLogsArchiveIsFirstStatementInTransaction` | 同上 | 自定义 GORM logger 挂在 `database.DB.Session` 上，事务里恰好 3 条语句，依次以 `INSERT INTO task_log_daily_stats` → `SELECT log_path FROM task_logs` → `DELETE FROM task_logs` 开头（去掉反引号再比） |
| `TestTaskLogDeletesOnlyGoThroughDeleteTaskLogs` | 同上 | 语法扫描四条 + 两个「恰好 1 处」 |
| `TestTaskLogDeleteGuardRejectsUnmarkedDelete` | 同上 | 护栏自测：三种没带标记的删法都报护栏错误、行数不变；`DeleteTaskLogs` 通过；标记不串到同一事务的下一条；别的表不受影响（护栏坏了这条会红，否则别的用例会空跑） |
| `TestDeleteSubscriptionTaskIfUnchangedArchivesLogsWithTask` | 同上 | D8：成功时计数 = 被删日志按天按状态的分布；删任务失败时计数 0 行、日志仍在（D8 没有 HTTP 入口，只在 service 层验证） |
| `TestBackupManifestCarriesTaskLogDailyStatsWithLogs`、`TestRestoreLogsReplacesTaskLogDailyStats`、`TestRestoreTasksOnlyArchivesExistingLogs`、`TestRestoreLegacyLogsBackupClearsTaskLogDailyStats`、`TestQingLongImportKeepsTaskLogDailyStats`、`TestBackupRestoreRoundTripDoesNotDoubleCount` | `service/backup_task_log_daily_stats_test.go` | 清单带计数（只勾任务时没有这个键）；勾日志整体替换（重复 day 合计）；只勾任务并入；老备份清零；青龙不清零；tgz 往返不重复计数 |
| `TestDashboardTrendSurvivesEveryLogDeletionEntry` | `handler/task_log_trend_history_test.go` | 表驱动 D1、D2、D3、D4、D5a、D5b、D6、D7a、D7b 九个子用例：每个入口真的删掉了行（D1~D3 的期望数在删前按同一条件现数，且 > 0）；计数表各列之和 = 删除数；range=1 / 7 / 30 三份仪表板删前删后逐字段相等 |
| `TestDashboardAddsArchivedCountsIncludingRangeOne` | 同上 | 直接插计数行：today_logs 含 other；不带 range、range=1 / 7 / 30 的昨日都加上（昨天那行带 other，抓「昨日漏加 other」）；range=7 看不到 7 天前那行、range=30 看得到；角标、`/system/stats`、`/tasks/:id/stats` 与插入前逐字节相同 |
| `TestDashboardReadsOneSnapshotWhileLogsAreDeleted` | 同上 | 在「读计数表」那条查询的回调里起 goroutine 清理：7 天合计删前 = 交错那次 = 清理后。回调里**只能等 goroutine 起来**，不能等清理进入事务（正确实现下清理要等仪表板事务提交才拿得到连接，等它会死锁）；去掉事务实测 12→0→12 |
| `TestDeletingRunningLogThenRecoveryCountsTwice` | 同上 | 已接受的例外：删运行中日志后今日 1（other）、失败 0；`RecoverAbandonedActiveTasks` 后今日 2、失败 1 |
| `TestLogDeleteFailureReturns500AndKeepsCounts` | 同上 | 触发器拦下删行：`DELETE /logs/:id`、`DELETE /logs/batch` 都是 500「删除日志失败」；计数 0 行；日志行还在 |
| `TestTaskLogHotQueriesUseQueryIndexes`（改） | `handler/task_log_query_plan_test.go` | 见上面的执行计划护栏 |
| `TestTaskLogDailyStatsAutoMigrateDoesNotRebuildTable` | `database/task_log_daily_stats_migration_test.go` | `SetupTestEnv` 后表已存在；再连跑两次 AutoMigrate 不出现 `task_log_daily_stats__temp`、除 SELECT / PRAGMA 外 0 条写语句；DDL 含 ``PRIMARY KEY (`day`)`` 与 `NOT NULL DEFAULT 0` |

- 必须仍绿：`system_dashboard_counts_test.go`（口径锁，一字不改）、`system_aborted_stats_test.go`、`system_badges_test.go`、`log_cleanup_regression_test.go`、`task_delete_scripts_test.go`、
  `log_retention_message_test.go` / `task_log_retention_test.go`、`TestBackupPayloadModelsHaveNoJSONHiddenFields` 与 `backup_*_test.go`。
- 突变（各做一次，改回后全绿）：去掉仪表板事务 → `ReadsOneSnapshot` 红；`DeleteTaskLogs` 去掉归档 → `SurvivesEveryLogDeletionEntry` 红；去掉 Delete 的标记 → 大面积红（护栏拦下）；
  计数表下界改回趋势第一天 → `IncludingRangeOne` 红；今日或昨日总数漏加 other → 同上；去掉青龙判断 → `TestQingLongImportKeepsTaskLogDailyStats` 红；upsert 换回 `Create` → `TestRestoreLogsReplacesTaskLogDailyStats` 红；
  取路径挪到归档之前 → `FirstStatementInTransaction` 红；`cleanExpiredTaskLogRows` 出错后照旧按路径删文件 → `RollsBackArchiveWhenDeleteFails` 红。
- 修改后至少运行：

```bash
cd server
go test ./service -run "DeleteTaskLogs|TaskLogDelete|ArchivesLogsWithTask|TaskLogDailyStats|RestoreTasksOnly|BackupRestoreRoundTrip" -count=1
go test ./handler -run "TestDashboard|TestDeletingRunningLog|TestLogDeleteFailure|TestTaskLogHotQueries|TestSystemDashboard|TestSystemBadges|TestTaskDelete|TestTaskBatch" -count=1
go test ./database -run "TaskLogDailyStats" -count=1
go test ./...
```

### 7. Wrong vs Correct

#### Wrong

```go
// 错误：事务外先 Pluck 再 Delete，错误被吞（失败时误报 404 /「已删除 0 条」）；
// 不经 DeleteTaskLogs，被删的行从执行趋势里消失，测试期护栏也会把这条 Delete 拦下
var paths []string
database.DB.Model(&model.TaskLog{}).Where("id IN ? AND log_path IS NOT NULL AND log_path <> ''", req.IDs).Pluck("log_path", &paths)
result := database.DB.Where("id IN ?", req.IDs).Delete(&model.TaskLog{})
service.DeleteLogFilesForRecords(paths, config.C.Data.LogDir)
```

```go
// 错误：仪表板的现算与读计数表不在同一个快照里，中间插进一次清理就少算（或多算）
database.DB.Model(&model.TaskLog{}).Where("created_at >= ? AND created_at < ? AND status = ?", day, nextDay, model.LogStatusSuccess).Count(&s)
database.DB.Where("day >= ? AND day <= ?", firstDay, todayKey).Find(&archivedRows)
```

#### Correct

```go
// 正确：开事务，DeleteTaskLogs 是事务里的第一条语句（where 是代码里写死的常量）；出错回 500，提交之后才删磁盘文件
var deleted int64
var paths []string
if err := database.DB.Transaction(func(tx *gorm.DB) error {
    var err error
    deleted, paths, err = service.DeleteTaskLogs(tx, "id IN ?", req.IDs)
    return err
}); err != nil {
    response.InternalError(c, "删除日志失败") // 已整体回滚：行与计数都没动，文件也不删
    return
}
service.DeleteLogFilesForRecords(paths, config.C.Data.LogDir)
```

```go
// 正确：一次请求的全部读取放进同一个只读事务，事务里一律用 tx；计数表只读一次，下界取 min(趋势第一天, 昨天)
_ = database.DB.Transaction(func(tx *gorm.DB) error {
    // ……原有的全部计数，database.DB 换成 tx，顺序与条件一字不改……
    first := today.AddDate(0, 0, -(rangeDays - 1))
    if yesterday.Before(first) {
        first = yesterday
    }
    var archivedRows []model.TaskLogDailyStat
    tx.Where("day >= ? AND day <= ?", first.Format("2006-01-02"), today.Format("2006-01-02")).Find(&archivedRows)
    // 今日 / 昨日总数加四列（含 other），三条折线与成功 / 失败 / 终止只加对应列
    return nil
})
```

## 订阅列表的 `next_pull_at`（v3.3.8，#163）

- `GET /subscriptions` 每项只在「启用 + 定时非空 + 整串能被 `ParseSchedule` 解析」时带 `next_pull_at`，其余不带这个键，前端显示 `-`。
- 用 `panelcron.NextRunTimes(schedule, 1)`，**不要换成 `NextRunTimesForExpressions`**：后者按行拆分，而订阅调度器 `AddOrUpdateJob` 把整串交给 `ParseSchedule`、不拆行，多行规则在调度器里注册失败、根本不会触发；拆行就会显示一个永远不会到的时间。任务的 `next_run_at` 用拆行版本，是因为任务本身支持多条定时规则。
- 演示站 `web/src/demo/adapter.ts` 的 `GET /subscriptions` 同口径现算，规则含换行就不给。

## 场景：任务级可用环境变量与只能发通知的脚本令牌（v3.4.0，#168）

### 1. 范围与触发

- 改 `model.Task` 的 `EnvScope` / `EnvGroups` / `EnvNames`、`handler/task_mutate.go`（Create / Update / Copy）、`handler/task_import_export.go`、
  `service/runtime_exec.go` 的 `BuildManagedRuntimeEnvMapWithScriptToken`、`middleware/auth.go` 的 `Claims` / `JWTAuth`、`handler/mcp_auth.go`，
  或**新增任何「执行任务」「解析令牌」的入口**时必须看本节。
- 这是「减少不必要的暴露」，**不是沙箱**：脚本与面板以同一系统用户运行（Docker 里是 root），仍能用 `ddp env list`、`ddp shell` 或直接读 `daidai.db` 拿到全部变量。
  界面、`docs/script-api.md`、发布说明、issue 回复都要如实写这一句。

### 2. 签名

- 列（tag 与 `EnsureColumns` 补列 SQL 逐字一致，见 `database-guidelines.md` 契约 S3）：`env_scope VARCHAR(16) NOT NULL DEFAULT 'all'`、`env_groups TEXT DEFAULT ''`、`env_names TEXT DEFAULT ''`（都是逗号拼接）。
- `model.Task.GetEnvScope()`：不是 `selected` 一律 `all`；`GetEnvGroups()` / `GetEnvNames()` 永远非 nil。
- `service.TaskRuntimeEnvScope(task) *RuntimeEnvScope`：只有 selected 返回非 nil。
- `BuildManagedRuntimeEnvMapWithScriptToken(workDir, scriptsDir, channelID, ttl, pythonVersion, envScope *RuntimeEnvScope)`：唯一传非 nil 的是 `task_executor.go` 的任务执行；
  `ddp python/shell`、脚本调试运行、run-code 传 nil（它们不属于任何任务）。
- `BuildNotifyHelperEnv(..., notifyOnly bool)` → `middleware.GenerateNotifyOnlyAccessTokenInfo`；``Claims.NotifyOnly bool `json:"notify_only,omitempty"` ``。
- 放行表 `middleware.notifyOnlyAllowedPaths`：`POST /api/v1/notifications/send` 与 `POST /api/notifications/send`（按 `c.FullPath()` 判）；403 文案常量 `middleware.NotifyOnlyTokenForbiddenMessage`。

### 3. 契约

- `ToDict` 与任务导出：`env_scope` 恒为 `all` 或 `selected`，`env_groups` / `env_names` 恒为数组。APP（v1.4.4 起）靠「`env_scope` 是字符串」判断面板支不支持，这个键不能省。
- 入参：数组或逗号字符串都收，去空去重；最终 scope 是 all 时两列清空，不留看不见的旧选择。
- Update 是局部更新：三项都不带就三列不碰（旧网页缓存、旧版 APP、开放 API、MCP 的 `update_task` 都不带）；任一出现时按「库里旧值 + 本次请求」算出最终值，三项一起写。
- 注入：selected 时只留「已启用，且变量名命中或分组有交集」的记录，再走原有的按名合并；两个列表都空 = 不注入任何用户变量。
  config.sh、青龙兼容变量、TZ、PATH、NO_PROXY、DAIDAI_* 照常注入；前后置脚本与全局 task_before.sh / task_after.sh / extra.sh 共用这一份变量。
- 任务日志：selected 时在「=== 开始执行 ===」之后写 `[环境变量] 已按任务设置只注入 N 个：…`（只写名字不写值，最多列 20 个）。
- 令牌：selected 时注入的 `DAIDAI_TOKEN` 是 notify-only。JWTAuth 在**撤销判定之后**拦（已吊销的照旧 401），MCP 的 `authenticateMCPBearer` 直接 403；
  MCP 工具回放也过 JWTAuth，两处都挡。内置 notify.py / sendNotify.js 只调发通知接口，不受影响。
- 备份直接序列化 `model.Task`，三列以字符串原样带上；老备份没有这三个键 → 恢复成 all。订阅自动建任务、青龙导入不设这三项，由 tag 默认值落成 all。
- MCP：`get_task` 原样带三项、`list_tasks` 带 `env_scope`；`create_task` / `update_task` 不开放（AI 不能改这类保护性设置，同契约 E1「重要」标记的先例）。

### 4. 校验与错误矩阵

| 情形 | 结果 |
|---|---|
| `env_scope` 是字符串但不是 all / selected | 400「env_scope 只能是 all 或 selected」，不写库 |
| Update 里 `env_scope` 不是字符串（含 null） | 同上 400；Create 里 null 视同没带，非字符串由绑定拦成 400「请求参数错误」 |
| `env_names` 里有不符合 `^[A-Za-z_][A-Za-z0-9_]*$` 的名字 | 400，点名是哪个 |
| `env_groups` / `env_names` 的元素不是字符串，或整体是对象 | 400「… 只能是字符串数组」 |
| Create 只带列表、没带 `env_scope` | 按 all，两列清空（不凭列表猜成「指定」） |
| 任务导入文件里某一条非法 | 那一条进 errors 并跳过，其余照常导入 |
| notify-only 令牌调发通知以外的任何接口（含 `GET /notifications`） | 403「该任务限定了可用环境变量，脚本令牌只能用来发通知」 |
| notify-only 令牌已被吊销 | 401「令牌已被撤销」 |

### 5. 正常 / 基础 / 错误用例

- 正常：任务指定分组 g1 + 变量名 C → 脚本只看到 g1 的变量和 C；拿 `DAIDAI_TOKEN` 调 `GET /envs`、`POST /tasks` 都是 403；发通知照常。
- 基础：老库升级后全是 all，注入与令牌与 v3.3.9 一致，只多三个键。
- 错误：只拦 `/envs`——operator 令牌还能 `POST /tasks` 建一个 all 的任务再 `PUT /tasks/:id/run`，限制形同虚设。

### 6. 必须有的测试

- `service/runtime_env_scope_test.go`：全部模式与改动前一致；按名、按分组（含一条变量多个分组）、并集；禁用变量不注入；两个列表都空；config.sh 与 DAIDAI_* 仍在；
  selected 时令牌带 notify_only、all 时不带；日志行 0 / 3 / 25 个；备份往返（含没有这三个键的老备份）。
- `handler/task_env_scope_test.go`：Create / Update / Copy / 导入导出的契约与 400 矩阵；`TestNotifyOnlyScriptTokenOnlyReachesNotificationSend` 走 `router.Setup` 完整路由，v1 与旧 `/api` 都要测；
  `TestMCPRejectsNotifyOnlyScriptToken`。
- `database/task_env_scope_migration_test.go`：老 tasks 表补列只发 3 条 ADD、不重建；连跑两次 AutoMigrate 不出现 `tasks__temp`。
- 端到端（v3.4.0 实测过）：真起一个实例，selected 与 all 两个任务跑同一个探针脚本，比对注入的变量，以及令牌调 `/envs`、`/tasks`、`/notifications/send` 的状态码。

### 7. 错误写法 vs 正确写法

```go
// 错误：只拦变量接口。operator 令牌还能新建一个「全部」范围的任务再运行它，照样拿到全部变量
if claims.EnvScoped && strings.HasPrefix(c.FullPath(), "/api/v1/envs") {
    c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "..."})
    return
}
```

```go
// 正确：notify-only 令牌只放行发通知这一条路由（v1 与旧 /api 各一份），其余一律 403
if claims.NotifyOnly && !(c.Request.Method == http.MethodPost && notifyOnlyAllowedPaths[c.FullPath()]) {
    c.JSON(http.StatusForbidden, gin.H{"error": NotifyOnlyTokenForbiddenMessage})
    c.Abort()
    return
}
```

- 以后新增解析令牌的入口（JWTAuth 与 MCP 之外），必须同样检查 `NotifyOnly`；新增执行任务的路径，必须传 `TaskRuntimeEnvScope(task)`；
  内置 helper 将来要调新接口时，先把它加进放行表，否则「指定」任务里的通知会被 403。

---

## 场景：公开入口的客户端 IP、限流与防重放（v3.4.4，安全审计 H-1 / H-2 / M-1 / M-2）

### 1. Scope / Trigger
- 新增或修改任何**免登录入口**（登录、触发链接、企业微信回调、开放 API 取令牌）、任何**按客户端 IP 判定**的逻辑（限流、锁定、白名单、登录日志）时适用。

### 2. Signatures
- 客户端 IP：`middleware.ResolveClientIP(c)`；可信代理默认 `netutil.DefaultTrustedProxyCIDRs()` = `127.0.0.1/32`、`::1/128`（旧默认含三个私网段，见 database-guidelines「一次性标记」）。
- 登录用户名口径：`handler.Login` 入口 `username := validator.SanitizeString(req.Username)`，锁定 / 验证码 / 失败计数 / 登录日志 / 清计数 / `authService.Login` 全用它；`loginAttemptUsername` 先 TrimSpace 再截断。
- 触发链接：`OpenAPIHandler.acceptTaskTrigger(taskID)`，同一任务受理后 10 秒冷却（`taskTriggerCooldown`），只在受理时写时间。
- 企业微信回调：验签后 `timestamp` 偏差 > 300 秒 → 401；`sha256(trigger.ID|timestamp|nonce|encrypt)` 与 `trigger.ID|MsgId` 两种指纹 10 分钟去重，命中回 200 空串不执行。

### 3. Contracts
- 容器内 nginx 经回环转发，`X-Forwarded-For` 最右侧是 nginx 看到的对端；宿主机反代后面的用户要自己把反代地址填进「可信代理 CIDR」，否则全员共用反代 IP。
- 触发链接验签后按 subject `app:<app_key>` 回查应用：不存在 / 已禁用 → 与票据无效同一口径 401「链接已失效」；任务 `Status == TaskStatusDisabled` → 403。

### 4. Validation & Error Matrix
- 私网对端 + 伪造 XFF → 取对端地址（白名单 403、日志记对端）
- `"admin "` 在 `"admin"` 被锁后 → 429 `account_locked`
- 触发链接 10 秒内第二次 → 429「操作太频繁」；垃圾票 → 401，**不占任何额度**
- 企业微信旧报文（>300s）→ 401；同报文 / 同 MsgId 重投 → 200 空串、不执行

### 5. Good/Base/Bad Cases
- Good：限流放在鉴权 / 验签**之后**，key 是已认证的资源（任务 ID）。
- Base：登录限流按 IP（`RateLimit(5, time.Minute)`），这是唯一可接受的「验证前按 IP 限流」，因为登录本身就是验证。
- Bad：在验签前用共享 key（IP）挂 `middleware.RateLimiter`——反代后面所有人共用一个 IP，匿名请求就能让全员一直 429。

### 6. Tests Required
- `middleware/client_ip_default_trust_test.go`：默认配置下 192.168 / 10 / 172.17 对端伪造 XFF 不生效；回环对端取 XFF 最右侧非可信地址。
- `handler/auth_login_hardening_test.go`：带空白的用户名共用锁定。
- `handler/task_trigger_hardening_test.go`：禁用任务 403、禁用 / 删除应用 401、垃圾票不限流、被拒的点击不延长冷却（用 `SetTaskTriggerClockForTest` 拨表，不真睡）。
- `handler/wecom_callback_replay_test.go`：重放只执行一次、换 nonce 同 MsgId 只执行一次、±310 秒 401。用例的 timestamp 必须取当前时间、nonce / MsgId 每次递增，否则会撞去重表。

### 7. Wrong vs Correct
#### Wrong
```go
// middleware.RateLimiter 是滑动窗口：被拒的请求也会刷新 lastSeen，持续刷就永远不放行
if !h.triggerByIP.Allow(middleware.ResolveClientIP(c)) { /* 429 */ }
subject, err := trigticket.Verify(...)
```
#### Correct
```go
subject, err := trigticket.Verify(...) // 先验签，垃圾票零成本
// ……应用 / 开关 / 任务状态校验……
if !h.acceptTaskTrigger(uint(taskID)) { /* 429，只有受理才记时间 */ }
```