package agent

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/chat"
	"companion-ai/internal/conversation"
	"companion-ai/internal/memory"
	"companion-ai/internal/orchestration"
	"companion-ai/internal/summarize"
)

type Server struct {
	agentv1.UnimplementedAgentServiceServer
	repo          *memory.Repo
	conversations conversation.Store
	replier       *chat.Replier
	sum           *summarize.Summarizer
	log           *slog.Logger
	app           *orchestration.Application
}

func (s *Server) WithConversationApplication(app *orchestration.Application) *Server {
	s.app = app
	return s
}

func NewServer(repo *memory.Repo, conversations conversation.Store, replier *chat.Replier, sum *summarize.Summarizer) *Server {
	return &Server{repo: repo, conversations: conversations, replier: replier, sum: sum, log: slog.Default()}
}

func (s *Server) WithLogger(l *slog.Logger) *Server {
	s.log = l
	return s
}

func toTurns(msgs []conversation.Turn) []chat.Turn {
	turns := make([]chat.Turn, 0, len(msgs))
	for _, msg := range msgs {
		turns = append(turns, chat.Turn{Role: msg.Role, Content: msg.Content})
	}
	return turns
}

func legacyIdentity(openID string) conversation.Identity {
	return conversation.Identity{Channel: "wechat", ExternalID: openID}
}

func identityFromReply(req *agentv1.ReplyRequest) conversation.Identity {
	if req.GetChannel() != "" || req.GetExternalId() != "" {
		return conversation.Identity{Channel: req.GetChannel(), ExternalID: req.GetExternalId()}
	}
	return legacyIdentity(req.GetOpenId())
}

func identityFromList(req *agentv1.ListConversationMessagesRequest) conversation.Identity {
	if req.GetChannel() != "" || req.GetExternalId() != "" {
		return conversation.Identity{Channel: req.GetChannel(), ExternalID: req.GetExternalId()}
	}
	return legacyIdentity(req.GetOpenId())
}

func identityFromDelete(req *agentv1.DeleteConversationMessagesRequest) conversation.Identity {
	if req.GetChannel() != "" || req.GetExternalId() != "" {
		return conversation.Identity{Channel: req.GetChannel(), ExternalID: req.GetExternalId()}
	}
	return legacyIdentity(req.GetOpenId())
}

func validateIdentity(identity conversation.Identity) error {
	if identity.Channel == "" || identity.ExternalID == "" {
		return status.Error(codes.InvalidArgument, "channel and external_id are required")
	}
	if !conversation.IsSupportedChannel(identity.Channel) {
		return status.Error(codes.InvalidArgument, "unsupported channel")
	}
	if len(identity.ExternalID) > conversation.MaxExternalIDLength {
		return status.Error(codes.InvalidArgument, "external_id is too long")
	}
	return nil
}

func compatibilityMessageID(turnID string, position int) uint64 {
	if turnID == "" {
		return uint64(position + 1)
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(turnID))
	id := h.Sum64()
	if id == 0 {
		return uint64(position + 1)
	}
	return id
}

func (s *Server) Reply(ctx context.Context, req *agentv1.ReplyRequest) (*agentv1.ReplyResponse, error) {
	identity := identityFromReply(req)
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	if s.app != nil {
		batch, err := s.app.Send(ctx, orchestration.SendCommand{
			Scope: orchestration.Scope{
				Owner:          orchestration.Owner{Channel: identity.Channel, ID: identity.ExternalID},
				ConversationID: conversation.DefaultConversationID,
			},
			Content: req.GetText(), ClientRequestID: uuid.NewString(),
		})
		if err != nil {
			return nil, orchestrationStatus(err)
		}
		if len(batch.CharacterMessages) == 0 {
			return nil, status.Error(codes.Unavailable, "reply generation interrupted")
		}
		return &agentv1.ReplyResponse{ReplyText: batch.CharacterMessages[0].Content}, nil
	}
	summaries, err := s.repo.RecentSummariesForIdentity(identity.Channel, identity.ExternalID)
	if err != nil {
		return nil, err
	}
	summaryTexts := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		summaryTexts = append(summaryTexts, summary.Content)
	}

	history, err := s.conversations.RecentTurns(ctx, identity, 30)
	if err != nil {
		return nil, err
	}
	if _, err := s.conversations.AddTurn(ctx, identity, memory.RoleUser, req.Text); err != nil {
		return nil, err
	}

	reply, err := s.replier.Reply(ctx, summaryTexts, toTurns(history), req.Text)
	if err != nil {
		return nil, err
	}
	if _, err := s.conversations.AddTurn(ctx, identity, memory.RoleAssistant, reply); err != nil {
		return nil, err
	}
	return &agentv1.ReplyResponse{ReplyText: reply}, nil
}

