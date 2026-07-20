package conversation

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newRedisStoreTest(t *testing.T) (*miniredis.Miniredis, *RedisStore) {
	t.Helper()
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})
	store := NewRedisStore(client, "test:", 72*time.Hour)
	return s, store
}

func TestRedisStoreAddTurnStoresTurnAndActiveIdentityWithTTL(t *testing.T) {
	s, store := newRedisStoreTest(t)
	store.now = func() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, beijingLocation) }
	identity := Identity{Channel: "api", ExternalID: "u1"}

	turn, err := store.AddTurn(context.Background(), identity, RoleUser, "你好")
	require.NoError(t, err)
	require.NotEmpty(t, turn.TurnID)
	require.Equal(t, RoleUser, turn.Role)
	require.Equal(t, "你好", turn.Content)

	turns, err := store.TurnsForDate(context.Background(), identity, time.Date(2026, 7, 9, 0, 0, 0, 0, beijingLocation))
	require.NoError(t, err)
	require.Len(t, turns, 1)
	require.Equal(t, turn.TurnID, turns[0].TurnID)

	require.True(t, s.Exists("test:conversation:api:u1:2026-07-09"))
	require.True(t, s.Exists("test:conversation:active:2026-07-09"))
	require.Equal(t, 72*time.Hour, s.TTL("test:conversation:api:u1:2026-07-09"))
	require.Equal(t, 72*time.Hour, s.TTL("test:conversation:active:2026-07-09"))

	active, err := store.ActiveIdentities(context.Background(), time.Date(2026, 7, 9, 0, 0, 0, 0, beijingLocation))
	require.NoError(t, err)
	require.ElementsMatch(t, []Identity{identity}, active)
}

func TestRedisStoreRecentTurnsReturnsChronologicalSuffix(t *testing.T) {
	_, store := newRedisStoreTest(t)
	store.now = func() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, beijingLocation) }
	identity := Identity{Channel: "api", ExternalID: "u1"}
	for _, content := range []string{"一", "二", "三"} {
		_, err := store.AddTurn(context.Background(), identity, RoleUser, content)
		require.NoError(t, err)
	}

	turns, err := store.RecentTurns(context.Background(), identity, 2)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	require.Equal(t, "二", turns[0].Content)
	require.Equal(t, "三", turns[1].Content)
}

func TestRedisStoreUsesBeijingDateKeys(t *testing.T) {
	s, store := newRedisStoreTest(t)
	store.now = func() time.Time {
		return time.Date(2026, 7, 8, 16, 30, 0, 0, time.UTC) // 2026-07-09 00:30 Beijing.
	}
	identity := Identity{Channel: "wechat", ExternalID: "open1"}

	_, err := store.AddTurn(context.Background(), identity, RoleUser, "跨日")
	require.NoError(t, err)

	require.True(t, s.Exists("test:conversation:wechat:open1:2026-07-09"))
	require.False(t, s.Exists("test:conversation:wechat:open1:2026-07-08"))
}

func TestRedisStoreClearTodayDeletesTurnsAndRemovesActiveIdentity(t *testing.T) {
	s, store := newRedisStoreTest(t)
	store.now = func() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, beijingLocation) }
	identity := Identity{Channel: "api", ExternalID: "u1"}
	other := Identity{Channel: "api", ExternalID: "u2"}
	_, err := store.AddTurn(context.Background(), identity, RoleUser, "删掉")
	require.NoError(t, err)
	_, err = store.AddTurn(context.Background(), other, RoleUser, "保留")
	require.NoError(t, err)

	require.NoError(t, store.ClearToday(context.Background(), identity))
	require.False(t, s.Exists("test:conversation:api:u1:2026-07-09"))
	require.True(t, s.Exists("test:conversation:api:u2:2026-07-09"))

	active, err := store.ActiveIdentities(context.Background(), time.Date(2026, 7, 9, 0, 0, 0, 0, beijingLocation))
	require.NoError(t, err)
	require.ElementsMatch(t, []Identity{other}, active)
}

func TestRedisStoreClearDateIsIdempotent(t *testing.T) {
	_, store := newRedisStoreTest(t)
	identity := Identity{Channel: "api", ExternalID: "u1"}
	date := time.Date(2026, 7, 9, 0, 0, 0, 0, beijingLocation)

	require.NoError(t, store.ClearDate(context.Background(), identity, date))
	require.NoError(t, store.ClearDate(context.Background(), identity, date))
}

