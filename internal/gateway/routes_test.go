package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWebOnlyDefaultDoesNotRegisterWechatRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	(&Handlers{Agent: &fakeAgent{}, Log: slogDiscard()}).RegisterRoutes(router)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(method, "/wechat", nil))
		require.Equal(t, http.StatusNotFound, response.Code)
	}
}
func TestWebUIIsServedAtRootWithSPAHistoryFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	(&Handlers{Agent: &fakeAgent{}, Log: slogDiscard()}).RegisterRoutes(router)

	for _, path := range []string{"/", "/conversation/direct-haruhi"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, response.Code, path)
		require.Contains(t, response.Header().Get("Content-Type"), "text/html")
		require.Contains(t, response.Body.String(), "SOS 团聊天室")
	}

	reserved := httptest.NewRecorder()
	router.ServeHTTP(reserved, httptest.NewRequest(http.MethodGet, "/api/not-a-route", nil))
	require.Equal(t, http.StatusNotFound, reserved.Code)
	require.NotContains(t, reserved.Body.String(), "SOS 团聊天室")
}
