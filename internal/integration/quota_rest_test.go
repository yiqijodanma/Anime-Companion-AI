package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"companion-ai/internal/gateway"
	"companion-ai/internal/orchestration"
	"companion-ai/internal/persona"
)

type limitedQuotaBody struct {
	Unlimited bool   `json:"unlimited"`
	Limit     int    `json:"limit"`
	Used      int    `json:"used"`
	Remaining int    `json:"remaining"`
	ResetAt   string `json:"reset_at"`
	Revision  int64  `json:"revision"`
}

func TestDailyQuotaAcrossSpacesRetryExhaustionAndAdmin(t *testing.T) {
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{
		persona.Haruhi: "春日回复",
		persona.Yuki:   "有希回复",
	}}
	fixture := newConversationRESTFixture(t, model)

	invalid := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "quota-user",
		`{"content":"invalid","client_request_id":"not-a-uuid"}`)
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())

	firstRequestID := "c0a80101-0000-4000-8000-000000000800"
	var firstBatchID string
	for i := 0; i < 20; i++ {
		path := "/api/v1/conversations/direct-haruhi/messages"
		if i%2 == 1 {
			path = "/api/v1/conversations/direct-yuki/messages"
		}
		requestID := fmt.Sprintf("c0a80101-0000-4000-8000-%012d", i+800)
		response := authenticatedRequest(fixture.router, http.MethodPost, path, "quota-user",
			fmt.Sprintf(`{"content":"message-%d","client_request_id":%q}`, i, requestID))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Batch gateway.ResponseBatch `json:"batch"`
			Quota limitedQuotaBody      `json:"quota"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.False(t, body.Quota.Unlimited)
		require.Equal(t, 20, body.Quota.Limit)
		require.Equal(t, i+1, body.Quota.Used)
		require.Equal(t, 19-i, body.Quota.Remaining)
		require.EqualValues(t, 2*(i+1), body.Quota.Revision)
		if i == 0 {
			firstBatchID = body.Batch.BatchID
		}
	}

	retry := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "quota-user",
		fmt.Sprintf(`{"content":"message-0","client_request_id":%q}`, firstRequestID))
	require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())
	var retryBody struct {
		Batch gateway.ResponseBatch `json:"batch"`
		Quota limitedQuotaBody      `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(retry.Body.Bytes(), &retryBody))
	require.Equal(t, firstBatchID, retryBody.Batch.BatchID)
	require.Equal(t, 20, retryBody.Quota.Used)
	require.Zero(t, retryBody.Quota.Remaining)
	require.EqualValues(t, 40, retryBody.Quota.Revision)

	exhausted := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "quota-user",
		`{"content":"one too many","client_request_id":"c0a80101-0000-4000-8000-000000000999"}`)
	require.Equal(t, http.StatusTooManyRequests, exhausted.Code, exhausted.Body.String())
	var exhaustedBody struct {
		Error struct {
			Code    string `json:"code"`
			RetryAt string `json:"retry_at"`
		} `json:"error"`
		Quota limitedQuotaBody `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(exhausted.Body.Bytes(), &exhaustedBody))
	require.Equal(t, "daily_quota_exhausted", exhaustedBody.Error.Code)
	require.Equal(t, "2026-07-24T00:00:00+08:00", exhaustedBody.Error.RetryAt)
	require.Zero(t, exhaustedBody.Quota.Remaining)
	require.EqualValues(t, 40, exhaustedBody.Quota.Revision)

	model.mu.Lock()
	require.Len(t, model.generateCalls, 20, "an invalid request, duplicate, and quota rejection must not call the model")
	model.mu.Unlock()

	admin := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "admin",
		`{"content":"admin message","client_request_id":"c0a80101-0000-4000-8000-000000001000"}`)
	require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
	var adminBody struct {
		Quota map[string]any `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(admin.Body.Bytes(), &adminBody))
	require.Equal(t, map[string]any{"unlimited": true}, adminBody.Quota)
}