func TestStaleBatchConvergesWithoutGeneratingSuffix(t *testing.T) {
	mini, store := newRedisStoreTest(t)
	store.now = func() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, beijingLocation) }
	scope := Scope{Identity: Identity{Channel: "api", ExternalID: "u1"}, ConversationID: "sos-group"}
	batch, state, _, err := store.BeginBatch(context.Background(), scope, "c0a80101-0000-4000-8000-000000000530", "未完成")
	require.NoError(t, err)
	require.Equal(t, BeginStarted, state)
	require.Equal(t, BatchGenerating, batch.Status)

	mini.FastForward(46 * time.Second)
	retried, state, _, err := store.BeginBatch(context.Background(), scope, "c0a80101-0000-4000-8000-000000000530", "未完成")
	require.NoError(t, err)
	require.Equal(t, BeginExisting, state)
	require.Equal(t, BatchFailed, retried.Status)
	require.Equal(t, "generation_interrupted", retried.InterruptionCode)
	require.NotNil(t, retried.PlannedSpeakerIDs)
	require.NotNil(t, retried.CharacterMessages)
	retriedAgain, state, _, err := store.BeginBatch(context.Background(), scope, "c0a80101-0000-4000-8000-000000000530", "未完成")
	require.NoError(t, err)
	require.Equal(t, BeginExisting, state)
	require.Equal(t, BatchFailed, retriedAgain.Status)
	require.NotNil(t, retriedAgain.PlannedSpeakerIDs)
	require.NotNil(t, retriedAgain.CharacterMessages)
	messages, err := store.Messages(context.Background(), scope)
	require.NoError(t, err)
	require.Len(t, messages, 1)
}

func TestBeginBatchInitializesJSONCollections(t *testing.T) {
	_, store := newRedisStoreTest(t)
	store.now = func() time.Time { return time.Date(2026, 7, 9, 12, 0, 0, 0, beijingLocation) }
	scope := Scope{Identity: Identity{Channel: "api", ExternalID: "u1"}, ConversationID: "sos-group"}

	batch, state, _, err := store.BeginBatch(context.Background(), scope, "request-arrays", "开始讨论")
	require.NoError(t, err)
	require.Equal(t, BeginStarted, state)
	require.NotNil(t, batch.PlannedSpeakerIDs)
	require.NotNil(t, batch.CharacterMessages)
	require.Empty(t, batch.PlannedSpeakerIDs)
	require.Empty(t, batch.CharacterMessages)
}

func TestRealRedisBeginBatchPreservesEmptyCollections(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR is not set")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	require.NoError(t, client.Ping(context.Background()).Err())

	prefix := "test:conversation:" + uuid.NewString() + ":"
	t.Cleanup(func() {
		var cursor uint64
		for {
			keys, next, err := client.Scan(context.Background(), cursor, prefix+"*", 100).Result()
			require.NoError(t, err)
			if len(keys) > 0 {
				require.NoError(t, client.Del(context.Background(), keys...).Err())
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	})

	store := NewRedisStore(client, prefix, time.Minute)
	store.now = func() time.Time { return time.Date(2026, 7, 17, 19, 0, 0, 0, beijingLocation) }
	scope := Scope{Identity: Identity{Channel: "api", ExternalID: "real-redis"}, ConversationID: "sos-group"}

	batch, state, _, err := store.BeginBatch(context.Background(), scope, uuid.NewString(), "开始讨论")
	require.NoError(t, err)
	require.Equal(t, BeginStarted, state)
	require.NotNil(t, batch.PlannedSpeakerIDs)
	require.NotNil(t, batch.CharacterMessages)
	require.Empty(t, batch.PlannedSpeakerIDs)
	require.Empty(t, batch.CharacterMessages)
}

func TestActiveScopesAndClearAreConversationScoped(t *testing.T) {
	_, store := newRedisStoreTest(t)
	day := time.Date(2026, 7, 9, 12, 0, 0, 0, beijingLocation)
	store.now = func() time.Time { return day }
	group := Scope{Identity: Identity{Channel: "api", ExternalID: "u1"}, ConversationID: "sos-group"}
	direct := Scope{Identity: Identity{Channel: "api", ExternalID: "u1"}, ConversationID: "direct-yuki"}
	_, _, _, err := store.BeginBatch(context.Background(), group, "request-group", "群聊")
	require.NoError(t, err)
	_, _, _, err = store.BeginBatch(context.Background(), direct, "request-direct", "单聊")
	require.NoError(t, err)
	_, err = store.AddTurn(context.Background(), Identity{Channel: "wechat", ExternalID: "legacy"}, RoleUser, "旧微信")
	require.NoError(t, err)

	active, err := store.ActiveScopes(context.Background(), day)
	require.NoError(t, err)
	require.ElementsMatch(t, []Scope{
		group, direct,
		{Identity: Identity{Channel: "wechat", ExternalID: "legacy"}, ConversationID: DefaultConversationID},
	}, active)

	require.NoError(t, store.ClearScopeDate(context.Background(), direct, day))
	groupMessages, err := store.MessagesForDateScope(context.Background(), group, day)
	require.NoError(t, err)
	require.Len(t, groupMessages, 1)
	directMessages, err := store.MessagesForDateScope(context.Background(), direct, day)
	require.NoError(t, err)
	require.Empty(t, directMessages)
}
