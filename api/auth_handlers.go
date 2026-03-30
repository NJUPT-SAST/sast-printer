package api

import (
	"bytes"
	"encoding/json"
	"fmt"
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

type feishuTokenExchangePayload struct {
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	CodeVerifier string `json:"code_verifier,omitempty"`
}

type feishuTokenExchangeResponse struct {
	Code                  int    `json:"code"`
	Msg                   string `json:"msg"`
	Error                 string `json:"error"`
	ErrorDescription      string `json:"error_description"`
	AccessToken           string `json:"access_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	TokenType             string `json:"token_type"`
	Scope                 string `json:"scope"`
}

// BuildFeishuAuthorizeURL 生成飞书 OAuth 授权地址，供前端端外或回退流程使用。
func BuildFeishuAuthorizeURL(c *gin.Context) {
	cfg, err := requireConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	appID := strings.TrimSpace(cfg.Auth.Feishu.AppID)
	if appID == "" {
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
	c.JSON(http.StatusOK, gin.H{
		"authorize_url": u.String(),
		"state":         state,
	})
}

// ExchangeFeishuCode 使用前端 requestAccess/requestAuthCode 拿到的 code 交换 user_access_token。
func ExchangeFeishuCode(c *gin.Context) {
	cfg, err := requireConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	appID := strings.TrimSpace(cfg.Auth.Feishu.AppID)
	appSecret := strings.TrimSpace(cfg.Auth.Feishu.AppSecret)
	if appID == "" || appSecret == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth.feishu.app_id/app_secret is not configured"})
		return
	}

	var reqBody feishuTokenExchangeRequest
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body", "details": err.Error()})
		return
	}

	code := strings.TrimSpace(reqBody.Code)
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code is required"})
		return
	}

	redirectURI := strings.TrimSpace(reqBody.RedirectURI)
	if redirectURI == "" {
		redirectURI = strings.TrimSpace(cfg.Auth.Feishu.RedirectURI)
	}

	payload := feishuTokenExchangePayload{
		GrantType:    "authorization_code",
		ClientID:     appID,
		ClientSecret: appSecret,
		Code:         code,
	}
	if redirectURI != "" {
		payload.RedirectURI = redirectURI
	}
	if verifier := strings.TrimSpace(reqBody.CodeVerifier); verifier != "" {
		payload.CodeVerifier = verifier
	}

	raw, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build token exchange request", "details": marshalErr.Error()})
		return
	}

	tokenURL := strings.TrimSpace(cfg.Auth.Feishu.TokenURL)
	if tokenURL == "" {
		tokenURL = "https://open.feishu.cn/open-apis/authen/v2/oauth/token"
	}

	requestTimeout, err := time.ParseDuration(cfg.Auth.Feishu.RequestTimeout)
	if err != nil || requestTimeout <= 0 {
		requestTimeout = 3 * time.Second
	}

	httpClient := &http.Client{Timeout: requestTimeout}
	req, reqErr := http.NewRequest(http.MethodPost, tokenURL, bytes.NewReader(raw))
	if reqErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create token request", "details": reqErr.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, doErr := httpClient.Do(req)
	if doErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to call feishu token endpoint", "details": doErr.Error()})
		return
	}
	defer resp.Body.Close()

	var tokenResp feishuTokenExchangeResponse
	if decodeErr := json.NewDecoder(resp.Body).Decode(&tokenResp); decodeErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to parse feishu token response", "details": decodeErr.Error()})
		return
	}

	if resp.StatusCode != http.StatusOK || tokenResp.Code != 0 || strings.TrimSpace(tokenResp.AccessToken) == "" {
		details := tokenResp.ErrorDescription
		if details == "" {
			details = tokenResp.Msg
		}
		if details == "" {
			details = fmt.Sprintf("feishu token exchange failed, status=%d", resp.StatusCode)
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "failed to exchange authorization code",
			"details": details,
			"code":    tokenResp.Code,
		})
		return
	}

	user, userErr := fetchFeishuUserInfo(tokenResp.AccessToken, cfg)
	if userErr != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "failed to fetch user info with exchanged token", "details": userErr.Error()})
		return
	}

	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(tokenResp.ExpiresIn) * time.Second)

	result := gin.H{
		"token_type":   tokenResp.TokenType,
		"access_token": tokenResp.AccessToken,
		"expires_in":   tokenResp.ExpiresIn,
		"expires_at":   expiresAt.Format(time.RFC3339),
		"scope":        tokenResp.Scope,
		"user":         user,
	}

	if tokenResp.RefreshToken != "" {
		result["refresh_token"] = tokenResp.RefreshToken
		result["refresh_token_expires_in"] = tokenResp.RefreshTokenExpiresIn
		if tokenResp.RefreshTokenExpiresIn > 0 {
			result["refresh_token_expires_at"] = now.Add(time.Duration(tokenResp.RefreshTokenExpiresIn) * time.Second).Format(time.RFC3339)
		}
	}

	if tokenResp.TokenType == "" {
		result["token_type"] = "Bearer"
	}

	result["issued_at"] = now.Format(time.RFC3339)
	result["feishu_code"] = strconv.Itoa(tokenResp.Code)

	c.JSON(http.StatusOK, result)
}
