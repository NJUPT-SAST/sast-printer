# Session 认证迁移 - 安装与部署指南

## 前端改动（已完成）

位置：`/home/william/Documents/Code/sast-printer-next-frontend`

已修改的文件：
- `src/lib/utils.ts` - API 客户端添加 `withCredentials: true`
- `src/components/AuthChecker.tsx` - 移除 token 管理，添加 session 验证
- `docs/superpowers/specs/2026-06-12-session-based-auth.md` - 详细文档

前端已提交并准备就绪。

## 后端改动（需要安装依赖）

位置：`/home/william/Documents/Code/goprint`

### 步骤 1：安装依赖

由于网络问题，需要配置 Go proxy 或使用镜像：

```bash
cd /home/william/Documents/Code/goprint

# 使用国内镜像（推荐）
export GOPROXY=https://goproxy.cn,direct

# 或使用七牛云镜像
# export GOPROXY=https://goproxy.cn,https://goproxy.io,direct

# 下载依赖
go get github.com/gin-contrib/sessions@v1.0.1
go mod tidy
```

### 步骤 2：验证编译

```bash
go build -o goprint ./cmd
```

如果编译成功，应该看到生成的 `goprint` 可执行文件。

### 步骤 3：测试运行

```bash
./goprint
```

检查启动日志，确认没有错误。

### 步骤 4：测试认证流程

使用前端测试完整流程：

1. 清除浏览器所有 cookie
2. 访问前端 URL
3. 应该自动跳转到飞书登录
4. 登录后检查浏览器 Cookie 中是否有 `goprint_session`
5. 刷新页面，应该保持登录状态

## 改动文件清单

### 后端新增文件：
- `api/session_middleware.go` - Session 中间件实现

### 后端修改文件：
- `api/auth_handlers.go` - 登录成功后设置 session
- `api/router.go` - 使用 session 中间件
- `go.mod` - 添加 sessions 依赖
- `docs/session-auth-migration.md` - 完整迁移文档

## 验证清单

- [ ] Go 依赖安装成功
- [ ] 后端编译通过
- [ ] 后端启动无错误
- [ ] 前端登录流程正常
- [ ] 浏览器收到 session cookie
- [ ] 刷新页面保持登录状态
- [ ] API 请求携带 cookie
- [ ] `/api/auth/session` 返回正确状态

## 故障排查

### 1. 依赖下载失败

```bash
# 检查当前 proxy
go env GOPROXY

# 设置镜像
export GOPROXY=https://goproxy.cn,direct
go env -w GOPROXY=https://goproxy.cn,direct

# 清除缓存重试
go clean -modcache
go mod download
```

### 2. 编译错误

如果出现 `undefined` 错误，确认：
- `api/session_middleware.go` 文件存在
- `go.mod` 包含 `github.com/gin-contrib/sessions`
- 运行 `go mod tidy`

### 3. Cookie 未发送

检查：
- 浏览器开发者工具 → Application → Cookies
- Network 面板检查请求头是否包含 Cookie
- 确认前端 `withCredentials: true` 已设置

### 4. CORS 错误

如果前后端不同域名，需要添加 CORS 配置：

```bash
cd /home/william/Documents/Code/goprint
go get github.com/gin-contrib/cors
```

然后在 `api/router.go` 添加：

```go
import "github.com/gin-contrib/cors"

func SetupRouter() *gin.Engine {
    router := gin.Default()
    
    // CORS 配置
    router.Use(cors.New(cors.Config{
        AllowOrigins:     []string{"http://localhost:3000", "https://your-domain.com"},
        AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowHeaders:     []string{"Content-Type"},
        AllowCredentials: true, // 必须
    }))
    
    // ... 其他配置
}
```

## 生产环境部署

### 必须修改的配置

1. **Session Secret**（`api/session_middleware.go:17`）
   ```go
   // 当前：硬编码
   secret := []byte("change-this-secret-in-production")
   
   // 改为：从环境变量读取
   secret := []byte(os.Getenv("SESSION_SECRET"))
   ```

2. **Cookie Secure**（生产环境必须 HTTPS）
   ```go
   store.Options(sessions.Options{
       Secure: true, // 改为 true
       // ...
   })
   ```

3. **使用 Redis Store**（支持多实例）
   ```bash
   go get github.com/gin-contrib/sessions/redis
   ```
   
   参考 `docs/session-auth-migration.md` 中的 Redis 配置。

## 下一步

1. 配置 Go proxy 并安装依赖
2. 编译并测试后端
3. 与前端联调测试完整流程
4. 准备生产环境配置（Redis、Secret、HTTPS）
5. 部署到生产环境

## 文档

- 前端迁移文档：`sast-printer-next-frontend/docs/superpowers/specs/2026-06-12-session-based-auth.md`
- 后端迁移文档：`goprint/docs/session-auth-migration.md`
