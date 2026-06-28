package gateway

import (
	"context"
	"testing"

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

func TestPushTextRefreshesExpiredTokenOnce(t *testing.T) {
	tokens := &fakeTokens{token: "OLD"}
	push := &fakePusher{failOnce: true}

	err := pushTextWithTokenRefresh(context.Background(), tokens, push, "u1", "hi")
	require.NoError(t, err)
	require.Equal(t, "REFRESHED:hi", push.sent["u1"])
}
