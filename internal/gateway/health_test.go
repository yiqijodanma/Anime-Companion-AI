package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOperationalHealthSeparatesLivenessAndReadiness(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	checks := []ReadinessCheck{
		{Name: "agent", Check: func(context.Context) error { return nil }},
		{Name: "postgres", Check: func(context.Context) error { return errors.New("dsn with secret") }},
	}
	RegisterOperationalHealth(router, checks...)

	live := httptest.NewRecorder()
	router.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/livez", nil))
	require.Equal(t, http.StatusOK, live.Code)
	require.JSONEq(t, `{"status":"ok"}`, live.Body.String())

	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, ready.Code)
	require.Contains(t, ready.Body.String(), "dependency_unavailable")
	require.NotContains(t, ready.Body.String(), "dsn with secret")
}

func TestOperationalReadinessSucceedsWhenAllDependenciesAreReady(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterOperationalHealth(router,
		ReadinessCheck{Name: "agent", Check: func(context.Context) error { return nil }},
		ReadinessCheck{Name: "postgres", Check: func(context.Context) error { return nil }},
		ReadinessCheck{Name: "redis", Check: func(context.Context) error { return nil }},
	)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"status":"ok"}`, response.Body.String())
}
