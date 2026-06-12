# Session-Based Authentication Implementation (Backend)

## 概述

后端已从 Bearer Token 认证迁移到基于 Cookie 的 Session 认证。

## 改动文件

### 1. 新增文件

#### `api/session_middleware.go`
- 实现 session 中间件和认证逻辑
- `SetupSessionMiddleware()` - 配置 session 存储
- `SessionAuthRequired()` - 验证 session 的中间件
- `SetSession()` - 登录成功后存储 session
- `CheckSession()` - 验证 session 有效性的端点处理器

### 2. 修改文件

#### `api/auth_handlers.go`
- `ExchangeFeishuCode()` 函数改动：
  - 移除返回 `access_token`、`refresh_token` 等字段
  - 调用 `SetSession()` 将用户信息存储到 session
  - 返回简化的响应：`{message, user}`

#### `api/router.go`
- 添加 session 中间件初始化
- 将 `AuthRequired()` 改为 `SessionAuthRequired()`
- 新增 `GET /api/auth/session` 端点用于验证 session
- 保持 `/api/auth/config` 路由组向后兼容

#### `go.mod`
- 添加依赖：`github.com/gin-contrib/sessions v1.0.1`

## 安装依赖

```bash
cd /home/william/Documents/Code/goprint
go get github.com/gin-contrib/sessions
go mod tidy
```

## Session 配置

### 当前配置（Cookie Store）

```go
// api/session_middleware.go
secret := []byte("change-this-secret-in-production")
store := cookie.NewStore(secret)

store.Options(sessions.Options{
    Path:     "/",
    MaxAge:   86400 * 7, // 7 days
    HttpOnly: true,      // Prevent XSS
    Secure:   false,     // Set to true in production with HTTPS
    SameSite: http.SameSiteLaxMode,
})
```

### 生产环境推荐（Redis Store）

对于生产环境，建议使用 Redis 存储以支持多实例部署：

```bash
go get github.com/gin-contrib/sessions/redis
```

```go
import (
    "github.com/gin-contrib/sessions"
    "github.com/gin-contrib/sessions/redis"
)

func SetupSessionMiddleware(cfg *config.Config) gin.HandlerFunc {
    // Use Redis store
    store, _ := redis.NewStore(10, "tcp", "localhost:6379", "", []byte("secret"))
    
    store.Options(sessions.Options{
        Path:     "/",
        MaxAge:   86400 * 7,
        HttpOnly: true,
        Secure:   true, // HTTPS only
        SameSite: http.SameSiteNoneMode, // For cross-domain
    })
    
    return sessions.Sessions("goprint_session", store)
}
```

## API 变更

### 登录端点 `POST /api/auth/config/code-login`

**Before (返回 token):**
```json
{
  "token_type": "Bearer",
  "access_token": "u-xxx",
  "expires_in": 7200,
  "expires_at": "2026-06-12T12:00:00Z",
  "refresh_token": "...",
  "user": { ... }
}
```

**After (设置 session cookie):**
```json
{
  "message": "login successful",
  "user": {
    "open_id": "ou_xxx",
    "user_id": "xxx",
    "name": "张三",
    "avatar_url": "https://..."
  }
}
```

**响应头包含:**
```
Set-Cookie: goprint_session=MTcxODI...; Path=/; HttpOnly; Max-Age=604800
```

### 新增端点 `GET /api/auth/session`

验证当前 session 是否有效：

**Response (有效):**
```json
{
  "authenticated": true,
  "user": {
    "open_id": "ou_xxx",
    "user_id": "xxx",
    "name": "张三",
    "avatar_url": "https://..."
  }
}
```

**Response (无效):**
```json
{
  "authenticated": false
}
```

**Response (认证禁用):**
```json
{
  "authenticated": false,
  "reason": "auth disabled"
}
```

## 测试

### 1. 编译运行

```bash
cd /home/william/Documents/Code/goprint
go mod tidy
go build -o goprint cmd/main.go
./goprint
```

### 2. 测试登录流程

```bash
# 1. 获取认证配置
curl http://localhost:5001/api/auth/config

# 2. 使用 code 登录（模拟前端）
curl -c cookies.txt -X POST http://localhost:5001/api/auth/config/code-login \
  -H "Content-Type: application/json" \
  -d '{"code":"your-oauth-code"}'

# 3. 验证 session
curl -b cookies.txt http://localhost:5001/api/auth/session

# 4. 访问保护的端点
curl -b cookies.txt http://localhost:5001/api/printers
```

