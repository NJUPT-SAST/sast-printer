package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// SetupRouter 配置API路由
func SetupRouter() *gin.Engine {
	router := gin.Default()
	router.Use(IPRateLimit())

	// 健康检查
	router.GET("/health", HealthCheck)
	authMiddleware := AuthRequired()

	// 飞书免登流程接口（前端用 code 换 token）
	feishuAuth := router.Group("/api/auth/config")
	{
		feishuAuth.GET("", GetAuthConfig) // 返回认证配置（appID等）给前端
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
		jobs.POST("", SubmitPrintJob)                                // 提交打印任务
		jobs.POST("/preview", PreviewConvertedDocument)              // 仅转换并预览文件
		jobs.GET("/supported-file-types", GetSupportedFileTypes)     // 获取当前支持上传的文件类型
		jobs.GET("", ListPrintJobs)                                  // 列出所有打印任务
		jobs.GET("/:id", GetJobStatus)                               // 获取任务状态
		jobs.DELETE("/:id", CancelPrintJob)                          // 仅删除多维表任务记录
	}

	// 手动双面打印相关接口
	manualDuplex := router.Group("/api/manual-duplex-hooks")
	// manualDuplex.Use(authMiddleware)
	{
		manualDuplex.POST("/:token/continue", ContinueManualDuplexPrint) // 继续手动双面打印
		manualDuplex.POST("/:token/cancel", CancelManualDuplexPrint)     // 取消手动双面打印
	}

	// 前端路由：从 public 目录读取静态资源，并为 SPA 路由回退 index.html。
	registerFrontendRoutes(router)

	return router
}

func registerFrontendRoutes(router *gin.Engine) {
	const publicRoot = "public"
	indexPath := filepath.Join(publicRoot, "index.html")

	router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		method := c.Request.Method

		if strings.HasPrefix(path, "/api/") || path == "/api" {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		if method != http.MethodGet && method != http.MethodHead {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		cleanPath := filepath.Clean("/" + path)
		if cleanPath == "/" {
			if _, err := os.Stat(indexPath); err == nil {
				c.File(indexPath)
				return
			}
			c.JSON(http.StatusNotFound, gin.H{"error": "frontend index not found"})
			return
		}

		candidate := filepath.Join(publicRoot, strings.TrimPrefix(cleanPath, "/"))
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			c.File(candidate)
			return
		}

		if _, err := os.Stat(indexPath); err == nil {
			c.File(indexPath)
			return
		}

		c.JSON(http.StatusNotFound, gin.H{"error": "frontend index not found"})
	})
}
