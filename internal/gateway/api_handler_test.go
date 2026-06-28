package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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
