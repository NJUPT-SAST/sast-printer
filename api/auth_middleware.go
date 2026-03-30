package api

import (
	"encoding/json"
	"fmt"
	"goprint/config"
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
	return func(c *gin.Context) {
		cfg, err := requireConfig()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			c.Abort()
			return
		}

		if !cfg.Auth.Enabled {
			c.Next()
			return
		}

		token := extractBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "missing Authorization Bearer token",
			})
			c.Abort()
			return
		}

		user, validateErr := validateFeishuToken(token, cfg)
		if validateErr != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error":   "unauthorized",
				"details": validateErr.Error(),
			})
			c.Abort()
			return
		}

		c.Set("auth_provider", "feishu")
		c.Set("auth_user", user)
		c.Next()
	}
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
		return cached.User, nil
	}

	cacheTTL, err := time.ParseDuration(cfg.Auth.Feishu.TokenCacheTTL)
	if err != nil || cacheTTL <= 0 {
		cacheTTL = 2 * time.Minute
	}

	user, err := fetchFeishuUserInfo(token, cfg)
	if err != nil {
		return feishuUserInfo{}, err
	}

	feishuTokenCache.Lock()
	feishuTokenCache.items[token] = cachedFeishuToken{
		User:      user,
		ExpiresAt: now.Add(cacheTTL),
	}
	feishuTokenCache.Unlock()

	return user, nil
}

func fetchFeishuUserInfo(token string, cfg *config.Config) (feishuUserInfo, error) {
	requestTimeout, err := time.ParseDuration(cfg.Auth.Feishu.RequestTimeout)
	if err != nil || requestTimeout <= 0 {
		requestTimeout = 3 * time.Second
	}

	url := strings.TrimSpace(cfg.Auth.Feishu.UserInfoURL)
	if url == "" {
		url = "https://open.feishu.cn/open-apis/authen/v1/user_info"
	}

	client := &http.Client{Timeout: requestTimeout}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return feishuUserInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := client.Do(req)
	if err != nil {
		return feishuUserInfo{}, fmt.Errorf("failed to verify token with Feishu: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return feishuUserInfo{}, fmt.Errorf("feishu user_info returned status %d", resp.StatusCode)
	}

	var payload feishuUserInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return feishuUserInfo{}, fmt.Errorf("failed to parse feishu user_info response: %w", err)
	}

	if payload.Code != 0 {
		return feishuUserInfo{}, fmt.Errorf("feishu user_info error: %s (code=%d)", payload.Msg, payload.Code)
	}

	if payload.Data.UserID == "" && payload.Data.OpenID == "" && payload.Data.UnionID == "" {
		return feishuUserInfo{}, fmt.Errorf("feishu user_info response missing user identity")
	}

	return payload.Data, nil
}
