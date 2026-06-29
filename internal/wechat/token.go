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
	cache     TokenCache

	mu      sync.Mutex
	token   string
	expires time.Time
}

type TokenCache interface {
	Get(ctx context.Context) (token string, ok bool, err error)
	Set(ctx context.Context, token string, ttl time.Duration) error
	Delete(ctx context.Context) error
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

func (tm *TokenManager) WithCache(cache TokenCache) *TokenManager {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.cache = cache
	return tm
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
	if tm.cache != nil {
		token, ok, err := tm.cache.Get(ctx)
		if err == nil && ok && token != "" {
			tm.token = token
			tm.expires = time.Now().Add(time.Minute)
			return tm.token, nil
		}
	}
	return tm.fetchLocked(ctx)
}

func (tm *TokenManager) Refresh(ctx context.Context) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.token = ""
	tm.expires = time.Time{}
	if tm.cache != nil {
		_ = tm.cache.Delete(ctx)
	}
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
	ttl := time.Duration(tr.ExpiresIn-60) * time.Second
	tm.token = tr.AccessToken
	tm.expires = time.Now().Add(ttl)
	if tm.cache != nil && ttl > 0 {
		_ = tm.cache.Set(ctx, tm.token, ttl)
	}
	return tm.token, nil
}
