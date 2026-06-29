package redisstore

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newRedisTestClient(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})
	return s, client
}

func TestMessageDeduperUsesSetNXWithTTL(t *testing.T) {
	s, client := newRedisTestClient(t)
	deduper := NewMessageDeduper(client, "wechat:msg:", time.Hour)

	seen, err := deduper.SeenOrAdd(context.Background(), "m1")
	require.NoError(t, err)
	require.False(t, seen)
	require.True(t, s.Exists("wechat:msg:m1"))

	seen, err = deduper.SeenOrAdd(context.Background(), "m1")
	require.NoError(t, err)
	require.True(t, seen)

	s.FastForward(time.Hour)
	seen, err = deduper.SeenOrAdd(context.Background(), "m1")
	require.NoError(t, err)
	require.False(t, seen)
}

func TestTokenCacheMissHitAndDelete(t *testing.T) {
	_, client := newRedisTestClient(t)
	cache := NewTokenCache(client, "wechat:access_token")

	token, ok, err := cache.Get(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, token)

	require.NoError(t, cache.Set(context.Background(), "TOKEN", time.Minute))

	token, ok, err = cache.Get(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "TOKEN", token)

	require.NoError(t, cache.Delete(context.Background()))
	token, ok, err = cache.Get(context.Background())
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, token)
}

func TestFixedWindowLimiterLimitsUntilWindowExpires(t *testing.T) {
	s, client := newRedisTestClient(t)
	limiter := NewFixedWindowLimiter(client, "gateway:rl:", 2, time.Minute)

	allowed, err := limiter.Allow(context.Background(), "u1")
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, err = limiter.Allow(context.Background(), "u1")
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, err = limiter.Allow(context.Background(), "u1")
	require.NoError(t, err)
	require.False(t, allowed)

	s.FastForward(time.Minute)
	allowed, err = limiter.Allow(context.Background(), "u1")
	require.NoError(t, err)
	require.True(t, allowed)
}