func (s *Server) ListConversationSpaces(_ context.Context, req *agentv1.ListConversationSpacesRequest) (*agentv1.ListConversationSpacesResponse, error) {
	identity := conversation.Identity{Channel: req.GetChannel(), ExternalID: req.GetExternalId()}
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	spaces := orchestration.FixedSpaces()
	out := make([]*agentv1.ConversationSpace, 0, len(spaces))
	for _, space := range spaces {
		participants := make([]*agentv1.CharacterMetadata, 0, len(space.Participants))
		for _, participant := range space.Participants {
			participants = append(participants, &agentv1.CharacterMetadata{
				Id: string(participant.ID), DisplayName: participant.DisplayName,
				AvatarUrl: participant.AvatarURL, Description: participant.Description,
			})
		}
		out = append(out, &agentv1.ConversationSpace{
			Id: space.ID, Kind: string(space.Kind), DisplayName: space.DisplayName, Participants: participants,
		})
	}
	return &agentv1.ListConversationSpacesResponse{Spaces: out}, nil
}

func (s *Server) ListConversationMessages(ctx context.Context, req *agentv1.ListConversationMessagesRequest) (*agentv1.ListConversationMessagesResponse, error) {
	identity := identityFromList(req)
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	if s.app != nil {
		conversationID := req.GetConversationId()
		if conversationID == "" {
			conversationID = conversation.DefaultConversationID
		}
		messages, err := s.app.ListMessages(ctx, orchestration.Scope{
			Owner: orchestration.Owner{Channel: identity.Channel, ID: identity.ExternalID}, ConversationID: conversationID,
		})
		if err != nil {
			return nil, orchestrationStatus(err)
		}
		out := make([]*agentv1.ConversationMessage, 0, len(messages))
		for i, message := range messages {
			out = append(out, conversationMessageProto(message, i))
		}
		return &agentv1.ListConversationMessagesResponse{Messages: out}, nil
	}
	msgs, err := s.conversations.TurnsForDate(ctx, identity, time.Now())
	if err != nil {
		return nil, err
	}
	out := make([]*agentv1.ConversationMessage, 0, len(msgs))
	for i, msg := range msgs {
		out = append(out, &agentv1.ConversationMessage{
			Id:        compatibilityMessageID(msg.TurnID, i),
			Role:      msg.Role,
			Content:   msg.Content,
			CreatedAt: timestamppb.New(msg.CreatedAt),
			TurnId:    msg.TurnID,
		})
	}
	return &agentv1.ListConversationMessagesResponse{Messages: out}, nil
}

func (s *Server) SendConversationMessage(ctx context.Context, req *agentv1.SendConversationMessageRequest) (*agentv1.SendConversationMessageResponse, error) {
	identity := conversation.Identity{Channel: req.GetChannel(), ExternalID: req.GetExternalId()}
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	if s.app == nil {
		return nil, status.Error(codes.FailedPrecondition, "conversation application unavailable")
	}
	batch, err := s.app.Send(ctx, orchestration.SendCommand{
		Scope: orchestration.Scope{
			Owner:          orchestration.Owner{Channel: identity.Channel, ID: identity.ExternalID},
			ConversationID: req.GetConversationId(),
		},
		Content: req.GetContent(), ClientRequestID: req.GetClientRequestId(),
	})
	if err != nil {
		return nil, orchestrationStatus(err)
	}
	return &agentv1.SendConversationMessageResponse{Batch: responseBatchProto(batch)}, nil
}

func (s *Server) DeleteConversationMessages(ctx context.Context, req *agentv1.DeleteConversationMessagesRequest) (*agentv1.DeleteConversationMessagesResponse, error) {
	identity := identityFromDelete(req)
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	if s.app != nil {
		conversationID := req.GetConversationId()
		if conversationID == "" {
			conversationID = conversation.DefaultConversationID
		}
		err := s.app.ClearToday(ctx, orchestration.Scope{
			Owner: orchestration.Owner{Channel: identity.Channel, ID: identity.ExternalID}, ConversationID: conversationID,
		})
		if err != nil {
			return nil, orchestrationStatus(err)
		}
		return &agentv1.DeleteConversationMessagesResponse{}, nil
	}
	if err := s.conversations.ClearToday(ctx, identity); err != nil {
		return nil, err
	}
	return &agentv1.DeleteConversationMessagesResponse{}, nil
}

func conversationMessageProto(message conversation.Turn, position int) *agentv1.ConversationMessage {
	return &agentv1.ConversationMessage{
		Id: compatibilityMessageID(message.TurnID, position), Role: message.Role, Content: message.Content,
		CreatedAt: timestamppb.New(message.CreatedAt), TurnId: message.TurnID,
		ConversationId: message.ConversationID, SpeakerKind: message.SpeakerKind, SpeakerId: message.SpeakerID,
		BatchId: message.BatchID, Sequence: message.Sequence, DisplayName: message.DisplayName, AvatarUrl: message.AvatarURL,
	}
}

