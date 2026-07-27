package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

func TestUpdateAgentServingStatusReflectsDependencies(t *testing.T) {
	tests := []struct {
		name       string
		checks     []dependencyCheck
		wantStatus healthv1.HealthCheckResponse_ServingStatus
		wantReady  bool
	}{
		{
			name: "all dependencies ready",
			checks: []dependencyCheck{
				func(context.Context) error { return nil },
				func(context.Context) error { return nil },
			},
			wantStatus: healthv1.HealthCheckResponse_SERVING,
			wantReady:  true,
		},
		{
			name: "redis unavailable",
			checks: []dependencyCheck{
				func(context.Context) error { return nil },
				func(context.Context) error { return errors.New("redis down") },
			},
			wantStatus: healthv1.HealthCheckResponse_NOT_SERVING,
			wantReady:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := health.NewServer()
			ready := updateAgentServingStatus(context.Background(), server, tt.checks...)
			require.Equal(t, tt.wantReady, ready)
			response, err := server.Check(context.Background(), &healthv1.HealthCheckRequest{})
			require.NoError(t, err)
			require.Equal(t, tt.wantStatus, response.Status)
		})
	}
}

func TestAgentLivenessStaysServingWhenDependenciesAreUnavailable(t *testing.T) {
	server := health.NewServer()
	initializeAgentHealth(server)
	ready := updateAgentServingStatus(context.Background(), server, func(context.Context) error {
		return errors.New("postgres down")
	})
	require.False(t, ready)

	readiness, err := server.Check(context.Background(), &healthv1.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, healthv1.HealthCheckResponse_NOT_SERVING, readiness.Status)

	liveness, err := server.Check(context.Background(), &healthv1.HealthCheckRequest{Service: agentLivenessService})
	require.NoError(t, err)
	require.Equal(t, healthv1.HealthCheckResponse_SERVING, liveness.Status)
}
