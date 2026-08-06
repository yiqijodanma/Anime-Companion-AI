package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	authn "companion-ai/internal/auth"
	"companion-ai/internal/conversation"
	"companion-ai/internal/quota"
)

func newQuotaHandler(t *testing.T, limit int, agent *fakeAgent) (*Handlers, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	manager, err := quota.NewRedis(client, "test:http-quota:", limit)
	require.NoError(t, err)
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	server.SetTime(now)
	return &Handlers{
		Agent: agent,
		Quota: manager,
		Now:   func() time.Time { return now },
		AuthenticateSession: func(_ context.Context, token string) (authn.User, error) {
			return authn.User{ID: strings.TrimPrefix(token, "token-"), IsAdmin: token == "token-admin"}, nil
		},
	}, client
}

func TestAuthenticatedSessionIncludesQuotaContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newQuotaHandler(t, 20, &fakeAgent{})
	router := gin.New()
	router.GET("/api/v1/auth/session", h.requireUser(), h.authSession)

	ordinary := authenticatedGatewayRequest(router, http.MethodGet, "/api/v1/auth/session", "user", "")
	require.Equal(t, http.StatusOK, ordinary.Code, ordinary.Body.String())
	var ordinaryBody struct {
		Quota map[string]any `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(ordinary.Body.Bytes(), &ordinaryBody))
	require.Equal(t, map[string]any{
		"unlimited": false,
		"limit":     float64(20),
		"used":      float64(0),
		"remaining": float64(20),
		"reset_at":  "2026-07-24T00:00:00+08:00",
		"revision":  float64(0),
	}, ordinaryBody.Quota)

	admin := authenticatedGatewayRequest(router, http.MethodGet, "/api/v1/auth/session", "admin", "")
	require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
	var adminBody struct {
		Quota map[string]any `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(admin.Body.Bytes(), &adminBody))
	require.Equal(t, map[string]any{"unlimited": true}, adminBody.Quota)
}

func TestQuotaUnavailableFailsClosedBeforeAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	agent := &fakeAgent{batch: deliveredTestBatch()}
	h, client := newQuotaHandler(t, 20, agent)
	require.NoError(t, client.Close())
	router := gin.New()
	h.RegisterRoutes(router)

	response := authenticatedGatewayRequest(router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "user",
		`{"content":"hello","client_request_id":"00000000-0000-4000-8000-000000000001"}`)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.JSONEq(t, `{"error":{"code":"quota_unavailable","message":"对话额度暂时不可用，请稍后重试"}}`, response.Body.String())
	require.Zero(t, agent.SendCalls())
}

func TestConventionalRateLimitRejectionDoesNotConsumeDailyQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	agent := &fakeAgent{batch: deliveredTestBatch()}
	h, _ := newQuotaHandler(t, 20, agent)
	limiter := &fakeLimiter{allow: false}
	h.Limiter = limiter
	router := gin.New()
	h.RegisterRoutes(router)

	rejected := authenticatedGatewayRequest(router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "rate-user",
		`{"content":"first","client_request_id":"00000000-0000-4000-8000-000000000010"}`)
	require.Equal(t, http.StatusTooManyRequests, rejected.Code, rejected.Body.String())
	var rejectedBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rejected.Body.Bytes(), &rejectedBody))
	require.Equal(t, "rate_limited", rejectedBody.Error.Code)
	require.Zero(t, agent.SendCalls())

	limiter.mu.Lock()
	limiter.allow = true
	limiter.mu.Unlock()
	accepted := authenticatedGatewayRequest(router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "rate-user",
		`{"content":"second","client_request_id":"00000000-0000-4000-8000-000000000011"}`)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
	var acceptedBody struct {
		Quota limitedQuotaResponse `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(accepted.Body.Bytes(), &acceptedBody))
	require.Equal(t, 1, acceptedBody.Quota.Used)
	require.Equal(t, 19, acceptedBody.Quota.Remaining)
}

func TestMissingQuotaManagerFailsClosedForOrdinaryUserButNotAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	agent := &fakeAgent{batch: deliveredTestBatch()}
	h := &Handlers{
		Agent: agent,
		AuthenticateSession: func(_ context.Context, token string) (authn.User, error) {
			return authn.User{ID: strings.TrimPrefix(token, "token-"), IsAdmin: token == "token-admin"}, nil
		},
	}
	router := gin.New()
	h.RegisterRoutes(router)

	ordinary := authenticatedGatewayRequest(router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "user",
		`{"content":"ordinary","client_request_id":"00000000-0000-4000-8000-000000000020"}`)
	require.Equal(t, http.StatusServiceUnavailable, ordinary.Code, ordinary.Body.String())
	require.Zero(t, agent.SendCalls())

	admin := authenticatedGatewayRequest(router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "admin",
		`{"content":"admin","client_request_id":"00000000-0000-4000-8000-000000000021"}`)
	require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
	var adminBody struct {
		Quota map[string]any `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(admin.Body.Bytes(), &adminBody))
	require.Equal(t, map[string]any{"unlimited": true}, adminBody.Quota)
	require.Equal(t, 1, agent.SendCalls())
}

