package api

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"goprint/config"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkext "github.com/larksuite/oapi-sdk-go/v3/service/ext"
)

type feishuSDKClient struct {
	client  *lark.Client
	timeout time.Duration
}

func maskSensitive(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}
	if len(value) <= 8 {
		return fmt.Sprintf("%s(len=%d)", strings.Repeat("*", len(value)), len(value))
	}
	return fmt.Sprintf("%s...%s(len=%d)", value[:4], value[len(value)-4:], len(value))
}

func newFeishuSDKClient(cfg *config.Config) (*feishuSDKClient, error) {
	appID := strings.TrimSpace(cfg.Auth.Feishu.AppID)
	appSecret := strings.TrimSpace(cfg.Auth.Feishu.AppSecret)
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("auth.feishu.app_id/app_secret is not configured")
	}

	timeout, err := time.ParseDuration(cfg.Auth.Feishu.RequestTimeout)
	if err != nil || timeout <= 0 {
		timeout = 3 * time.Second
	}

	log.Printf("[auth][sdk] init feishu sdk client app_id=%s timeout=%s", maskSensitive(appID), timeout)

	return &feishuSDKClient{
		client:  lark.NewClient(appID, appSecret),
		timeout: timeout,
	}, nil
}

func (c *feishuSDKClient) exchangeCode(ctx context.Context, code string) (*larkext.AuthenAccessTokenResp, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("code is required")
	}
	log.Printf("[auth][sdk] exchange code start code=%s", maskSensitive(code))

	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req := larkext.NewAuthenAccessTokenReqBuilder().
		Body(larkext.NewAuthenAccessTokenReqBodyBuilder().
			GrantType("authorization_code").
			Code(code).
			Build()).
		Build()

	resp, err := c.client.Ext.Authen.AuthenAccessToken(timeoutCtx, req)
	if err != nil {
		log.Printf("[auth][sdk] exchange code failed code=%s err=%v", maskSensitive(code), err)
		return nil, fmt.Errorf("failed to exchange authorization code via feishu sdk: %w", err)
	}
	if resp == nil {
		log.Printf("[auth][sdk] exchange code got nil response code=%s", maskSensitive(code))
		return nil, fmt.Errorf("empty response from feishu token api")
	}

	accessToken := ""
	if resp.Data != nil {
		accessToken = resp.Data.AccessToken
	}

	log.Printf("[auth][sdk] exchange code done code=%s success=%v feishu_code=%d feishu_msg=%q request_id=%s token=%s",
		maskSensitive(code), resp.Success(), resp.Code, resp.Msg, resp.RequestId(), maskSensitive(accessToken))

	return resp, nil
}

func (c *feishuSDKClient) getUserInfo(ctx context.Context, token string) (*larkext.AuthenUserInfoResp, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("user access token is required")
	}
	log.Printf("[auth][sdk] user_info start token=%s", maskSensitive(token))

	timeoutCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, err := c.client.Ext.Authen.AuthenUserInfo(timeoutCtx, larkcore.WithUserAccessToken(token))
	if err != nil {
		log.Printf("[auth][sdk] user_info failed token=%s err=%v", maskSensitive(token), err)
		return nil, fmt.Errorf("failed to query feishu user_info via sdk: %w", err)
	}
	if resp == nil {
		log.Printf("[auth][sdk] user_info got nil response token=%s", maskSensitive(token))
		return nil, fmt.Errorf("empty response from feishu user_info api")
	}

	openID := ""
	if resp.Data != nil {
		openID = resp.Data.OpenID
	}
	log.Printf("[auth][sdk] user_info done token=%s success=%v feishu_code=%d feishu_msg=%q request_id=%s open_id=%s",
		maskSensitive(token), resp.Success(), resp.Code, resp.Msg, resp.RequestId(), maskSensitive(openID))

	return resp, nil
}

func mapSDKUserInfo(data *larkext.AuthenUserInfoRespBody) feishuUserInfo {
	if data == nil {
		return feishuUserInfo{}
	}

	return feishuUserInfo{
		OpenID:    data.OpenID,
		UnionID:   data.UnionID,
		UserID:    data.UserID,
		Name:      data.Name,
		EnName:    data.EnName,
		Avatar:    data.AvatarURL,
		TenantKey: data.TenantKey,
	}
}

func randomString(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("randomString: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)[:n]
}

func getJSAPITicket(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://open.feishu.cn/open-apis/jssdk/ticket/get", nil)
	if err != nil {
		return "", fmt.Errorf("build ticket request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("get jsapi_ticket: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Ticket   string `json:"ticket"`
			ExpireIn int    `json:"expire_in"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return "", fmt.Errorf("parse ticket response: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("jsapi_ticket error: code=%d msg=%s", result.Code, result.Msg)
	}
	if result.Data.Ticket == "" {
		return "", fmt.Errorf("empty jsapi_ticket")
	}

	log.Printf("[jssdk] got jsapi_ticket expires_in=%d", result.Data.ExpireIn)
	return result.Data.Ticket, nil
}

func generateJSSDKConfig(ctx context.Context, cfg *config.Config, pageURL string) (map[string]string, error) {
	appID := strings.TrimSpace(cfg.Auth.Feishu.AppID)
	appSecret := strings.TrimSpace(cfg.Auth.Feishu.AppSecret)
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("auth.feishu.app_id/app_secret is not configured")
	}

	client := lark.NewClient(appID, appSecret)
	tokenReq := larkcore.SelfBuiltTenantAccessTokenReq{
		AppID:     appID,
		AppSecret: appSecret,
	}
	tokenResp, err := client.GetTenantAccessTokenBySelfBuiltApp(ctx, &tokenReq)
	if err != nil {
		return nil, fmt.Errorf("get tenant_access_token: %w", err)
	}
	if !tokenResp.Success() {
		return nil, fmt.Errorf("tenant_access_token error: code=%d msg=%s", tokenResp.Code, tokenResp.Msg)
	}

	ticket, err := getJSAPITicket(ctx, tokenResp.TenantAccessToken)
	if err != nil {
		return nil, err
	}

	nonceStr := randomString(16)
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	verifyStr := fmt.Sprintf("jsapi_ticket=%s&noncestr=%s&timestamp=%s&url=%s",
		ticket, nonceStr, timestamp, pageURL)
	h := sha1.New()
	h.Write([]byte(verifyStr))
	signature := hex.EncodeToString(h.Sum(nil))

	return map[string]string{
		"appId":     appID,
		"timestamp": timestamp,
		"nonceStr":  nonceStr,
		"signature": signature,
	}, nil
}
