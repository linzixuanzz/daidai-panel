package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"

	"gorm.io/gorm"
)

type LegacyBackupData struct {
	Version   string                `json:"version"`
	CreatedAt time.Time             `json:"created_at"`
	Tasks     []model.Task          `json:"tasks"`
	EnvVars   []BackupEnvVar        `json:"env_vars"`
	Subs      []model.Subscription  `json:"subscriptions"`
	Channels  []model.NotifyChannel `json:"notify_channels"`
	SSHKeys   []model.SSHKey        `json:"ssh_keys"`
	Configs   []model.SystemConfig  `json:"system_configs"`
	Scripts   []ScriptFile          `json:"scripts,omitempty"`
	Deps      []model.Dependency    `json:"dependencies,omitempty"`
}

func createBackupArchive(options BackupCreateOptions) (string, error) {
	selection := options.Selection.NormalizeDefaults()
	if !selection.Any() {
		return "", fmt.Errorf("至少选择一个备份项")
	}

	manifest, err := buildBackupManifest(selection)
	if err != nil {
		return "", err
	}

	archiveData, err := buildBackupArchive(manifest)
	if err != nil {
		return "", err
	}

	backupDir := filepath.Join(config.C.Data.Dir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create backup dir: %w", err)
	}

	var (
		filename  string
		finalData []byte
	)

	if strings.TrimSpace(options.Password) != "" {
		finalData, err = encryptData(archiveData, options.Password)
		if err != nil {
			return "", fmt.Errorf("failed to encrypt backup: %w", err)
		}
		filename = normalizeBackupArchiveName(options.Name, true)
	} else {
		finalData = archiveData
		filename = normalizeBackupArchiveName(options.Name, false)
	}

	filePath := filepath.Join(backupDir, filename)
	if err := os.WriteFile(filePath, finalData, 0o644); err != nil {
		return "", fmt.Errorf("failed to write backup: %w", err)
	}

	return filePath, nil
}

func normalizeBackupArchiveName(raw string, encrypted bool) string {
	fallback := fmt.Sprintf("backup_%s", time.Now().Format("20060102_150405"))
	name := strings.TrimSpace(raw)
	for _, suffix := range []string{".tar.gz", ".tgz", ".enc", ".json"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			name = name[:len(name)-len(suffix)]
			break
		}
	}
	if name == "" {
		name = fallback
	}

	name = strings.Map(func(r rune) rune {
		switch {
		case r < 32:
			return -1
		case strings.ContainsRune(`<>:"/\|?*`, r):
			return '_'
		default:
			return r
		}
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		name = fallback
	}

	if encrypted {
		return name + ".enc"
	}
	return name + ".tgz"
}

func buildBackupManifest(selection BackupSelection) (BackupManifest, error) {
	manifest := BackupManifest{
		Format:    "daidai-panel-backup",
		Version:   "0.4.0",
		Source:    "daidai-panel",
		CreatedAt: time.Now(),
		Selection: selection,
	}

	if selection.Configs {
		cfgBundle, err := snapshotConfigBundle()
		if err != nil {
			return BackupManifest{}, err
		}
		manifest.Data.Configs = cfgBundle
	}

	if selection.Tasks {
		// 不能直接 Find(&manifest.Data.Tasks)：清单里存的是 BackupTask，标签要单独拎成数组。
		// 形态照下面紧邻的 EnvVars 分支。
		var tasks []model.Task
		if err := database.DB.Order("id ASC").Find(&tasks).Error; err != nil {
			return BackupManifest{}, fmt.Errorf("load tasks: %w", err)
		}
		manifest.Data.Tasks = make([]BackupTask, 0, len(tasks))
		for _, task := range tasks {
			manifest.Data.Tasks = append(manifest.Data.Tasks, backupTaskFromModel(task))
		}
	}

	if selection.EnvVars {
		var envVars []model.EnvVar
		if err := database.DB.Order("position ASC, id ASC").Find(&envVars).Error; err != nil {
			return BackupManifest{}, fmt.Errorf("load env vars: %w", err)
		}
		manifest.Data.EnvVars = make([]BackupEnvVar, 0, len(envVars))
		for _, envVar := range envVars {
			manifest.Data.EnvVars = append(manifest.Data.EnvVars, backupEnvVarFromModel(envVar))
		}
	}

	if selection.Subscriptions {
		// 同上：清单里存的是 BackupSubscription，auth_token 要单独带出来。
		var subscriptions []model.Subscription
		if err := database.DB.Order("id ASC").Find(&subscriptions).Error; err != nil {
			return BackupManifest{}, fmt.Errorf("load subscriptions: %w", err)
		}
		manifest.Data.Subscriptions = make([]BackupSubscription, 0, len(subscriptions))
		for _, subscription := range subscriptions {
			manifest.Data.Subscriptions = append(manifest.Data.Subscriptions, backupSubscriptionFromModel(subscription))
		}

		var sshKeys []model.SSHKey
		if err := database.DB.Order("id ASC").Find(&sshKeys).Error; err != nil {
			return BackupManifest{}, fmt.Errorf("load ssh keys: %w", err)
		}
		for _, key := range sshKeys {
			manifest.Data.SSHKeys = append(manifest.Data.SSHKeys, BackupSSHKey{
				ID:         key.ID,
				Name:       key.Name,
				PrivateKey: key.PrivateKey,
				CreatedAt:  key.CreatedAt,
				UpdatedAt:  key.UpdatedAt,
			})
		}
	}

	if selection.Dependencies {
		var deps []model.Dependency
		if err := database.DB.Order("id ASC").Find(&deps).Error; err != nil {
			return BackupManifest{}, fmt.Errorf("load dependencies: %w", err)
		}
		for _, dep := range deps {
			manifest.Data.Dependencies = append(manifest.Data.Dependencies, BackupDependency{
				Type:          dep.Type,
				Name:          dep.Name,
				PythonVersion: dep.PythonVersion,
			})
		}
	}

	if selection.TaskViews {
		if err := database.DB.Order("sort_order ASC, id ASC").Find(&manifest.Data.TaskViews).Error; err != nil {
			return BackupManifest{}, fmt.Errorf("load task views: %w", err)
		}
	}

	if selection.Logs {
		var taskLogs []model.TaskLog
		if err := database.DB.Preload("Task").Order("id ASC").Find(&taskLogs).Error; err != nil {
			return BackupManifest{}, fmt.Errorf("load task logs: %w", err)
		}
		for _, logItem := range taskLogs {
			taskName := ""
			if logItem.Task != nil {
				taskName = logItem.Task.Name
			}
			manifest.Data.TaskLogs = append(manifest.Data.TaskLogs, BackupTaskLog{
				TaskID:    logItem.TaskID,
				TaskName:  taskName,
				Content:   logItem.Content,
				Status:    logItem.Status,
				Duration:  logItem.Duration,
				LogPath:   logItem.LogPath,
				StartedAt: logItem.StartedAt,
				EndedAt:   logItem.EndedAt,
				CreatedAt: logItem.CreatedAt,
				UpdatedAt: logItem.UpdatedAt,
			})
		}
	}

	return manifest, nil
}

