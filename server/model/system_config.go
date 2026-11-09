package model

import (
	"errors"
	"strings"
	"time"

	"daidai-panel/database"
	"daidai-panel/pkg/netutil"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type SystemConfig struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	Key         string    `gorm:"size:64;uniqueIndex;not null" json:"key"`
	Value       string    `gorm:"type:text;default:''" json:"value"`
	Description string    `gorm:"size:256;default:''" json:"description"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var deprecatedSystemConfigKeys = map[string]bool{
	"command_timeout": true,
}

func (SystemConfig) TableName() string {
	return "system_configs"
}

func silentDB() *gorm.DB {
	return database.DB.Session(&gorm.Session{Logger: database.DB.Logger.LogMode(logger.Silent)})
}

func GetConfig(key string, defaultValue string) string {
	if defaultValue == "" {
		if def, exists := GetSystemConfigDefinition(key); exists {
			defaultValue = def.DefaultValue
		}
	}

	var cfg SystemConfig
	if err := silentDB().Where("`key` = ?", key).First(&cfg).Error; err != nil {
		return defaultValue
	}
	if cfg.Value == "" {
		return defaultValue
	}
	return cfg.Value
}

func GetConfigInt(key string, defaultValue int) int {
	val := GetConfig(key, "")
	if val == "" {
		return defaultValue
	}
	var result int
	for _, c := range val {
		if c >= '0' && c <= '9' {
			result = result*10 + int(c-'0')
		} else {
			return defaultValue
		}
	}
	return result
}

func GetConfigBool(key string, defaultValue bool) bool {
	val := GetConfig(key, "")
	if val == "" {
		return defaultValue
	}

	parsed, ok := parseBoolString(val)
	if !ok {
		return defaultValue
	}
	return parsed
}

func SetConfig(key, value string) error {
	if deprecatedSystemConfigKeys[key] {
		return removeDeprecatedSystemConfigs(key)
	}

	normalized, err := NormalizeSystemConfigValue(key, value)
	if err != nil {
		return err
	}

	var cfg SystemConfig
	if err := silentDB().Where("`key` = ?", key).First(&cfg).Error; err != nil {
		cfg = SystemConfig{Key: key, Value: normalized}
		if def, exists := GetSystemConfigDefinition(key); exists {
			cfg.Description = def.Description
		}
		return database.DB.Create(&cfg).Error
	}

	updates := map[string]interface{}{"value": normalized}
	if def, exists := GetSystemConfigDefinition(key); exists && cfg.Description != def.Description {
		updates["description"] = def.Description
	}

	return database.DB.Model(&cfg).Updates(updates).Error
}

func InitDefaultConfigs() {
	db := silentDB()
	for _, def := range SystemConfigDefinitions() {
		var existing SystemConfig
		if err := db.Where("`key` = ?", def.Key).First(&existing).Error; err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
			database.DB.Create(&SystemConfig{
				Key:         def.Key,
				Value:       def.DefaultValue,
				Description: def.Description,
			})
			continue
		}

		normalizedValue := existing.Value
		if normalizedValue == "" {
			normalizedValue = def.DefaultValue
		} else if normalized, err := NormalizeSystemConfigValue(def.Key, existing.Value); err == nil {
			normalizedValue = normalized
		} else {
			normalizedValue = def.DefaultValue
		}

		updates := map[string]interface{}{}
		if strings.TrimSpace(existing.Description) != def.Description {
			updates["description"] = def.Description
		}
		if def.Key == "max_log_content_size" && strings.TrimSpace(existing.Value) == "102400" {
			normalizedValue = def.DefaultValue
		}
		// repo_file_extensions 的历史默认值漏了 mjs，导致 .mjs 脚本在订阅同步时连扫描都进不去
		// （表现为「仓库拉取成功但 mjs 不建任务」）。库里存的还正好是那份旧默认，说明用户从没动过
		// 这项配置，直接抬到新默认；用户自己改过的值一律不动，免得覆盖掉「故意不要某后缀」的意图。
		if def.Key == "repo_file_extensions" && strings.TrimSpace(existing.Value) == LegacyRepoFileExtensions {
			normalizedValue = def.DefaultValue
		}
		// log_background_color 在 v1.8.0 ~ v2.2.3 的默认值是固定深色 #0f172a，v2.2.4 才改成
		// 「留空跟随明暗主题」。老实例库里那行一直是旧默认，升级不会自愈，表现为浅色面板下
		// 日志区仍然是黑底。库里存的正好等于旧默认 ⇒ 用户从没动过这项，抬到新默认（空串）；
		// 用户自己选过的颜色（哪怕也是深色）一律不动。
		// 写回空串后下次启动 existing.Value 已经是空串、不再命中这条分支，因此是幂等的。
		if def.Key == "log_background_color" && strings.TrimSpace(existing.Value) == LegacyLogBackgroundColor {
			normalizedValue = def.DefaultValue
		}
		// trusted_proxy_cidrs 的旧默认迁移不在这里做，见循环后面的 migrateLegacyTrustedProxyCIDRsOnce。
		if normalizedValue != existing.Value {
			updates["value"] = normalizedValue
		}
		if len(updates) > 0 {
			database.DB.Model(&existing).Updates(updates)
		}
	}

	// 放在上面的循环之后：此时 trusted_proxy_cidrs 这一行一定存在且已规范化。
	migrateLegacyTrustedProxyCIDRsOnce()

	removeDeprecatedSystemConfigs("command_timeout")
}

// TrustedProxyCIDRsMigratedConfigKey 标记「trusted_proxy_cidrs 的旧默认迁移已经做过」，值固定写 "1"。
//
// 和 WecomTriggerLinkGenerationConfigKey 一样刻意不进注册表：这是面板自己写的状态，不是给人填的配置项。
// 它会随备份导出、随恢复写回（见 service/backup_runtime.go 的 restoreBackupManifest）。
const TrustedProxyCIDRsMigratedConfigKey = "trusted_proxy_cidrs_migrated"

// migrateLegacyTrustedProxyCIDRsOnce 把 v3.4.3 及之前的旧默认抬到新默认，一辈子只做一次。
//
// trusted_proxy_cidrs 在 v3.4.3 及之前默认信任全部私网段（审计 H-2）：经 Docker 网桥、内网反代
// 进来的请求可以随手伪造 X-Forwarded-For，绕过 IP 白名单和登录限流。v3.4.4 起默认只信任本机。
// 老实例库里那行是建表时写入的旧默认，升级不会自愈，所以库里存的（按集合）正好等于旧默认时要改写。
//
// 为什么要标记行，而不像 log_background_color 那样「等于旧默认就改」每次启动都判断：
// 旧默认那几段私网是一个有意义的安全选择 —— 反代确实在内网另一台机器上的管理员，会在设置页里
// 主动把这几段加回来。没有标记的话，每次重启、每次恢复备份都会把它悄悄改回本机，管理员的选择被吞掉。
// 有了标记：只在第一次（升级到 v3.4.4 后首次启动，或恢复了不带标记的老备份）判断一次，之后永不再碰。
func migrateLegacyTrustedProxyCIDRsOnce() {
	var marker SystemConfig
	if err := silentDB().Where("`key` = ?", TrustedProxyCIDRsMigratedConfigKey).First(&marker).Error; err == nil {
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		// 查库本身出错：这次先不动，下次启动再判断。不能当成「没迁移过」，否则可能误改用户的设置。
		return
	}

	if netutil.IsLegacyDefaultTrustedProxyCIDRs(GetConfig("trusted_proxy_cidrs", "")) {
		if err := SetConfig("trusted_proxy_cidrs", strings.Join(netutil.DefaultTrustedProxyCIDRs(), "\n")); err != nil {
			// 改写失败就不写标记，下次启动还会再试
			return
		}
	}
	// 新装实例、用户改过的实例也写标记：它们同样「已经过了迁移这一关」，以后无论值变成什么都不再碰
	_ = SetConfig(TrustedProxyCIDRsMigratedConfigKey, "1")
}

func removeDeprecatedSystemConfigs(keys ...string) error {
	for _, key := range keys {
		if err := database.DB.Where("`key` = ?", key).Delete(&SystemConfig{}).Error; err != nil {
			return err
		}
	}
	return nil
}
