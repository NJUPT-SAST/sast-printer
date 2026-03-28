package api

import "github.com/gin-gonic/gin"

// SetupRouter 配置API路由
func SetupRouter() *gin.Engine {
	router := gin.Default()

	// 健康检查
	router.GET("/health", HealthCheck)

	// CUPS相关接口
	printers := router.Group("/api/printers")
	{
		printers.GET("", ListPrinters)       // 列出所有打印机
		printers.GET("/:id", GetPrinterInfo) // 获取打印机信息
	}

	// 打印任务相关接口
	jobs := router.Group("/api/jobs")
	{
		jobs.POST("", SubmitPrintJob)       // 提交打印任务
		jobs.GET("", ListPrintJobs)         // 列出所有打印任务
		jobs.GET("/:id", GetJobStatus)      // 获取任务状态
		jobs.DELETE("/:id", CancelPrintJob) // 取消打印任务
	}

	return router
}
