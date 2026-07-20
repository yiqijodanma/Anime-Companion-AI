package gateway

import (
	"context"
	"time"

	"google.golang.org/grpc"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	"companion-ai/gen/agentv1"
)

type AgentCaller interface {
	Reply(ctx context.Context, channel, externalID, text string) (string, error)
	ListConversationSpaces(ctx context.Context, channel, externalID string) ([]ConversationSpace, error)
	SendConversationMessage(ctx context.Context, channel, externalID, conversationID, content, clientRequestID string) (ResponseBatch, error)
	ListConversationMessages(ctx context.Context, channel, externalID, conversationID string) ([]ConversationMessage, error)
	DeleteConversationMessages(ctx context.Context, channel, externalID, conversationID string) error
	ListMessages(ctx context.Context, channel, externalID string) ([]ConversationMessage, error)
	DeleteMessages(ctx context.Context, channel, externalID string) error
	RunDailyMaintenance(ctx context.Context, targetDate string) ([]string, error)
	Check(ctx context.Context) error
}

type Character struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	Description string `json:"description"`
}

type ConversationSpace struct {
	ID           string      `json:"id"`
	Kind         string      `json:"kind"`
	DisplayName  string      `json:"display_name"`
	Participants []Character `json:"participants"`
}

type ConversationMessage struct {
	ID             uint64 `json:"id"`
	TurnID         string `json:"turn_id,omitempty"`
	Role           string `json:"role"`
	Content        string `json:"content"`
	CreatedAt      string `json:"created_at"`
	ConversationID string `json:"conversation_id,omitempty"`
	SpeakerKind    string `json:"speaker_kind,omitempty"`
	SpeakerID      string `json:"speaker_id,omitempty"`
	BatchID        string `json:"batch_id,omitempty"`
	Sequence       uint64 `json:"sequence,omitempty"`
	DisplayName    string `json:"display_name,omitempty"`
	AvatarURL      string `json:"avatar_url,omitempty"`
}

