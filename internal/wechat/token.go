package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type TokenManager struct {
	appID     string
	appSecret string
	client    *http.Client
	Endpoint  string

	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewTokenManager(appID, appSecret string, client *http.Client) *TokenManager {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &TokenManager{
		appID:     appID,
		appSecret: appSecret,
		client:    client,
		Endpoint:  "https://api.weixin.qq.com/cgi-bin/token",
	}
}

type tokenResp struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

func (tm *TokenManager) Get(ctx context.Context) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.token != "" && time.Now().Before(tm.expires) {
		return tm.token, nil
	}
	return tm.fetchLocked(ctx)
}

func (tm *TokenManager) Refresh(ctx context.Context) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.token = ""
	tm.expires = time.Time{}
	return tm.fetchLocked(ctx)
}

func (tm *TokenManager) fetchLocked(ctx context.Context) (string, error) {
	u, err := url.Parse(tm.Endpoint)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("grant_type", "client_credential")
	q.Set("appid", tm.appID)
	q.Set("secret", tm.appSecret)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := tm.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var tr tokenResp
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", err
	}
	if tr.ErrCode != 0 {
		return "", &APIError{ErrCode: tr.ErrCode, ErrMsg: tr.ErrMsg}
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("wechat token response missing access_token")
	}
	tm.token = tr.AccessToken
	tm.expires = time.Now().Add(time.Duration(tr.ExpiresIn-60) * time.Second)
	return tm.token, nil
}
