package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"companion-ai/internal/conversation"
)

func TestSendMarksHistoryFailureAsDefinitelyNotStarted(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := conversation.NewRedisStore(client, "test:not-started:", 72*time.Hour)
	app := NewApplication(store, nil, nil)
	server.Close()

	_, err := app.Send(context.Background(), SendCommand{
		Scope: Scope{
			Owner:          Owner{Channel: "api", ID: "user"},
			ConversationID: "direct-haruhi",
		},
		Content:         "hello",
		ClientRequestID: uuid.NewString(),
	})
	require.ErrorIs(t, err, ErrNotStarted)
}
