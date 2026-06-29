package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAPIChat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handlers{Agent: &fakeAgent{reply: "你好团员！"}, Log: slogDiscard()}
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"open_id":"u1","text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "你好团员！")
}

func TestAPIChatBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handlers{Agent: &fakeAgent{}, Log: slogDiscard()}
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPIListConversationMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agent := &fakeAgent{messages: []ConversationMessage{{
		ID:        12,
		Role:      "user",
		Content:   "你好",
		CreatedAt: "2026-06-29T12:00:00Z",
	}}}
	h := &Handlers{Agent: agent, Log: slogDiscard()}
	h.RegisterRoutes(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/conversations/u1/messages", nil))

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "u1", agent.lastListID)
	require.JSONEq(t, `{"messages":[{"id":12,"role":"user","content":"你好","created_at":"2026-06-29T12:00:00Z"}]}`, w.Body.String())
}

func TestAPIListConversationMessagesReturnsEmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handlers{Agent: &fakeAgent{}, Log: slogDiscard()}
	h.RegisterRoutes(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/conversations/u1/messages", nil))

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"messages":[]}`, w.Body.String())
}

func TestAPIDeleteConversationMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agent := &fakeAgent{}
	h := &Handlers{Agent: agent, Log: slogDiscard()}
	h.RegisterRoutes(r)

	for range 2 {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/u1/messages", nil))

		require.Equal(t, http.StatusNoContent, w.Code)
		require.Empty(t, w.Body.String())
	}
	require.Equal(t, "u1", agent.lastDeleteID)
	require.Equal(t, 2, agent.deleteCalls)
}

func TestAPIConversationMessagesAgentError(t *testing.T) {
	tests := []struct {
		name   string
		method string
		agent  *fakeAgent
	}{
		{name: "list", method: http.MethodGet, agent: &fakeAgent{listErr: errors.New("agent down")}},
		{name: "delete", method: http.MethodDelete, agent: &fakeAgent{deleteErr: errors.New("agent down")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			h := &Handlers{Agent: tt.agent, Log: slogDiscard()}
			h.RegisterRoutes(r)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tt.method, "/api/v1/conversations/u1/messages", nil))

			require.Equal(t, http.StatusBadGateway, w.Code)
		})
	}
}

func TestAPIConversationMessagesInvalidArgument(t *testing.T) {
	invalid := status.Error(codes.InvalidArgument, "open_id is required")
	tests := []struct {
		name   string
		method string
		agent  *fakeAgent
	}{
		{name: "list", method: http.MethodGet, agent: &fakeAgent{listErr: invalid}},
		{name: "delete", method: http.MethodDelete, agent: &fakeAgent{deleteErr: invalid}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			h := &Handlers{Agent: tt.agent, Log: slogDiscard()}
			h.RegisterRoutes(r)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tt.method, "/api/v1/conversations/u1/messages", nil))

			require.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestHealthzChecksAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handlers{Agent: &fakeAgent{}, Log: slogDiscard()}
	h.RegisterRoutes(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, w.Code)
}

func TestHealthzFailsWhenAgentUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handlers{Agent: &fakeAgent{healthErr: errors.New("down")}, Log: slogDiscard()}
	h.RegisterRoutes(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
