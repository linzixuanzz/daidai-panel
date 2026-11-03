package handler

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/response"
	"daidai-panel/service"

	"github.com/gin-gonic/gin"
)

type depLogBroadcaster struct {
	mu   sync.RWMutex
	subs map[chan string]struct{}
}

var (
	depLogStreams   = make(map[uint]*depLogBroadcaster)
	depLogStreamsMu sync.RWMutex
	depOperations   = make(map[uint]context.CancelFunc)
	depOpsMu        sync.Mutex

	dependencyInstallRunner = installDependency
)

// defaultDependencyOperationTimeout 是 dependency_install_timeout_minutes 读不出来时的兜底值，
// 与该配置项的注册默认值保持一致。真正生效的阈值一律走 resolveDependencyOperationTimeout()。
const defaultDependencyOperationTimeout = 20 * time.Minute

// dependencyOutputWaitDelay 是依赖命令本体退出之后，输出管道还被别的进程攥着时最多再等的时长。
//
// 依赖命令的输出接在 io.Pipe 上（见 runCmdWithSSEThen），Wait 要等所有拿着管道写端的进程都关掉它才返回。
// 命令拉起的后台进程（守护进程、postinst 起的服务、`xxx &`）会继承这根管道：命令正常退出时没人去杀它们，
// 取消 / 超时时整组杀也杀不到逃出了进程组的（setsid），Windows 上更是只杀得到直接子进程。
// 不设上限的话，这条依赖会一直停在「安装中」，还连带攥着 apt / npm 的包操作锁。
// 到点后 os/exec 强制关掉管道，Wait 返回 exec.ErrWaitDelay（处理见 runCmdWithSSEThen 的 waitCommand）。
// 与强制卸载、Node ABI 重建取同一个值；抽成变量只为测试能调短。
var dependencyOutputWaitDelay = 10 * time.Second

// resolveDependencyOperationTimeout 读取用户配置的依赖操作超时。
// 配置项注册在 model 层并带 5-720 分钟的区间校验，这里只对「数据库里存着历史越界值」
// 这一种情况再兜一次底，避免非法值把超时变成 0（等于立刻杀进程）。
func resolveDependencyOperationTimeout() time.Duration {
	minutes := model.GetRegisteredConfigInt("dependency_install_timeout_minutes")
	if minutes < 5 || minutes > 720 {
		return defaultDependencyOperationTimeout
	}
	return time.Duration(minutes) * time.Minute
}

func getOrCreateBroadcaster(id uint) *depLogBroadcaster {
	depLogStreamsMu.Lock()
	defer depLogStreamsMu.Unlock()
	if b, ok := depLogStreams[id]; ok {
		return b
	}
	b := &depLogBroadcaster{subs: make(map[chan string]struct{})}
	depLogStreams[id] = b
	return b
}

func removeBroadcaster(id uint) {
	depLogStreamsMu.Lock()
	defer depLogStreamsMu.Unlock()
	if b, ok := depLogStreams[id]; ok {
		b.mu.Lock()
		for ch := range b.subs {
			close(ch)
		}
		b.mu.Unlock()
		delete(depLogStreams, id)
	}
}

func registerDepOperation(id uint, cancel context.CancelFunc) {
	depOpsMu.Lock()
	defer depOpsMu.Unlock()
	depOperations[id] = cancel
}

func unregisterDepOperation(id uint) {
	depOpsMu.Lock()
	defer depOpsMu.Unlock()
	delete(depOperations, id)
}

func cancelDepOperation(id uint) bool {
	depOpsMu.Lock()
	cancel, exists := depOperations[id]
	depOpsMu.Unlock()
	if !exists {
		return false
	}

	cancel()
	return true
}

func (b *depLogBroadcaster) subscribe() chan string {
	ch := make(chan string, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *depLogBroadcaster) unsubscribe(ch chan string) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
}

func (b *depLogBroadcaster) broadcast(line string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- line:
		default:
		}
	}
}

func (b *depLogBroadcaster) done() {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- "\x00DONE":
		default:
		}
	}
}

type DepsHandler struct{}

func NewDepsHandler() *DepsHandler {
	return &DepsHandler{}
}

func normalizeDependencyPythonVersion(depType, raw string) (string, error) {
	if depType != model.DepTypePython {
		return "", nil
	}
	return service.NormalizePythonVersionStrict(raw)
}

func dependencyPythonInstallVersions(depType string) []string {
	if depType != model.DepTypePython {
		return []string{""}
	}
	return service.SupportedPythonVersions()
}

func (h *DepsHandler) List(c *gin.Context) {
	depType := c.DefaultQuery("type", "nodejs")

	validTypes := map[string]bool{
		model.DepTypeNodeJS: true,
		model.DepTypePython: true,
		model.DepTypeLinux:  true,
	}
	if !validTypes[depType] {
		response.BadRequest(c, "无效的依赖类型")
		return
	}

	var deps []model.Dependency
	query := database.DB.Where("type = ?", depType)
	if depType == model.DepTypePython {
		pythonVersion, err := normalizeDependencyPythonVersion(depType, c.Query("python_version"))
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		query = query.Where("COALESCE(NULLIF(python_version, ''), ?) = ?", service.LegacyPythonVersion(), pythonVersion)
	}
	query.Order("created_at DESC").Find(&deps)

	data := make([]map[string]interface{}, len(deps))
	for i, d := range deps {
		data[i] = d.ToDict()
	}

	// failed_by_type 是跨类型的失败数汇总，专门给依赖页三个类型页签上的「失败 N」用。
	//
	// 【为什么要额外下发，而不是让前端数 data】
	// 上面的 data 只含当前请求的那个类型（Python 还只含当前版本），前端数出来的失败数
	// 天然只是「当前标签页」的。而侧栏「依赖管理」角标（server/handler/system_badges.go 的
	// deps_failed）是不分类型、不分版本的全表 status='failed' 计数，两个数字对不上，
	// 用户得挨个切三个标签页才能凑出角标那个数。
	//
	// 【为什么 Python 不跟着 python_version 过滤】
	// 同理：只有让 nodejs + python + linux 三个数之和恰好等于全表失败数，页签上的数字
	// 才能和侧栏角标对上。所以这里刻意不复用上面的版本过滤，Python 跨所有版本一起统计。
	//
	// 查询用一条 GROUP BY type 完成，不为每个类型各查一次。
	failedByType := map[string]int64{
		model.DepTypeNodeJS: 0,
		model.DepTypePython: 0,
		model.DepTypeLinux:  0,
	}
	type depFailedCountRow struct {
		Type  string
		Total int64
	}
	var failedRows []depFailedCountRow
	database.DB.Model(&model.Dependency{}).
		Select("type, COUNT(*) AS total").
		Where("status = ?", model.DepStatusFailed).
		Group("type").
		Scan(&failedRows)
	for _, row := range failedRows {
		// 只回填三个已知类型，历史脏数据里若有别的 type，忽略即可，
		// 免得凭空多出一个前端不认识的键。
		if _, ok := failedByType[row.Type]; ok {
			failedByType[row.Type] = row.Total
		}
	}

	response.Success(c, gin.H{"data": data, "total": len(data), "failed_by_type": failedByType})
}

func (h *DepsHandler) Create(c *gin.Context) {
	var req struct {
		Type          string   `json:"type" binding:"required"`
		Names         []string `json:"names" binding:"required"`
		PythonVersion string   `json:"python_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	validTypes := map[string]bool{
		model.DepTypeNodeJS: true,
		model.DepTypePython: true,
		model.DepTypeLinux:  true,
	}
	if !validTypes[req.Type] {
		response.BadRequest(c, "无效的依赖类型")
		return
	}
	created := []map[string]interface{}{}
	skipped := 0
	for _, name := range req.Names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if strings.ContainsAny(name, ";|&`$(){}") {
			continue
		}
		// 以 - 开头的名字会被 pip / npm / apk / apt 当成选项（如 --index-url、--registry 换掉安装源），
		// 与上面的非法字符同口径直接跳过，不建记录、不安装。
		if strings.HasPrefix(name, "-") {
			continue
		}

		for _, pythonVersion := range dependencyPythonInstallVersions(req.Type) {
			// 同名已安装 / 安装中 / 排队中的跳过、不重复安装。查重口径见 findExistingDependency。
			if existing, exists := findExistingDependency(req.Type, name, pythonVersion, dependencyActiveStatuses...); exists {
				// issue #166：Python 查重会剥掉 extras，httpx[http2] 会命中已装的 httpx 而被「已存在跳过」，
				// h2 永远装不上。命中的是已装好的记录、且它的写法没覆盖本次的 extras 时，不跳过：
				// 把这条记录原地改名成本次写法、置为安装中、重新安装 —— 仍是一包一条，不新建记录。
				// 命中安装中 / 排队中的照旧跳过：那边的安装还没结束，这时改名重装会和它抢同一条记录。
				if req.Type == model.DepTypePython && existing.Status == model.DepStatusInstalled && !service.PythonExtrasCovered(existing.Name, name) {
					// 新名字是「旧写法 + 本次 extras」合并的结果，不是整串换成本次写法：旧记录里的版本锁定
					// （httpx==0.27.0）和已有 extras（httpx[socks]）在重建容器重装时还要用，整串替换会丢。
					// 记录名和这次交给 pip 的写法用同一个，装的就是登记的。
					// 用独立变量，不能覆盖循环里的 name：下一个 Python 版本还要拿用户原始写法去查重、合并。
					upgradedName := service.MergePythonSpecExtras(existing.Name, name)
					if err := database.DB.Model(&existing).Updates(map[string]interface{}{
						"name":   upgradedName,
						"status": model.DepStatusInstalling,
						"log":    "",
					}).Error; err != nil {
						continue
					}
					existing.Name = upgradedName
					existing.Status = model.DepStatusInstalling
					existing.Log = ""
					created = append(created, existing.ToDict())

					go dependencyInstallRunner(existing.ID, req.Type, upgradedName)
					continue
				}
				skipped++
				continue
			}

			dep, err := createDependencyRecord(req.Type, name, pythonVersion, model.DepStatusInstalling, "")
			if err != nil {
				continue
			}
			created = append(created, dep.ToDict())

			go dependencyInstallRunner(dep.ID, req.Type, name)
		}
	}

	message := fmt.Sprintf("已提交 %d 个依赖安装", len(created))
	if req.Type == model.DepTypePython && len(created) > 0 {
		message = fmt.Sprintf("已提交 %d 个 Python 版本依赖安装", len(created))
	}
	if skipped > 0 {
		message = fmt.Sprintf("%s，已存在跳过 %d 个", message, skipped)
	}
	response.Created(c, gin.H{
		"message": message,
		"data":    created,
	})
}

