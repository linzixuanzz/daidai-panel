package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"daidai-panel/config"
	"daidai-panel/database"
	"daidai-panel/middleware"
	"daidai-panel/model"
	"daidai-panel/pkg/crypto"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func closeExistingDB() {
	if database.DB == nil {
		return
	}

	sqlDB, err := database.DB.DB()
	if err == nil {
		_ = sqlDB.Close()
	}
}

func SetupTestEnv(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")

	gin.SetMode(gin.TestMode)
	closeExistingDB()

	config.C = &config.Config{
		Server: config.ServerConfig{
			Port: 5701,
			Mode: "test",
		},
		Database: config.DatabaseConfig{
			Path: filepath.Join(root, "test.db"),
		},
		JWT: config.JWTConfig{
			Secret:             "test-secret",
			AccessTokenExpire:  time.Hour,
			RefreshTokenExpire: 2 * time.Hour,
		},
		Data: config.DataConfig{
			Dir:        dataDir,
			ScriptsDir: filepath.Join(dataDir, "scripts"),
			LogDir:     filepath.Join(dataDir, "logs"),
		},
		CORS: config.CORSConfig{
			Origins: []string{"https://allowed.example.com"},
		},
	}

	// 大多数后端测试会直接往 scripts/logs 目录写文件。
	// 测试环境初始化时先建好目录，避免每个用例重复 mkdir，也避免 Windows 下直接写文件时报路径不存在。
	if err := os.MkdirAll(config.C.Data.ScriptsDir, 0o755); err != nil {
		t.Fatalf("create test scripts dir: %v", err)
	}
	if err := os.MkdirAll(config.C.Data.LogDir, 0o755); err != nil {
		t.Fatalf("create test log dir: %v", err)
	}

	database.Init(&config.C.Database)
	database.AutoMigrate(
		&model.User{},
		&model.TokenBlocklist{},
		&model.Task{},
		&model.TaskLog{},
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
		&model.Passkey{},
		&model.UserPreference{},
		&model.OpenApp{},
		&model.ApiCallLog{},
		&model.Platform{},
		&model.PlatformToken{},
		&model.PlatformTokenLog{},
		&model.Dependency{},
		&model.TaskView{},
		&model.WecomTrigger{},
	)
	// #158 测试期护栏：删 task_logs 必须经 service.DeleteTaskLogs（它先把要删的行并入执行趋势计数，
	// 并给自己那条 Delete 打上 model.TaskLogDeleteGuardKey 标记）。漏走它的 GORM 删除——Where(...).Delete(&model.TaskLog{})、
	// Delete(&logs)、Table("task_logs")…Delete 等写法——在这里直接报错、一行不删，用例会红。
	// 每次 SetupTestEnv 都是新建的 database.DB，回调挂在它自己身上，不会重复注册。
	// 原生 Exec("DELETE …") 不走 Delete 回调链，由 service 包的语法扫描用例兜。
	if err := database.DB.Callback().Delete().Before("gorm:delete").Register("testutil:task_logs_delete_guard", func(db *gorm.DB) {
		if db.Statement.Table != "task_logs" {
			return
		}
		if _, ok := db.Get(model.TaskLogDeleteGuardKey); !ok {
			_ = db.AddError(errors.New("task_logs 只能经 service.DeleteTaskLogs 删除（先并入执行趋势计数，#158）"))
		}
	}); err != nil {
		t.Fatalf("register task_logs delete guard: %v", err)
	}
	model.InitDefaultConfigs()

	// middleware 的可信代理列表是包级全局，只在 middleware 包 init() 时装一次默认值（v3.4.4 起只有本机地址），
	// 而任何经 ConfigHandler 写 trusted_proxy_cidrs 的路径（Set 与 BatchSet 都会走
	// reloadRuntimeConfigKeys）都是整体「替换」而非追加，用例跑完也不会自己恢复。
	// 上一个用例改过的列表一旦残留，后续用例对 X-Forwarded-For 的解析就会和默认口径不一致。
	// 进入侧重置：保证当前用例不继承上一个用例留下的脏状态。
	// 这里必须传空串（等价于注册表默认值），不要改成 model.GetRegisteredConfig 读 DB，
	// 否则会给这行加上「必须排在 database.Init 之后」的隐式顺序依赖。
	_ = middleware.ConfigureTrustedProxyCIDRs("")

	t.Cleanup(func() {
		closeExistingDB()
		config.C = nil
		// 退出侧重置：保证当前用例不污染下一个用例，顺带覆盖那些不调 SetupTestEnv 的用例，
		// 以及 service/backup_runtime.go 恢复备份后同源的跨包写。
		_ = middleware.ConfigureTrustedProxyCIDRs("")
	})

	return root
}

