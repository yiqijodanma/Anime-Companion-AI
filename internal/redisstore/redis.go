package redisstore

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type MessageDeduper struct {
	client *redis.Client
	prefix string
	ttl    time.Duration
}

func NewMessageDeduper(client *redis.Client, prefix string, ttl time.Duration) *MessageDeduper {
	return &MessageDeduper{client: client, prefix: prefix, ttl: ttl}
}

func (d *MessageDeduper) SeenOrAdd(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	added, err := d.client.SetNX(ctx, d.prefix+id, "1", d.ttl).Result()
	if err != nil {
		return false, err
	}
	return !added, nil
}

type TokenCache struct {
	client *redis.Client
	key    string
}

func NewTokenCache(client *redis.Client, key string) *TokenCache {
	return &TokenCache{client: client, key: key}
}

func (c *TokenCache) Get(ctx context.Context) (string, bool, error) {
	token, err := c.client.Get(ctx, c.key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return token, true, nil
}

func (c *TokenCache) Set(ctx context.Context, token string, ttl time.Duration) error {
	return c.client.Set(ctx, c.key, token, ttl).Err()
}

func (c *TokenCache) Delete(ctx context.Context) error {
	return c.client.Del(ctx, c.key).Err()
}

type FixedWindowLimiter struct {
	client *redis.Client
	prefix string
	limit  int64
	window time.Duration
}

func NewFixedWindowLimiter(client *redis.Client, prefix string, limit int64, window time.Duration) *FixedWindowLimiter {
	return &FixedWindowLimiter{client: client, prefix: prefix, limit: limit, window: window}
}

func (l *FixedWindowLimiter) Allow(ctx context.Context, openID string) (bool, error) {
	if openID == "" {
		return true, nil
	}
	count, err := l.client.Incr(ctx, l.prefix+openID).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		if err := l.client.Expire(ctx, l.prefix+openID, l.window).Err(); err != nil {
			return false, err
		}
	}
	return count <= l.limit, nil
}