// dependencyActiveStatuses 是「同名已存在就不再重复提交」认的三种状态。
var dependencyActiveStatuses = []string{model.DepStatusInstalled, model.DepStatusInstalling, model.DepStatusQueued}

// findExistingDependency 按「类型 + 名称（+ Python 版本）」找一条已登记的依赖记录，statuses 为空表示不限状态。
// POST /deps 与 Playwright 一键安装共用这一份查重口径（#142 抽出）。
//
//   - Python 按 PEP 503 归一化键匹配（忽略大小写 / 分隔符差异，requests==2.31.0 也算 requests）；
//   - nodejs / linux 只按名称精确匹配，不套 PEP 503 —— 各生态的包名归一规则不同（npm 区分大小写、
//     apt 包名带冒号架构后缀），硬套会把不同的包判成同一个，属于误伤。
//     这里也刻意不给 dependencies 加 DB 唯一索引，理由同上。
//
// 同名多条（历史遗留）时 nodejs / linux 取 id 最大的一条，即最近登记的那条。
func findExistingDependency(depType, name, pythonVersion string, statuses ...string) (model.Dependency, bool) {
	if depType == model.DepTypePython {
		return service.FindExistingPythonDependency(name, pythonVersion, statuses...)
	}

	query := database.DB.Where("type = ? AND name = ?", depType, name)
	if len(statuses) > 0 {
		query = query.Where("status IN ?", statuses)
	}
	// 用 Limit(1).Find 而不是 First：查不到是常态，First 会让 GORM 打一条 record not found 日志。
	var found []model.Dependency
	if err := query.Order("id DESC").Limit(1).Find(&found).Error; err != nil || len(found) == 0 {
		return model.Dependency{}, false
	}
	return found[0], true
}

// createDependencyRecord 登记一条新的依赖记录；POST /deps 建成 installing 后立刻起安装协程，
// 一键安装建成 queued 后交给顺序队列。
func createDependencyRecord(depType, name, pythonVersion, status, logText string) (model.Dependency, error) {
	dep := model.Dependency{
		Type:          depType,
		Name:          name,
		PythonVersion: pythonVersion,
		Status:        status,
		Log:           logText,
	}
	if err := database.DB.Create(&dep).Error; err != nil {
		return model.Dependency{}, err
	}
	return dep, nil
}

func (h *DepsHandler) Delete(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var dep model.Dependency
	if err := database.DB.First(&dep, id).Error; err != nil {
		response.NotFound(c, "依赖不存在")
		return
	}

	if dep.Status == model.DepStatusQueued || dep.Status == model.DepStatusInstalling || dep.Status == model.DepStatusRemoving {
		response.BadRequest(c, "依赖正在处理中")
		return
	}

	if c.Query("force") == "true" {
		database.DB.Delete(&dep)
		go forceUninstallDependency(dep.Type, dep.Name, dep.PythonVersion)
		response.Success(c, gin.H{"message": "强制卸载中"})
		return
	}

	database.DB.Model(&dep).Update("status", model.DepStatusRemoving)

	go uninstallDependency(dep.ID, dep.Type, dep.Name, dep.PythonVersion)

	response.Success(c, gin.H{"message": "卸载中"})
}

func (h *DepsHandler) BatchDelete(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		response.BadRequest(c, "请求参数错误")
		return
	}

	var deps []model.Dependency
	database.DB.Where("id IN ? AND status NOT IN ?", req.IDs, []string{model.DepStatusQueued, model.DepStatusInstalling, model.DepStatusRemoving}).Find(&deps)

	for _, dep := range deps {
		database.DB.Delete(&dep)
		go forceUninstallDependency(dep.Type, dep.Name, dep.PythonVersion)
	}

	response.Success(c, gin.H{"message": fmt.Sprintf("已提交 %d 个依赖卸载", len(deps))})
}

func (h *DepsHandler) GetStatus(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var dep model.Dependency
	if err := database.DB.First(&dep, id).Error; err != nil {
		response.NotFound(c, "依赖不存在")
		return
	}

	response.Success(c, gin.H{"data": dep.ToDictWithLog()})
}