type ResponseBatch struct {
	BatchID           string                `json:"batch_id"`
	ClientRequestID   string                `json:"client_request_id"`
	ConversationID    string                `json:"conversation_id"`
	PlannedSpeakerIDs []string              `json:"planned_speaker_ids"`
	UserMessage       ConversationMessage   `json:"user_message"`
	CharacterMessages []ConversationMessage `json:"character_messages"`
	Status            string                `json:"status"`
	InterruptionCode  string                `json:"interruption_code,omitempty"`
	CreatedAt         string                `json:"created_at"`
	UpdatedAt         string                `json:"updated_at"`
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

func (a *AgentClient) Reply(ctx context.Context, channel, externalID, text string) (string, error) {
	resp, err := a.c.Reply(ctx, &agentv1.ReplyRequest{Channel: channel, ExternalId: externalID, Text: text})
	if err != nil {
		return "", err
	}
	return resp.ReplyText, nil
}

func (a *AgentClient) ListConversationSpaces(ctx context.Context, channel, externalID string) ([]ConversationSpace, error) {
	resp, err := a.c.ListConversationSpaces(ctx, &agentv1.ListConversationSpacesRequest{Channel: channel, ExternalId: externalID})
	if err != nil {
		return nil, err
	}
	spaces := make([]ConversationSpace, 0, len(resp.GetSpaces()))
	for _, space := range resp.GetSpaces() {
		participants := make([]Character, 0, len(space.GetParticipants()))
		for _, participant := range space.GetParticipants() {
			participants = append(participants, Character{
				ID: participant.GetId(), DisplayName: participant.GetDisplayName(),
				AvatarURL: participant.GetAvatarUrl(), Description: participant.GetDescription(),
			})
		}
		spaces = append(spaces, ConversationSpace{
			ID: space.GetId(), Kind: space.GetKind(), DisplayName: space.GetDisplayName(), Participants: participants,
		})
	}
	return spaces, nil
}

func (a *AgentClient) SendConversationMessage(ctx context.Context, channel, externalID, conversationID, content, clientRequestID string) (ResponseBatch, error) {
	resp, err := a.c.SendConversationMessage(ctx, &agentv1.SendConversationMessageRequest{
		Channel: channel, ExternalId: externalID, ConversationId: conversationID,
		Content: content, ClientRequestId: clientRequestID,
	})
	if err != nil {
		return ResponseBatch{}, err
	}
	return responseBatchDTO(resp.GetBatch()), nil
}

func (a *AgentClient) ListConversationMessages(ctx context.Context, channel, externalID, conversationID string) ([]ConversationMessage, error) {
	resp, err := a.c.ListConversationMessages(ctx, &agentv1.ListConversationMessagesRequest{
		Channel: channel, ExternalId: externalID, ConversationId: conversationID,
	})
	if err != nil {
		return nil, err
	}
	return conversationMessagesDTO(resp.GetMessages()), nil
}

func (a *AgentClient) DeleteConversationMessages(ctx context.Context, channel, externalID, conversationID string) error {
	_, err := a.c.DeleteConversationMessages(ctx, &agentv1.DeleteConversationMessagesRequest{
		Channel: channel, ExternalId: externalID, ConversationId: conversationID,
	})
	return err
}

func (a *AgentClient) ListMessages(ctx context.Context, channel, externalID string) ([]ConversationMessage, error) {
	resp, err := a.c.ListConversationMessages(ctx, &agentv1.ListConversationMessagesRequest{Channel: channel, ExternalId: externalID})
	if err != nil {
		return nil, err
	}
	return conversationMessagesDTO(resp.GetMessages()), nil
}

func conversationMessagesDTO(messages []*agentv1.ConversationMessage) []ConversationMessage {
	out := make([]ConversationMessage, 0, len(messages))
	for _, msg := range messages {
		createdAt := ""
		if msg.GetCreatedAt() != nil {
			createdAt = msg.GetCreatedAt().AsTime().Format(time.RFC3339)
		}
		out = append(out, ConversationMessage{
			ID: msg.GetId(), TurnID: msg.GetTurnId(), Role: msg.GetRole(), Content: msg.GetContent(), CreatedAt: createdAt,
			ConversationID: msg.GetConversationId(), SpeakerKind: msg.GetSpeakerKind(), SpeakerID: msg.GetSpeakerId(),
			BatchID: msg.GetBatchId(), Sequence: msg.GetSequence(), DisplayName: msg.GetDisplayName(), AvatarURL: msg.GetAvatarUrl(),
		})
	}
	return out
}

func responseBatchDTO(batch *agentv1.ResponseBatch) ResponseBatch {
	if batch == nil {
		return ResponseBatch{}
	}
	createdAt, updatedAt := "", ""
	if batch.GetCreatedAt() != nil {
		createdAt = batch.GetCreatedAt().AsTime().Format(time.RFC3339)
	}
	if batch.GetUpdatedAt() != nil {
		updatedAt = batch.GetUpdatedAt().AsTime().Format(time.RFC3339)
	}
	user := conversationMessagesDTO([]*agentv1.ConversationMessage{batch.GetUserMessage()})
	var userMessage ConversationMessage
	if len(user) > 0 {
		userMessage = user[0]
	}
	return ResponseBatch{
		BatchID: batch.GetBatchId(), ClientRequestID: batch.GetClientRequestId(), ConversationID: batch.GetConversationId(),
		PlannedSpeakerIDs: append([]string(nil), batch.GetPlannedSpeakerIds()...), UserMessage: userMessage,
		CharacterMessages: conversationMessagesDTO(batch.GetCharacterMessages()), Status: batch.GetStatus(),
		InterruptionCode: batch.GetInterruptionCode(), CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

func (a *AgentClient) DeleteMessages(ctx context.Context, channel, externalID string) error {
	_, err := a.c.DeleteConversationMessages(ctx, &agentv1.DeleteConversationMessagesRequest{Channel: channel, ExternalId: externalID})
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
