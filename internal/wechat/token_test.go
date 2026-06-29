package wechat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTokenManagerCachesAndRefreshes(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			_, _ = w.Write([]byte(`{"access_token":"ACCESS_TOKEN_1","expires_in":7200}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"ACCESS_TOKEN_2","expires_in":7200}`))
	}))
	defer ts.Close()

	tm := NewTokenManager("appid", "secret", ts.Client())
	tm.Endpoint = ts.URL
	tok, err := tm.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ACCESS_TOKEN_1", tok)
	tok2, err := tm.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ACCESS_TOKEN_1", tok2)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))

	tok3, err := tm.Refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ACCESS_TOKEN_2", tok3)
	require.Equal(t, int32(2), atomic.LoadInt32(&calls))
}

func TestTokenManagerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":40013,"errmsg":"invalid appid"}`))
	}))
	defer ts.Close()

	tm := NewTokenManager("appid", "secret", ts.Client())
	tm.Endpoint = ts.URL
	_, err := tm.Get(context.Background())
	require.Error(t, err)
}

type fakeTokenCache struct {
	token       string
	ok          bool
	getErr      error
	setErr      error
	deleteErr   error
	getCalls    int
	setCalls    int
	deleteCalls int
	setToken    string
	setTTL      time.Duration
}

func (f *fakeTokenCache) Get(context.Context) (string, bool, error) {
	f.getCalls++
	if f.getErr != nil {
		return "", false, f.getErr
	}
	return f.token, f.ok, nil
}

func (f *fakeTokenCache) Set(_ context.Context, token string, ttl time.Duration) error {
	f.setCalls++
	f.setToken = token
	f.setTTL = ttl
	if f.setErr != nil {
		return f.setErr
	}
	f.token = token
	f.ok = true
	return nil
}

func (f *fakeTokenCache) Delete(context.Context) error {
	f.deleteCalls++
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.token = ""
	f.ok = false
	return nil
}

func TestTokenManagerCacheHitAvoidsHTTP(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"access_token":"HTTP_TOKEN","expires_in":7200}`))
	}))
	defer ts.Close()

	cache := &fakeTokenCache{token: "CACHED_TOKEN", ok: true}
	tm := NewTokenManager("appid", "secret", ts.Client()).WithCache(cache)
	tm.Endpoint = ts.URL

	tok, err := tm.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "CACHED_TOKEN", tok)

	tok2, err := tm.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "CACHED_TOKEN", tok2)
	require.Equal(t, int32(0), atomic.LoadInt32(&calls))
	require.Equal(t, 1, cache.getCalls)
}

func TestTokenManagerCacheMissFetchesAndWritesCache(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"FETCHED_TOKEN","expires_in":120}`))
	}))
	defer ts.Close()

	cache := &fakeTokenCache{}
	tm := NewTokenManager("appid", "secret", ts.Client()).WithCache(cache)
	tm.Endpoint = ts.URL

	tok, err := tm.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "FETCHED_TOKEN", tok)
	require.Equal(t, 1, cache.getCalls)
	require.Equal(t, 1, cache.setCalls)
	require.Equal(t, "FETCHED_TOKEN", cache.setToken)
	require.Equal(t, 60*time.Second, cache.setTTL)
}

func TestTokenManagerRefreshDeletesAndReplacesCache(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"REFRESHED_TOKEN","expires_in":120}`))
	}))
	defer ts.Close()

	cache := &fakeTokenCache{token: "OLD_TOKEN", ok: true}
	tm := NewTokenManager("appid", "secret", ts.Client()).WithCache(cache)
	tm.Endpoint = ts.URL

	tok, err := tm.Refresh(context.Background())
	require.NoError(t, err)
	require.Equal(t, "REFRESHED_TOKEN", tok)
	require.Equal(t, 1, cache.deleteCalls)
	require.Equal(t, 1, cache.setCalls)
	require.Equal(t, "REFRESHED_TOKEN", cache.setToken)
}

func TestTokenManagerCacheErrorsFallBackToHTTP(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		_, _ = w.Write([]byte(`{"access_token":"FETCHED_TOKEN","expires_in":120}`))
	}))
	defer ts.Close()

	cache := &fakeTokenCache{getErr: errors.New("redis get failed"), setErr: errors.New("redis set failed")}
	tm := NewTokenManager("appid", "secret", ts.Client()).WithCache(cache)
	tm.Endpoint = ts.URL

	tok, err := tm.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "FETCHED_TOKEN", tok)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
	require.Equal(t, 1, cache.getCalls)
	require.Equal(t, 1, cache.setCalls)
}
