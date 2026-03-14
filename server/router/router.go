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
	openAPIHandler := handler.NewOpenAPIHandler()
	depsHandler := handler.NewDepsHandler()
	configHandler := handler.NewConfigHandler()
	platformTokenHandler := handler.NewPlatformTokenHandler()

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

	openAPIHandler.RegisterRoutes(v1)
	openAPIHandler.RegisterRoutes(legacy)

	depsHandler.RegisterRoutes(v1)
	depsHandler.RegisterRoutes(legacy)

	configHandler.RegisterRoutes(v1)
	configHandler.RegisterRoutes(legacy)

	platformTokenHandler.RegisterRoutes(v1)
	platformTokenHandler.RegisterRoutes(legacy)

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
