package gateway

import (
	"context"
	"sync"
)

type MessageDeduper interface {
	SeenOrAdd(ctx context.Context, id string) (bool, error)
}

type RateLimiter interface {
	Allow(ctx context.Context, openID string) (bool, error)
}

type MsgDeduper struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func NewMsgDeduper() *MsgDeduper {
	return &MsgDeduper{seen: map[string]struct{}{}}
}

func (d *MsgDeduper) SeenOrAdd(_ context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[id]; ok {
		return true, nil
	}
	d.seen[id] = struct{}{}
	return false, nil
}
