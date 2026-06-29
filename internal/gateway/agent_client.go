package gateway

import (
	"context"
	"time"

	"google.golang.org/grpc"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	"companion-ai/gen/agentv1"
)

type AgentCaller interface {
	Reply(ctx context.Context, openID, text string) (string, error)
	ListMessages(ctx context.Context, openID string) ([]ConversationMessage, error)
	DeleteMessages(ctx context.Context, openID string) error
	RunDailyMaintenance(ctx context.Context, targetDate string) ([]string, error)
	Check(ctx context.Context) error
}

type ConversationMessage struct {
	ID        uint64 `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
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

func (a *AgentClient) ListMessages(ctx context.Context, openID string) ([]ConversationMessage, error) {
	resp, err := a.c.ListConversationMessages(ctx, &agentv1.ListConversationMessagesRequest{OpenId: openID})
	if err != nil {
		return nil, err
	}
	messages := make([]ConversationMessage, 0, len(resp.GetMessages()))
	for _, msg := range resp.GetMessages() {
		createdAt := ""
		if msg.GetCreatedAt() != nil {
			createdAt = msg.GetCreatedAt().AsTime().Format(time.RFC3339)
		}
		messages = append(messages, ConversationMessage{
			ID:        msg.GetId(),
			Role:      msg.GetRole(),
			Content:   msg.GetContent(),
			CreatedAt: createdAt,
		})
	}
	return messages, nil
}

func (a *AgentClient) DeleteMessages(ctx context.Context, openID string) error {
	_, err := a.c.DeleteConversationMessages(ctx, &agentv1.DeleteConversationMessagesRequest{OpenId: openID})
	return err
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
