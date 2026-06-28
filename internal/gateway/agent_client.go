package gateway

import (
	"context"

	"google.golang.org/grpc"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	"companion-ai/gen/agentv1"
)

type AgentCaller interface {
	Reply(ctx context.Context, openID, text string) (string, error)
	RunDailyMaintenance(ctx context.Context, targetDate string) ([]string, error)
	Check(ctx context.Context) error
}

type AgentClient struct {
	c      agentv1.AgentServiceClient
	health healthv1.HealthClient
}

func NewAgentClient(conn *grpc.ClientConn) *AgentClient {
	return &AgentClient{
		c:      agentv1.NewAgentServiceClient(conn),
		health: healthv1.NewHealthClient(conn),
	}
}

func (a *AgentClient) Reply(ctx context.Context, openID, text string) (string, error) {
	resp, err := a.c.Reply(ctx, &agentv1.ReplyRequest{OpenId: openID, Text: text})
	if err != nil {
		return "", err
	}
	return resp.ReplyText, nil
}

func (a *AgentClient) RunDailyMaintenance(ctx context.Context, targetDate string) ([]string, error) {
	resp, err := a.c.RunDailyMaintenance(ctx, &agentv1.MaintenanceRequest{TargetDate: targetDate})
	if err != nil {
		return nil, err
	}
	return resp.GreetOpenIds, nil
}

func (a *AgentClient) Check(ctx context.Context) error {
	_, err := a.health.Check(ctx, &healthv1.HealthCheckRequest{})
	return err
}
