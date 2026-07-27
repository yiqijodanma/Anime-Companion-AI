package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	authn "companion-ai/internal/auth"
)

func TestAuthErrorHidesMailProviderFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	(&Handlers{}).authError(context, fmt.Errorf("smtp provider secret detail: %w", authn.ErrMailUnavailable))

	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	require.Contains(t, response.Body.String(), "email_unavailable")
	require.NotContains(t, response.Body.String(), "smtp provider")
	require.NotContains(t, response.Body.String(), "secret detail")
}
