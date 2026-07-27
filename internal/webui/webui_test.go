package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSPAHandlerServesRootAssetsAndClientRoutes(t *testing.T) {
	handler := SPAHandler()
	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/", contentType: "text/html", contains: "SOS 团聊天室"},
		{path: "/app.js", contentType: "javascript", contains: "requestAnimationFrame"},
		{path: "/conversation/direct-haruhi", contentType: "text/html", contains: "SOS 团聊天室"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code)
			require.Contains(t, response.Header().Get("Content-Type"), tt.contentType)
			require.Contains(t, response.Body.String(), tt.contains)
		})
	}
}

func TestSPAHandlerRejectsNonReadMethods(t *testing.T) {
	response := httptest.NewRecorder()
	SPAHandler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	require.Equal(t, http.StatusMethodNotAllowed, response.Code)
}