func MustCreateUser(t *testing.T, username, role string) *model.User {
	t.Helper()

	user := &model.User{
		Username: username,
		Password: "test-password-hash",
		Role:     role,
		Enabled:  true,
	}

	if err := database.DB.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	return user
}

func MustCreateLoginUser(t *testing.T, username, role, password string) *model.User {
	t.Helper()

	hash, err := crypto.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}

	user := &model.User{
		Username: username,
		Password: hash,
		Role:     role,
		Enabled:  true,
	}

	if err := database.DB.Create(user).Error; err != nil {
		t.Fatalf("create login user: %v", err)
	}

	return user
}

func MustCreateAccessToken(t *testing.T, username, role string) string {
	t.Helper()

	token, err := middleware.GenerateAccessToken(username, role)
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	return token
}

func MustCreateRefreshToken(t *testing.T, username, role string) string {
	t.Helper()

	refreshInfo, err := middleware.GenerateRefreshTokenInfo(username, role)
	if err != nil {
		t.Fatalf("generate refresh token: %v", err)
	}

	// 续期要求 refresh 能按 refresh_jti 找到所属的登录会话（撤销会话就是删这一行），
	// 所以这里与真实登录一样顺手落一行会话：会话号取一枚新 access 的 jti，与登录时同形。
	// 用户不存在时 user_id 记 0，续期照样会因为「用户不存在」被拒，不改变这类用例原来的断言。
	// 这里不能调 service.CreateSessionWithRefresh：service 包自己的测试也 import testutil，会成环。
	var user model.User
	database.DB.Where("username = ?", username).Limit(1).Find(&user)
	accessInfo, err := middleware.GenerateAccessTokenInfo(username, role)
	if err != nil {
		t.Fatalf("generate session access token: %v", err)
	}
	refreshExpiresAt := refreshInfo.ExpiresAt
	session := model.UserSession{
		UserID:           user.ID,
		Username:         username,
		JTI:              accessInfo.JTI,
		RefreshJTI:       refreshInfo.JTI,
		ClientType:       "web",
		ExpiresAt:        accessInfo.ExpiresAt,
		RefreshExpiresAt: &refreshExpiresAt,
	}
	if err := database.DB.Create(&session).Error; err != nil {
		t.Fatalf("create session for refresh token: %v", err)
	}

	return refreshInfo.Token
}

func MustCreateOpenApp(t *testing.T, appKey, scopes string) *model.OpenApp {
	t.Helper()

	app := &model.OpenApp{
		Name:      appKey,
		AppKey:    appKey,
		AppSecret: "secret-" + appKey,
		Scopes:    scopes,
		Enabled:   true,
		RateLimit: 1000,
	}

	if err := database.DB.Create(app).Error; err != nil {
		t.Fatalf("create open app: %v", err)
	}

	return app
}

func MustCreateAppToken(t *testing.T, appKey, scopes string) string {
	t.Helper()

	MustCreateOpenApp(t, appKey, scopes)
	return MustCreateAccessToken(t, "app:"+appKey, "app:"+scopes)
}
