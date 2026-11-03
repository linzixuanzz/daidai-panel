package appboot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/service"
)

// ResolveConfigPath 查找 config.yaml，覆盖 Docker / 二进制 / Windows 双击 / cwd 漂移等场景。
// 顺序：
//  1. DAIDAI_CONFIG 环境变量
//  2. /app/config.yaml（Docker 镜像固定位置）
//  3. 当前可执行文件同目录（Windows 双击 / 二进制从其他 cwd 启动也能找到）
//  4. cwd 下的 config.yaml（兼容历史行为）
func ResolveConfigPath() string {
	candidates := []string{
		os.Getenv("DAIDAI_CONFIG"),
		"/app/config.yaml",
	}
	if exePath, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exePath), "config.yaml"))
	}
	candidates = append(candidates, "config.yaml")

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return "config.yaml"
}

// TaskLogQueryIndexNames 是 v3.3.4（issue #153）给 task_logs 补的三个查询索引名。
// 启动时只拿它判断「老库还缺不缺这几个索引」，决定要不要打那两行补建日志；索引本身由 AutoMigrate 按 tag 建。
// 必须与 model/task_log.go 的 tag 一致，迁移测试兜底（database/task_log_indexes_migration_test.go）：
// 名字一旦对不上，HasIndex 永远判「缺」，以后每次启动都会误打「正在补建」。导出只是为了让那条测试能核对它。
var TaskLogQueryIndexNames = []string{
	"idx_task_logs_created_at_status",
	"idx_task_logs_started_at_status",
	"idx_task_logs_task_id_started_at",
}

func LoadAndInit(configPath string) (*config.Config, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	if err := InitWithConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func InitWithConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("配置为空")
	}
	config.C = cfg

	database.Init(&cfg.Database)
	// 必须排在 AutoMigrate 之前：AutoMigrate 建唯一索引失败会直接 log.Fatalf，
	// 老库里的同名数据要先改名让路，否则升级后面板起不来。
	database.DeduplicateBeforeUniqueIndex()

	// 老库升级后第一次启动时，AutoMigrate 要给 task_logs 补建三个查询索引（issue #153）。
	// 每建一个约等于把整张执行日志表连同日志正文读一遍，日志多的库要等好几秒甚至更久；
	// 这一步在 HTTP 服务起来之前同步执行，GORM 建索引又全程不打日志，用户只会看到「升级后面板半天打不开」。
	// 所以只在「表已存在、且至少缺一个索引」时前后各打一行。全新库（表还不存在）建表时顺带建索引、很快，不打；
	// 补建过之后的每次启动索引都在，也不打。
	taskLogIndexMissing := false
	if database.DB.Migrator().HasTable(&model.TaskLog{}) {
		for _, name := range TaskLogQueryIndexNames {
			if !database.DB.Migrator().HasIndex(&model.TaskLog{}, name) {
				taskLogIndexMissing = true
				break
			}
		}
	}
	migrateStartedAt := time.Now()
	if taskLogIndexMissing {
		log.Printf("正在为执行日志补建查询索引（v3.3.4，仅升级后首次启动需要，日志越多耗时越长）...")
	}
	database.AutoMigrate(allModels()...)
	if taskLogIndexMissing {
		log.Printf("执行日志查询索引补建完成，耗时 %s", time.Since(migrateStartedAt).Round(time.Millisecond))
	}
	database.EnsureColumns()

	legacyPythonVenvMigration := service.MigrateLegacyManagedPythonVenvInfo()

	model.InitDefaultConfigs()
	if err := service.ApplyRegisteredPanelTimezone(); err != nil {
		return fmt.Errorf("failed to apply panel timezone: %w", err)
	}
	service.NormalizeLegacyPythonVersionColumnsAfterVenvMigration(legacyPythonVenvMigration)
	service.ApplySinglePythonRuntimePolicyOnStartup()
	// 必须排在合并重复依赖之前：迁移会让旧版本的依赖与当前版本的同名依赖撞到一起，交给下一行合并。
	service.ApplyMagiskPythonRuntimeMigrationOnStartup()
	service.MergeDuplicatePythonDependencies()
	if err := middleware.ConfigureTrustedProxyCIDRs(model.GetRegisteredConfig("trusted_proxy_cidrs")); err != nil {
		return fmt.Errorf("failed to configure trusted proxies: %w", err)
	}

	return nil
}

func allModels() []interface{} {
	return []interface{}{
		&model.User{},
		&model.TokenBlocklist{},
		&model.Task{},
		&model.TaskLog{},
		// 执行趋势里已删日志的按天计数（#158，v3.3.6）。新表，存量库 AutoMigrate 时建出来即可，不补列、不回填。
		&model.TaskLogDailyStat{},
		&model.SystemConfig{},
		&model.EnvVar{},
		&model.ScriptVersion{},
		&model.Subscription{},
		&model.SubLog{},
		&model.NotifyChannel{},
		&model.SSHKey{},
		&model.LoginLog{},
		&model.LoginAttempt{},
		&model.UserSession{},
		&model.IPWhitelist{},
		&model.SecurityAudit{},
		&model.TwoFactorAuth{},
		// 通行密钥（issue #175，v3.4.2）。新表，存量库 AutoMigrate 时建出来即可，一行都没有时与升级前一致。
		&model.Passkey{},
		&model.UserPreference{},
		&model.OpenApp{},
		&model.ApiCallLog{},
		&model.Platform{},
		&model.PlatformToken{},
		&model.PlatformTokenLog{},
		&model.Dependency{},
		&model.TaskView{},
		// 企业微信回调接入配置（issue #145，v3.3.2）。新表，存量库 AutoMigrate 时建出来即可，
		// 一行都没有时所有企业微信入口都查不到配置直接 404，行为与升级前一致。
		&model.WecomTrigger{},
	}
}
