package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"goprint/config"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type feishuUserInfo struct {
	OpenID    string `json:"open_id"`
	UnionID   string `json:"union_id"`
	UserID    string `json:"user_id"`
	Name      string `json:"name"`
	EnName    string `json:"en_name"`
	Avatar    string `json:"avatar_url"`
	TenantKey string `json:"tenant_key"`
}

type feishuUserInfoResponse struct {
	Code int            `json:"code"`
	Msg  string         `json:"msg"`
	Data feishuUserInfo `json:"data"`
}

type cachedFeishuToken struct {
	User      feishuUserInfo
	ExpiresAt time.Time
}

var feishuTokenCache = struct {
	sync.RWMutex
	items map[string]cachedFeishuToken
}{
	items: map[string]cachedFeishuToken{},
}

// AuthRequired 对除健康检查外的业务接口执行飞书 OAuth2 user_access_token 校验。
func AuthRequired() gin.HandlerFunc {
	return authRequired(false)
}

// SaneAPIAuthRequired 对 /sane-api 路径执行强制鉴权。
func SaneAPIAuthRequired() gin.HandlerFunc {
	return authRequired(true)
}

func authRequired(strict bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		method := c.Request.Method
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		clientIP := c.ClientIP()

		cfg, err := requireConfig()
		if err != nil {
			log.Printf("[auth][middleware] config error method=%s path=%s ip=%s err=%v", method, path, clientIP, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			c.Abort()
			return
		}

		if !strict && !cfg.Auth.Enabled {
			log.Printf("[auth][middleware] skip auth (disabled) method=%s path=%s ip=%s", method, path, clientIP)
			c.Next()
			return
		}

		if strict {
			if !cfg.SaneAPI.IsAuthEnabled() {
				log.Printf("[auth][middleware] skip sane-api auth (disabled) method=%s path=%s ip=%s", method, path, clientIP)
				c.Next()
				return
			}

			if ok, err := verifySaneAPIToken(c, cfg); err != nil {
				log.Printf("[auth][middleware] sane-api config error method=%s path=%s ip=%s err=%v", method, path, clientIP, err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				c.Abort()
				return
			} else if !ok {
				log.Printf("[auth][middleware] sane-api reject method=%s path=%s ip=%s", method, path, clientIP)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
				c.Abort()
				return
			}
			c.Next()
			return
		}

		authHeader := c.GetHeader("Authorization")
		token := extractBearerToken(authHeader)
		log.Printf("[auth][middleware] auth check method=%s path=%s ip=%s auth_header_present=%t token=%s",
			method, path, clientIP, strings.TrimSpace(authHeader) != "", maskSensitive(token))

		if token == "" {
			log.Printf("[auth][middleware] reject missing bearer method=%s path=%s ip=%s", method, path, clientIP)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "missing Authorization Bearer token",
			})
			c.Abort()
			return
		}

		user, validateErr := validateFeishuToken(token, cfg)
		if validateErr != nil {
			log.Printf("[auth][middleware] reject unauthorized method=%s path=%s ip=%s token=%s err=%v cost=%s",
				method, path, clientIP, maskSensitive(token), validateErr, time.Since(start))
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"details": validateErr.Error(),
			})
			c.Abort()
			return
		}

		c.Set("auth_provider", "feishu")
		c.Set("auth_user", user)
		log.Printf("[auth][middleware] auth success method=%s path=%s ip=%s token=%s open_id=%s cost=%s",
			method, path, clientIP, maskSensitive(token), maskSensitive(user.OpenID), time.Since(start))
		c.Next()
	}
}

func verifySaneAPIToken(c *gin.Context, cfg *config.Config) (bool, error) {
	if strings.TrimSpace(cfg.SaneAPI.AuthToken) != "" {
		headers := []string{
			strings.TrimSpace(c.GetHeader(cfg.SaneAPI.AuthHeader)),
			extractBearerToken(c.GetHeader("Authorization")),
		}
		for _, candidate := range headers {
			if candidate == "" {
				continue
			}
			if subtle.ConstantTimeCompare([]byte(candidate), []byte(cfg.SaneAPI.AuthToken)) == 1 {
				return true, nil
			}
		}
		return false, nil
	}

	if cfg.Auth.Enabled {
		token := extractBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			return false, nil
		}
		_, err := validateFeishuToken(token, cfg)
		if err != nil {
			return false, nil
		}
		return true, nil
	}

	return false, fmt.Errorf("sane_api.auth_token is not configured and global auth is disabled")
}

func extractBearerToken(header string) string {
	header = strings.TrimSpace(header)
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func validateFeishuToken(token string, cfg *config.Config) (feishuUserInfo, error) {
	now := time.Now()

	feishuTokenCache.RLock()
	cached, ok := feishuTokenCache.items[token]
	feishuTokenCache.RUnlock()
	if ok && now.Before(cached.ExpiresAt) {
		log.Printf("[auth][token] cache hit token=%s expire_at=%s", maskSensitive(token), cached.ExpiresAt.Format(time.RFC3339))
		return cached.User, nil
	}
	log.Printf("[auth][token] cache miss token=%s", maskSensitive(token))

	cacheTTL, err := time.ParseDuration(cfg.Auth.Feishu.TokenCacheTTL)
	if err != nil || cacheTTL <= 0 {
		cacheTTL = 2 * time.Minute
	}

	user, err := fetchFeishuUserInfo(token, cfg)
	if err != nil {
		log.Printf("[auth][token] validate failed token=%s err=%v", maskSensitive(token), err)
		return feishuUserInfo{}, err
	}

	feishuTokenCache.Lock()
	feishuTokenCache.items[token] = cachedFeishuToken{
		User:      user,
		ExpiresAt: now.Add(cacheTTL),
	}
	feishuTokenCache.Unlock()
	log.Printf("[auth][token] cache set token=%s ttl=%s", maskSensitive(token), cacheTTL)

	return user, nil
}

func fetchFeishuUserInfo(token string, cfg *config.Config) (feishuUserInfo, error) {
	sdkClient, err := newFeishuSDKClient(cfg)
	if err != nil {
		return feishuUserInfo{}, err
	}

	resp, err := sdkClient.getUserInfo(context.Background(), token)
	if err != nil {
		log.Printf("[auth][userinfo] sdk call failed token=%s err=%v", maskSensitive(token), err)
		return feishuUserInfo{}, err
	}
	if resp == nil || resp.Data == nil {
		log.Printf("[auth][userinfo] empty response token=%s", maskSensitive(token))
		return feishuUserInfo{}, fmt.Errorf("empty response from feishu user_info")
	}
	if !resp.Success() {
		log.Printf("[auth][userinfo] feishu reject token=%s code=%d msg=%q request_id=%s", maskSensitive(token), resp.Code, resp.Msg, resp.RequestId())
		return feishuUserInfo{}, fmt.Errorf("feishu user_info error: %s (code=%d, request_id=%s)", resp.Msg, resp.Code, resp.RequestId())
	}

	user := mapSDKUserInfo(resp.Data)
	if user.UserID == "" && user.OpenID == "" && user.UnionID == "" {
		log.Printf("[auth][userinfo] identity missing token=%s", maskSensitive(token))
		return feishuUserInfo{}, fmt.Errorf("feishu user_info response missing user identity")
	}

	log.Printf("[auth][userinfo] success token=%s open_id=%s", maskSensitive(token), maskSensitive(user.OpenID))

	return user, nil
}
