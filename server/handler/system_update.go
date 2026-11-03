package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"daidai-panel/model"
	"daidai-panel/pkg/response"

	"github.com/gin-gonic/gin"
)

const (
	dockerSocketPath             = "/var/run/docker.sock"
	defaultDockerHubRegistryHost = "registry-1.docker.io"
	panelUpdateDeploymentDocker  = "docker"
	panelUpdateDeploymentBinary  = "binary"
	panelUpdateDeploymentMagisk  = "magisk"
	panelUpdateManagerPanel      = "panel"
	panelUpdateManagerWatchtower = "watchtower"
)

type panelUpdateStatusSnapshot struct {
	Status         string    `json:"status"`
	Phase          string    `json:"phase"`
	Message        string    `json:"message"`
	Error          string    `json:"error,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
	DeploymentType string    `json:"deployment_type,omitempty"`
	UpdateManager  string    `json:"update_manager,omitempty"`
	ContainerName  string    `json:"container_name,omitempty"`
	ImageName      string    `json:"image_name,omitempty"`
	PullImageName  string    `json:"pull_image_name,omitempty"`
	MirrorHost     string    `json:"mirror_host,omitempty"`
	RegistryURL    string    `json:"registry_url,omitempty"`
	ReleaseVersion string    `json:"release_version,omitempty"`
	AssetName      string    `json:"asset_name,omitempty"`
	AssetURL       string    `json:"asset_url,omitempty"`
	InstallDir     string    `json:"install_dir,omitempty"`
	BinaryName     string    `json:"binary_name,omitempty"`
}

type panelUpdateManager struct {
	mu       sync.RWMutex
	snapshot panelUpdateStatusSnapshot
}

type panelUpdatePlan struct {
	DeploymentType  string
	UpdateManager   string
	ContainerName   string
	ImageName       string
	PullImageName   string
	PreviousImageID string
	Channel         string
	MirrorHost      string
	RegistryURL     string
	RunArgs         []string
	ReleaseVersion  string
	AssetName       string
	AssetURL        string
	AssetDigest     string // Release 附件的 "sha256:<hex>"，下载后据此校验；为空时拒绝更新
	InstallDir      string
	BinaryName      string
	ExecutablePath  string
	// 下面三个只在 Magisk 模块版用到：面板数据目录（新进程的工作目录）、
	// 前端静态目录、模块本体目录。见 system_update_magisk.go 顶部的路径说明。
	DataDir       string
	WebDir        string
	ModuleDir     string
	CurrentPID    int
	ServerPID     int
	ServerPIDFile string
	Watchtower    watchtowerRuntimeConfig
}

type watchtowerRuntimeConfig struct {
	Managed                bool
	APIURL                 string
	APIToken               string
	Schedule               string
	PeriodicPollsEnabled   bool
	ManualTriggerSupported bool
}

type dockerInspectInfo struct {
	Name       string `json:"Name"`
	Image      string `json:"Image"`
	Mounts     []dockerInspectMount
	Config     dockerInspectConfig     `json:"Config"`
	HostConfig dockerInspectHostConfig `json:"HostConfig"`
}

type dockerInspectConfig struct {
	Image string   `json:"Image"`
	Env   []string `json:"Env"`
}

type dockerInspectHostConfig struct {
	Binds         []string `json:"Binds"`
	ExtraHosts    []string `json:"ExtraHosts"`
	NetworkMode   string   `json:"NetworkMode"`
	RestartPolicy struct {
		Name string `json:"Name"`
	} `json:"RestartPolicy"`
	PortBindings map[string][]struct {
		HostIP   string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	} `json:"PortBindings"`
}

type dockerInspectMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

var panelUpdater = newPanelUpdateManager()

func newPanelUpdateManager() *panelUpdateManager {
	return &panelUpdateManager{
		snapshot: panelUpdateStatusSnapshot{
			Status:    "idle",
			Phase:     "idle",
			Message:   "当前没有进行中的更新任务",
			UpdatedAt: time.Now(),
		},
	}
}

func (m *panelUpdateManager) begin(plan *panelUpdatePlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.snapshot.Status == "running" || m.snapshot.Status == "restarting" {
		return fmt.Errorf("已有更新任务正在进行中，请稍后查看状态")
	}

	now := time.Now()
	m.snapshot = panelUpdateStatusSnapshot{
		Status:         "running",
		Phase:          "preparing",
		Message:        buildPanelUpdateBeginMessage(plan),
		StartedAt:      now,
		UpdatedAt:      now,
		DeploymentType: plan.DeploymentType,
		UpdateManager:  plan.UpdateManager,
		ContainerName:  plan.ContainerName,
		ImageName:      plan.ImageName,
		PullImageName:  plan.PullImageName,
		MirrorHost:     plan.MirrorHost,
		RegistryURL:    plan.RegistryURL,
		ReleaseVersion: plan.ReleaseVersion,
		AssetName:      plan.AssetName,
		AssetURL:       plan.AssetURL,
		InstallDir:     plan.InstallDir,
		BinaryName:     plan.BinaryName,
	}
	return nil
}

func (m *panelUpdateManager) setRunning(phase, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.snapshot.Status = "running"
	m.snapshot.Phase = phase
	m.snapshot.Message = message
	m.snapshot.Error = ""
	m.snapshot.UpdatedAt = time.Now()
}

func (m *panelUpdateManager) setRestarting(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.snapshot.Status = "restarting"
	m.snapshot.Phase = "restarting"
	m.snapshot.Message = message
	m.snapshot.Error = ""
	m.snapshot.UpdatedAt = time.Now()
}

func (m *panelUpdateManager) complete(phase, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.snapshot.Status = "completed"
	m.snapshot.Phase = phase
	m.snapshot.Message = message
	m.snapshot.Error = ""
	m.snapshot.UpdatedAt = time.Now()
}

func (m *panelUpdateManager) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	msg := "更新失败"
	if err != nil {
		msg = err.Error()
	}

	m.snapshot.Status = "failed"
	m.snapshot.Phase = "failed"
	m.snapshot.Message = msg
	m.snapshot.Error = msg
	m.snapshot.UpdatedAt = time.Now()
}

func (m *panelUpdateManager) snapshotCopy() panelUpdateStatusSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snapshot
}

func (h *SystemHandler) UpdateStatus(c *gin.Context) {
	response.Success(c, gin.H{"data": panelUpdater.snapshotCopy()})
}

func currentWatchtowerRuntimeConfig() watchtowerRuntimeConfig {
	manager := strings.ToLower(strings.TrimSpace(os.Getenv("PANEL_UPDATE_MANAGER")))
	apiURL := strings.TrimSpace(os.Getenv("WATCHTOWER_HTTP_API_URL"))
	apiToken := strings.TrimSpace(os.Getenv("WATCHTOWER_HTTP_API_TOKEN"))
	schedule := strings.TrimSpace(os.Getenv("WATCHTOWER_SCHEDULE"))
	periodicPolls := parseEnvBool(os.Getenv("WATCHTOWER_HTTP_API_PERIODIC_POLLS"))

	// 只有显式声明更新管理器时才进入 Watchtower 路径，避免裸机残留 URL 覆盖二进制更新。
	managed := manager == panelUpdateManagerWatchtower
	manualSupported := managed && apiURL != "" && apiToken != ""

	return watchtowerRuntimeConfig{
		Managed:                managed,
		APIURL:                 apiURL,
		APIToken:               apiToken,
		Schedule:               schedule,
		PeriodicPollsEnabled:   periodicPolls,
		ManualTriggerSupported: manualSupported,
	}
}

func parseEnvBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func buildWatchtowerUpdateTarget(cfg watchtowerRuntimeConfig) gin.H {
	// Watchtower 更新的是容器当前 Config.Image；固定版本标签不能在这里只改成滚动标签，
	// 否则页面展示的目标会与 Watchtower 实际检查的镜像不一致。
	imageName := strings.TrimSpace(os.Getenv("IMAGE_NAME"))
	target := gin.H{
		"deployment_type":              panelUpdateDeploymentDocker,
		"update_manager":               panelUpdateManagerWatchtower,
		"watchtower_managed":           true,
		"watchtower_schedule":          cfg.Schedule,
		"watchtower_http_api_enabled":  cfg.APIURL != "",
		"watchtower_trigger_supported": cfg.ManualTriggerSupported,
		"watchtower_periodic_polls":    cfg.PeriodicPollsEnabled,
	}
	if imageName != "" {
		target["image_name"] = imageName
		target["pull_image_name"] = imageName
		target["channel"] = resolvePanelUpdateChannel(imageName)
	}
	return target
}

// watchtowerAPIUnreachable 判断错误是不是「压根没连上」这一类的传输层失败：
// 端口没人监听（connection refused）、DNS 解析不了（no such host）、拨号超时。
// 只按错误类型判断，不匹配错误文案 —— 这些文本在不同系统、不同语言环境下都不一样，
// 用字符串匹配必然漏判。
func watchtowerAPIUnreachable(err error) bool {
	if err == nil {
		return false
	}

	// DNS 解析失败通常被 net.OpError 包着，少数解析路径会裸抛 DNSError，所以先单独认一次。
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// 拨号阶段失败 = 连接根本没建立起来。
	// 连上之后的读写错误不算，那说明 API 其实在监听，属于另一类问题。
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op == "dial"
	}

	// 兜底：连响应都没拿到就整体超时（例如 http.Client.Timeout 到点）。
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	return false
}

// watchtowerServiceNameFromAPIURL 从 WATCHTOWER_HTTP_API_URL 的 host 部分推导 Watchtower 的
// compose 服务名 —— 容器之间就是靠这个名字做 DNS 解析的。
// 用户可能把服务名改成别的，所以不能写死成参考部署里的名字；
// 遇到 IP、localhost 这种拿去执行 docker 命令只会误导的 host，就返回空串让调用方退回通用说法。
func watchtowerServiceNameFromAPIURL(apiURL string) string {
	raw := strings.TrimSpace(apiURL)
	if raw == "" {
		return ""
	}
	// 允许用户只写 host:port；没有 scheme 时 url.Parse 会把整串当成路径，解析不出 host。
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" || net.ParseIP(host) != nil {
		return ""
	}
	switch strings.ToLower(host) {
	case "localhost", "host.docker.internal":
		return ""
	}
	return host
}

// watchtowerUnreachableHint 生成「Watchtower 的 HTTP API 连不上」时的可操作诊断文案。
// 面板一键更新和 ddp check 的探活共用这一份，避免两处各写各的然后慢慢漂移。
//
// 背景（issue #108）：参考部署使用的 WATCHTOWER_HTTP_API_ENDPOINTS 是 watchtower v1.20.0
// 才引入的，更早的版本遇到不认识的环境变量是静默忽略 —— 容器正常启动、日志也不报错，
// 但 API 端口从头到尾没打开；而 Watchtower 自己带着 enable=false 标签不会自我更新，
// 早期部署的用户本地那份 latest 会一直停在老版本。
func watchtowerUnreachableHint(apiURL string, cause error) string {
	target := strings.TrimSpace(apiURL)
	if target == "" {
		target = "Watchtower HTTP API 地址"
	}
	name := watchtowerServiceNameFromAPIURL(apiURL)
	if name == "" {
		name = "<你的 watchtower 服务名>"
	}

	lines := []string{
		"连不上 Watchtower 的 HTTP API（" + target + "）：这个地址解析不到、或者端口上没有服务在监听，更新请求根本发不出去 —— 不是更新本身失败。",
		"最可能的原因：Watchtower 版本低于 v1.20.0。参考部署里的 WATCHTOWER_HTTP_API_ENDPOINTS 是 v1.20.0 才引入的，更老的版本会静默忽略它 —— 容器照常启动、日志也不报错，但 API 端口不会打开。",
		"第二个原因：Watchtower 打了 com.centurylinklabs.watchtower.enable=false 标签，不会更新自己，本地那份 latest 镜像可能还是很久以前拉的旧版本。",
		"请按两步自查：",
		"1. 看版本：docker compose logs " + name + "（启动日志开头会打印 Watchtower 版本号；不是 compose 部署就用 docker logs <你的 watchtower 容器名>）",
		"2. 拉新版并重建：docker compose pull " + name + " && docker compose up -d " + name,
	}
	if cause != nil {
		lines = append(lines, "原始错误："+cause.Error())
	}
	return strings.Join(lines, "\n")
}

func triggerWatchtowerUpdate(cfg watchtowerRuntimeConfig) (map[string]interface{}, error) {
	if !cfg.Managed {
		return nil, fmt.Errorf("当前部署未启用 Watchtower 托管更新")
	}
	if !cfg.ManualTriggerSupported {
		return nil, fmt.Errorf("当前 Watchtower 未配置 HTTP API 手动触发能力，请先设置 WATCHTOWER_HTTP_API_URL 与 WATCHTOWER_HTTP_API_TOKEN")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Timeout:   2 * time.Minute,
		Transport: transport,
	}

	// async=true 表示请求成功即由 Watchtower 接管后续拉取和重建，
	// 避免大镜像下载超过面板 HTTP 超时后被误报为触发失败。
	query := url.Values{}
	query.Set("async", "true")
	containerName := strings.TrimSpace(os.Getenv("CONTAINER_NAME"))
	if containerName == "" {
		containerName = "daidai-panel"
	}
	// Watchtower 的 container 参数支持正则，锚定并转义后只更新当前面板容器。
	query.Set("container", "^"+regexp.QuoteMeta(containerName)+"$")
	apiURL := strings.TrimRight(cfg.APIURL, "/") + "/v1/update?" + query.Encode()
	req, err := http.NewRequest(http.MethodPost, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构建 Watchtower 更新请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIToken)

	resp, err := client.Do(req)
	if err != nil {
		// 地址根本连不上时，裸抛一句 dial tcp ...: connection refused 用户完全无从自查，
		// 换成带判定结论和自查步骤的诊断（issue #108）；其余失败保持原样。
		if watchtowerAPIUnreachable(err) {
			return nil, errors.New(watchtowerUnreachableHint(cfg.APIURL, err))
		}
		return nil, fmt.Errorf("调用 Watchtower 更新接口失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 Watchtower 更新响应失败: %w", err)
	}

	payload := map[string]interface{}{}
	bodyText := strings.TrimSpace(string(body))
	if bodyText != "" {
		if err := json.Unmarshal(body, &payload); err != nil {
			// async=true 的标准成功响应是 202 + Accepted 文本，不是 JSON。
			// 成功状态保留原始消息即可，错误状态则在下面连同 HTTP 状态返回。
			payload = map[string]interface{}{"message": bodyText}
		}
	}

	if resp.StatusCode >= http.StatusBadRequest {
		if message, ok := payload["error"].(string); ok && strings.TrimSpace(message) != "" {
			return nil, fmt.Errorf("Watchtower 更新触发失败: %s", message)
		}
		if bodyText != "" {
			return nil, fmt.Errorf("Watchtower 更新触发失败: HTTP %d: %s", resp.StatusCode, bodyText)
		}
		return nil, fmt.Errorf("Watchtower 更新触发失败: HTTP %d", resp.StatusCode)
	}

	return payload, nil
}

func buildPanelUpdatePlan() (*panelUpdatePlan, error) {
	return buildPanelUpdatePlanForRelease(nil)
}

func buildPanelUpdatePlanForRelease(release *panelReleaseInfo) (*panelUpdatePlan, error) {
	// 只要部署声明由 Watchtower 托管，页面静默更新和 ddp 都统一走 Watchtower，
	// 即使 full 镜像里仍有 Docker CLI，也不会绕过既有托管配置直接重建容器。
	if watchtowerCfg := currentWatchtowerRuntimeConfig(); watchtowerCfg.Managed {
		// Watchtower 不会重写容器镜像引用，因此保持 Compose 传入的真实 IMAGE_NAME。
		imageName := strings.TrimSpace(os.Getenv("IMAGE_NAME"))
		if isPinnedPanelImageReference(imageName) {
			return nil, fmt.Errorf("当前镜像使用固定版本标签或 digest，Watchtower 只会检查这个固定引用；请把 DAIDAI_PANEL_IMAGE 改为同系列浮动标签后再启用一键或自动更新")
		}
		containerName := strings.TrimSpace(os.Getenv("CONTAINER_NAME"))
		if containerName == "" {
			containerName = "daidai-panel"
		}
		return &panelUpdatePlan{
			DeploymentType: panelUpdateDeploymentDocker,
			UpdateManager:  panelUpdateManagerWatchtower,
			ContainerName:  containerName,
			ImageName:      imageName,
			PullImageName:  imageName,
			Channel:        resolvePanelUpdateChannel(imageName),
			Watchtower:     watchtowerCfg,
		}, nil
	}

	// Magisk 模块版必须排在 Docker 探测之前。
	// 模块版跑在 Android 的 chroot 容器里，既没有 Docker CLI 也没有 docker.sock，
	// 走到下面就只会拿到一句「未提供 Docker CLI，请配置 Watchtower」——
	// 对着手机用户提示 docker compose 是纯粹的误导。
	magiskPlan, magiskErr := buildMagiskPanelUpdatePlan(release)
	if magiskErr == nil {
		return magiskPlan, nil
	}
	// 只有「不是模块版」这一种情况才继续往下走；模块版自身的失败（例如外壳版本过旧）
	// 必须原样抛给用户，否则又会被 Docker 的报错盖掉。
	if !errors.Is(magiskErr, errMagiskRuntimeNotDetected) {
		return nil, magiskErr
	}

	dockerPlan, dockerErr := buildDockerPanelUpdatePlan()
	if dockerErr == nil {
		return dockerPlan, nil
	}

	if shouldRequireDockerPanelUpdate() {
		return nil, dockerErr
	}

	binaryPlan, binaryErr := buildBinaryPanelUpdatePlan(release)
	if binaryErr == nil {
		binaryPlan.UpdateManager = panelUpdateManagerPanel
		return binaryPlan, nil
	}

	return nil, fmt.Errorf("%s；二进制后台更新也不可用：%s", dockerErr.Error(), binaryErr.Error())
}

func buildDockerPanelUpdatePlan() (*panelUpdatePlan, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("当前运行环境未提供 Docker CLI；精简镜像请配置 Watchtower 托管更新，如需沿用 docker.sock 一键更新请使用 full 镜像")
	}

	if _, err := os.Stat(dockerSocketPath); err != nil {
		return nil, fmt.Errorf("当前 Docker 部署未检测到可用的更新托管方式。推荐使用 Watchtower 托管自动更新；如需立即手动更新，请在宿主机执行 docker compose pull && docker compose up -d。早期版本若仍使用面板内 Docker Socket 一键更新，请确认已挂载 %s", dockerSocketPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if output, err := dockerCommandOutput(ctx, "info"); err != nil {
		return nil, formatDockerCommandError("无法连接 Docker 守护进程，请确认 docker.sock 可访问", err, output)
	}

	info, err := inspectCurrentPanelContainer()
	if err != nil {
		return nil, err
	}

	containerName := strings.TrimPrefix(strings.TrimSpace(info.Name), "/")
	if envName := strings.TrimSpace(os.Getenv("CONTAINER_NAME")); envName != "" {
		containerName = envName
	}
	if containerName == "" {
		return nil, fmt.Errorf("无法识别当前面板容器名称，请设置环境变量 CONTAINER_NAME")
	}

	// docker inspect 的 Config.Image 是容器真实运行引用，应优先于可能残留的 IMAGE_NAME。
	// 重建参数会在下面把 IMAGE_NAME 同步成这个真实镜像族，兼容旧 Compose 配置漂移。
	imageName := strings.TrimSpace(info.Config.Image)
	if imageName == "" {
		imageName = strings.TrimSpace(os.Getenv("IMAGE_NAME"))
	}
	if imageName == "" {
		return nil, fmt.Errorf("无法识别当前容器镜像，请设置环境变量 IMAGE_NAME")
	}
	if strings.Contains(imageName, "@") {
		return nil, fmt.Errorf("当前容器使用 digest 固定镜像，不能自动切换到后续版本；请先在 Compose 中改为对应浮动标签")
	}
	imageName = normalizePanelUpdateImageName(imageName)
	if !supportsDockerSocketPanelUpdate(imageName) {
		return nil, fmt.Errorf("当前镜像 %s 不包含新版更新辅助容器所需的 Docker CLI；旧 docker.sock 一键更新只支持 latest-full 或 debian-full，请改用 Watchtower 或切换到 full 镜像", imageName)
	}

	pullImageName, mirrorHost, registryURL := resolveUpdateImageTarget(
		imageName,
		model.GetRegisteredConfig("update_image_mirror"),
	)

	return &panelUpdatePlan{
		DeploymentType:  panelUpdateDeploymentDocker,
		UpdateManager:   panelUpdateManagerPanel,
		ContainerName:   containerName,
		ImageName:       imageName,
		PullImageName:   pullImageName,
		PreviousImageID: normalizeDockerImageID(info.Image),
		Channel:         resolvePanelUpdateChannel(imageName),
		MirrorHost:      mirrorHost,
		RegistryURL:     registryURL,
		RunArgs:         buildContainerRunArgs(containerName, imageName, info),
	}, nil
}

// detectPanelDeploymentTypeHint 在无法构建更新方案时，给前端一个「当前部署形态」的判断依据。
// 只用于展示层挑选正确的手动更新指引，不参与任何真正的更新决策。
func detectPanelDeploymentTypeHint() string {
	if isMagiskPanelUpdateRuntime() {
		return panelUpdateDeploymentMagisk
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return panelUpdateDeploymentDocker
	}
	if _, err := os.Stat(dockerSocketPath); err == nil {
		return panelUpdateDeploymentDocker
	}
	return panelUpdateDeploymentBinary
}

func shouldRequireDockerPanelUpdate() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

func inspectCurrentPanelContainer() (*dockerInspectInfo, error) {
	candidates := uniqueNonEmptyStrings(
		os.Getenv("CONTAINER_NAME"),
		os.Getenv("HOSTNAME"),
		mustHostname(),
		"daidai-panel",
	)

	for _, candidate := range candidates {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		output, err := dockerCommandOutput(ctx, "inspect", "--format", "{{json .}}", candidate)
		cancel()
		if err != nil {
			continue
		}

		var info dockerInspectInfo
		if err := json.Unmarshal(output, &info); err != nil {
			continue
		}
		if strings.TrimSpace(info.Name) == "" {
			continue
		}
		return &info, nil
	}

	return nil, fmt.Errorf("无法识别当前面板容器，请设置环境变量 CONTAINER_NAME 后重试")
}

func normalizeDockerImageID(imageID string) string {
	imageID = strings.TrimSpace(imageID)
	if !strings.HasPrefix(imageID, "sha256:") {
		return ""
	}

	digest := strings.TrimPrefix(imageID, "sha256:")
	if len(digest) != 64 {
		return ""
	}
	for _, ch := range digest {
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F') {
			continue
		}
		return ""
	}
	return "sha256:" + strings.ToLower(digest)
}

func buildContainerRunArgs(containerName, imageName string, info *dockerInspectInfo) []string {
	runArgs := []string{"run", "-d", "--name", containerName}

	restartPolicy := strings.TrimSpace(info.HostConfig.RestartPolicy.Name)
	if restartPolicy != "" && restartPolicy != "no" {
		runArgs = append(runArgs, "--restart", restartPolicy)
	}

	networkMode := strings.TrimSpace(info.HostConfig.NetworkMode)
	if networkMode != "" && networkMode != "default" {
		runArgs = append(runArgs, "--network", networkMode)
	}

	extraHosts := make([]string, 0, len(info.HostConfig.ExtraHosts))
	for _, item := range info.HostConfig.ExtraHosts {
		item = strings.TrimSpace(item)
		if item != "" {
			extraHosts = append(extraHosts, item)
		}
	}
	sort.Strings(extraHosts)
	for _, item := range extraHosts {
		runArgs = append(runArgs, "--add-host", item)
	}

	for _, mapping := range collectPortMappings(info.HostConfig.PortBindings) {
		runArgs = append(runArgs, "-p", mapping)
	}

	for _, volume := range collectVolumeMappings(info) {
		runArgs = append(runArgs, "-v", volume)
	}

	for _, env := range filterContainerEnv(info.Config.Env) {
		if strings.HasPrefix(env, "IMAGE_NAME=") {
			continue
		}
		runArgs = append(runArgs, "-e", env)
	}
	// 版本号标签会被归一化为当前镜像族的滚动标签，重建时必须同步环境变量，
	// 否则下一次更新会再次读取旧标签并错误判断目标镜像。
	runArgs = append(runArgs, "-e", "IMAGE_NAME="+imageName)

	runArgs = append(runArgs, imageName)
	return runArgs
}

func collectPortMappings(portBindings map[string][]struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}) []string {
	keys := make([]string, 0, len(portBindings))
	for port := range portBindings {
		keys = append(keys, port)
	}
	sort.Strings(keys)

	var result []string
	for _, port := range keys {
		bindings := portBindings[port]
		for _, binding := range bindings {
			if strings.TrimSpace(binding.HostPort) == "" {
				continue
			}

			containerPort := strings.Split(port, "/")[0]
			mapping := binding.HostPort + ":" + containerPort
			hostIP := strings.TrimSpace(binding.HostIP)
			if hostIP != "" && hostIP != "0.0.0.0" && hostIP != "::" {
				mapping = hostIP + ":" + mapping
			}
			result = append(result, mapping)
		}
	}
	return result
}

func collectVolumeMappings(info *dockerInspectInfo) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(info.HostConfig.Binds)+len(info.Mounts))
	for _, bind := range info.HostConfig.Binds {
		bind = strings.TrimSpace(bind)
		if bind == "" {
			continue
		}
		key, ok := buildVolumeMappingDedupKeyFromRaw(bind)
		if !ok {
			key = bind
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, bind)
	}

	for _, mount := range info.Mounts {
		destination := strings.TrimSpace(mount.Destination)
		if destination == "" {
			continue
		}

		var source string
		switch mount.Type {
		case "bind":
			source = strings.TrimSpace(mount.Source)
		case "volume":
			source = strings.TrimSpace(mount.Name)
			if source == "" {
				source = strings.TrimSpace(mount.Source)
			}
		default:
			continue
		}

		if source == "" {
			continue
		}

		mapping := source + ":" + destination
		if !mount.RW {
			mapping += ":ro"
		}
		key := buildVolumeMappingDedupKey(source, destination, !mount.RW)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, mapping)
	}

	sort.Strings(result)
	return result
}

func buildVolumeMappingDedupKey(source, destination string, readOnly bool) string {
	mode := "rw"
	if readOnly {
		mode = "ro"
	}
	return strings.TrimSpace(source) + "\x00" + strings.TrimSpace(destination) + "\x00" + mode
}

func buildVolumeMappingDedupKeyFromRaw(mapping string) (string, bool) {
	mapping = strings.TrimSpace(mapping)
	if mapping == "" {
		return "", false
	}

	parts := strings.Split(mapping, ":")
	if len(parts) < 2 {
		return "", false
	}

	source := strings.TrimSpace(parts[0])
	destination := strings.TrimSpace(parts[1])
	if source == "" || destination == "" {
		return "", false
	}

	readOnly := false
	for _, rawOptionGroup := range parts[2:] {
		for _, option := range strings.Split(rawOptionGroup, ",") {
			if strings.EqualFold(strings.TrimSpace(option), "ro") {
				readOnly = true
				break
			}
		}
		if readOnly {
			break
		}
	}

	return buildVolumeMappingDedupKey(source, destination, readOnly), true
}

func filterContainerEnv(envList []string) []string {
	skipPrefixes := []string{
		"PATH=",
		"HOME=",
		"HOSTNAME=",
		"LANG=",
		"LC_",
		"TERM=",
		"PWD=",
		"SHLVL=",
		"_=",
	}

	result := make([]string, 0, len(envList))
	for _, env := range envList {
		env = strings.TrimSpace(env)
		if env == "" {
			continue
		}

		skip := false
		for _, prefix := range skipPrefixes {
			if strings.HasPrefix(env, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			result = append(result, env)
		}
	}

	return result
}

func executePanelUpdate(plan *panelUpdatePlan) {
	executePanelUpdateWithOptions(plan, panelUpdateExecutionOptions{})
}

func executePanelUpdateWithOptions(plan *panelUpdatePlan, options panelUpdateExecutionOptions) {
	if plan.UpdateManager == panelUpdateManagerWatchtower {
		panelUpdater.setRunning("watchtower-triggering", "正在请求 Watchtower 检查并更新面板容器")
		if _, err := triggerWatchtowerUpdate(plan.Watchtower); err != nil {
			panelUpdater.fail(err)
			if options.AutoUpdate {
				notifyAutoUpdateFailure(options.TargetVersion, err)
			}
			return
		}
		// HTTP 请求成功只代表 Watchtower 已接管后续检查；这里立即进入终态，
		// 避免没有新镜像、容器不重启时永久占用更新锁。
		panelUpdater.complete("watchtower-triggered", "已请求 Watchtower 立即检查并执行容器更新")
		return
	}

	if plan.DeploymentType == panelUpdateDeploymentMagisk {
		executeMagiskPanelUpdateWithOptions(plan, options)
		return
	}
	if plan.DeploymentType == panelUpdateDeploymentBinary {
		executeBinaryPanelUpdateWithOptions(plan, options)
		return
	}
	executeDockerPanelUpdateWithOptions(plan, options)
}

func executeDockerPanelUpdateWithOptions(plan *panelUpdatePlan, options panelUpdateExecutionOptions) {
	panelUpdater.setRunning("preparing", fmt.Sprintf("正在检查镜像仓库连通性 %s", plan.RegistryURL))
	if err := preflightUpdateRegistry(plan); err != nil {
		panelUpdater.fail(err)
		if options.AutoUpdate {
			notifyAutoUpdateFailure(options.TargetVersion, err)
		}
		return
	}

	panelUpdater.setRunning("pulling", fmt.Sprintf("正在拉取最新镜像 %s", plan.PullImageName))

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 20*time.Minute)
	pullOutput, err := dockerCommandOutput(pullCtx, "pull", plan.PullImageName)
	pullCancel()
	if err != nil {
		formatted := formatPanelUpdatePullError(plan, err, pullOutput)
		panelUpdater.fail(formatted)
		if options.AutoUpdate {
			notifyAutoUpdateFailure(options.TargetVersion, formatted)
		}
		return
	}

	if plan.PullImageName != "" && plan.PullImageName != plan.ImageName {
		panelUpdater.setRunning("pulling", fmt.Sprintf("镜像已拉取完成，正在同步更新标签 %s", plan.ImageName))

		tagCtx, tagCancel := context.WithTimeout(context.Background(), time.Minute)
		tagOutput, tagErr := dockerCommandOutput(tagCtx, "tag", plan.PullImageName, plan.ImageName)
		tagCancel()
		if tagErr != nil {
			formatted := formatDockerCommandError("同步更新镜像标签失败", tagErr, tagOutput)
			panelUpdater.fail(formatted)
			if options.AutoUpdate {
				notifyAutoUpdateFailure(options.TargetVersion, formatted)
			}
			return
		}

		rmiCtx, rmiCancel := context.WithTimeout(context.Background(), 30*time.Second)
		dockerCommandOutput(rmiCtx, "rmi", plan.PullImageName)
		rmiCancel()
	}

	// 更新辅助容器直接使用目标业务镜像执行 docker 命令；拉取后先验证工具，
	// 避免自定义镜像缺少 Docker CLI 时仍进入 restarting 并留下未完成的更新。
	panelUpdater.setRunning("validating", "镜像已拉取完成，正在验证更新辅助工具")
	validateCtx, validateCancel := context.WithTimeout(context.Background(), time.Minute)
	validateOutput, validateErr := dockerCommandOutput(validateCtx, "run", "--rm", "--entrypoint", "sh", plan.ImageName, "-c", "command -v docker >/dev/null 2>&1")
	validateCancel()
	if validateErr != nil {
		formatted := formatDockerCommandError("目标镜像缺少 Docker CLI，不能继续使用旧 docker.sock 一键更新；请改用 Watchtower 或 full 镜像", validateErr, validateOutput)
		panelUpdater.fail(formatted)
		if options.AutoUpdate {
			notifyAutoUpdateFailure(options.TargetVersion, formatted)
		}
		return
	}

	panelUpdater.setRunning("scheduling", "镜像已拉取完成，正在启动更新辅助容器")

	helperScript := buildPanelUpdateHelperScript(plan)
	helperArgs := []string{
		"run", "-d", "--rm",
		"-v", dockerSocketPath + ":" + dockerSocketPath,
		"--entrypoint", "sh",
		plan.ImageName,
		"-c", helperScript,
	}

	helperCtx, helperCancel := context.WithTimeout(context.Background(), time.Minute)
	helperOutput, err := dockerCommandOutput(helperCtx, helperArgs...)
	helperCancel()
	if err != nil {
		formatted := formatDockerCommandError("启动更新辅助容器失败", err, helperOutput)
		panelUpdater.fail(formatted)
		if options.AutoUpdate {
			notifyAutoUpdateFailure(options.TargetVersion, formatted)
		}
		return
	}

	panelUpdater.setRestarting("更新任务已启动，正在重建面板容器并切换到新版本")
}

func buildPanelUpdateBeginMessage(plan *panelUpdatePlan) string {
	if plan.UpdateManager == panelUpdateManagerWatchtower {
		return "更新环境校验通过，准备触发 Watchtower 检查"
	}
	if plan.DeploymentType == panelUpdateDeploymentMagisk {
		if strings.TrimSpace(plan.AssetName) != "" {
			return fmt.Sprintf("更新环境校验通过，准备下载更新包 %s（只更新面板程序与前端）", plan.AssetName)
		}
		return "更新环境校验通过，准备下载更新包（只更新面板程序与前端）"
	}
	if plan.DeploymentType == panelUpdateDeploymentBinary {
		if strings.TrimSpace(plan.AssetName) != "" {
			return fmt.Sprintf("更新环境校验通过，准备下载二进制更新包 %s", plan.AssetName)
		}
		return "更新环境校验通过，准备下载二进制更新包"
	}
	return "更新环境校验通过，准备检查镜像仓库并拉取最新镜像"
}

func buildPanelUpdateTarget(plan *panelUpdatePlan) gin.H {
	updateManager := plan.UpdateManager
	if updateManager == "" {
		updateManager = panelUpdateManagerPanel
	}
	target := gin.H{
		"deployment_type": plan.DeploymentType,
		"update_manager":  updateManager,
	}

	if plan.DeploymentType == panelUpdateDeploymentMagisk {
		target["release_version"] = plan.ReleaseVersion
		target["asset_name"] = plan.AssetName
		target["asset_url"] = plan.AssetURL
		target["web_dir"] = plan.WebDir
		target["module_dir"] = plan.ModuleDir
		target["binary_name"] = plan.BinaryName
		return target
	}

	if plan.DeploymentType == panelUpdateDeploymentBinary {
		target["release_version"] = plan.ReleaseVersion
		target["asset_name"] = plan.AssetName
		target["asset_url"] = plan.AssetURL
		target["install_dir"] = plan.InstallDir
		target["binary_name"] = plan.BinaryName
		return target
	}

	target["container_name"] = plan.ContainerName
	target["image_name"] = plan.ImageName
	target["pull_image_name"] = plan.PullImageName
	target["channel"] = plan.Channel
	target["mirror_host"] = plan.MirrorHost
	target["registry_url"] = plan.RegistryURL
	return target
}

func buildPanelUpdateHelperScript(plan *panelUpdatePlan) string {
	quotedArgs := make([]string, 0, len(plan.RunArgs))
	for _, arg := range plan.RunArgs {
		quotedArgs = append(quotedArgs, shellQuote(arg))
	}

	cleanupBlock := ""
	if previousImageID := normalizeDockerImageID(plan.PreviousImageID); previousImageID != "" {
		cleanupBlock = fmt.Sprintf(`
if [ "$status" -eq 0 ]; then
  docker image rm %s >/dev/null 2>&1 || true
fi`, shellQuote(previousImageID))
	}

	// 先 docker stop -t 10 再 rm -f：rm -f 等于直接 SIGKILL，面板一点收尾都做不了（任务停在运行中、
	// 软链残留、WAL 没 checkpoint）。stop 给面板发 SIGTERM，面板 8 秒内自行收完尾退出；
	// stop 失败（容器已经不在等）时 || true 放行，后面的 rm -f 照样兜底。
	return fmt.Sprintf(`sleep 2
docker stop -t 10 %s >/dev/null 2>&1 || true
docker rm -f %s >/dev/null 2>&1 || true
docker %s
status=$?%s
exit "$status"`,
		shellQuote(plan.ContainerName),
		shellQuote(plan.ContainerName),
		strings.Join(quotedArgs, " "),
		cleanupBlock,
	)
}

func dockerCommandOutput(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd.CombinedOutput()
}

func preflightUpdateRegistry(plan *panelUpdatePlan) error {
	registryURL := strings.TrimSpace(plan.RegistryURL)
	if registryURL == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{
		Timeout:   12 * time.Second,
		Transport: transport,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, registryURL, nil)
	if err != nil {
		return fmt.Errorf("更新前镜像仓库连通性检查失败：%w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("更新前镜像仓库连通性检查失败：%w。%s", err, buildPanelUpdateNetworkHint(plan))
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("更新前镜像仓库连通性检查失败：镜像仓库返回状态 %d。%s", resp.StatusCode, buildPanelUpdateNetworkHint(plan))
	}

	return nil
}

func formatDockerCommandError(prefix string, err error, output []byte) error {
	detail := trimCommandOutput(output)
	switch {
	case detail != "":
		return fmt.Errorf("%s: %s", prefix, detail)
	case err != nil:
		return fmt.Errorf("%s: %v", prefix, err)
	default:
		return fmt.Errorf("%s", prefix)
	}
}

func formatPanelUpdatePullError(plan *panelUpdatePlan, err error, output []byte) error {
	base := formatDockerCommandError("拉取最新镜像失败", err, output)
	hint := buildPanelUpdatePullHint(plan, err, output)
	if hint == "" {
		return base
	}
	return fmt.Errorf("%s。%s", strings.TrimSpace(base.Error()), hint)
}

func trimCommandOutput(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return ""
	}

	lines := strings.Split(text, "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func buildPanelUpdatePullHint(plan *panelUpdatePlan, err error, output []byte) string {
	lower := strings.ToLower(strings.TrimSpace(string(output)))
	if err != nil {
		lower += "\n" + strings.ToLower(err.Error())
	}

	if strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "client.timeout exceeded while awaiting headers") ||
		strings.Contains(lower, "i/o timeout") ||
		strings.Contains(lower, "tls handshake timeout") ||
		strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "temporary failure in name resolution") {
		return fmt.Sprintf("这通常是宿主机到镜像仓库的网络或 DNS 异常。目标仓库：%s。%s", plan.RegistryURL, buildPanelUpdateNetworkHint(plan))
	}

	return ""
}

func buildPanelUpdateNetworkHint(plan *panelUpdatePlan) string {
	if strings.TrimSpace(plan.MirrorHost) != "" {
		return fmt.Sprintf("当前系统更新镜像源为 %s，请检查该镜像源是否可访问；如需恢复直连，可在“系统设置 / 网络代理”中清空系统更新镜像源。", plan.MirrorHost)
	}
	return "当前将直连 Docker Hub；如宿主机访问 Docker Hub 较慢，可在“系统设置 / 网络代理”中配置系统更新镜像源。"
}

func uniqueNonEmptyStrings(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func resolveUpdateImageTarget(imageName, mirrorHost string) (pullImageName, resolvedMirrorHost, registryURL string) {
	imageName = strings.TrimSpace(imageName)
	mirrorHost = strings.TrimSpace(mirrorHost)
	registryHost, repoRef := splitImageRegistry(imageName)
	baseImage, _, _ := splitImageTag(imageName)
	_, repoIdentifier := splitImageRegistry(baseImage)
	if strings.TrimSpace(repoIdentifier) == "" {
		repoIdentifier = strings.TrimSpace(baseImage)
	}

	if mirrorHost != "" {
		if repoRef == "" {
			repoRef = repoIdentifier
		}
		if repoIdentifier != "linzixuanzz/daidai-panel" {
			return imageName, "", buildRegistryEndpoint(registryHost)
		}
		if registryHost == mirrorHost {
			return imageName, mirrorHost, buildRegistryEndpoint(mirrorHost)
		}
		return mirrorHost + "/" + repoRef, mirrorHost, buildRegistryEndpoint(mirrorHost)
	}

	return imageName, "", buildRegistryEndpoint(registryHost)
}

func splitImageRegistry(imageName string) (registryHost, repoRef string) {
	imageName = strings.TrimSpace(imageName)
	if imageName == "" {
		return defaultDockerHubRegistryHost, ""
	}

	parts := strings.Split(imageName, "/")
	if len(parts) <= 1 || !isExplicitRegistryHost(parts[0]) {
		return defaultDockerHubRegistryHost, imageName
	}

	registryHost = strings.ToLower(strings.TrimSpace(parts[0]))
	switch registryHost {
	case "docker.io", "index.docker.io":
		registryHost = defaultDockerHubRegistryHost
	}
	return registryHost, strings.Join(parts[1:], "/")
}

func isExplicitRegistryHost(segment string) bool {
	segment = strings.ToLower(strings.TrimSpace(segment))
	return strings.Contains(segment, ".") || strings.Contains(segment, ":") || segment == "localhost"
}

func buildRegistryEndpoint(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		host = defaultDockerHubRegistryHost
	}
	return "https://" + host + "/v2/"
}

func mustHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return ""
	}
	return hostname
}

func respondUpdateConflict(c *gin.Context, message string) {
	response.Error(c, http.StatusConflict, message)
}

func normalizePanelUpdateImageName(imageName string) string {
	imageName = strings.TrimSpace(imageName)
	if strings.Contains(imageName, "@") {
		// digest 是用户明确锁定的不可变引用，不能静默改成 latest。
		return imageName
	}
	baseImage, tag, _ := splitImageTag(imageName)
	_, repoRef := splitImageRegistry(baseImage)
	if repoRef != "linzixuanzz/daidai-panel" {
		return imageName
	}

	rollingTag := resolvePanelUpdateRollingTag(tag)
	if rollingTag == "" {
		// 未知标签保持原样，避免把测试版或后续新增镜像线静默降级到 latest。
		return imageName
	}
	return baseImage + ":" + rollingTag
}

func supportsDockerSocketPanelUpdate(imageName string) bool {
	_, tag, hasTag := splitImageTag(strings.TrimSpace(imageName))
	if !hasTag {
		return false
	}

	// 进入这里前官方固定 full 标签已归一化为滚动标签；自定义仓库也必须显式使用 full 标签，
	// 防止旧 docker.sock 链先拉取精简镜像，再到辅助容器检查阶段才失败。
	tag = strings.ToLower(strings.TrimSpace(tag))
	return tag == "latest-full" || tag == "debian-full"
}

func isPinnedPanelImageReference(imageName string) bool {
	imageName = strings.TrimSpace(imageName)
	if strings.Contains(imageName, "@") {
		return true
	}

	baseImage, tag, hasTag := splitImageTag(imageName)
	_, repoRef := splitImageRegistry(baseImage)
	if !hasTag || repoRef != "linzixuanzz/daidai-panel" {
		return false
	}

	tag = strings.ToLower(strings.TrimSpace(tag))
	switch tag {
	case "latest", "debian", "latest-full", "debian-full",
		"latest-3.10", "latest-3.11", "debian-3.10", "debian-3.11", "latest-all", "debian-all",
		"latest3.10", "latest3.11", "debian3.10", "debian3.11", "latestall", "debianall":
		return false
	}

	// 只阻止能够明确识别的官方固定版本；preview 等自定义浮动通道保持原行为。
	return resolvePanelUpdateRollingTag(tag) != ""
}

func resolvePanelUpdateChannel(imageName string) string {
	_, tag, _ := splitImageTag(strings.TrimSpace(imageName))
	return resolvePanelUpdateChannelFromTag(tag)
}

func resolvePanelUpdateChannelFromTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	rollingTag := resolvePanelUpdateRollingTag(tag)
	if rollingTag == "debian" || strings.HasPrefix(rollingTag, "debian-") || tag == "debian" || strings.HasSuffix(tag, "-debian") {
		return "debian"
	}
	return "latest"
}

func resolvePanelUpdateRollingTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	switch tag {
	case "", "latest":
		return "latest"
	case "debian", "latest-full", "debian-full", "latest-3.10", "latest-3.11", "debian-3.10", "debian-3.11", "latest-all", "debian-all":
		return tag
	case "latest3.10":
		return "latest-3.10"
	case "latest3.11":
		return "latest-3.11"
	case "latestall":
		return "latest-all"
	case "debian3.10":
		return "debian-3.10"
	case "debian3.11":
		return "debian-3.11"
	case "debianall":
		return "debian-all"
	}

	// 正式版本标签转换为对应滚动标签；同时兼容历史 Debian 无连字符格式。
	versionSuffixes := []struct {
		suffix  string
		rolling string
	}{
		{"-debian-3.10", "debian-3.10"},
		{"-debian3.10", "debian-3.10"},
		{"-debian-3.11", "debian-3.11"},
		{"-debian3.11", "debian-3.11"},
		{"-debian-full", "debian-full"},
		{"-debian-all", "debian-all"},
		{"-debianall", "debian-all"},
		{"-debian", "debian"},
		{"-3.10", "latest-3.10"},
		{"-3.11", "latest-3.11"},
		{"-full", "latest-full"},
		{"-all", "latest-all"},
	}
	for _, item := range versionSuffixes {
		if strings.HasSuffix(tag, item.suffix) && isPanelVersionTag(strings.TrimSuffix(tag, item.suffix)) {
			return item.rolling
		}
	}
	if isPanelVersionTag(tag) {
		return "latest"
	}
	return ""
}

func isPanelVersionTag(tag string) bool {
	tag = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(tag)), "v")
	parts := strings.Split(tag, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func splitImageTag(imageName string) (base string, tag string, hasTag bool) {
	imageName = strings.TrimSpace(imageName)
	if imageName == "" {
		return "", "", false
	}

	if digestIdx := strings.Index(imageName, "@"); digestIdx >= 0 {
		imageName = imageName[:digestIdx]
	}

	lastSlash := strings.LastIndex(imageName, "/")
	lastColon := strings.LastIndex(imageName, ":")
	if lastColon > lastSlash {
		return imageName[:lastColon], imageName[lastColon+1:], true
	}

	return imageName, "", false
}
