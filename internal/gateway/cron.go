package gateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"companion-ai/internal/persona"
	"companion-ai/internal/wechat"
)

type Pusher interface {
	SendText(ctx context.Context, token, openID, text string) error
}

type TokenSource interface {
	Get(ctx context.Context) (string, error)
	Refresh(ctx context.Context) (string, error)
}

func pushTextWithTokenRefresh(ctx context.Context, tokens TokenSource, push Pusher, openID, text string) error {
	token, err := tokens.Get(ctx)
	if err != nil {
		return err
	}
	err = push.SendText(ctx, token, openID, text)
	if !wechat.IsTokenExpiredError(err) {
		return err
	}
	token, err = tokens.Refresh(ctx)
	if err != nil {
		return err
	}
	return push.SendText(ctx, token, openID, text)
}

func RunMaintenanceForDate(ctx context.Context, targetDate string, agent AgentCaller, tokens TokenSource, push Pusher, log *slog.Logger) {
	ids, err := agent.RunDailyMaintenance(ctx, targetDate)
	if err != nil {
		log.Error("daily maintenance rpc failed", "err", err)
		return
	}
	for _, id := range ids {
		if err := pushTextWithTokenRefresh(ctx, tokens, push, id, persona.GoodNight); err != nil {
			log.Error("goodnight push failed", "open_id", id, "err", err)
		}
	}
}

func RunMaintenance(ctx context.Context, agent AgentCaller, tokens TokenSource, push Pusher, log *slog.Logger) {
	targetDate := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	RunMaintenanceForDate(ctx, targetDate, agent, tokens, push, log)
}

func StartCron(agent AgentCaller, tokens TokenSource, push Pusher, log *slog.Logger) *cron.Cron {
	c := cron.New()
	_, _ = c.AddFunc("0 0 * * *", func() {
		RunMaintenance(context.Background(), agent, tokens, push, log)
	})
	c.Start()
	return c
}
