package api

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type feishuTokenExchangeRequest struct {
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier"`
}

// BuildFeishuAuthorizeURL 生成飞书 OAuth 授权地址，供前端端外或回退流程使用。
func BuildFeishuAuthorizeURL(c *gin.Context) {
	log.Printf("[auth][authorize-url] request ip=%s query=%q", c.ClientIP(), c.Request.URL.RawQuery)

	cfg, err := requireConfig()
	if err != nil {
		log.Printf("[auth][authorize-url] config error ip=%s err=%v", c.ClientIP(), err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	appID := strings.TrimSpace(cfg.Auth.Feishu.AppID)
	if appID == "" {
		log.Printf("[auth][authorize-url] app_id missing ip=%s", c.ClientIP())
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth.feishu.app_id is not configured"})
		return
	}

	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		generated, genErr := randomToken(16)
		if genErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state", "details": genErr.Error()})
			return
		}
		state = generated
	}

	rawRedirectURI := strings.TrimSpace(c.Query("redirect_uri"))
	if rawRedirectURI == "" {
		rawRedirectURI = strings.TrimSpace(cfg.Auth.Feishu.RedirectURI)
	}

	authorizeURL := strings.TrimSpace(cfg.Auth.Feishu.AuthorizeURL)
	if authorizeURL == "" {
		authorizeURL = "https://accounts.feishu.cn/open-apis/authen/v1/authorize"
	}

	u, parseErr := url.Parse(authorizeURL)
	if parseErr != nil {
		log.Printf("[auth][authorize-url] invalid authorize_url=%q err=%v", authorizeURL, parseErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid auth.feishu.authorize_url", "details": parseErr.Error()})
		return
	}

	q := u.Query()
	q.Set("client_id", appID)
	q.Set("response_type", "code")
	if rawRedirectURI != "" {
		q.Set("redirect_uri", rawRedirectURI)
	}
	if scope := strings.TrimSpace(c.Query("scope")); scope != "" {
		q.Set("scope", scope)
	}
	q.Set("state", state)

	if codeChallenge := strings.TrimSpace(c.Query("code_challenge")); codeChallenge != "" {
		q.Set("code_challenge", codeChallenge)
		method := strings.TrimSpace(c.Query("code_challenge_method"))
		if method == "" {
			method = "S256"
		}
		q.Set("code_challenge_method", method)
	}
	if prompt := strings.TrimSpace(c.Query("prompt")); prompt != "" {
		q.Set("prompt", prompt)
	}

	u.RawQuery = q.Encode()
	log.Printf("[auth][authorize-url] success ip=%s state=%s redirect_uri=%q app_id=%s", c.ClientIP(), state, rawRedirectURI, maskSensitive(appID))
	c.JSON(http.StatusOK, gin.H{
		"authorize_url": u.String(),
		"state":         state,
	})
}

// ExchangeFeishuCode 使用前端 requestAccess/requestAuthCode 拿到的 code 交换 user_access_token。
func ExchangeFeishuCode(c *gin.Context) {
	start := time.Now()
	log.Printf("[auth][code-login] request ip=%s path=%s", c.ClientIP(), c.Request.URL.Path)

	cfg, err := requireConfig()
	if err != nil {
		log.Printf("[auth][code-login] config error ip=%s err=%v", c.ClientIP(), err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	sdkClient, err := newFeishuSDKClient(cfg)
	if err != nil {
		log.Printf("[auth][code-login] sdk init failed ip=%s err=%v", c.ClientIP(), err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var reqBody feishuTokenExchangeRequest
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		log.Printf("[auth][code-login] invalid body ip=%s err=%v", c.ClientIP(), err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body", "details": err.Error()})
		return
	}

	code := strings.TrimSpace(reqBody.Code)
	log.Printf("[auth][code-login] payload ip=%s code=%s redirect_uri=%q code_verifier=%s", c.ClientIP(), maskSensitive(code), reqBody.RedirectURI, maskSensitive(reqBody.CodeVerifier))
	if code == "" {
		log.Printf("[auth][code-login] missing code ip=%s", c.ClientIP())
		c.JSON(http.StatusBadRequest, gin.H{"error": "code is required"})
		return
	}

	tokenResp, exchangeErr := sdkClient.exchangeCode(context.Background(), code)
	if exchangeErr != nil {
		log.Printf("[auth][code-login] exchange failed ip=%s code=%s err=%v", c.ClientIP(), maskSensitive(code), exchangeErr)
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to exchange authorization code", "details": exchangeErr.Error()})
		return
	}
	if tokenResp == nil || !tokenResp.Success() || tokenResp.Data == nil || strings.TrimSpace(tokenResp.Data.AccessToken) == "" {
		details := "feishu token exchange failed"
		respCode := 0
		requestID := ""
		if tokenResp != nil {
			details = tokenResp.Msg
			respCode = tokenResp.Code
			requestID = tokenResp.RequestId()
		}
		log.Printf("[auth][code-login] exchange rejected ip=%s code=%s feishu_code=%d request_id=%s msg=%q", c.ClientIP(), maskSensitive(code), respCode, requestID, details)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":      "failed to exchange authorization code",
			"details":    details,
			"code":       respCode,
			"request_id": requestID,
		})
		return
	}

	accessToken := strings.TrimSpace(tokenResp.Data.AccessToken)
	log.Printf("[auth][code-login] exchange success ip=%s code=%s access_token=%s expires_in=%d", c.ClientIP(), maskSensitive(code), maskSensitive(accessToken), tokenResp.Data.ExpiresIn)
	user, userErr := fetchFeishuUserInfo(accessToken, cfg)
	if userErr != nil {
		log.Printf("[auth][code-login] user_info failed ip=%s access_token=%s err=%v", c.ClientIP(), maskSensitive(accessToken), userErr)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "failed to fetch user info with exchanged token", "details": userErr.Error()})
		return
	}

	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(tokenResp.Data.ExpiresIn) * time.Second)

	result := gin.H{
		"token_type":   tokenResp.Data.TokenType,
		"access_token": accessToken,
		"expires_in":   tokenResp.Data.ExpiresIn,
		"expires_at":   expiresAt.Format(time.RFC3339),
		"scope":        "",
		"user":         user,
	}

	if tokenResp.Data.RefreshToken != "" {
		result["refresh_token"] = tokenResp.Data.RefreshToken
		result["refresh_token_expires_in"] = tokenResp.Data.RefreshExpiresIn
		if tokenResp.Data.RefreshExpiresIn > 0 {
			result["refresh_token_expires_at"] = now.Add(time.Duration(tokenResp.Data.RefreshExpiresIn) * time.Second).Format(time.RFC3339)
		}
	}

	if tokenResp.Data.TokenType == "" {
		result["token_type"] = "Bearer"
	}

	result["issued_at"] = now.Format(time.RFC3339)
	result["feishu_code"] = strconv.Itoa(tokenResp.Code)
	log.Printf("[auth][code-login] done ip=%s user_id=%s open_id=%s cost=%s", c.ClientIP(), maskSensitive(user.UserID), maskSensitive(user.OpenID), time.Since(start))

	c.JSON(http.StatusOK, result)
}

// GetAuthConfig 返回认证配置给前端（如 appID 等）
func GetAuthConfig(c *gin.Context) {
	cfg, err := requireConfig()
	if err != nil {
		log.Printf("[auth][config] config error ip=%s err=%v", c.ClientIP(), err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[auth][config] success ip=%s app_id=%s", c.ClientIP(), maskSensitive(cfg.Auth.Feishu.AppID))

	c.JSON(http.StatusOK, gin.H{
		// "enabled":       cfg.Auth.Enabled,
		"app_id": cfg.Auth.Feishu.AppID,
		// "authorize_url": cfg.Auth.Feishu.AuthorizeURL,
		// "redirect_uri":  cfg.Auth.Feishu.RedirectURI,
	})
}
