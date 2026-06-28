package wechat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSendText(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &gotBody))
		require.Equal(t, "ACCESS_TOKEN_1", r.URL.Query().Get("access_token"))
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer ts.Close()

	kf := NewKFClient(ts.Client())
	kf.Endpoint = ts.URL
	err := kf.SendText(context.Background(), "ACCESS_TOKEN_1", "open_id_1", "晚安啦！")
	require.NoError(t, err)
	require.Equal(t, "open_id_1", gotBody["touser"])
	require.Equal(t, "text", gotBody["msgtype"])
}

func TestSendTextWeChatErrorIncludesCode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":42001,"errmsg":"access_token expired"}`))
	}))
	defer ts.Close()

	kf := NewKFClient(ts.Client())
	kf.Endpoint = ts.URL
	err := kf.SendText(context.Background(), "tok", "open_id_1", "hi")
	require.Error(t, err)
	var apiErr *APIError
	require.True(t, errors.As(err, &apiErr))
	require.Equal(t, 42001, apiErr.ErrCode)
}

func TestIsTokenExpiredError(t *testing.T) {
	require.True(t, IsTokenExpiredError(&APIError{ErrCode: 40001, ErrMsg: "invalid credential"}))
	require.True(t, IsTokenExpiredError(fmt.Errorf("wrapped: %w", &APIError{ErrCode: 42001, ErrMsg: "expired"})))
	require.False(t, IsTokenExpiredError(&APIError{ErrCode: 45015, ErrMsg: "out of time"}))
}
