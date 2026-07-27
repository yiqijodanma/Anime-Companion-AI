package main

import (
	"context"

	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

const agentLivenessService = "liveness"

func initializeAgentHealth(server *health.Server) {
	server.SetServingStatus(agentLivenessService, healthv1.HealthCheckResponse_SERVING)
}

type dependencyCheck func(context.Context) error

func updateAgentServingStatus(ctx context.Context, server *health.Server, checks ...dependencyCheck) bool {
	for _, check := range checks {
		if err := check(ctx); err != nil {
			server.SetServingStatus("", healthv1.HealthCheckResponse_NOT_SERVING)
			return false
		}
	}
	server.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	return true
}
