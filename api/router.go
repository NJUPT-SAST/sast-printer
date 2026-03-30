package api

import "github.com/gin-gonic/gin"

// SetupRouter 配置API路由
func SetupRouter() *gin.Engine {
	router := gin.Default()

	// 健康检查
	router.GET("/health", HealthCheck)
	authMiddleware := AuthRequired()

	// 飞书免登流程接口（前端用 code 换 token）
	feishuAuth := router.Group("/api/auth/feishu")
	{
		feishuAuth.GET("/authorize-url", BuildFeishuAuthorizeURL)
		feishuAuth.POST("/code-login", ExchangeFeishuCode)
	}

	// CUPS相关接口
	printers := router.Group("/api/printers")
	printers.Use(authMiddleware)
	{
		printers.GET("", ListPrinters)       // 列出所有打印机
		printers.GET("/:id", GetPrinterInfo) // 获取打印机信息
	}

	// 打印任务相关接口
	jobs := router.Group("/api/jobs")
	jobs.Use(authMiddleware)
	{
		jobs.POST("", SubmitPrintJob)       // 提交打印任务
		jobs.GET("", ListPrintJobs)         // 列出所有打印任务
		jobs.GET("/:id", GetJobStatus)      // 获取任务状态
		jobs.DELETE("/:id", CancelPrintJob) // 取消打印任务
	}

	// 手动双面打印相关接口
	manualDuplex := router.Group("/api/manual-duplex-hooks")
	manualDuplex.Use(authMiddleware)
	{
		manualDuplex.POST("/:token/continue", ContinueManualDuplexPrint) // 继续手动双面打印
		manualDuplex.POST("/:token/cancel", CancelManualDuplexPrint)     // 取消手动双面打印
	}

	return router
}