func (h *DepsHandler) LogStream(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var dep model.Dependency
	if err := database.DB.First(&dep, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "依赖不存在"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	if dep.Log != "" {
		for _, line := range strings.Split(dep.Log, "\n") {
			if line != "" {
				fmt.Fprintf(c.Writer, "data: %s\n\n", line)
			}
		}
		c.Writer.Flush()
	}

	if dep.Status != model.DepStatusInstalling && dep.Status != model.DepStatusRemoving {
		fmt.Fprintf(c.Writer, "event: done\ndata: %s\n\n", dep.Status)
		c.Writer.Flush()
		return
	}

	depLogStreamsMu.RLock()
	b, exists := depLogStreams[uint(id)]
	depLogStreamsMu.RUnlock()

	if !exists {
		fmt.Fprintf(c.Writer, "event: done\ndata: %s\n\n", dep.Status)
		c.Writer.Flush()
		return
	}

	sub := b.subscribe()
	defer b.unsubscribe(sub)

	// pip 现场编译 wheel（例如 opencv）时可以几十分钟一行输出都没有，
	// 原来的「静默 5 分钟就发 done」会让前端以为任务结束了，而进程其实还在跑。
	// 改成周期性心跳注释行（SSE 规范里以 : 开头的行会被客户端忽略），只用来保活连接；
	// 真正的结束仍然只由 \x00DONE 或订阅通道关闭来决定。
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	// 硬上限只是防止 broadcaster 泄漏导致连接永不释放，正常路径不会走到。
	// 必须比依赖任务本身的超时更长，否则又会退化成「任务还在跑就断流」。
	hardDeadline := time.After(resolveDependencyOperationTimeout() + 5*time.Minute)

	ctx := c.Request.Context()
	for {
		select {
		case line, ok := <-sub:
			if !ok {
				fmt.Fprintf(c.Writer, "event: done\ndata: closed\n\n")
				c.Writer.Flush()
				return
			}
			if line == "\x00DONE" {
				var latest model.Dependency
				database.DB.First(&latest, id)
				fmt.Fprintf(c.Writer, "event: done\ndata: %s\n\n", latest.Status)
				c.Writer.Flush()
				return
			}
			fmt.Fprintf(c.Writer, "data: %s\n\n", line)
			c.Writer.Flush()
		case <-ctx.Done():
			// 面板关停时请求 ctx 会被取消（main 里的 BaseContext）：先发 done:reconnect 再收流，与实时日志同口径。
			// 不发的话网页依赖页只认 done / onError，干净 EOF 时会静默卡在「安装中」；
			// 网页依赖页对 reconnect 与 timeout 都提示「日志流已断开，任务可能仍在进行」。
			// 不能发 timeout：APP 的依赖页（v1.0.2 起）把 reconnect 以外的 done 都当成完成，会显示绿色「安装完成」；
			// reconnect 让各版本 APP 重连一次，面板没起来就显示红色「连接已断开」，与面板被强杀时一样。
			// 客户端自己断开时这次写入只是写失败。
			fmt.Fprintf(c.Writer, "event: done\ndata: reconnect\n\n")
			c.Writer.Flush()
			return
		case <-heartbeat.C:
			fmt.Fprintf(c.Writer, ": ping\n\n")
			c.Writer.Flush()
		case <-hardDeadline:
			fmt.Fprintf(c.Writer, "event: done\ndata: timeout\n\n")
			c.Writer.Flush()
			return
		}
	}
}

func (h *DepsHandler) Reinstall(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var dep model.Dependency
	if err := database.DB.First(&dep, id).Error; err != nil {
		response.NotFound(c, "依赖不存在")
		return
	}

	if dep.Status == model.DepStatusQueued || dep.Status == model.DepStatusInstalling || dep.Status == model.DepStatusRemoving {
		response.BadRequest(c, "依赖正在处理中")
		return
	}

	database.DB.Model(&dep).Updates(map[string]interface{}{
		"status": model.DepStatusInstalling,
		"log":    "",
	})

	go dependencyInstallRunner(dep.ID, dep.Type, dep.Name)

	response.Success(c, gin.H{"message": "重新安装中"})
}

func appendDepsLog(existing, line string) string {
	existing = strings.TrimRight(existing, "\n")
	line = strings.TrimSpace(line)
	if line == "" {
		return existing
	}
	if existing == "" {
		return line
	}
	if strings.Contains(existing, line) {
		return existing
	}
	return existing + "\n" + line
}

func (h *DepsHandler) BatchReinstall(c *gin.Context) {
	var req struct {
		IDs []uint `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		response.BadRequest(c, "请求参数错误")
		return
	}

	var deps []model.Dependency
	database.DB.Where("id IN ? AND status NOT IN ?", req.IDs, []string{model.DepStatusQueued, model.DepStatusInstalling, model.DepStatusRemoving}).Find(&deps)
	if len(deps) == 0 {
		response.BadRequest(c, "选中的依赖当前无法重装")
		return
	}

	depMap := make(map[uint]model.Dependency, len(deps))
	for _, dep := range deps {
		depMap[dep.ID] = dep
	}

	queue := make([]model.Dependency, 0, len(req.IDs))
	for _, id := range req.IDs {
		dep, ok := depMap[id]
		if !ok {
			continue
		}
		queue = append(queue, dep)
	}
	if len(queue) == 0 {
		response.BadRequest(c, "选中的依赖当前无法重装")
		return
	}

	for index, dep := range queue {
		database.DB.Model(&model.Dependency{}).Where("id = ?", dep.ID).Updates(map[string]interface{}{
			"status": model.DepStatusQueued,
			"log":    appendDepsLog(dep.Log, fmt.Sprintf("[批量重装] 已加入顺序队列（%d/%d）", index+1, len(queue))),
		})
	}

	go func(ordered []model.Dependency) {
		for index, dep := range ordered {
			var current model.Dependency
			if err := database.DB.First(&current, dep.ID).Error; err != nil {
				continue
			}

			database.DB.Model(&model.Dependency{}).Where("id = ?", dep.ID).Updates(map[string]interface{}{
				"status": model.DepStatusInstalling,
				"log":    appendDepsLog(current.Log, fmt.Sprintf("[批量重装] 开始执行（%d/%d）", index+1, len(ordered))),
			})

			dependencyInstallRunner(dep.ID, dep.Type, dep.Name)
		}
	}(queue)

	response.Success(c, gin.H{"message": fmt.Sprintf("已提交 %d 个依赖顺序重装", len(queue))})
}

func (h *DepsHandler) Cancel(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)

	var dep model.Dependency
	if err := database.DB.First(&dep, id).Error; err != nil {
		response.NotFound(c, "依赖不存在")
		return
	}

	if dep.Status != model.DepStatusInstalling && dep.Status != model.DepStatusRemoving {
		response.BadRequest(c, "当前依赖任务未在处理中")
		return
	}

	if !cancelDepOperation(uint(id)) {
		response.BadRequest(c, "当前依赖任务未在运行中")
		return
	}

	response.Success(c, gin.H{"message": "取消请求已提交"})
}

func (h *DepsHandler) PipList(c *gin.Context) {
	pythonVersion, err := service.NormalizePythonVersionStrict(c.Query("python_version"))
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	pipEnv := service.WritableHomeEnv(service.SanitizePipEnv(os.Environ()))
	listCmd, err := service.NewPipCommandForPythonVersion(pythonVersion, []string{"list", "--format=json"})
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	listCmd.Env = pipEnv
	out, err := listCmd.Output()
	if err != nil {
		response.InternalError(c, "pip 不可用")
		return
	}
	c.Data(200, "application/json", out)
}

func (h *DepsHandler) Export(c *gin.Context) {
	depType := c.DefaultQuery("type", model.DepTypeNodeJS)

	validTypes := map[string]bool{
		model.DepTypeNodeJS: true,
		model.DepTypePython: true,
		model.DepTypeLinux:  true,
	}
	if !validTypes[depType] {
		response.BadRequest(c, "无效的依赖类型")
		return
	}

	var deps []model.Dependency
	query := database.DB.Where("type = ? AND status = ?", depType, model.DepStatusInstalled)
	filenameType := depType
	if depType == model.DepTypePython {
		pythonVersion, err := normalizeDependencyPythonVersion(depType, c.Query("python_version"))
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		query = query.Where("COALESCE(NULLIF(python_version, ''), ?) = ?", service.LegacyPythonVersion(), pythonVersion)
		filenameType = depType + "-" + strings.ReplaceAll(pythonVersion, ".", "")
	}
	query.Order("name ASC").Find(&deps)

	// 按安装时填写的原样导出（#150）：记录的 Name 就是用户当初填的内容，当初带版本号的才带，没带的就不带。
	// 每行一个，可以直接粘贴回「新建依赖」重新安装；Python 这份同时也是合法的 requirements.txt。
	// v1.9.8 起这里会现场跑 pip list / npm list / dpkg-query，拼成「名称==>已装版本」：那个格式 pip 不认，
	// 手动改成 == 再导回去又会把当初没钉版本的依赖全部钉死，所以不再附带已装版本，
	// 导出也就不再依赖 pip / npm / dpkg 能不能跑起来（以前任何一个跑不起来整份导出就 500）。
	lines := make([]string, 0, len(deps))
	for _, dep := range deps {
		lines = append(lines, dep.Name)
	}

	filename := fmt.Sprintf("dependencies-%s-%s.txt", filenameType, time.Now().Format("20060102-150405"))
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	c.String(200, strings.Join(lines, "\n"))
}

func (h *DepsHandler) PythonRuntimes(c *gin.Context) {
	response.Success(c, gin.H{
		"data":            service.PythonRuntimeInfos(),
		"default_version": service.DefaultPythonVersion(),
	})
}

func (h *DepsHandler) SetDefaultPythonRuntime(c *gin.Context) {
	var req struct {
		Version string `json:"version" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}
	version, err := service.NormalizePythonVersionStrict(req.Version)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if !service.PythonVersionSupportedByCurrentRuntime(version) {
		response.BadRequest(c, fmt.Sprintf("当前镜像不支持 Python %s，请切换到对应 Python 版本镜像或 all 镜像", version))
		return
	}
	if err := model.SetConfig("python_default_version", version); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, gin.H{"message": "默认 Python 版本已更新", "default_version": version})
}

func (h *DepsHandler) NpmList(c *gin.Context) {
	// 与安装路径共用同一份 HOME 判定：HOME 不可写时 npm 连 list 都跑不起来
	// （启动即初始化 $HOME/.npm 的 cache），会变成「装得上却看不到」。
	listCmd := exec.Command("npm", "list", "-g", "--json", "--depth=0")
	listCmd.Env = service.WritableHomeEnv(os.Environ())
	out, err := listCmd.Output()
	if err != nil {
		response.InternalError(c, "npm 不可用")
		return
	}
	c.Data(200, "application/json", out)
}

func (h *DepsHandler) GetMirrors(c *gin.Context) {
	result := gin.H{
		"pip_mirror":             service.CurrentEffectivePipMirror(),
		"npm_mirror":             service.CurrentEffectiveNpmMirror(),
		"linux_mirror":           "",
		"linux_package_manager":  "",
		"linux_distribution":     "",
		"linux_mirror_supported": false,
		"linux_mirror_label":     "Linux",
		"linux_mirror_message":   "",
	}

	linuxMirrorInfo := getLinuxMirrorInfo()
	result["linux_package_manager"] = linuxMirrorInfo.Manager
	result["linux_distribution"] = linuxMirrorInfo.Distribution
	result["linux_mirror"] = linuxMirrorInfo.Mirror
	result["linux_mirror_supported"] = linuxMirrorInfo.Supported
	result["linux_mirror_label"] = linuxMirrorInfo.Label
	result["linux_mirror_message"] = linuxMirrorInfo.Message

	response.Success(c, result)
}

