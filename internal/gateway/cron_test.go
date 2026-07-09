package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunMaintenanceForDateGreetsActiveUsers(t *testing.T) {
	agent := &fakeAgent{maintenance: []string{"u1", "u2"}}
	tokens := &fakeTokens{token: "TOK"}
	push := &fakePusher{}

	RunMaintenanceForDate(context.Background(), "2026-06-27", agent, tokens, push, slogDiscard())

	require.Equal(t, "2026-06-27", agent.lastDate)
	require.Equal(t, "TOK:晚安啦！", push.sent["u1"])
	require.Equal(t, "TOK:晚安啦！", push.sent["u2"])
}

func TestRunMaintenanceUsesPreviousBeijingDate(t *testing.T) {
	oldNow := maintenanceNow
	maintenanceNow = func() time.Time {
		return time.Date(2026, 7, 8, 16, 30, 0, 0, time.UTC)
	}
	t.Cleanup(func() { maintenanceNow = oldNow })
	agent := &fakeAgent{}
	tokens := &fakeTokens{token: "TOK"}
	push := &fakePusher{}

	RunMaintenance(context.Background(), agent, tokens, push, slogDiscard())

	require.Equal(t, "2026-07-08", agent.lastDate)
}

func TestPushTextRefreshesExpiredTokenOnce(t *testing.T) {
	tokens := &fakeTokens{token: "OLD"}
	push := &fakePusher{failOnce: true}

	err := pushTextWithTokenRefresh(context.Background(), tokens, push, "u1", "hi")
	require.NoError(t, err)
	require.Equal(t, "REFRESHED:hi", push.sent["u1"])
}