func responseBatchProto(batch orchestration.ResponseBatch) *agentv1.ResponseBatch {
	characters := make([]*agentv1.ConversationMessage, 0, len(batch.CharacterMessages))
	for i, message := range batch.CharacterMessages {
		characters = append(characters, conversationMessageProto(message, i+1))
	}
	return &agentv1.ResponseBatch{
		BatchId: batch.BatchID, ClientRequestId: batch.ClientRequestID, ConversationId: batch.ConversationID,
		PlannedSpeakerIds: append([]string(nil), batch.PlannedSpeakerIDs...),
		UserMessage:       conversationMessageProto(batch.UserMessage, 0), CharacterMessages: characters,
		Status: batch.Status, InterruptionCode: batch.InterruptionCode,
		CreatedAt: timestamppb.New(batch.CreatedAt), UpdatedAt: timestamppb.New(batch.UpdatedAt),
	}
}

func orchestrationStatus(err error) error {
	switch {
	case errors.Is(err, orchestration.ErrInvalidRequest):
		return status.Error(codes.InvalidArgument, "invalid request")
	case errors.Is(err, orchestration.ErrMessageTooLarge):
		return status.Error(codes.ResourceExhausted, "message too large")
	case errors.Is(err, orchestration.ErrConversationNotFound):
		return status.Error(codes.NotFound, "conversation not found")
	case errors.Is(err, conversation.ErrConversationBusy), errors.Is(err, conversation.ErrLeaseLost):
		return status.Error(codes.Aborted, "conversation busy")
	case errors.Is(err, orchestration.ErrNotStarted):
		return status.Error(codes.FailedPrecondition, "conversation generation not started")
	default:
		return err
	}
}

func (s *Server) RunDailyMaintenance(ctx context.Context, req *agentv1.MaintenanceRequest) (*agentv1.MaintenanceResult, error) {
	if req.TargetDate == "" {
		return nil, fmt.Errorf("target_date is required")
	}
	targetDate, err := conversation.ParseBeijingDate(req.TargetDate)
	if err != nil {
		return nil, err
	}
	if s.app != nil {
		result, err := s.app.MaintainDay(ctx, targetDate)
		if err != nil {
			return nil, err
		}
		return &agentv1.MaintenanceResult{GreetOpenIds: result.GreetOwnerIDs}, nil
	}

	ids, err := s.conversations.ActiveIdentities(ctx, targetDate)
	if err != nil {
		return nil, err
	}
	greet := make([]string, 0, len(ids))

	for _, id := range ids {
		msgs, err := s.conversations.TurnsForDate(ctx, id, targetDate)
		if err != nil {
			s.log.Error("maintenance: load messages failed", "channel", id.Channel, "external_id", id.ExternalID, "err", err)
			continue
		}
		if len(msgs) == 0 {
			if err := s.conversations.ClearDate(ctx, id, targetDate); err != nil {
				s.log.Error("maintenance: delete empty conversation marker failed", "channel", id.Channel, "external_id", id.ExternalID, "err", err)
			}
			continue
		}
		archiveTurns := make([]memory.ArchiveTurn, 0, len(msgs))
		for _, msg := range msgs {
			archiveTurns = append(archiveTurns, memory.ArchiveTurn{
				TurnID:    msg.TurnID,
				Role:      msg.Role,
				Content:   msg.Content,
				CreatedAt: msg.CreatedAt,
			})
		}
		if err := s.repo.ArchiveDailyConversation(id.Channel, id.ExternalID, targetDate, archiveTurns, ""); err != nil {
			s.log.Error("maintenance: archive conversation failed", "channel", id.Channel, "external_id", id.ExternalID, "err", err)
			continue
		}
		summary, err := s.sum.Summarize(ctx, toTurns(msgs))
		if err != nil {
			s.log.Error("maintenance: summarize failed", "channel", id.Channel, "external_id", id.ExternalID, "err", err)
			continue
		}
		if err := s.repo.ArchiveDailyConversation(id.Channel, id.ExternalID, targetDate, nil, summary); err != nil {
			s.log.Error("maintenance: archive summary failed", "channel", id.Channel, "external_id", id.ExternalID, "err", err)
			continue
		}
		if err := s.conversations.ClearDate(ctx, id, targetDate); err != nil {
			s.log.Error("maintenance: delete messages failed", "channel", id.Channel, "external_id", id.ExternalID, "err", err)
		}
		if id.Channel == "wechat" {
			greet = append(greet, id.ExternalID)
		}
	}

	if err := s.repo.PurgeSummariesOlderThan(targetDate.AddDate(0, 0, -7)); err != nil {
		s.log.Error("maintenance: purge old summaries failed", "err", err)
	}
	return &agentv1.MaintenanceResult{GreetOpenIds: greet}, nil
}