func (h *DepsHandler) SetMirrors(c *gin.Context) {
	var req struct {
		PipMirror   *string `json:"pip_mirror"`
		NpmMirror   *string `json:"npm_mirror"`
		LinuxMirror *string `json:"linux_mirror"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	var errors []string

	if req.PipMirror != nil {
		if err := service.SetPipMirror(*req.PipMirror); err != nil {
			errors = append(errors, err.Error())
		}
	}

	if req.NpmMirror != nil {
		if err := service.SetNpmMirror(*req.NpmMirror); err != nil {
			errors = append(errors, err.Error())
		}
	}

	if req.LinuxMirror != nil {
		mirror := strings.TrimSpace(*req.LinuxMirror)
		manager, err := detectLinuxPackageManager()
		if err != nil {
			errors = append(errors, err.Error())
		} else {
			distribution := detectLinuxDistribution()
			changed, err := setLinuxMirror(manager, distribution, mirror)
			if err != nil {
				errors = append(errors, "设置 Linux 镜像源失败: "+err.Error())
			} else if changed {
				// 换完源必须让 apt 索引作废，否则 6 小时 TTL 内装包仍拿旧源的索引，
				// 报 E: Unable to locate package（issue #146）。
				//
				// 这里刻意【不】同步跑 apt-get update：SetMirrors 是同步 HTTP handler、前端只等 30 秒，
				// 换到慢源跑一次 update 轻松超时；而且这条路不拿 LockLinuxPackageOperation，
				// 同步跑会与后台安装抢 /var/lib/apt/lists/lock（那把锁不等待），把别人的安装搞 failed。
				// 作废索引之后，由下一次安装在包锁保护下顺带跑 update。
				//
				// 删索引要 root，而这条路没有权限闸，降权部署下会 EACCES：
				// 只记日志不阻断，不能让「镜像源设置成功」变成失败。
				if err := service.InvalidateAptPackageIndex(); err != nil {
					log.Printf("warn: 换源后作废 apt 索引失败（下次安装仍按 6 小时 TTL 判断是否刷新）: %v", err)
				}
			}
		}
	}

	if len(errors) > 0 {
		response.BadRequest(c, strings.Join(errors, "; "))
		return
	}

	// 用户显式保存过镜像源之后，旧默认源（阿里云）就只是一个普通的用户选择，
	// v3.3.2 的一次性迁移整体失效，免得他主动选回阿里云又被静默改走（issue #146）。
	if req.PipMirror != nil || req.LinuxMirror != nil {
		service.MarkDependencyMirrorChoiceSaved()
	}

	response.Success(c, gin.H{"message": "镜像源设置成功"})
}

// depFollowUpStep 是主命令成功之后、在同一条依赖记录里接着执行的一步（#142 的浏览器下载就是这样一步）。
//
// 它与主命令共用同一个 SSE 广播、同一份日志、同一个超时 / 取消 ctx：用户在弹窗里点「取消」、
// 或者整体超时，都会连同这一步一起终止；这一步失败，整条记录记为 failed。
type depFollowUpStep struct {
	// build 在主命令成功之后才调用：像 `python -m playwright install` 这种命令，
	// 要等 pip 装完才找得到模块。返回的 startLine 会在命令启动前写进日志。
	build func() (cmd *exec.Cmd, startLine string, err error)
	// doneLine 在这一步成功结束后写进日志，留空不写。
	doneLine string
	// acquire 可选，在 build 之前调用，用来拿这一步需要的全进程槽位（#142 的 Chromium 下载同一时刻只许一条）。
	// 拿不到时先调 waiting 写一行排队提示再阻塞；ctx 结束（取消 / 超时）时必须立刻返回 ctx.Err()，
	// 不能用拿不到就一直等的 sync.Mutex —— 那样排队中的记录点取消、到超时都停不下来。
	// 返回的 release 在这一步命令结束之后才调用，而不是 build 返回时。
	acquire func(ctx context.Context, waiting func(line string)) (release func(), err error)
}

// dependencyRunStartMarker 是每次依赖任务启动时写的第一行的开头。
// 失败提示靠它切出「本次运行」的日志段：批量重装、一键安装都会把新一轮日志追加在旧日志后面。
const dependencyRunStartMarker = "[依赖任务已启动，超时阈值："

func runCmdWithSSE(cmd *exec.Cmd, id uint, successStatus string, deleteOnSuccess bool) {
	runCmdWithSSEThen(cmd, id, successStatus, deleteOnSuccess, nil)
}

// runCmdWithSSEThen 是 runCmdWithSSE 加上「成功后接着执行的后续步骤」，followUps 为空时两者完全相同。
// 走这里的是 installDependency（网页安装、重装、批量重装、Playwright 一键安装）与 uninstallDependency（卸载）。
// 强制卸载（删除、批量删除）不走这里：它的记录事先就删掉了，用的是 forceUninstallDependency 自己的
// bytes.Buffer + WaitDelay。
func runCmdWithSSEThen(cmd *exec.Cmd, id uint, successStatus string, deleteOnSuccess bool, followUps []depFollowUpStep) {
	broadcaster := getOrCreateBroadcaster(id)
	defer removeBroadcaster(id)

	service.SetPgid(cmd)

	// 输出接 io.Pipe 的写端，不用 cmd.StdoutPipe()。
	// StdoutPipe 的读端会在 Wait 看到进程退出时被直接关掉（os/exec 文档：读完之前不能调 Wait），
	// 而下面的读协程与 Wait 是并发的：秒退的命令偶尔整段输出丢失，日志里只剩一行
	// 「[读取安装输出失败] read |0: file already closed」；输出多的命令则是丢掉末尾那一截。
	// 换成不是 *os.File 的 Writer 后，Wait 会等 exec 内部的拷贝协程把输出全部交给读协程之后才返回。
	// stdout 与 stderr 给同一个写端，exec 只建一根 OS 管道、一个拷贝协程，两路输出照旧按到达顺序混在一起。
	pipeReader, pipeWriter := io.Pipe()
	cmd.Stdout = pipeWriter
	cmd.Stderr = pipeWriter
	// 必须在 Start 之前设置。语义见 dependencyOutputWaitDelay 的注释。
	cmd.WaitDelay = dependencyOutputWaitDelay

	// 阈值只在任务启动时读一次并存下来，保证下面日志里写的数字就是本次实际生效的数字；
	// 中途用户改配置不影响已经跑起来的任务。
	operationTimeout := resolveDependencyOperationTimeout()

	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	registerDepOperation(id, cancel)
	defer func() {
		cancel()
		unregisterDepOperation(id)
	}()

	if err := cmd.Start(); err != nil {
		database.DB.Model(&model.Dependency{}).Where("id = ?", id).Updates(map[string]interface{}{
			"status": model.DepStatusFailed,
			"log":    err.Error(),
		})
		broadcaster.done()
		return
	}

	var logBuf strings.Builder
	var logMu sync.Mutex
	var existing model.Dependency
	if err := database.DB.Select("log").First(&existing, id).Error; err == nil && existing.Log != "" {
		logBuf.WriteString(existing.Log)
		if !strings.HasSuffix(existing.Log, "\n") {
			logBuf.WriteString("\n")
		}
	}
	lastPersistAt := time.Now()
	logDirty := false
	appendLine := func(line string, broadcast bool) {
		logMu.Lock()
		defer logMu.Unlock()

		logBuf.WriteString(line)
		logBuf.WriteString("\n")
		logDirty = true
		if broadcast {
			broadcaster.broadcast(line)
		}
	}
	flushLog := func(force bool) {
		logMu.Lock()
		defer logMu.Unlock()

		if !logDirty {
			return
		}
		if !force && time.Since(lastPersistAt) < 250*time.Millisecond {
			return
		}
		database.DB.Model(&model.Dependency{}).Where("id = ?", id).Update("log", logBuf.String())
		lastPersistAt = time.Now()
		logDirty = false
	}

	appendLine(fmt.Sprintf("%s%s，可在「系统设置 - 任务运行 - 依赖安装超时(分钟)」调整]", dependencyRunStartMarker, operationTimeout.Truncate(time.Second)), true)

	// waitCommand 读完一条已启动命令的输出并等它退出；超时 / 取消时杀掉整个进程组。
	// 返回命令的退出错误，以及被 ctx 打断时应记的终态（没被打断为空串）。
	// 主命令与后续步骤共用它，所以取消、超时对第二段下载同样生效。
	// output / outputWriter 是同一根 io.Pipe 的两端，启动前已经接到 running.Stdout / Stderr 上。
	waitCommand := func(running *exec.Cmd, output *io.PipeReader, outputWriter *io.PipeWriter) (error, string) {
		scanDone := make(chan struct{})
		go func() {
			defer close(scanDone)
			// 不管读循环从哪条路退出（读到 EOF、单行超长，或者将来有人加的提前 return），都要先把管道读到 EOF：
			// exec 内部往 io.Pipe 写端灌输出的拷贝协程没人读就永久阻塞，Wait 跟着卡死，
			// 杀进程、WaitDelay 都救不回来（卡住的是面板进程自己），还一直攥着包操作锁。
			// 正常读到 EOF 时这里立即返回。defer 后进先出：先读空，再 close(scanDone)。
			defer func() { _, _ = io.Copy(io.Discard, output) }()

			scanner := bufio.NewScanner(output)
			scanner.Buffer(make([]byte, 64*1024), 256*1024)
			for scanner.Scan() {
				appendLine(scanner.Text(), true)
				flushLog(false)
			}

			// 目前只有单行超过 256KB 会走到这里；这一行之后的输出由上面的 defer 读空丢弃，不再进日志。
			if err := scanner.Err(); err != nil {
				appendLine("[读取安装输出失败] "+err.Error(), true)
			}
		}()

		// backgroundHeld 只在下面的 Wait 协程里写、收到 waitCh 之后才读，channel 保证了先后，不用加锁。
		backgroundHeld := false
		waitCh := make(chan error, 1)
		go func() {
			err := running.Wait()
			// Wait 返回时输出已经全部交给了读协程（或者 WaitDelay 到点强制收了管道），
			// 这时关掉写端，读协程才会读到 EOF 结束。
			outputWriter.Close()
			if errors.Is(err, exec.ErrWaitDelay) {
				// 只有命令本体以 0 退出时 Wait 才会报 ErrWaitDelay：命令成功了，只是它拉起的后台进程
				// 还攥着输出管道，被 WaitDelay 到点强制收了管道。不算失败（同系统命令行的 resolveConsoleRunOutcome）。
				backgroundHeld = true
				err = nil
			}
			waitCh <- err
		}()

		interrupted := ""
		waitErr := error(nil)
		select {
		case waitErr = <-waitCh:
		case <-ctx.Done():
			if running.Process != nil {
				service.KillProcessGroup(running.Process)
			}
			waitErr = <-waitCh
			// 先等读协程把剩下的输出写完，下面的收尾行才不会插到输出中间。
			<-scanDone
			switch {
			case waitErr == nil:
				// 临界情况：进程恰好在超时/取消的同一瞬间正常退出，杀进程没杀到活的。
				// 命令本身是成功的，不能因为抢跑了 ctx.Done() 就把成功记成失败。
				appendLine("[依赖任务在超时/取消触发的同时已正常结束，按成功处理]", true)
			case ctx.Err() == context.DeadlineExceeded:
				appendLine("[依赖任务已超时，进程已终止]", true)
				interrupted = model.DepStatusFailed
			default:
				appendLine("[依赖任务已取消]", true)
				interrupted = model.DepStatusCancelled
			}
		}

		<-scanDone
		if backgroundHeld {
			appendLine("[命令已结束；仍有它拉起的后台进程持有输出管道，其后续输出不再收集]", true)
		}
		return waitErr, interrupted
	}

	status := successStatus
	waitErr, interrupted := waitCommand(cmd, pipeReader, pipeWriter)
	if interrupted != "" {
		status = interrupted
	}

	// skipFollowUps 在 ctx 已经结束（超时 / 取消）时记下「后续步骤没有执行」及对应的终态。
	skipFollowUps := func(ctxErr error) {
		if ctxErr == context.DeadlineExceeded {
			appendLine("[依赖任务已超时，后续步骤未执行]", true)
			status = model.DepStatusFailed
		} else {
			appendLine("[依赖任务已取消，后续步骤未执行]", true)
			status = model.DepStatusCancelled
		}
	}

	for _, step := range followUps {
		if waitErr != nil || status != successStatus {
			break
		}
		// 主命令恰好在超时 / 取消的同一瞬间成功结束（上面的临界分支）时 ctx 已经失效，
		// 后续步骤不能再启动 —— 否则它会脱离超时与取消的管控。
		if ctxErr := ctx.Err(); ctxErr != nil {
			skipFollowUps(ctxErr)
			break
		}

		// 每一步包进一个函数，只为用 defer 释放 acquire 拿到的槽位：
		// 构造 / 启动失败、命令跑完、被取消，哪条路退出都会放掉；命令跑起来了的话，一定等它退出之后才放。
		stop := func() bool {
			if step.acquire != nil {
				release, acquireErr := step.acquire(ctx, func(line string) {
					appendLine(line, true)
					// 排队可能要等好几分钟，立刻落库：只看列表、不开日志流的用户也能看到它在等什么。
					flushLog(true)
				})
				if acquireErr != nil {
					if ctxErr := ctx.Err(); ctxErr != nil {
						skipFollowUps(ctxErr)
					} else {
						appendLine("[后续步骤启动失败] "+acquireErr.Error(), true)
						waitErr = acquireErr
					}
					return true
				}
				defer release()
			}

			next, startLine, buildErr := step.build()
			if buildErr != nil {
				appendLine(buildErr.Error(), true)
				waitErr = buildErr
				return true
			}
			service.SetPgid(next)
			// 与主命令同一套接法，理由见上面主命令处的注释。
			nextReader, nextWriter := io.Pipe()
			next.Stdout = nextWriter
			next.Stderr = nextWriter
			next.WaitDelay = dependencyOutputWaitDelay
			if startLine != "" {
				appendLine(startLine, true)
			}
			if startErr := next.Start(); startErr != nil {
				appendLine("[后续步骤启动失败] "+startErr.Error(), true)
				waitErr = startErr
				return true
			}
			waitErr, interrupted = waitCommand(next, nextReader, nextWriter)
			if interrupted != "" {
				status = interrupted
				return true
			}
			if waitErr == nil && step.doneLine != "" {
				appendLine(step.doneLine, true)
			}
			return false
		}()
		if stop {
			break
		}
	}

	if waitErr != nil && status == successStatus {
		status = model.DepStatusFailed
		if hint := buildDependencyFailureHint(logBuf.String()); hint != "" {
			appendLine(hint, true)
		}
	}

	flushLog(true)

	if deleteOnSuccess && status == successStatus {
		database.DB.Delete(&model.Dependency{}, id)
	} else {
		logMu.Lock()
		finalLog := logBuf.String()
		logMu.Unlock()
		database.DB.Model(&model.Dependency{}).Where("id = ?", id).Updates(map[string]interface{}{
			"status": status,
			"log":    finalLog,
		})
	}

	if status == successStatus {
		go dependencySnapshotFunc()
	}

	broadcaster.done()
}

// dependencySnapshotFunc 抽成变量只为测试能换掉它：真实实现在后台协程里读 config.C，
// 用例结束时 testutil 会把 config.C 置空，协程晚一步执行就会空指针崩掉整个测试进程。
var dependencySnapshotFunc = service.SnapshotDepsToHost

func buildDependencyFailureHint(logText string) string {
	lower := strings.ToLower(logText)
	switch {
	// 顺序契约：Playwright 浏览器下载失败必须排在最前面，连锁冲突都要让它。
	//   - 其余分支都是在整段日志里「猜关键词」，而这条的判据是结构性事实：本次运行的日志里出现了
	//     下载开始行、却没有就绪行，说明 pip 那一段已经成功结束、失败只可能发生在下载阶段。
	//   - pip 阶段中途重试成功时会留下 Temporary failure in name resolution、Connection timed out
	//     之类的行，按原顺序会被 DNS / 镜像源分支抢走，把用户引去查一个本来就没问题的 pip 镜像；
	//     下载阶段不碰 apt，也不可能是 dpkg 锁冲突。
	//   - 反过来它不会误伤别的分支：没有下载开始行（pip 就失败了、或根本不是 playwright）时一律不命中。
	// 下载阶段里再细分一种：撞上浏览器目录的安装锁（__dirlock）不是网络问题，不能引去配代理。
	case isPlaywrightBrowserDownloadFailure(logText):
		if isPlaywrightBrowserDirLockConflict(logText) {
			return buildPlaywrightDownloadLockHint()
		}
		return buildPlaywrightDownloadFailureHint()
	case strings.Contains(lower, "could not get lock") ||
		strings.Contains(lower, "unable to acquire the dpkg frontend lock") ||
		strings.Contains(lower, "unable to lock database") ||
		strings.Contains(lower, "another app is currently holding the yum lock"):
		return "[检测到系统包管理器锁冲突，请稍后重试，或先确认没有其他 apt/yum/dnf/apk 任务正在运行]"
	// DNS 解析失败必须与「网络/镜像源不可达」分开报。
	// 这两类的排查方向完全相反：解析失败时宿主机往往一切正常（宿主走系统 DNS，
	// 容器走自己的 /etc/resolv.conf），此时让用户去「检查宿主机网络连通性」
	// 只会让他反复确认一个本来就没问题的东西，真正坏掉的那条线索反而被抹掉。
	// 模块版 Debian 容器的装依赖失败就长这样，之前一直被归到下面那条里误诊。
	case strings.Contains(lower, "temporary failure resolving") ||
		strings.Contains(lower, "temporary failure in name resolution") ||
		strings.Contains(lower, "could not resolve") ||
		strings.Contains(lower, "name or service not known"):
		return "[检测到容器内 DNS 解析失败：域名解析不出来，这与宿主机能否上网是两回事——" +
			"容器用的是自己的 /etc/resolv.conf。请在容器内执行 getent hosts mirrors.nju.edu.cn 复现，" +
			"并确认 /etc/resolv.conf 里的 nameserver 在当前网络下可用（校园网/企业网强制 DNS、" +
			"公共 Wi-Fi 登录门户、运营商屏蔽对外 53 端口都会导致这种失败）；" +
			"若 root 能解析而 apt 仍失败，则是 apt 降权用户 _apt 被限制联网，" +
			"需在 /etc/apt/apt.conf.d/ 下配置 APT::Sandbox::User \"root\"]"
	case strings.Contains(lower, "connection timed out") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "failed to fetch"):
		return "[检测到镜像源不可达或网络中断（域名能解析但连不上/下载失败），" +
			"请检查 Linux 镜像源配置、代理设置和网络连通性，必要时更换镜像源后重试]"
	// 顺序契约：apt 索引过期这条必须排在上面 DNS 与「Failed to fetch」两条【之后】。
	//   - 安装脚本用 ; 串联 apt-get update 与 apt-get install（见 linux_packages.go 的 LinuxInstallCommandSpec），
	//     update 因网络失败后 install 照跑、照样报 E: Unable to locate package；
	//     也就是说一次纯网络故障的日志里会【同时】出现 Failed to fetch 和 Unable to locate package。
	//   - 排在网络分支前面，就会把「网都不通」误诊成「索引过期」，让用户去换源、刷索引，白折腾一圈。
	//   - 反过来排在后面不会漏报：索引是真过期时日志里只有 Unable to locate package，网络分支不命中。
	case strings.Contains(lower, "e: unable to locate package"):
		return buildAptStaleIndexHint()
	// 顺序契约：这条必须夹在「镜像源」与「Alpine glibc 不兼容」之间。
	//   - 排在锁冲突 / DNS / 镜像源之后：那三类是更靠前的次生故障，先解决它们才对；
	//   - 排在 isAlpineGlibcIncompatible 之前：后者的关键词（failed to build installable wheels、
	//     manylinux）太宽，日志里一出现就会盖掉「编译器 / CMake 根本不存在」这个更具体的真因。
	//     两条结论的第一步动作并不相同（换镜像拿预编译包 vs 装工具链现场编），所以两边的文案
	//     都必须把这两条出路并列写出来，而不是各给一半（issue #120）。
	case isMissingBuildToolchain(lower):
		return buildMissingToolchainHint(detectDependencyToolchainEnv())
	case isAlpineGlibcIncompatible(lower):
		return buildAlpineGlibcHint(detectDependencyToolchainEnv())
	default:
		return ""
	}
}

// buildAptStaleIndexHint 是「apt 索引与当前镜像源对不上」这条归因的结论文案（issue #146）。
//
// 典型成因：换了 apt 源，但索引还是旧源那一批 —— 索引文件名按仓库 URL 编码，换源后 apt 在新源的索引里
// 一个包都找不到。v3.3.2 起面板自己的换源路径已经会作废索引，这条提示主要覆盖面板管不到的换源
// （系统控制台里手敲 sed / apt edit-sources，或直接改 /etc/apt 下的文件）。
func buildAptStaleIndexHint() string {
	return "[检测到 apt 找不到软件包（E: Unable to locate package）：多半是软件包索引与当前镜像源对不上 —— " +
		"索引按仓库地址缓存，换过源却没刷新索引就会在新源里一个包都找不到。" +
		"出路一：在容器内执行 apt-get update 后重装本依赖；" +
		"出路二：到「依赖管理 → 镜像源设置」重新保存一次 Linux 镜像源，面板会自动作废旧索引；" +
		"若包名本身就不在当前发行版的软件源里（例如 Debian 换代后包名带了 t64 后缀），则需要改用新包名]"
}

// buildAlpineGlibcHint 是「musl 上没有预编译包」这条归因的结论文案。
//
// 主出路是换 Debian 版镜像：PyPI 上的科学计算包普遍只发 manylinux wheel（只认 glibc）、
// 不发 musllinux wheel，换过去 pip 直接下预编译包，根本不用编译 ——
// issue #120 的 opencv-python 就属于这一类（有 manylinux_2_17 的 x86_64/aarch64 wheel，没有任何 musllinux wheel）。
// 但必须同时写上退路：万一该包连 manylinux wheel 都没有，换完镜像照样会掉进源码编译，
// 那时该做的是装编译工具链，而不是继续换镜像。
//
// 这条退路要指到哪儿，必须跟着面板的运行身份走，理由与 buildMissingToolchainHint 完全一样：
// 降权部署（PUID/PGID）下用户照着「去 Linux 页签装」点下去，会先建出 3 条依赖记录、
// 再被 EnsureLinuxPackageManagerPrivilege 全部拒掉，白白多出 3 条 failed 和侧栏角标。
// 所以这里从 const 改成按环境组装的函数，非 root 时改指提权出路。
func buildAlpineGlibcHint(env dependencyToolchainEnv) string {
	install := "请到「依赖管理 → Linux」页签装好编译器与 cmake" +
		"（Alpine 装 build-base、linux-headers、cmake，Debian 装 build-essential、cmake）后再重装"
	if env.PrivilegeHint != "" {
		install = "面板内的「依赖管理 → Linux」页签在当前的非 root 运行身份下装不了系统包，" +
			"只能按下面的提权出路装 build-base、linux-headers、cmake" +
			"（换到 Debian 版镜像后则是 build-essential、cmake）后再重装"
	}

	// 镜像按标签对应换（与 Playwright 的 Alpine 提示同一个说法）：写死 :debian 的话，latest-full 的用户照做会丢掉 Go 与编译链。
	hint := "[当前容器使用 Alpine 镜像（musl libc），该依赖在 musl 上没有可用的预编译包" +
		"（PyPI 上常见的 manylinux wheel 只认 glibc）。请切换到 Debian 版镜像（把镜像标签里的 latest 换成 debian，如 latest → debian、latest-full → debian-full）后重试，" +
		"多数这类包换过去就能直接装上、不用编译；若换镜像后仍然报编译失败，说明该包连 manylinux wheel 都没有，只能现场编译 —— " +
		install
	if env.PrivilegeHint != "" {
		// 与 buildMissingToolchainHint 同款收尾：把作用域写清楚，
		// 否则 PrivilegeHint 末句「Node.js / Python 依赖不受此限制」贴在一条刚失败的
		// Python 依赖日志末尾，会被读成自相矛盾。
		hint += "。装系统包这一步受面板运行身份限制：" + env.PrivilegeHint
	}
	return hint + "]"
}

func isAlpineGlibcIncompatible(lowerLog string) bool {
	if detectLinuxDistribution() != "alpine" {
		return false
	}
	return matchesAlpineGlibcHints(lowerLog)
}

// matchesAlpineGlibcHints 只做关键词判定、不读环境。
//
// 拆出来有两个原因：一是让 issue #120 那份原样日志能在非 Alpine 的开发机 / CI 上被断言
// 「仍然落在 glibc 这条车道里」；二是钉住 failed to build installable wheels ——
// 它是 #120 那份日志唯一的命中项，从这里删掉等于让用户拿到一条空提示。
func matchesAlpineGlibcHints(lowerLog string) bool {
	glibcHints := []string{
		"no matching distribution",
		"resolutionimpossible",
		"not a supported wheel on this platform",
		"failed to build installable wheels",
		"manylinux",
	}
	for _, hint := range glibcHints {
		if strings.Contains(lowerLog, hint) {
			return true
		}
	}
	return false
}

// isMissingBuildToolchain 判断日志是不是「机器上根本没有 C/C++ 编译器 / CMake」。
//
// 典型场景：pip 装 opencv-python、lxml、numpy 这类没有对应平台 wheel 的包时会回退到
// 源码编译，而面板镜像的精简档刻意不装编译器（Dockerfile 里还有构建期断言守着），
// 完整档虽有 build-base / build-essential 但不含 cmake，于是编译在第一步就断了。
//
// 关键词只收「工具本身不存在」这一类明确信号，不收 "failed building wheel" 之类
// 只说明「编译失败了」的宽泛结论 —— 那种日志的真实原因可能是缺头文件、缺系统库、
// 版本不兼容等等，硬归到这里就是新的误诊。只写 Failed building wheel 的日志由后面的
// matchesAlpineGlibcHints 接住（在 musl 上它多半确实是「没有预编译包」），两条结论的
// 文案都并列给了「换镜像」和「装工具链」，所以落到哪一条都不会把用户引进死胡同。
func isMissingBuildToolchain(lowerLog string) bool {
	hints := []string{
		// CMake 在、编译器不在：opencv-python 这类把 cmake 写进 build-system.requires 的包，
		// pip 构建隔离会先从 PyPI 装一个 cmake wheel（musllinux 的也有），于是现场变成
		// 「cmake 有、gcc/g++ 没有」，此时 CMake 会点名吐出下面这几句，pip 那一层只剩
		// Failed building wheel 这种看不出真因的结论（issue #120 的最可能形态）。
		//
		// 反过来，"An error occurred while configuring with CMake." 这句绝不能收：
		// 它不是 CMake 自己吐的，而是 scikit-build 的 cmaker.configure() 包装 ——
		// cmake 子进程返回非 0 就抛。也就是说它出现时 cmake 明明已经跑起来了，
		// 与「cmake / 编译器不存在」互斥；缺 zlib 之类系统开发包的失败照样会带上它
		// （上面还明明白白印着 The CXX compiler identification is GNU 12.2.0），
		// 收进来就是把工具链齐全的现场误判成缺工具链、把真因盖掉。
		// 真缺工具链时 CMake 必定另外打印下面这几条点名信号，不靠这句也照样命中。
		"no cmake_cxx_compiler could be found",
		"no cmake_c_compiler could be found",
		"cmake_cxx_compiler not set",
		"cmake_c_compiler not set",
		"the cxx compiler identification is unknown",
		"the c compiler identification is unknown",
		// 编译器缺失：sh / bash / exec.Command 三种报法都覆盖
		"gcc: not found",
		"g++: not found",
		"cc: not found",
		"make: not found",
		"gcc: command not found",
		"g++: command not found",
		"make: command not found",
		`exec: "gcc"`,
		`exec: "g++"`,
		`exec: "cc"`,
		"command 'gcc' failed",
		"command 'cc' failed",
		"command 'g++' failed",
		"unable to execute 'cc1plus'",
		"unable to execute 'gcc'",
		// CMake / Ninja 缺失：setuptools、scikit-build、cmake 包装器各有各的措辞
		"cmake: not found",
		"cmake: command not found",
		"cmake must be installed",
		"no cmake found",
		"cmake is required",
		"could not find cmake",
		"problem with the cmake installation",
		"ninja is required",
		"error: [errno 2] no such file or directory: 'cmake'",
		// 缺开发头文件，本质同样是「编译环境没准备好」
		"fatal error: python.h",
		// Windows 二进制部署下的等价故障
		"visual c++ 14.0 or greater is required",
	}
	for _, hint := range hints {
		if strings.Contains(lowerLog, hint) {
			return true
		}
	}
	return false
}

// dependencyToolchainEnv 是「缺编译工具链」这条归因分支需要的全部环境事实。
//
// 之所以抽成参数而不是在文案函数里现读环境：isAlpineGlibcIncompatible 直接读
// /etc/os-release，在 Windows 开发机上恒为 false，那条分支的文案单测根本覆盖不到。
// 这里改成「外层读环境、内层纯函数」，buildMissingToolchainHint 就能被直接调用测试。
type dependencyToolchainEnv struct {
	// Distribution 取 /etc/os-release 的 ID（alpine / debian / ubuntu ...），探测不到为空串
	Distribution string
	// PackageManager 取包管理器名（apk / apt / dnf / yum / microdnf / zypper），探测不到为空串
	PackageManager string
	// PrivilegeHint 非空即表示当前是非 root：内容就是用户真去点「安装」时会撞上的那段拦截说明
	PrivilegeHint string
}

// detectDependencyToolchainEnv 负责读环境，文案拼装交给纯函数 buildMissingToolchainHint。
func detectDependencyToolchainEnv() dependencyToolchainEnv {
	env := dependencyToolchainEnv{Distribution: detectLinuxDistribution()}
	if manager, err := detectLinuxPackageManager(); err == nil {
		env.PackageManager = manager.Name
	}
	// 非 root 时不能只叫用户「去 Linux 页签装」——他一点安装就会被
	// EnsureLinuxPackageManagerPrivilege 拦下，提示就自相矛盾了。
	// 这里直接复用它那段（已经按 Docker / Magisk / 裸机分岔好的）出路说明，避免两处维护同一套文案。
	if err := service.EnsureLinuxPackageManagerPrivilege(); err != nil {
		env.PrivilegeHint = err.Error()
	}
	return env
}

// buildMissingToolchainHint 是这条分支的纯函数内核：只依赖入参，不碰环境、不碰 DB。
func buildMissingToolchainHint(env dependencyToolchainEnv) string {
	// 包管理器比 /etc/os-release 更能代表「实际能用哪条命令装」，所以优先按它分岔；
	// 只有探测不到包管理器时才退回发行版 ID。
	family := strings.ToLower(strings.TrimSpace(env.PackageManager))
	if family == "" {
		switch strings.ToLower(strings.TrimSpace(env.Distribution)) {
		case "alpine":
			family = "apk"
		case "debian", "ubuntu":
			family = "apt"
		}
	}

	// packages 只回答「要装哪些包」，「去哪儿装」由下面按 PrivilegeHint 决定。
	// 两件事必须拆开：非 root 时面板内的「依赖管理 → Linux」页签根本装不了
	// （BuildLinuxPackageCommand 第一行就被 EnsureLinuxPackageManagerPrivilege 拒掉），
	// 再指路过去只会凭空多出几条 failed 依赖记录、把侧栏角标顶上去。
	packages := ""
	// musl 上优先换镜像：多数科学计算包（opencv-python、grpcio…）在 PyPI 上只发
	// manylinux wheel、不发 musllinux wheel，换到 Debian 版镜像 pip 直接下预编译包，
	// 根本不用编译；留在 Alpine 硬编反而是最慢、最容易撞超时的一条路。
	preferDebianImage := false
	switch family {
	case "apk":
		packages = "build-base、linux-headers、cmake"
		preferDebianImage = true
	case "apt":
		packages = "build-essential、cmake"
	case "dnf", "yum", "microdnf", "zypper":
		packages = "gcc、gcc-c++、make、cmake"
	}

	var body string
	if packages == "" {
		// 探测不到包管理器（Windows、裸机等），只能给通用结论，不编造包名。
		body = "当前环境探测不到 Linux 包管理器，需要先自行装好 C/C++ 编译工具链" +
			"（Linux 为 gcc/g++/make，Windows 为 Visual Studio 生成工具）与 CMake，再重装本依赖"
	} else {
		install := "请到「依赖管理 → Linux」页签安装 " + packages
		if env.PrivilegeHint != "" {
			install = "面板内的「依赖管理 → Linux」页签在当前的非 root 运行身份下装不了系统包，" +
				"只能按下面的提权出路装 " + packages
		}
		compile := install + "，装完再重装本依赖；现场编译很慢，" +
			"记得先到「系统设置 - 任务运行 - 依赖安装超时(分钟)」把阈值调大，默认 20 分钟往往不够"
		if preferDebianImage {
			// 两条出路是并列关系，不是二选一：先给成本最低的换镜像，再给兜底的现场编译。
			// 镜像按标签对应换，理由同 buildAlpineGlibcHint：写死 :debian 的话，latest-full 的用户照做会丢掉 Go 与编译链。
			body = "出路一：换到 Debian 版镜像（把镜像标签里的 latest 换成 debian，如 latest → debian、latest-full → debian-full）后重装 —— " +
				"多数科学计算包在 musl 上没有预编译包、在 glibc 上却有 manylinux wheel，换过去往往直接装上、不用编译。" +
				"出路二：若该包连 manylinux wheel 都没有，就只能现场编译：" + compile
		} else {
			body = compile
		}
	}

	hint := "[检测到缺少 C/C++ 编译工具链或 CMake：当前平台找不到可直接使用的预编译包，" +
		"回退到源码编译时发现编译器或 CMake 不存在。" + body
	if env.PrivilegeHint != "" {
		// 作用域必须写清楚：这条限制只卡「装 Linux 系统包」这一步。
		// 否则 PrivilegeHint 末句「Node.js / Python 依赖不受此限制」贴在一条刚失败的
		// Python 依赖日志末尾，会被读成自相矛盾。
		hint += "。装系统包这一步受面板运行身份限制：" + env.PrivilegeHint
	}
	return hint + "]"
}

func ensureTmpDir() {
	os.MkdirAll("/tmp", 0o1777)
}

func installDependency(id uint, depType, name string) {
	ensureTmpDir()
	var cmd *exec.Cmd
	var followUps []depFollowUpStep
	pythonVersion := ""
	if depType == model.DepTypePython {
		var dep model.Dependency
		if err := database.DB.Select("python_version").First(&dep, id).Error; err == nil {
			pythonVersion = dep.PythonVersion
		}
		pythonVersion = service.NormalizePythonVersionOrDefault(pythonVersion)
		database.DB.Model(&model.Dependency{}).Where("id = ?", id).Update("python_version", pythonVersion)
	}
	switch depType {
	case model.DepTypeNodeJS:
		nodeUnlock := service.LockNodePackageOperation()
		defer nodeUnlock()

		if notice := service.NodeInstallCompatibilityNotice(name); notice != "" {
			database.DB.Model(&model.Dependency{}).Where("id = ?", id).Update("log", notice+"\n")
		}

		var err error
		cmd, err = service.NewNpmInstallCommand(name)
		if err != nil {
			database.DB.Model(&model.Dependency{}).Where("id = ?", id).Updates(map[string]interface{}{
				"status": model.DepStatusFailed,
				"log":    err.Error(),
			})
			return
		}
	case model.DepTypePython:
		var err error
		cmd, err = service.NewPipInstallCommandForPythonVersion(pythonVersion, name)
		if err != nil {
			database.DB.Model(&model.Dependency{}).Where("id = ?", id).Updates(map[string]interface{}{
				"status": model.DepStatusFailed,
				"log":    err.Error(),
			})
			return
		}
		cmd.Env = append(service.PipInstallEnv(service.AppendProxyEnv(os.Environ()), service.CurrentPipMirror()), "TMPDIR=/tmp")
		// playwright 装完接着把 Chromium 下到数据卷（#142）：网页安装、重装、一键安装都经过这里。
		// 适用范围（只在容器、非 Alpine）由 service.PlaywrightBrowserDownloadApplies 判定。
		if playwrightBrowserDownloadAppliesFunc(name) {
			followUps = append(followUps, newPlaywrightBrowserDownloadStep(pythonVersion))
		}
	case model.DepTypeLinux:
		// 与容器重建后的自动重装共用 service 层这把锁，构造命令之前就拿、持有到命令结束（理由见 LockLinuxPackageOperation）。
		linuxUnlock := service.LockLinuxPackageOperation()
		defer linuxUnlock()

		manager, err := detectLinuxPackageManager()
		if err != nil {
			database.DB.Model(&model.Dependency{}).Where("id = ?", id).Updates(map[string]interface{}{
				"status": model.DepStatusFailed,
				"log":    err.Error(),
			})
			return
		}

		initialLog := fmt.Sprintf("[Linux] 已检测到包管理器：%s", manager.Binary)
		database.DB.Model(&model.Dependency{}).Where("id = ?", id).Update("log", initialLog+"\n")

		cmd, err = buildLinuxPackageCommand(manager, "install", name, false)
		if err != nil {
			database.DB.Model(&model.Dependency{}).Where("id = ?", id).Updates(map[string]interface{}{
				"status": model.DepStatusFailed,
				"log":    initialLog + "\n" + err.Error(),
			})
			return
		}
	default:
		database.DB.Model(&model.Dependency{}).Where("id = ?", id).Updates(map[string]interface{}{
			"status": model.DepStatusFailed,
			"log":    "不支持的类型",
		})
		return
	}

	runCmdWithSSEThen(cmd, id, model.DepStatusInstalled, false, followUps)
}

func uninstallDependency(id uint, depType, name, pythonVersion string) {
	var cmd *exec.Cmd
	switch depType {
	case model.DepTypeNodeJS:
		nodeUnlock := service.LockNodePackageOperation()
		defer nodeUnlock()

		var err error
		cmd, err = service.NewNpmUninstallCommand(name, false)
		if err != nil {
			database.DB.Model(&model.Dependency{}).Where("id = ?", id).Updates(map[string]interface{}{
				"status": model.DepStatusFailed,
				"log":    err.Error(),
			})
			return
		}
	case model.DepTypePython:
		var err error
		cmd, err = service.NewPipUninstallCommandForPythonVersion(pythonVersion, name)
		if err != nil {
			database.DB.Model(&model.Dependency{}).Where("id = ?", id).Updates(map[string]interface{}{
				"status": model.DepStatusFailed,
				"log":    err.Error(),
			})
			return
		}
		cmd.Env = service.SanitizePipEnv(service.AppendProxyEnv(os.Environ()))
	case model.DepTypeLinux:
		linuxUnlock := service.LockLinuxPackageOperation()
		defer linuxUnlock()

		manager, err := detectLinuxPackageManager()
		if err != nil {
			database.DB.Delete(&model.Dependency{}, id)
			return
		}

		cmd, err = buildLinuxPackageCommand(manager, "remove", name, false)
		if err != nil {
			// 这里【不能】删记录。容器配了 PUID/PGID 降权之后，这条路会因为
			// 「apk/apt 需要 root」直接返回错误；删掉记录的话前端看到行消失，
			// 等同于「卸载成功」，而包其实还在系统里，那段专门写的中文说明
			// 也一个字都不会显示出来。改成与上面 NodeJS / Python 两个分支一致：
			// 标记 failed 并把原因写进日志。
			database.DB.Model(&model.Dependency{}).Where("id = ?", id).Updates(map[string]interface{}{
				"status": model.DepStatusFailed,
				"log":    err.Error(),
			})
			return
		}
	default:
		database.DB.Delete(&model.Dependency{}, id)
		return
	}

	runCmdWithSSE(cmd, id, model.DepStatusInstalled, true)
}

func forceUninstallDependency(depType, name, pythonVersion string) {
	var cmd *exec.Cmd
	switch depType {
	case model.DepTypeNodeJS:
		nodeUnlock := service.LockNodePackageOperation()
		defer nodeUnlock()

		var err error
		cmd, err = service.NewNpmUninstallCommand(name, true)
		if err != nil {
			return
		}
	case model.DepTypePython:
		// 与普通卸载是同一条 pip 命令，「强制」只体现在不管成败先删记录。
		// 这里曾经多传了一个 --no-deps：pip uninstall 没有这个选项（它本来就不删依赖），
		// 带上它 pip 在参数解析阶段就以退出码 2 退出、一个包都不卸 —— 强制 / 批量卸载从 v1.3.0 起
		// 一直只删了记录，site-packages 原样不动（#150）。
		var err error
		cmd, err = service.NewPipUninstallCommandForPythonVersion(pythonVersion, name)
		if err != nil {
			return
		}
		cmd.Env = service.SanitizePipEnv(service.AppendProxyEnv(os.Environ()))
	case model.DepTypeLinux:
		linuxUnlock := service.LockLinuxPackageOperation()
		defer linuxUnlock()

		manager, err := detectLinuxPackageManager()
		if err != nil {
			return
		}

		cmd, err = buildLinuxPackageCommand(manager, "remove", name, true)
		if err != nil {
			return
		}
	default:
		return
	}

	// 强制卸载时依赖行已经被删掉了，没有 id 可以注册取消函数，前端也没有入口去点取消。
	// 但它照样持有 apt / npm 的包锁（上面的 LockLinuxPackageOperation、LockNodePackageOperation），
	// 卡死就会把后续所有依赖任务一起堵住，所以这里必须有超时兜底。
	service.SetPgid(cmd)

	// 记录在调用前就已经删掉了，卸载失败在页面上看不到。把输出收下来，失败时至少写进面板日志，
	// 不再像 #150 那样无声失败。接了输出管道后 Wait 要等管道关闭，WaitDelay 防止逃出进程组的
	// 孙进程攥着管道，让这里连同包锁一起卡住。
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	cmd.WaitDelay = 10 * time.Second

	timeout := resolveDependencyOperationTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := cmd.Start(); err != nil {
		log.Printf("warn: 强制卸载 %s 依赖 %s 失败: %v", depType, name, err)
		return
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var err error
	select {
	case err = <-waitCh:
	case <-ctx.Done():
		if cmd.Process != nil {
			service.KillProcessGroup(cmd.Process)
		}
		<-waitCh
		err = fmt.Errorf("执行超过 %s 仍未结束，已终止", timeout)
	}
	if err != nil {
		// 只留输出末尾：apt / npm 失败时输出很长，真正的报错在最后；截断处可能切开多字节字符，顺手清掉。
		text := strings.TrimSpace(output.String())
		if len(text) > 2000 {
			text = "..." + strings.ToValidUTF8(text[len(text)-2000:], "")
		}
		log.Printf("warn: 强制卸载 %s 依赖 %s 失败: %v；输出末尾：\n%s", depType, name, err, text)
	}
}

func (h *DepsHandler) RegisterRoutes(r *gin.RouterGroup) {
	deps := r.Group("/deps", middleware.JWTAuth(), middleware.RequireAdmin())
	{
		deps.GET("", h.List)
		deps.POST("", h.Create)
		deps.POST("/batch-reinstall", h.BatchReinstall)
		deps.POST("/batch-delete", h.BatchDelete)
		deps.DELETE("/:id", h.Delete)
		deps.PUT("/:id/cancel", h.Cancel)
		deps.GET("/:id/status", h.GetStatus)
		deps.GET("/:id/log-stream", h.LogStream)
		deps.PUT("/:id/reinstall", h.Reinstall)
		deps.GET("/export", h.Export)

		deps.GET("/python-runtimes", h.PythonRuntimes)
		deps.PUT("/python-runtime-default", h.SetDefaultPythonRuntime)
		deps.GET("/pip", h.PipList)
		deps.GET("/npm", h.NpmList)

		deps.GET("/mirrors", h.GetMirrors)
		deps.PUT("/mirrors", h.SetMirrors)

		// Playwright 运行环境一键安装（#142），实现在 deps_playwright.go。
		// /deps 不在开放 API 的 scope 里，MCP 不需要同步。
		deps.GET("/playwright", h.PlaywrightStatus)
		deps.POST("/playwright/install", h.InstallPlaywright)
	}
}