func TestQuotaRequestIdentityIsScopedAndSurvivesConversationClear(t *testing.T) {
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{
		persona.Haruhi: "春日回复",
		persona.Yuki:   "有希回复",
	}}
	fixture := newConversationRESTFixture(t, model)
	requestID := "c0a80101-0000-4000-8000-000000001050"

	first := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "scoped-user",
		fmt.Sprintf(`{"content":"same id in haruhi","client_request_id":%q}`, requestID))
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var firstBody struct {
		Batch gateway.ResponseBatch `json:"batch"`
		Quota limitedQuotaBody      `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstBody))
	require.Equal(t, 1, firstBody.Quota.Used)

	second := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-yuki/messages", "scoped-user",
		fmt.Sprintf(`{"content":"same id in yuki","client_request_id":%q}`, requestID))
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	var secondBody struct {
		Quota limitedQuotaBody `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondBody))
	require.Equal(t, 2, secondBody.Quota.Used, "the same client UUID in another conversation is a distinct request")

	cleared := authenticatedRequest(fixture.router, http.MethodDelete, "/api/v1/conversations/direct-haruhi/messages", "scoped-user", "")
	require.Equal(t, http.StatusNoContent, cleared.Code, cleared.Body.String())
	retry := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "scoped-user",
		fmt.Sprintf(`{"content":"same id in haruhi","client_request_id":%q}`, requestID))
	require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())
	var retryBody struct {
		Batch gateway.ResponseBatch `json:"batch"`
		Quota limitedQuotaBody      `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(retry.Body.Bytes(), &retryBody))
	require.Equal(t, firstBody.Batch.BatchID, retryBody.Batch.BatchID)
	require.Equal(t, 2, retryBody.Quota.Used)

	model.mu.Lock()
	require.Len(t, model.generateCalls, 2, "clearing visible history must not erase request idempotency")
	model.mu.Unlock()
}

func TestSameRequestRetryAcrossBeijingMidnightIsNotGeneratedOrChargedTwice(t *testing.T) {
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{persona.Haruhi: "reply"}}
	fixture := newConversationRESTFixture(t, model)
	requestID := "c0a80101-0000-4000-8000-000000001060"
	*fixture.now = time.Date(2026, 7, 23, 15, 59, 50, 0, time.UTC)

	first := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "midnight-retry-user",
		fmt.Sprintf(`{"content":"same request","client_request_id":%q}`, requestID))
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var firstBody struct {
		Batch gateway.ResponseBatch `json:"batch"`
		Quota limitedQuotaBody      `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstBody))
	require.Equal(t, 1, firstBody.Quota.Used)

	*fixture.now = time.Date(2026, 7, 23, 16, 0, 10, 0, time.UTC)
	retry := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "midnight-retry-user",
		fmt.Sprintf(`{"content":"same request","client_request_id":%q}`, requestID))
	require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())
	var retryBody struct {
		Batch gateway.ResponseBatch `json:"batch"`
		Quota limitedQuotaBody      `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(retry.Body.Bytes(), &retryBody))
	require.Equal(t, firstBody.Batch.BatchID, retryBody.Batch.BatchID)
	require.Zero(t, retryBody.Quota.Used)
	require.Equal(t, 20, retryBody.Quota.Remaining)
	require.Equal(t, "2026-07-25T00:00:00+08:00", retryBody.Quota.ResetAt)

	model.mu.Lock()
	require.Len(t, model.generateCalls, 1)
	model.mu.Unlock()
}

func TestFailedGenerationReleasesQuotaAndPartialGenerationCommits(t *testing.T) {
	t.Run("complete failure releases", func(t *testing.T) {
		model := &scriptedConversationModel{generate: func(orchestration.CharacterInput) (string, error) {
			return "", errors.New("provider unavailable")
		}}
		fixture := newConversationRESTFixture(t, model)
		response := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "failed-user",
			`{"content":"fail","client_request_id":"c0a80101-0000-4000-8000-000000001100"}`)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Batch gateway.ResponseBatch `json:"batch"`
			Quota limitedQuotaBody      `json:"quota"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Equal(t, "failed", body.Batch.Status)
		require.Zero(t, body.Quota.Used)
		require.Equal(t, 20, body.Quota.Remaining)
	})

	t.Run("partial response commits one", func(t *testing.T) {
		model := &scriptedConversationModel{plan: []persona.CharacterID{persona.Yuki, persona.Kyon}}
		model.generate = func(input orchestration.CharacterInput) (string, error) {
			if input.Character.ID == persona.Yuki {
				return "有希完成了回复", nil
			}
			return "", errors.New("second speaker unavailable")
		}
		fixture := newConversationRESTFixture(t, model)
		response := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/sos-group/messages", "partial-user",
			`{"content":"partial","client_request_id":"c0a80101-0000-4000-8000-000000001101"}`)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Batch gateway.ResponseBatch `json:"batch"`
			Quota limitedQuotaBody      `json:"quota"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Equal(t, "partial", body.Batch.Status)
		require.Len(t, body.Batch.CharacterMessages, 1)
		require.Equal(t, 1, body.Quota.Used)
		require.Equal(t, 19, body.Quota.Remaining)
	})
}

func TestQuotaResetsAtBeijingMidnightThroughHTTP(t *testing.T) {
	model := &scriptedConversationModel{replies: map[persona.CharacterID]string{persona.Haruhi: "reply"}}
	fixture := newConversationRESTFixture(t, model)
	*fixture.now = time.Date(2026, 7, 23, 15, 59, 0, 0, time.UTC)

	before := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "midnight-http-user",
		`{"content":"before","client_request_id":"c0a80101-0000-4000-8000-000000001200"}`)
	require.Equal(t, http.StatusOK, before.Code, before.Body.String())
	var beforeBody struct {
		Quota limitedQuotaBody `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(before.Body.Bytes(), &beforeBody))
	require.Equal(t, 1, beforeBody.Quota.Used)
	require.Equal(t, "2026-07-24T00:00:00+08:00", beforeBody.Quota.ResetAt)

	*fixture.now = time.Date(2026, 7, 23, 16, 0, 0, 0, time.UTC)
	after := authenticatedRequest(fixture.router, http.MethodPost, "/api/v1/conversations/direct-haruhi/messages", "midnight-http-user",
		`{"content":"after","client_request_id":"c0a80101-0000-4000-8000-000000001201"}`)
	require.Equal(t, http.StatusOK, after.Code, after.Body.String())
	var afterBody struct {
		Quota limitedQuotaBody `json:"quota"`
	}
	require.NoError(t, json.Unmarshal(after.Body.Bytes(), &afterBody))
	require.Equal(t, 1, afterBody.Quota.Used, "the first request of the new Beijing day starts a fresh pool")
	require.Equal(t, 19, afterBody.Quota.Remaining)
	require.Equal(t, "2026-07-25T00:00:00+08:00", afterBody.Quota.ResetAt)
}