### 3. 测试 Session 过期

Session 默认有效期 7 天，可以通过修改 `MaxAge` 测试过期：

```go
store.Options(sessions.Options{
    MaxAge: 60, // 60 seconds for testing
    // ...
})
```

## CORS 配置

如果前后端分离部署，需要配置 CORS 允许携带凭据：

```bash
go get github.com/gin-contrib/cors
```

```go
import "github.com/gin-contrib/cors"

router := gin.Default()
router.Use(cors.New(cors.Config{
    AllowOrigins:     []string{"https://your-frontend.com"},
    AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    AllowHeaders:     []string{"Content-Type"},
    AllowCredentials: true, // 关键：允许跨域请求携带 cookie
}))
```

## 兼容性说明

### 保留的功能
- `AuthRequired()` 中间件仍然存在，用于向后兼容（如果有其他系统使用 Bearer token）
- `/api/auth/config/*` 路由组保持不变
- 飞书 token 验证逻辑 `validateFeishuToken()` 保留，session 中间件可选使用

### 迁移建议
1. 先部署后端，两种认证方式并存
2. 部署前端更新
3. 确认所有客户端都使用 session 后，可考虑移除 Bearer token 支持

## 安全最佳实践

### 生产环境必须：
1. **HTTPS**: `Secure: true` 要求必须使用 HTTPS
2. **Secret 管理**: 从环境变量或配置文件读取 session secret
3. **SameSite**: 跨域场景使用 `SameSiteNoneMode` + `Secure: true`
4. **Redis Store**: Cookie store 不适合生产，使用 Redis 支持水平扩展

### 推荐配置示例

```go
func SetupSessionMiddleware(cfg *config.Config) gin.HandlerFunc {
    secret := []byte(os.Getenv("SESSION_SECRET"))
    if len(secret) == 0 {
        log.Fatal("SESSION_SECRET environment variable required")
    }
    
    redisAddr := os.Getenv("REDIS_ADDR")
    if redisAddr == "" {
        redisAddr = "localhost:6379"
    }
    
    store, err := redis.NewStore(10, "tcp", redisAddr, "", secret)
    if err != nil {
        log.Fatalf("Failed to create redis store: %v", err)
    }
    
    isProduction := os.Getenv("ENV") == "production"
    
    store.Options(sessions.Options{
        Path:     "/",
        Domain:   os.Getenv("COOKIE_DOMAIN"), // e.g., ".example.com"
        MaxAge:   86400 * 7,
        HttpOnly: true,
        Secure:   isProduction,
        SameSite: http.SameSiteLaxMode,
    })
    
    return sessions.Sessions("goprint_session", store)
}
```

## 环境变量

```bash
# 必需
SESSION_SECRET=your-random-secret-key-here

# Redis（生产环境推荐）
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=your-redis-password

# Cookie 配置
COOKIE_DOMAIN=.example.com
ENV=production
```

## 故障排查

### 问题：前端请求返回 401

**检查项：**
1. 浏览器开发者工具 → Network → 检查请求是否携带 Cookie
2. 检查响应头是否包含 `Set-Cookie`
3. 前端 axios 配置是否包含 `withCredentials: true`
4. CORS 配置是否包含 `AllowCredentials: true`

### 问题：Session 无法持久化

**检查项：**
1. 使用 Cookie store 时，重启服务会丢失所有 session
2. 切换到 Redis store 解决此问题
3. 检查 Redis 连接是否正常

### 问题：跨域请求 Cookie 未发送

**检查项：**
1. `SameSite` 设置为 `None` 时，`Secure` 必须为 `true`
2. 必须使用 HTTPS（开发环境可用 `localhost` 例外）
3. 前端和后端域名必须在 CORS `AllowOrigins` 中

## 回滚方案

如果需要回滚到 Bearer Token 认证：

1. 前端：恢复 localStorage token 管理和 Authorization header
2. 后端：将 `router.go` 中的 `SessionAuthRequired()` 改回 `AuthRequired()`
3. 后端：恢复 `auth_handlers.go` 中的原始响应格式

所有旧代码都保留，回滚无需重新开发。
