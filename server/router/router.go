package router

import (
	"daidai-panel/handler"
	"daidai-panel/middleware"

	"github.com/gin-gonic/gin"
)

func Setup(engine *gin.Engine) {
	engine.Use(middleware.CORS())
	engine.Use(middleware.SecurityHeaders())

	v1 := engine.Group("/api/v1")
	legacy := engine.Group("/api")

	authHandler := handler.NewAuthHandler()
	taskHandler := handler.NewTaskHandler()
	logHandler := handler.NewLogHandler()
	scriptHandler := handler.NewScriptHandler()
	envHandler := handler.NewEnvHandler()
	subHandler := handler.NewSubscriptionHandler()
	notifyHandler := handler.NewNotificationHandler()
	sshKeyHandler := handler.NewSSHKeyHandler()
	userHandler := handler.NewUserHandler()
	securityHandler := handler.NewSecurityHandler()
	systemHandler := handler.NewSystemHandler()
	consoleHandler := handler.NewConsoleHandler()
	openAPIHandler := handler.NewOpenAPIHandler()
	depsHandler := handler.NewDepsHandler()
	configHandler := handler.NewConfigHandler()
	platformTokenHandler := handler.NewPlatformTokenHandler()
	sponsorHandler := handler.NewSponsorHandler()
	androidRuntimeHandler := handler.NewAndroidRuntimeHandler()
	processHandler := handler.NewProcessHandler()

	authHandler.RegisterRoutes(v1)
	authHandler.RegisterRoutes(legacy)

	taskHandler.RegisterRoutes(v1)
	taskHandler.RegisterRoutes(legacy)

	logHandler.RegisterRoutes(v1)
	logHandler.RegisterRoutes(legacy)

	scriptHandler.RegisterRoutes(v1)
	scriptHandler.RegisterRoutes(legacy)

	envHandler.RegisterRoutes(v1)
	envHandler.RegisterRoutes(legacy)

	subHandler.RegisterRoutes(v1)
	subHandler.RegisterRoutes(legacy)

	notifyHandler.RegisterRoutes(v1)
	notifyHandler.RegisterRoutes(legacy)

	sshKeyHandler.RegisterRoutes(v1)
	sshKeyHandler.RegisterRoutes(legacy)

	userHandler.RegisterRoutes(v1)
	userHandler.RegisterRoutes(legacy)

	securityHandler.RegisterRoutes(v1)
	securityHandler.RegisterRoutes(legacy)

	systemHandler.RegisterRoutes(v1)
	systemHandler.RegisterRoutes(legacy)

	consoleHandler.RegisterRoutes(v1)
	consoleHandler.RegisterRoutes(legacy)

	openAPIHandler.RegisterRoutes(v1)
	openAPIHandler.RegisterRoutes(legacy)

	depsHandler.RegisterRoutes(v1)
	depsHandler.RegisterRoutes(legacy)

	configHandler.RegisterRoutes(v1)
	configHandler.RegisterRoutes(legacy)

	platformTokenHandler.RegisterRoutes(v1)
	platformTokenHandler.RegisterRoutes(legacy)

	sponsorHandler.RegisterRoutes(v1)
	sponsorHandler.RegisterRoutes(legacy)

	androidRuntimeHandler.RegisterRoutes(v1)
	androidRuntimeHandler.RegisterRoutes(legacy)

	// 「后台进程」页（#176）：/system/processes 单独一个 handler，不往 system.go 里塞
	processHandler.RegisterRoutes(v1)
	processHandler.RegisterRoutes(legacy)

	// 内置 MCP 服务（issue #128）。它的工具调用会在进程内回放到上面这些 /api/v1 接口，
	// 所以要把 engine 本身交给它；回放发生在请求期，届时所有路由（含静态前端）都已注册完。
	mcpHandler := handler.NewMCPHandler(engine)
	mcpHandler.RegisterRoutes(v1)
	mcpHandler.RegisterRoutes(legacy)

	// 企业微信拉起任务的回调（issue #145）。和 MCP 是同一个形态：路由公开、鉴权在 handler 内，
	// 触发执行时在进程内回放到上面那些 /api/v1 接口，所以同样要把 engine 本身交给它。
	wecomCallbackHandler := handler.NewWecomCallbackHandler(engine)
	wecomCallbackHandler.RegisterRoutes(v1)
	wecomCallbackHandler.RegisterRoutes(legacy)

	engine.GET("/robots.txt", func(c *gin.Context) {
		c.Data(200, "text/plain; charset=utf-8", []byte("User-agent: *\nDisallow: /\n"))
	})

	engine.GET("/api/v1/version", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"version":     handler.Version,
			"api_version": "v1",
			"framework":   "gin",
		})
	})
	engine.GET("/api/v1/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	engine.GET("/api/version", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"version":     handler.Version,
			"api_version": "v1",
			"framework":   "gin",
		})
	})
	engine.GET("/api/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
}
