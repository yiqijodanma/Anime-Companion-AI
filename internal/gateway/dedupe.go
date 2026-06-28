package gateway

import "sync"

type MsgDeduper struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func NewMsgDeduper() *MsgDeduper {
	return &MsgDeduper{seen: map[string]struct{}{}}
}

func (d *MsgDeduper) SeenOrAdd(id string) bool {
	if id == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[id]; ok {
		return true
	}
	d.seen[id] = struct{}{}
	return false
}
