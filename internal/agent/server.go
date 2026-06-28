package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/chat"
	"companion-ai/internal/memory"
	"companion-ai/internal/summarize"
)

type Server struct {
	agentv1.UnimplementedAgentServiceServer
	repo    *memory.Repo
	replier *chat.Replier
	sum     *summarize.Summarizer
	log     *slog.Logger
}

func NewServer(repo *memory.Repo, replier *chat.Replier, sum *summarize.Summarizer) *Server {
	return &Server{repo: repo, replier: replier, sum: sum, log: slog.Default()}
}

func (s *Server) WithLogger(l *slog.Logger) *Server {
	s.log = l
	return s
}

func toTurns(msgs []memory.Message) []chat.Turn {
	turns := make([]chat.Turn, 0, len(msgs))
	for _, msg := range msgs {
		turns = append(turns, chat.Turn{Role: msg.Role, Content: msg.Content})
	}
	return turns
}

func (s *Server) Reply(ctx context.Context, req *agentv1.ReplyRequest) (*agentv1.ReplyResponse, error) {
	summaries, err := s.repo.RecentSummaries(req.OpenId)
	if err != nil {
		return nil, err
	}
	summaryTexts := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		summaryTexts = append(summaryTexts, summary.Content)
	}

	history, err := s.repo.TodayMessages(req.OpenId)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SaveMessage(req.OpenId, memory.RoleUser, req.Text); err != nil {
		return nil, err
	}

	reply, err := s.replier.Reply(ctx, summaryTexts, toTurns(history), req.Text)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SaveMessage(req.OpenId, memory.RoleAssistant, reply); err != nil {
		return nil, err
	}
	return &agentv1.ReplyResponse{ReplyText: reply}, nil
}

func (s *Server) RunDailyMaintenance(ctx context.Context, req *agentv1.MaintenanceRequest) (*agentv1.MaintenanceResult, error) {
	if req.TargetDate == "" {
		return nil, fmt.Errorf("target_date is required")
	}
	targetDate, err := time.ParseInLocation("2006-01-02", req.TargetDate, time.Local)
	if err != nil {
		return nil, err
	}

	ids, err := s.repo.ActiveOpenIDsForDate(targetDate)
	if err != nil {
		return nil, err
	}
	greet := make([]string, 0, len(ids))

	for _, id := range ids {
		msgs, err := s.repo.MessagesForDate(id, targetDate)
		if err != nil {
			s.log.Error("maintenance: load messages failed", "open_id", id, "err", err)
			continue
		}
		summary, err := s.sum.Summarize(ctx, toTurns(msgs))
		if err != nil {
			s.log.Error("maintenance: summarize failed", "open_id", id, "err", err)
		} else if summary != "" {
			if err := s.repo.SaveSummary(id, targetDate, summary); err != nil {
				s.log.Error("maintenance: save summary failed", "open_id", id, "err", err)
			}
		}
		if err := s.repo.DeleteMessagesForDate(id, targetDate); err != nil {
			s.log.Error("maintenance: delete messages failed", "open_id", id, "err", err)
		}
		greet = append(greet, id)
	}

	if err := s.repo.PurgeSummariesOlderThan(targetDate.AddDate(0, 0, -7)); err != nil {
		s.log.Error("maintenance: purge old summaries failed", "err", err)
	}
	return &agentv1.MaintenanceResult{GreetOpenIds: greet}, nil
}
