package conversation

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
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
