package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedWebUIIsAvailableAtApp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	(&Handlers{Agent: &fakeAgent{}, Log: slogDiscard()}).RegisterRoutes(router)

	index := httptest.NewRecorder()
	router.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/app/", nil))
	require.Equal(t, http.StatusOK, index.Code)
	require.Contains(t, index.Body.String(), "SOS 团聊天室")
	require.Contains(t, index.Body.String(), "login-form")
	require.Contains(t, index.Body.String(), "conversation-list")
	require.Contains(t, index.Body.String(), "discussion-status")

	for _, asset := range []string{"/app/app.js", "/app/styles.css", "/app/assets/avatars/haruhi.svg"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, asset, nil))
		require.Equal(t, http.StatusOK, w.Code, asset)
		require.NotEmpty(t, w.Body.String(), asset)
	}
}

func TestEmbeddedWebUIMobileChatKeepsMessagePaneScrollable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	(&Handlers{Agent: &fakeAgent{}, Log: slogDiscard()}).RegisterRoutes(router)

	styles := httptest.NewRecorder()
	router.ServeHTTP(styles, httptest.NewRequest(http.MethodGet, "/app/styles.css", nil))
	require.Equal(t, http.StatusOK, styles.Code)

	css := styles.Body.String()
	require.Contains(t, css, ".messages { min-height:0; overflow:auto;")
	require.Contains(t, css, "height:100dvh; min-height:0;")
	require.Contains(t, css, "grid-template-rows:auto minmax(0,1fr);")
	require.Contains(t, css, ".chat-panel { min-height:0; }")
}

func TestEmbeddedWebUIPositionsConversationAtLatestMessageAfterLayout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	(&Handlers{Agent: &fakeAgent{}, Log: slogDiscard()}).RegisterRoutes(router)

	index := httptest.NewRecorder()
	router.ServeHTTP(index, httptest.NewRequest(http.MethodGet, "/app/", nil))
	require.Equal(t, http.StatusOK, index.Code)
	require.Contains(t, index.Body.String(), "/app/styles.css?v=")
	require.Contains(t, index.Body.String(), "/app/app.js?v=")

	script := httptest.NewRecorder()
	router.ServeHTTP(script, httptest.NewRequest(http.MethodGet, "/app/app.js", nil))
	require.Equal(t, http.StatusOK, script.Code)
	require.Contains(t, script.Body.String(), "requestAnimationFrame")
	require.Contains(t, script.Body.String(), "setTimeout(positionLatest, 0)")
	require.Contains(t, script.Body.String(), `scrollIntoView({ block: "end" })`)

	styles := httptest.NewRecorder()
	router.ServeHTTP(styles, httptest.NewRequest(http.MethodGet, "/app/styles.css", nil))
	require.Equal(t, http.StatusOK, styles.Code)
	require.Contains(t, styles.Body.String(), "overflow-anchor:none;")
}