func snapshotConfigBundle() (BackupConfigBundle, error) {
	bundle := BackupConfigBundle{}
	mirrors := CurrentDependencyMirrorSettings()
	bundle.DependencyMirrors = &mirrors

	if err := database.DB.Order("key ASC").Find(&bundle.SystemConfigs).Error; err != nil {
		return BackupConfigBundle{}, fmt.Errorf("load system configs: %w", err)
	}

	var apps []model.OpenApp
	if err := database.DB.Order("id ASC").Find(&apps).Error; err != nil {
		return BackupConfigBundle{}, fmt.Errorf("load open apps: %w", err)
	}
	for _, app := range apps {
		bundle.OpenApps = append(bundle.OpenApps, BackupOpenApp{
			ID:        app.ID,
			Name:      app.Name,
			AppKey:    app.AppKey,
			AppSecret: app.AppSecret,
			Scopes:    app.Scopes,
			Enabled:   app.Enabled,
			RateLimit: app.RateLimit,
			CreatedAt: app.CreatedAt,
			UpdatedAt: app.UpdatedAt,
		})
	}

	var channels []model.NotifyChannel
	if err := database.DB.Order("id ASC").Find(&channels).Error; err != nil {
		return BackupConfigBundle{}, fmt.Errorf("load notify channels: %w", err)
	}
	for _, channel := range channels {
		bundle.NotifyChannels = append(bundle.NotifyChannels, BackupNotifyChannel{
			ID:        channel.ID,
			Name:      channel.Name,
			Type:      channel.Type,
			Config:    channel.Config,
			PushScope: channel.EffectivePushScope(),
			Enabled:   channel.Enabled,
			CreatedAt: channel.CreatedAt,
			UpdatedAt: channel.UpdatedAt,
		})
	}

	var users []model.User
	if err := database.DB.Order("id ASC").Find(&users).Error; err != nil {
		return BackupConfigBundle{}, fmt.Errorf("load users: %w", err)
	}
	for _, user := range users {
		bundle.Users = append(bundle.Users, BackupUser{
			ID:           user.ID,
			Username:     user.Username,
			PasswordHash: user.Password,
			Role:         user.Role,
			Enabled:      user.Enabled,
			LastLoginAt:  user.LastLoginAt,
			CreatedAt:    user.CreatedAt,
			UpdatedAt:    user.UpdatedAt,
		})
	}

	if err := database.DB.Order("id ASC").Find(&bundle.IPWhitelists).Error; err != nil {
		return BackupConfigBundle{}, fmt.Errorf("load ip whitelists: %w", err)
	}

	var twoFactor []model.TwoFactorAuth
	if err := database.DB.Order("id ASC").Find(&twoFactor).Error; err != nil {
		return BackupConfigBundle{}, fmt.Errorf("load two factor auths: %w", err)
	}
	for _, item := range twoFactor {
		bundle.TwoFactorAuths = append(bundle.TwoFactorAuths, BackupTwoFactorAuth{
			UserID:    item.UserID,
			Secret:    item.Secret,
			Enabled:   item.Enabled,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		})
	}

	var preferences []model.UserPreference
	if err := database.DB.Order("id ASC").Find(&preferences).Error; err != nil {
		return BackupConfigBundle{}, fmt.Errorf("load user preferences: %w", err)
	}
	for _, item := range preferences {
		bundle.UserPreferences = append(bundle.UserPreferences, BackupUserPreference{
			UserID:    item.UserID,
			Editor:    item.Editor,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		})
	}

	return bundle, nil
}

func buildBackupArchive(manifest BackupManifest) ([]byte, error) {
	var raw bytes.Buffer
	gzipWriter := gzip.NewWriter(&raw)
	tarWriter := tar.NewWriter(gzipWriter)

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := writeTarBytes(tarWriter, "manifest.json", manifestData, 0o644, manifest.CreatedAt); err != nil {
		return nil, err
	}

	if manifest.Selection.Scripts {
		if err := addDirectoryToTar(tarWriter, config.C.Data.ScriptsDir, "files/scripts"); err != nil {
			return nil, err
		}
	}

	if manifest.Selection.Logs {
		if err := addDirectoryToTar(tarWriter, config.C.Data.LogDir, "files/logs"); err != nil {
			return nil, err
		}
		panelLogPath := filepath.Join(config.C.Data.Dir, "panel.log")
		if _, err := os.Stat(panelLogPath); err == nil {
			if err := addFileToTar(tarWriter, panelLogPath, "files/panel.log"); err != nil {
				return nil, err
			}
		}
	}

	if err := tarWriter.Close(); err != nil {
		return nil, fmt.Errorf("close tar writer: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("close gzip writer: %w", err)
	}

	return raw.Bytes(), nil
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, mode int64, modTime time.Time) error {
	header := &tar.Header{
		Name:    filepath.ToSlash(name),
		Mode:    mode,
		Size:    int64(len(data)),
		ModTime: modTime,
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write tar body %s: %w", name, err)
	}
	return nil
}

func addFileToTar(tw *tar.Writer, sourcePath, archivePath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil || info.IsDir() {
		return nil
	}

	file, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", sourcePath, err)
	}
	defer file.Close()

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("build tar header %s: %w", sourcePath, err)
	}
	header.Name = filepath.ToSlash(archivePath)

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header %s: %w", sourcePath, err)
	}
	if _, err := io.Copy(tw, file); err != nil {
		return fmt.Errorf("copy tar file %s: %w", sourcePath, err)
	}
	return nil
}

func addDirectoryToTar(tw *tar.Writer, sourceDir, archiveRoot string) error {
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return nil
	}

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return err
		}
		if info.IsDir() {
			// 脚本目录打包时也要复用统一的异常目录过滤规则，
			// 避免已被隔离/应忽略的污染目录重新进入备份包。
			if strings.HasSuffix(filepath.Clean(archiveRoot), filepath.Clean("files"+string(os.PathSeparator)+"scripts")) && ShouldIgnoreScriptPath(sourceDir, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(filepath.Clean(archiveRoot), filepath.Clean("files"+string(os.PathSeparator)+"scripts")) && ShouldIgnoreScriptPath(sourceDir, path) {
			return nil
		}

		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		// .git 依然照常打包（还原行为完全不变、零数据丢失风险），
		// 但 .git/config 里存着订阅 Token 鉴权注入到 remote URL 的 PAT，
		// 必须在写进 tar 之前把凭据清掉。
		if isGitConfigRelativePath(relPath) {
			return addSanitizedGitConfigToTar(tw, path, filepath.Join(archiveRoot, relPath))
		}

		return addFileToTar(tw, path, filepath.Join(archiveRoot, relPath))
	})
}

// isGitConfigRelativePath 判断相对路径是不是某个 git 仓库的 .git/config。
func isGitConfigRelativePath(relPath string) bool {
	segments := strings.Split(filepath.ToSlash(relPath), "/")
	if len(segments) < 2 {
		return false
	}
	return strings.EqualFold(segments[len(segments)-2], ".git") &&
		strings.EqualFold(segments[len(segments)-1], "config")
}

