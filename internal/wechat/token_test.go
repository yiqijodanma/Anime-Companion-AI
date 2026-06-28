package wechat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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
