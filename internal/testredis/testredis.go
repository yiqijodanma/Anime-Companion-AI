package testredis

import (
	"context"
	"os"
	"testing"

	"github.com/redis/go-redis/v9"
)

func Open(t testing.TB, db int) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR is required for Redis script-backed tests")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("connect redis test server: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