// gitURLCredentialPattern 匹配 URL 里带密码的 userinfo（形如 https://user:token@host/...）。
// 刻意要求 userinfo 中含冒号：ssh://git@host/... 这种只有用户名、没有凭据，不能动，
// 否则还原后 SSH 订阅会连不上。
var gitURLCredentialPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/@\s:]+:[^/@\s]*@`)

// sanitizeGitConfigCredentials 去掉 git config 内容里 remote URL 内嵌的账号密码。
// 只作用于写进备份包的那份字节流，不动磁盘上的真实 .git/config ——
// 动了会让后续 fetch 直接失去鉴权。还原后下一次拉取会由
// syncGitRemoteWithCallback 重新写回带凭据的 remote URL。
func sanitizeGitConfigCredentials(content []byte) []byte {
	return gitURLCredentialPattern.ReplaceAll(content, []byte("$1"))
}

func addSanitizedGitConfigToTar(tw *tar.Writer, sourcePath, archivePath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil || info.IsDir() {
		return nil
	}

	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", sourcePath, err)
	}
	sanitized := sanitizeGitConfigCredentials(raw)

	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("build tar header %s: %w", sourcePath, err)
	}
	header.Name = filepath.ToSlash(archivePath)
	header.Size = int64(len(sanitized))

	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write tar header %s: %w", sourcePath, err)
	}
	if _, err := tw.Write(sanitized); err != nil {
		return fmt.Errorf("write tar body %s: %w", sourcePath, err)
	}
	return nil
}

func restoreBackupFile(filename, password string) (err error) {
	if err := BeginRestoreProgress(filename); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			FailRestoreProgress(err)
		}
	}()

	backupDir := filepath.Join(config.C.Data.Dir, "backups")
	filePath := filepath.Join(backupDir, filepath.Base(filename))

	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read backup: %w", err)
	}
	UpdateRestoreProgress("reading", "正在读取备份文件...", 10)

	rawData := fileData
	if strings.HasSuffix(strings.ToLower(filename), ".enc") {
		if strings.TrimSpace(password) == "" {
			return fmt.Errorf("加密备份需要密码")
		}
		UpdateRestoreProgress("decrypting", "正在解密加密备份...", 18)
		rawData, err = decryptData(fileData, password)
		if err != nil {
			return fmt.Errorf("failed to decrypt backup: %w", err)
		}
	}

	if looksLikeGzip(rawData) {
		if err := restoreArchiveBytes(rawData); err != nil {
			return err
		}
		CompleteRestoreProgress("数据已恢复完成，正在准备重启面板...")
		return nil
	}
	if looksLikeJSON(rawData) {
		if err := restoreLegacyJSONBytes(rawData); err != nil {
			return err
		}
		CompleteRestoreProgress("数据已恢复完成，正在准备重启面板...")
		return nil
	}

	return fmt.Errorf("无法识别的备份格式")
}

func looksLikeGzip(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

func looksLikeJSON(data []byte) bool {
	trimmed := strings.TrimSpace(string(data))
	return strings.HasPrefix(trimmed, "{")
}

func restoreArchiveBytes(data []byte) error {
	UpdateRestoreProgress("extracting", "正在解包并校验备份内容...", 28)

	tempDir, err := os.MkdirTemp("", "daidai-restore-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tempDir)

	if err := extractTarGzBytes(data, tempDir); err != nil {
		return err
	}

	manifestPath := filepath.Join(tempDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err == nil {
		UpdateRestoreProgress("analyzing", "正在读取备份清单并分析恢复内容...", 40)
		var manifest BackupManifest
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			return fmt.Errorf("读取备份清单失败: %w", err)
		}
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			return fmt.Errorf("解析备份清单失败: %w", err)
		}
		BindRestoreProgressPlan(manifest.Source, manifest.Selection)
		return restoreBackupManifest(manifest, tempDir)
	}

	manifest, err := buildQingLongManifest(tempDir)
	if err != nil {
		return err
	}
	UpdateRestoreProgress("analyzing", "已识别青龙备份，正在转换为面板数据...", 40)
	BindRestoreProgressPlan(manifest.Source, manifest.Selection)
	return restoreBackupManifest(manifest, tempDir)
}

func restoreLegacyJSONBytes(data []byte) error {
	UpdateRestoreProgress("analyzing", "正在解析旧版备份并转换结构...", 34)

	var legacy LegacyBackupData
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("failed to parse backup: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "daidai-legacy-restore-*")
	if err != nil {
		return fmt.Errorf("创建旧备份临时目录失败: %w", err)
	}
	defer os.RemoveAll(tempDir)

	scriptsDir := filepath.Join(tempDir, "files", "scripts")
	for _, scriptFile := range legacy.Scripts {
		if strings.Contains(scriptFile.Path, "..") {
			continue
		}
		content, err := base64.StdEncoding.DecodeString(scriptFile.Content)
		if err != nil {
			continue
		}
		targetPath := filepath.Join(scriptsDir, filepath.FromSlash(scriptFile.Path))
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("创建旧脚本目录失败: %w", err)
		}
		if err := os.WriteFile(targetPath, content, 0o755); err != nil {
			return fmt.Errorf("写入旧脚本失败: %w", err)
		}
	}

	// 老 JSON 备份里 tasks / subscriptions 存的是 model.X 原样序列化的结果，
	// 本来就没有 labels / auth_token 这两个键（它们在 model 上是 json:"-"）。
	// 这里只是把类型对齐到新的 BackupTask / BackupSubscription，标签与令牌仍为空 ——
	// 老备份的恢复行为逐字节不变。
	legacyTasks := make([]BackupTask, 0, len(legacy.Tasks))
	for _, task := range legacy.Tasks {
		legacyTasks = append(legacyTasks, backupTaskFromModel(task))
	}
	legacySubscriptions := make([]BackupSubscription, 0, len(legacy.Subs))
	for _, subscription := range legacy.Subs {
		legacySubscriptions = append(legacySubscriptions, backupSubscriptionFromModel(subscription))
	}

	manifest := BackupManifest{
		Format:    "daidai-panel-backup",
		Version:   legacy.Version,
		Source:    "daidai-panel",
		CreatedAt: legacy.CreatedAt,
		Selection: BackupSelection{
			Configs:       true,
			Tasks:         true,
			Subscriptions: true,
			EnvVars:       true,
			Logs:          false,
			Scripts:       len(legacy.Scripts) > 0,
			Dependencies:  len(legacy.Deps) > 0,
		},
		Data: BackupPayload{
			Configs: BackupConfigBundle{
				SystemConfigs: legacy.Configs,
			},
			Tasks:         legacyTasks,
			EnvVars:       legacy.EnvVars,
			Subscriptions: legacySubscriptions,
		},
	}

	// legacy.Channels 是 []model.NotifyChannel，老备份里 push_scope 是空串，
	// EffectivePushScope 归一后落成 default —— 老备份本来就没有「绑定推送」这个概念。
	for _, channel := range legacy.Channels {
		manifest.Data.Configs.NotifyChannels = append(manifest.Data.Configs.NotifyChannels, BackupNotifyChannel{
			ID:        channel.ID,
			Name:      channel.Name,
			Type:      channel.Type,
			Config:    channel.Config,
			PushScope: channel.EffectivePushScope(),
			Enabled:   channel.Enabled,
			CreatedAt: channel.CreatedAt,
			UpdatedAt: channel.UpdatedAt,
		})
	}

	for _, key := range legacy.SSHKeys {
		manifest.Data.SSHKeys = append(manifest.Data.SSHKeys, BackupSSHKey{
			ID:         key.ID,
			Name:       key.Name,
			PrivateKey: key.PrivateKey,
			CreatedAt:  key.CreatedAt,
			UpdatedAt:  key.UpdatedAt,
		})
	}

	for _, dep := range legacy.Deps {
		manifest.Data.Dependencies = append(manifest.Data.Dependencies, BackupDependency{
			Type:          dep.Type,
			Name:          dep.Name,
			PythonVersion: dep.PythonVersion,
		})
	}

	BindRestoreProgressPlan(manifest.Source, manifest.Selection)
	return restoreBackupManifest(manifest, tempDir)
}

func extractTarGzBytes(data []byte, targetDir string) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("打开备份压缩包失败: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取备份压缩内容失败: %w", err)
		}

		targetPath := filepath.Join(targetDir, filepath.FromSlash(header.Name))
		cleanTarget := filepath.Clean(targetPath)
		if !strings.HasPrefix(cleanTarget, filepath.Clean(targetDir)+string(os.PathSeparator)) && cleanTarget != filepath.Clean(targetDir) {
			return fmt.Errorf("备份包含非法路径: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(cleanTarget, 0o755); err != nil {
				return fmt.Errorf("创建目录失败: %w", err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o755); err != nil {
				return fmt.Errorf("创建文件目录失败: %w", err)
			}
			file, err := os.OpenFile(cleanTarget, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("创建文件失败: %w", err)
			}
			if _, err := io.Copy(file, tarReader); err != nil {
				file.Close()
				return fmt.Errorf("写入文件失败: %w", err)
			}
			file.Close()
		}
	}

	return nil
}

func restoreBackupManifest(manifest BackupManifest, extractedDir string) error {
	selection := manifest.Selection
	if !selection.Any() {
		return fmt.Errorf("备份内容为空")
	}
	UpdateRestoreProgress("restoring-data", "正在写入数据库与核心配置...", 56)

	runtimeConfigValues := snapshotProtectedRuntimeSystemConfigs()

	tx := database.DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	var createdDependencies []model.Dependency
	taskIDMap := map[uint]uint{}
	sshKeyIDMap := map[uint]uint{}
	// 订阅恢复会重新分配主键，任务上的 subscription:<旧ID> 标签要靠这份映射回头改写，见下面的后置 pass。
	subscriptionIDMap := map[uint]uint{}

	rollback := func(err error) error {
		tx.Rollback()
		return err
	}

	if selection.Logs || selection.Tasks {
		if err := deleteAll(tx, "task_logs"); err != nil {
			return rollback(err)
		}
	}
	if selection.Tasks {
		if err := deleteAll(tx, "tasks"); err != nil {
			return rollback(err)
		}
	}
	if selection.Subscriptions {
		for _, table := range []string{"sub_logs", "subscriptions", "ssh_keys"} {
			if err := deleteAll(tx, table); err != nil {
				return rollback(err)
			}
		}
	}
	if selection.EnvVars {
		if err := deleteAll(tx, "env_vars"); err != nil {
			return rollback(err)
		}
	}
	if selection.Configs {
		for _, table := range []string{
			"api_call_logs",
			"open_apps",
			"notify_channels",
			"ip_whitelists",
			"system_configs",
		} {
			if err := deleteAll(tx, table); err != nil {
				return rollback(err)
			}
		}
	}
	if selection.Dependencies {
		if err := deleteAll(tx, "dependencies"); err != nil {
			return rollback(err)
		}
	}
	if selection.TaskViews {
		if err := deleteAll(tx, "task_views"); err != nil {
			return rollback(err)
		}
	}

	notifyChannelIDMap := map[uint]uint{}

	if selection.Configs {
		if err := restoreSystemConfigs(tx, manifest.Data.Configs.SystemConfigs); err != nil {
			return rollback(err)
		}
		var err error
		notifyChannelIDMap, err = restoreNotifyChannels(tx, manifest.Data.Configs.NotifyChannels)
		if err != nil {
			return rollback(err)
		}
		if err := restoreOpenApps(tx, manifest.Data.Configs.OpenApps); err != nil {
			return rollback(err)
		}
		if err := restoreIPWhitelists(tx, manifest.Data.Configs.IPWhitelists); err != nil {
			return rollback(err)
		}
	}

	if selection.EnvVars {
		if err := restoreEnvVars(tx, manifest.Data.EnvVars); err != nil {
			return rollback(err)
		}
	}

	if selection.Tasks {
		var err error
		taskIDMap, err = restoreTasks(tx, manifest.Data.Tasks, notifyChannelIDMap)
		if err != nil {
			return rollback(err)
		}
	}

	if selection.Subscriptions {
		var err error
		sshKeyIDMap, err = restoreSSHKeys(tx, manifest.Data.SSHKeys)
		if err != nil {
			return rollback(err)
		}
		subscriptionIDMap, err = restoreSubscriptions(tx, manifest.Data.Subscriptions, sshKeyIDMap)
		if err != nil {
			return rollback(err)
		}
	}

	// 后置 pass：恢复顺序是先任务后订阅，等订阅拿到新主键之后再回头改任务标签，
	// 照上面 restoreTasks 里 pendingDepends 那套写法，不动既有恢复顺序。
	// 只勾选任务不勾选订阅时 subscriptionIDMap 为空 → 所有 subscription: 标签被丢弃，
	// 等价于今天（标签整列丢失）的行为。
	if selection.Tasks {
		if err := remapSubscriptionLabels(tx, subscriptionIDMap); err != nil {
			return rollback(err)
		}
	}

	if selection.Logs {
		if err := restoreTaskLogs(tx, manifest.Data.TaskLogs, taskIDMap); err != nil {
			return rollback(err)
		}
	}

	if selection.Dependencies {
		var err error
		createdDependencies, err = restoreDependencies(tx, manifest.Data.Dependencies)
		if err != nil {
			return rollback(err)
		}
	}

	if selection.TaskViews {
		// task_views.name 从 v3.1.0 起是唯一索引，归档里的重名要先改名再落库（理由见 restoreNotifyChannels）。
		// 表在上面已经 deleteAll 清空，所以只需要处理归档内部的重名。
		usedViewNames := make(map[string]bool, len(manifest.Data.TaskViews))
		for _, view := range manifest.Data.TaskViews {
			newView := model.TaskView{
				Name:      resolveRestoredUniqueName(usedViewNames, view.Name, "任务视图"),
				Filters:   view.Filters,
				SortRules: view.SortRules,
				Hidden:    view.Hidden,
				SortOrder: view.SortOrder,
			}
			if err := tx.Create(&newView).Error; err != nil {
				return rollback(fmt.Errorf("restore task view %q: %w", view.Name, err))
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		return err
	}

	restoreProtectedRuntimeSystemConfigs(runtimeConfigValues)

	if selection.Scripts {
		UpdateRestoreProgress("restoring-files", "正在恢复脚本文件与资源...", 72)
		if err := restoreScriptFiles(extractedDir, manifest.Source); err != nil {
			return err
		}
	}
	if selection.Logs {
		UpdateRestoreProgress("restoring-files", "正在恢复日志文件与面板日志...", 78)
		if err := restoreLogFiles(extractedDir, manifest.Source); err != nil {
			return err
		}
	}
	if selection.Configs && manifest.Data.Configs.DependencyMirrors != nil {
		UpdateRestoreProgress("restoring-mirrors", "正在恢复依赖镜像与运行时配置...", 84)
		if err := ApplyDependencyMirrorSettings(*manifest.Data.Configs.DependencyMirrors); err != nil {
			return err
		}
	}

	UpdateRestoreProgress("finalizing", "正在刷新任务调度与恢复后状态...", 92)
	model.InitDefaultConfigs()
	_ = middleware.ConfigureTrustedProxyCIDRs(model.GetRegisteredConfig("trusted_proxy_cidrs"))

	if scheduler := GetSchedulerV2(); scheduler != nil {
		scheduler.ReloadAllJobs()
	}
	if subScheduler := GetSubscriptionScheduler(); subScheduler != nil {
		subScheduler.ReloadAllJobs()
	}
	if backupScheduler := GetBackupScheduler(); backupScheduler != nil {
		backupScheduler.Reload()
	}
	if selection.Dependencies {
		UpdateRestoreProgress("finalizing", "正在提交依赖重装任务...", 96)
		dependencyReinstallBatchFunc(createdDependencies)
	}

	return nil
}

func deleteAll(tx *gorm.DB, table string) error {
	return tx.Exec("DELETE FROM " + table).Error
}

func restoreSystemConfigs(tx *gorm.DB, configs []model.SystemConfig) error {
	for _, item := range configs {
		if shouldSkipRestoredSystemConfigKey(item.Key) {
			continue
		}
		item.ID = 0
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
	}
	return nil
}

func shouldSkipRestoredSystemConfigKey(key string) bool {
	switch strings.TrimSpace(key) {
	case
		"auto_update_last_checked_at",
		"auto_update_pending_version",
		"auto_update_pending_started_at":
		return true
	default:
		return false
	}
}

func protectedRuntimeSystemConfigKeys() []string {
	return []string{
		"auto_update_last_checked_at",
		"auto_update_pending_version",
		"auto_update_pending_started_at",
	}
}

func snapshotProtectedRuntimeSystemConfigs() map[string]string {
	result := make(map[string]string, len(protectedRuntimeSystemConfigKeys()))
	for _, key := range protectedRuntimeSystemConfigKeys() {
		result[key] = model.GetConfig(key, "")
	}
	return result
}

func restoreProtectedRuntimeSystemConfigs(values map[string]string) {
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		_ = model.SetConfig(key, value)
	}
}

func restoreUsers(tx *gorm.DB, users []BackupUser) (map[uint]uint, error) {
	idMap := make(map[uint]uint, len(users))
	for _, item := range users {
		user := model.User{
			Username:    item.Username,
			Password:    item.PasswordHash,
			Role:        item.Role,
			Enabled:     item.Enabled,
			LastLoginAt: item.LastLoginAt,
			CreatedAt:   item.CreatedAt,
			UpdatedAt:   item.UpdatedAt,
		}
		if err := tx.Create(&user).Error; err != nil {
			return nil, err
		}
		idMap[item.ID] = user.ID
	}
	return idMap, nil
}

// resolveRestoredUniqueName 给恢复中的一条记录挑一个「本次归档里还没被用掉」的名字，
// 并把改名这件事写进面板日志 —— 用户恢复完发现「我的渠道名怎么多了个 (2)」，得能在日志里找到答案。
//
// used 会被就地更新（把最终采用的名字标记为已占用），调用方每张表各传一个 map 即可。
// label 只参与日志文案。
func resolveRestoredUniqueName(used map[string]bool, original, label string) string {
	name := database.NextAvailableName(used, original)
	if name == "" {
		// 极端脏数据（同一个名字在归档里重复上万次）时退回原名：
		// 宁可让 DB 唯一约束把这次恢复挡下来并报错，也不能拿一个空名字盖掉用户数据。
		name = original
	}
	if name != original {
		log.Printf("恢复备份：归档里存在重名的%s「%s」，已改名为「%s」", label, original, name)
	}
	used[name] = true
	return name
}

func restoreNotifyChannels(tx *gorm.DB, channels []BackupNotifyChannel) (map[uint]uint, error) {
	idMap := make(map[uint]uint, len(channels))
	// notify_channels.name 从 v3.1.0 起是唯一索引，但**归档里的数据永远不会随升级被清洗**：
	// 用户在 v3.0.10 连点创建出两条都叫「推送」的渠道（当时完全合法）并导出过备份，
	// 升级后启动迁移只改了活库，那份备份里仍然是两条「推送」。
	// 不在这里改名的话，第二条 Create 会返回 UNIQUE constraint failed → rollback → 整次恢复全白做，
	// 用户拿着一份永远恢复不了的备份，且没有任何自救入口。
	//
	// 这张表在上面已经被 deleteAll 清空，所以只需要处理**归档内部**的重名。
	// 改名规则复用 database.NextAvailableName，与启动迁移保持同一套口径。
	usedChannelNames := make(map[string]bool, len(channels))
	for _, item := range channels {
		// 老备份里可能带着被客户端写坏的 config（例如 smtp_ssl 是 JSON 布尔），
		// 那种渠道恢复回来会直接发不出任何通知。这里顺手归一一次，让它恢复即可用。
		//
		// 归一失败（值是嵌套对象/数组这类修不了的）时保留原文继续恢复：
		// 恢复流程的首要职责是把数据完整搬回来，不能因为一个渠道配置有问题就整批失败。
		// 用户后续在通知页编辑保存时会拿到明确的中文报错。
		config := item.Config
		if normalized, err := model.NormalizeNotifyChannelConfig(config); err == nil {
			config = normalized
		}

		// 老备份没有 push_scope 键，反序列化后是空串；非法值同样按 default 落库。
		// 归一后一定是 default / bound 之一，不会把「绑定推送」在恢复时悄悄翻成参与广播 ——
		// 那正是同一行 Enabled 踩过的坑（GORM 省略零值 false，DB 默认 true 反而生效）。
		pushScope, ok := model.NormalizeNotifyPushScope(item.PushScope)
		if !ok {
			pushScope = model.NotifyPushScopeDefault
		}

		name := resolveRestoredUniqueName(usedChannelNames, item.Name, "通知渠道")

		channel := model.NotifyChannel{
			Name:      name,
			Type:      item.Type,
			Config:    config,
			PushScope: pushScope,
			Enabled:   item.Enabled,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		}
		if err := tx.Create(&channel).Error; err != nil {
			return nil, err
		}
		idMap[item.ID] = channel.ID
	}
	return idMap, nil
}

func restoreOpenApps(tx *gorm.DB, apps []BackupOpenApp) error {
	// open_apps.name 从 v3.1.0 起是唯一索引，这里同样要处理归档内部的重名（理由见 restoreNotifyChannels）。
	// 这条路径比通知渠道更容易踩到：青龙导入会把青龙 app 表原样搬进 Configs.OpenApps，
	// 而青龙的 app.name 没有任何唯一约束 —— 青龙用户有两个同名应用时，整个导入会直接失败。
	usedAppNames := make(map[string]bool, len(apps))
	for _, item := range apps {
		app := model.OpenApp{
			Name:      resolveRestoredUniqueName(usedAppNames, item.Name, "OpenAPI 应用"),
			AppKey:    item.AppKey,
			AppSecret: item.AppSecret,
			Scopes:    item.Scopes,
			Enabled:   item.Enabled,
			RateLimit: item.RateLimit,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		}
		if err := tx.Create(&app).Error; err != nil {
			return err
		}
	}
	return nil
}

func restoreIPWhitelists(tx *gorm.DB, items []model.IPWhitelist) error {
	for _, item := range items {
		item.ID = 0
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
	}
	return nil
}

func restoreTwoFactorAuths(tx *gorm.DB, items []BackupTwoFactorAuth, userIDMap map[uint]uint) error {
	for _, item := range items {
		userID := userIDMap[item.UserID]
		if userID == 0 {
			continue
		}
		record := model.TwoFactorAuth{
			UserID:    userID,
			Secret:    item.Secret,
			Enabled:   item.Enabled,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
	}
	return nil
}

// restoreUserPreferences 与上面的 restoreTwoFactorAuths 逐条同构：
// 用户主键在恢复时会重新分配，只有能从 userIDMap 映射到新 ID 的记录才恢复，
// 映射不到（归档里的那个用户没被一起恢复）就整条跳过，写失败则把错误抛给外层事务回滚。
//
// ⚠️ 现状：restoreUsers / restoreTwoFactorAuths 目前都**没有调用点** —— 用户、2FA
// 只导出不恢复（恢复用户会连带换掉当前管理员的凭据，风险太大），
// 所以这个函数同样先与它们成对放着，等哪天把用户恢复整条链路接上时一起接。
// 不要以为「编辑器偏好已经能跟着备份恢复了」：现在只做到了随备份导出。
func restoreUserPreferences(tx *gorm.DB, items []BackupUserPreference, userIDMap map[uint]uint) error {
	for _, item := range items {
		userID := userIDMap[item.UserID]
		if userID == 0 {
			continue
		}
		record := model.UserPreference{
			UserID:    userID,
			Editor:    item.Editor,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
	}
	return nil
}

func backupEnvVarFromModel(item model.EnvVar) BackupEnvVar {
	enabled := item.Enabled
	return BackupEnvVar{
		ID:        item.ID,
		Name:      item.Name,
		Value:     item.Value,
		Remarks:   item.Remarks,
		Enabled:   &enabled,
		Position:  item.Position,
		SortOrder: item.SortOrder,
		Group:     item.Group,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}

func modelEnvVarFromBackup(item BackupEnvVar) model.EnvVar {
	// 老备份可能没有 enabled 字段；缺字段时沿用历史行为，默认按启用恢复。
	enabled := true
	if item.Enabled != nil {
		enabled = *item.Enabled
	}
	return model.EnvVar{
		Name:      item.Name,
		Value:     item.Value,
		Remarks:   item.Remarks,
		Enabled:   enabled,
		Position:  item.Position,
		SortOrder: item.SortOrder,
		Group:     item.Group,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}

func restoreEnvVars(tx *gorm.DB, envVars []BackupEnvVar) error {
	for _, item := range envVars {
		envVar := modelEnvVarFromBackup(item)
		shouldRestoreDisabled := !envVar.Enabled
		if err := tx.Create(&envVar).Error; err != nil {
			return err
		}
		// Enabled=false 是 Go 零值，EnvVar 又有 default:true；Create 时会被 GORM 交给 SQLite 默认成 true。
		// 所以明确禁用的备份项创建后再兜底写回 false，避免恢复后禁用变量全部变成启用。
		if shouldRestoreDisabled {
			if err := tx.Model(&model.EnvVar{}).Where("id = ?", envVar.ID).Update("enabled", false).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// backupTaskFromModel 把库里的 Task 转成备份形态：标签单独拎成数组，
// 否则 model.Task.Labels 上的 json:"-" 会让整列标签在写 manifest.json 时被丢掉（issue #112）。
func backupTaskFromModel(item model.Task) BackupTask {
	// GetLabels 在空标签时返回 []string{} 而不是 nil，所以清单里恒为 JSON 数组，
	// 与 /api/tasks/export 和 ToDict() 的形态一致。
	return BackupTask{Task: item, Labels: item.GetLabels()}
}

// modelTaskFromBackup 把备份形态转回落库用的 model.Task。
// 外层的 Labels 数组是唯一权威：老备份没有这个键 → nil → 拼出空串，与升级前完全一致。
func modelTaskFromBackup(item BackupTask) model.Task {
	task := item.Task
	task.SetLabelsFromSlice(item.Labels)
	return task
}

// backupSubscriptionFromModel / modelSubscriptionFromBackup 同理，处理 AuthToken（订阅 PAT）。
func backupSubscriptionFromModel(item model.Subscription) BackupSubscription {
	return BackupSubscription{Subscription: item, AuthToken: item.AuthToken}
}

func modelSubscriptionFromBackup(item BackupSubscription) model.Subscription {
	subscription := item.Subscription
	subscription.AuthToken = item.AuthToken
	return subscription
}

func restoreTasks(tx *gorm.DB, tasks []BackupTask, notifyChannelIDMap map[uint]uint) (map[uint]uint, error) {
	idMap := make(map[uint]uint, len(tasks))
	pendingDepends := make(map[uint]uint)

	for _, backupTask := range tasks {
		item := modelTaskFromBackup(backupTask)
		oldID := item.ID
		oldDepends := item.DependsOn
		oldNotificationChannelID := item.NotificationChannelID
		item.ID = 0
		item.PID = nil
		item.TaskType = item.GetTaskType()
		item.Status = normalizeRestoredTaskStatus(item.Status)
		item.DependsOn = nil
		item.NotificationChannelID = nil
		if oldNotificationChannelID != nil {
			if mapped := notifyChannelIDMap[*oldNotificationChannelID]; mapped != 0 {
				item.NotificationChannelID = &mapped
			}
		}

		if err := tx.Select("*").Create(&item).Error; err != nil {
			return nil, err
		}
		idMap[oldID] = item.ID
		if oldDepends != nil {
			pendingDepends[item.ID] = *oldDepends
		}
	}

	for newID, oldDepends := range pendingDepends {
		mapped := idMap[oldDepends]
		if mapped == 0 {
			continue
		}
		if err := tx.Model(&model.Task{}).Where("id = ?", newID).Update("depends_on", mapped).Error; err != nil {
			return nil, err
		}
	}

	return idMap, nil
}

func normalizeRestoredTaskStatus(status float64) float64 {
	if status == model.TaskStatusDisabled {
		return model.TaskStatusDisabled
	}
	return model.TaskStatusEnabled
}

func restoreSSHKeys(tx *gorm.DB, keys []BackupSSHKey) (map[uint]uint, error) {
	idMap := make(map[uint]uint, len(keys))
	// ssh_keys.name 从 v3.1.0 起是唯一索引，同样要处理归档内部的重名（理由见 restoreNotifyChannels）。
	// 这里失败的代价尤其大：SSH 密钥恢复失败会连带订阅一起 rollback，
	// 而私钥往往只有备份里这一份。
	usedKeyNames := make(map[string]bool, len(keys))
	for _, item := range keys {
		key := model.SSHKey{
			Name:       resolveRestoredUniqueName(usedKeyNames, item.Name, "SSH 密钥"),
			PrivateKey: item.PrivateKey,
			CreatedAt:  item.CreatedAt,
			UpdatedAt:  item.UpdatedAt,
		}
		if err := tx.Create(&key).Error; err != nil {
			return nil, err
		}
		idMap[item.ID] = key.ID
	}
	return idMap, nil
}

// restoreSubscriptions 返回「备份里的订阅 ID → 恢复后新 ID」的映射，
// 写法照 restoreSSHKeys / restoreNotifyChannels —— 以前只有它把映射扔了，
// 于是任务上的 subscription:<旧ID> 标签没法跟着改（见 remapSubscriptionLabels）。
func restoreSubscriptions(tx *gorm.DB, subscriptions []BackupSubscription, sshKeyIDMap map[uint]uint) (map[uint]uint, error) {
	idMap := make(map[uint]uint, len(subscriptions))
	for _, backupSubscription := range subscriptions {
		item := modelSubscriptionFromBackup(backupSubscription)
		oldID := item.ID
		item.ID = 0
		item.Status = 0
		// 老备份（v3.2.6 之前）里没有三态列，只有 auto_add_task / auto_del_task=1，
		// 语义是「开就是开、不看全局」。在落库前就地翻译成 enabled 并把源列清成 false，
		// 与 handler.Create / 青龙导入用的是同一个口径：
		//   - 还原完当场就有正确语义，不必等下次重启的启动回填；
		//   - 也不会给回填留下新的 legacy=1 行 —— 否则用户把这条订阅改回「跟随全局设置」，
		//     下次重启又会被提回「强制开启」。
		// ⚠️ Resolve 只在备份没带三态信息时才看旧布尔：新备份里的 mode 是准的，不能被 legacy 顶掉。
		item.AutoAddTaskMode = model.ResolveSubscriptionTaskSyncModeInput(item.AutoAddTaskMode, item.AutoAddTask)
		item.AutoDelTaskMode = model.ResolveSubscriptionTaskSyncModeInput(item.AutoDelTaskMode, item.AutoDelTask)
		item.AutoAddTask = false
		item.AutoDelTask = false
		if item.SSHKeyID != nil {
			mapped := sshKeyIDMap[*item.SSHKeyID]
			if mapped == 0 {
				item.SSHKeyID = nil
			} else {
				item.SSHKeyID = &mapped
			}
		}
		if err := tx.Create(&item).Error; err != nil {
			return nil, err
		}
		// 老备份里可能没有 id（青龙以外的历史格式），0 不是合法订阅 ID，不进映射表，
		// 免得把 subscription:0 这类脏标签误指到某个真订阅上。
		if oldID != 0 {
			idMap[oldID] = item.ID
		}
	}
	return idMap, nil
}

// remapSubscriptionLabels 把恢复后的任务标签里 subscription:<旧ID> 改写成 subscription:<新ID>。
//
// 为什么必须做：恢复流程给订阅重新分配主键（restoreSubscriptions 里 item.ID = 0，
// 而 deleteAll 只 DELETE FROM 不重置 sqlite_sequence，主键是 AUTOINCREMENT、语义上不复用已删除 ROWID），
// 把旧 ID 原样写回可能挂到**另一个不相干的订阅**头上；而订阅同步的 autoDelete 分支会对
// 「认领到但不在候选集里」的任务执行 RemoveJob + 删 task_logs + Delete(&task) —— 是物理删除，
// 比标签丢失严重得多。
//
// 规则：
//  1. 命中 old→new 映射 → 改写成 subscription:<新ID>；
//  2. 没命中（订阅没被一起恢复 / 备份里就没有那个订阅 / ID 解析不出来）→ 丢弃该条 subscription: 标签，
//     绝不保留旧 ID；
//  3. 非 subscription: 前缀的标签（含 分组: 与用户自定义）一律原样保留；
//  4. 绝不复用 handler/task_labels.go 的 sanitizeIncomingLabels —— 那是给用户输入用的，会把内部前缀洗掉。
//
// 只在 selection.Tasks 时调用：那种情况下 tasks 表已被 deleteAll 清空并重建，
// 表里的行恰好就是本次恢复出来的任务，所以这里可以直接按 labels 过滤全表。
func remapSubscriptionLabels(tx *gorm.DB, subscriptionIDMap map[uint]uint) error {
	// 与 subscriptionTaskLabel 生成的形态一一对应。
	const labelPrefix = "subscription:"

	// LIKE 只是个粗筛（"my-subscription:foo" 这类用户自建标签也会被捞出来），
	// 真正的边界判定在下面按标签逐条做前缀匹配。
	var tasks []model.Task
	if err := tx.Where("labels LIKE ?", "%"+labelPrefix+"%").Find(&tasks).Error; err != nil {
		return err
	}

	for _, task := range tasks {
		labels := task.GetLabels()
		remapped := make([]string, 0, len(labels))
		for _, label := range labels {
			// GetLabels 只按逗号切分、不做 trim，历史脏数据里可能是 " subscription:1"，
			// 所以先 TrimSpace 再判前缀。
			trimmed := strings.TrimSpace(label)
			if !strings.HasPrefix(trimmed, labelPrefix) {
				remapped = append(remapped, label)
				continue
			}
			oldID, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(trimmed, labelPrefix)), 10, 64)
			if err != nil {
				continue
			}
			if newID := subscriptionIDMap[uint(oldID)]; newID != 0 {
				remapped = append(remapped, subscriptionTaskLabel(newID))
			}
		}

		newLabels := strings.Join(remapped, ",")
		if newLabels == task.Labels {
			continue
		}
		if err := tx.Model(&model.Task{}).Where("id = ?", task.ID).Update("labels", newLabels).Error; err != nil {
			return err
		}
	}
	return nil
}

func restoreTaskLogs(tx *gorm.DB, logs []BackupTaskLog, taskIDMap map[uint]uint) error {
	for _, item := range logs {
		taskID := taskIDMap[item.TaskID]
		if taskID == 0 && item.TaskName != "" {
			var task model.Task
			if err := tx.Where("name = ?", item.TaskName).First(&task).Error; err == nil {
				taskID = task.ID
			}
		}
		if taskID == 0 {
			continue
		}

		logRecord := model.TaskLog{
			TaskID:    taskID,
			Content:   item.Content,
			Status:    item.Status,
			Duration:  item.Duration,
			LogPath:   item.LogPath,
			StartedAt: item.StartedAt,
			EndedAt:   item.EndedAt,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
		}
		if err := tx.Create(&logRecord).Error; err != nil {
			return err
		}
	}
	return nil
}

func restoreDependencies(tx *gorm.DB, deps []BackupDependency) ([]model.Dependency, error) {
	pending := make([]model.Dependency, 0, len(deps))
	seen := map[string]struct{}{}
	for _, item := range deps {
		depType := strings.TrimSpace(item.Type)
		name := strings.TrimSpace(item.Name)
		pythonVersion := ""
		if depType == "" || name == "" {
			continue
		}
		if depType == model.DepTypePython {
			pythonVersion = NormalizeDependencyPythonVersion(item.PythonVersion)
		}

		key := depType + "::" + pythonVersion + "::" + strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		dep := model.Dependency{
			Type:          depType,
			Name:          name,
			PythonVersion: pythonVersion,
		}
		if DependencyInstalledForPythonVersion(depType, name, pythonVersion) {
			dep.Status = model.DepStatusInstalled
			dep.Log = "[恢复备份] 已检测到依赖已存在，无需重装"
		} else {
			dep.Status = model.DepStatusInstalling
			dep.Log = "[恢复备份] 已提交依赖重装"
		}
		if err := tx.Create(&dep).Error; err != nil {
			return nil, err
		}
		if dep.Status == model.DepStatusInstalling {
			pending = append(pending, dep)
		}
	}
	return pending, nil
}

func restoreScriptFiles(extractedDir, source string) error {
	switch source {
	case "qinglong":
		return restoreQingLongScripts(extractedDir)
	default:
		sourceDir := filepath.Join(extractedDir, "files", "scripts")
		if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
			return nil
		}
		return restoreDirectoryWithStage(config.C.Data.ScriptsDir, func(stageDir string) error {
			return copyDirectoryContentsFunc(sourceDir, stageDir)
		})
	}
}

func restoreLogFiles(extractedDir, source string) error {
	panelLogPath := filepath.Join(config.C.Data.Dir, "panel.log")

	switch source {
	case "qinglong":
		if err := restoreQingLongLogs(extractedDir); err != nil {
			return err
		}
		return removePathIfExists(panelLogPath)
	default:
		logDir := filepath.Join(extractedDir, "files", "logs")
		if err := restoreDirectoryWithStage(config.C.Data.LogDir, func(stageDir string) error {
			return copyDirectoryContentsFunc(logDir, stageDir)
		}); err != nil {
			return err
		}
		panelLogSource := filepath.Join(extractedDir, "files", "panel.log")
		if _, err := os.Stat(panelLogSource); err == nil {
			return restoreFileWithStage(panelLogPath, func(stagePath string) error {
				return copyFileFunc(panelLogSource, stagePath)
			})
		}
		return removePathIfExists(panelLogPath)
	}
}

func clearDirectoryContents(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

var (
	// copyDirectoryContentsFunc / copyFileFunc 保留测试替身入口，
	// 用于验证“恢复 staging 失败时 live 目录仍保持原样”这类高风险回归。
	copyDirectoryContentsFunc = copyDirectoryContents
	copyFileFunc              = copyFile
)

// restoreDirectoryWithStage 先把恢复结果完整写入同目录下的 staging 目录，
// staging 成功后再原子切换 live 目录，避免“先删 live，再拷贝新内容”导致半失败时旧数据丢失。
func restoreDirectoryWithStage(targetDir string, fillStage func(stageDir string) error) error {
	parentDir := filepath.Dir(targetDir)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return err
	}

	stageDir := filepath.Join(parentDir, ".restore-stage-"+filepath.Base(targetDir)+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return err
	}

	cleanupStage := true
	defer func() {
		if cleanupStage {
			_ = os.RemoveAll(stageDir)
		}
	}()

	if err := fillStage(stageDir); err != nil {
		return err
	}

	backupDir := filepath.Join(parentDir, ".restore-backup-"+filepath.Base(targetDir)+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	targetExists := false
	if _, err := os.Stat(targetDir); err == nil {
		targetExists = true
		if err := os.Rename(targetDir, backupDir); err != nil {
			return err
		}
	}

	if err := os.Rename(stageDir, targetDir); err != nil {
		if targetExists {
			_ = os.Rename(backupDir, targetDir)
		}
		return err
	}

	cleanupStage = false
	if targetExists {
		_ = os.RemoveAll(backupDir)
	}
	return nil
}

// restoreFileWithStage 和目录恢复同理，先准备好 staging 文件，再切换 live 文件。
func restoreFileWithStage(targetPath string, fillStage func(stagePath string) error) error {
	parentDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return err
	}

	stagePath := filepath.Join(parentDir, ".restore-stage-"+filepath.Base(targetPath)+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	cleanupStage := true
	defer func() {
		if cleanupStage {
			_ = os.Remove(stagePath)
		}
	}()

	if err := fillStage(stagePath); err != nil {
		return err
	}

	backupPath := filepath.Join(parentDir, ".restore-backup-"+filepath.Base(targetPath)+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	targetExists := false
	if _, err := os.Stat(targetPath); err == nil {
		targetExists = true
		if err := os.Rename(targetPath, backupPath); err != nil {
			return err
		}
	}

	if err := os.Rename(stagePath, targetPath); err != nil {
		if targetExists {
			_ = os.Rename(backupPath, targetPath)
		}
		return err
	}

	cleanupStage = false
	if targetExists {
		_ = os.Remove(backupPath)
	}
	return nil
}

func removePathIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func copyDirectoryContents(sourceDir, targetDir string) error {
	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return nil
	}
	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info == nil {
			return nil
		}
		if info.IsDir() {
			if ShouldIgnoreScriptPath(sourceDir, path) {
				return filepath.SkipDir
			}
		} else if ShouldIgnoreScriptPath(sourceDir, path) {
			return nil
		}
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		targetPath := filepath.Join(targetDir, relPath)
		if info.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		return copyFile(path, targetPath)
	})
}

func copyFile(sourcePath, targetPath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	defer target.Close()

	_, err = io.Copy(target, source)
	return err
}

func reinstallDependenciesAsync(deps []model.Dependency) {
	reinstallDependenciesAsyncWithLogPrefix(deps, "[恢复备份]")
}

func reinstallDependenciesAfterRestartAsync(deps []model.Dependency) {
	reinstallDependenciesAsyncWithLogPrefix(deps, "[启动校验]")
}

var buildLinuxDependencyInstallCommandFunc = buildLinuxDependencyInstallCommand

func reinstallDependenciesAsyncWithLogPrefix(deps []model.Dependency, logPrefix string) {
	go func() {
		for _, dep := range deps {
			reinstallDependency(dep, logPrefix)
		}
	}()
}

func reinstallDependency(dep model.Dependency, logPrefix string) {
	logText := fmt.Sprintf("%s 正在安装 %s 依赖 %s", logPrefix, dep.Type, dep.Name)
	if dep.Type == model.DepTypePython {
		logText = fmt.Sprintf("%s 正在安装 Python %s 依赖 %s", logPrefix, NormalizeDependencyPythonVersion(dep.PythonVersion), dep.Name)
	}
	database.DB.Model(&model.Dependency{}).Where("id = ?", dep.ID).Update("log", logText)

	var cmd *exec.Cmd
	switch dep.Type {
	case model.DepTypeNodeJS:
		unlock := LockNodePackageOperation()
		defer unlock()

		var err error
		cmd, err = NewNpmInstallCommand(dep.Name)
		if err != nil {
			database.DB.Model(&model.Dependency{}).Where("id = ?", dep.ID).Updates(map[string]interface{}{
				"status": model.DepStatusFailed,
				"log":    logPrefix + " " + err.Error(),
			})
			return
		}
	case model.DepTypePython:
		var err error
		cmd, err = NewPipInstallCommandForPythonVersion(dep.PythonVersion, dep.Name)
		if err != nil {
			database.DB.Model(&model.Dependency{}).Where("id = ?", dep.ID).Updates(map[string]interface{}{
				"status": model.DepStatusFailed,
				"log":    logPrefix + " " + err.Error(),
			})
			return
		}
		cmd.Env = append(PipInstallEnv(AppendProxyEnv(os.Environ()), CurrentPipMirror()), "TMPDIR=/tmp")
	case model.DepTypeLinux:
		// 重启后的 Linux 依赖恢复必须复用当前主依赖安装路径的包管理器策略，
		// 避免 Debian 继续走旧的裸 apt-get update/install 分支，导致与
		// 依赖管理页的真实行为分叉，表现为“等待重启安装”后状态长期不收敛。
		var err error
		cmd, err = buildLinuxDependencyInstallCommandFunc(dep.Name)
		if err != nil {
			database.DB.Model(&model.Dependency{}).Where("id = ?", dep.ID).Updates(map[string]interface{}{
				"status": model.DepStatusFailed,
				"log":    logPrefix + " " + err.Error(),
			})
			return
		}
	default:
		database.DB.Model(&model.Dependency{}).Where("id = ?", dep.ID).Updates(map[string]interface{}{
			"status": model.DepStatusFailed,
			"log":    logPrefix + " 不支持的依赖类型",
		})
		return
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		database.DB.Model(&model.Dependency{}).Where("id = ?", dep.ID).Updates(map[string]interface{}{
			"status": model.DepStatusFailed,
			"log":    fmt.Sprintf("%s 安装失败\n%s", logPrefix, strings.TrimSpace(string(output))),
		})
		return
	}

	database.DB.Model(&model.Dependency{}).Where("id = ?", dep.ID).Updates(map[string]interface{}{
		"status": model.DepStatusInstalled,
		"log":    fmt.Sprintf("%s 安装成功\n%s", logPrefix, strings.TrimSpace(string(output))),
	})
}

func buildLinuxDependencyInstallCommand(packageName string) (*exec.Cmd, error) {
	manager, err := DetectLinuxPackageManager()
	if err != nil {
		return nil, err
	}
	return BuildLinuxPackageCommand(manager, "install", packageName, false, DetectLinuxDistribution(), nil)
}