func TestDeprecatedAliasReturnsDeliveredPartialReplyWhenItChargesQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	batch := deliveredTestBatch()
	batch.Status = conversation.BatchPartial
	agent := &fakeAgent{batch: batch}
	h, _ := newQuotaHandler(t, 20, agent)
	router := gin.New()
	h.RegisterRoutes(router)

	response := authenticatedGatewayRequest(router, http.MethodPost, "/api/v1/conversations/messages", "alias-partial-user",
		`{"content":"partial","client_request_id":"00000000-0000-4000-8000-000000000022"}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Reply string               `json:"reply"`
		Quota limitedQuotaResponse `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, "reply", body.Reply)
	require.Equal(t, 1, body.Quota.Used)
	require.Equal(t, 19, body.Quota.Remaining)
}

func TestConcurrentAuthenticatedRequestsCannotExceedQuota(t *testing.T) {
	gin.SetMode(gin.TestMode)
	release := make(chan struct{})
	agent := &fakeAgent{
		batch:       deliveredTestBatch(),
		sendStarted: make(chan struct{}, 64),
		sendRelease: release,
	}
	h, _ := newQuotaHandler(t, 20, agent)
	router := gin.New()
	h.RegisterRoutes(router)

	responses := make(chan *httptest.ResponseRecorder, 64)
	var wait sync.WaitGroup
	for i := 0; i < 64; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			requestID := fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
			responses <- authenticatedGatewayRequest(router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "parallel-user",
				fmt.Sprintf(`{"content":"hello","client_request_id":%q}`, requestID))
		}(i)
	}

	for i := 0; i < 20; i++ {
		select {
		case <-agent.sendStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the 20 admitted Agent calls")
		}
	}

	for i := 0; i < 44; i++ {
		select {
		case response := <-responses:
			require.Equal(t, http.StatusTooManyRequests, response.Code, response.Body.String())
			var body struct {
				Error struct {
					Code    string `json:"code"`
					RetryAt string `json:"retry_at"`
				} `json:"error"`
				Quota struct {
					Remaining int `json:"remaining"`
				} `json:"quota"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			require.Equal(t, "daily_quota_exhausted", body.Error.Code)
			require.Equal(t, "2026-07-24T00:00:00+08:00", body.Error.RetryAt)
			require.Zero(t, body.Quota.Remaining)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for quota rejections")
		}
	}
	close(release)
	wait.Wait()
	close(responses)
	for response := range responses {
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Batch ResponseBatch `json:"batch"`
			Quota struct {
				Used      int `json:"used"`
				Remaining int `json:"remaining"`
			} `json:"quota"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.NotEmpty(t, body.Batch.CharacterMessages)
		require.Equal(t, 20, body.Quota.Used)
		require.Zero(t, body.Quota.Remaining)
	}
	require.Equal(t, 20, agent.SendCalls())
}

func deliveredTestBatch() ResponseBatch {
	return ResponseBatch{
		BatchID: "batch-1", ClientRequestID: "request-1", ConversationID: "direct-haruhi",
		Status:            conversation.BatchComplete,
		CharacterMessages: []ConversationMessage{{ID: 1, Role: "assistant", Content: "reply"}},
	}
}

type limitedQuotaResponse struct {
	Used      int `json:"used"`
	Remaining int `json:"remaining"`
}

func authenticatedGatewayRequest(router *gin.Engine, method, path, owner, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "token-" + owner})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
