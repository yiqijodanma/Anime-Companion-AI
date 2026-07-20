package gateway

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"companion-ai/internal/wechat"
)

type fakeAgent struct {
	mu          sync.Mutex
	reply       string
	replyCalls  int
	maintenance []string
	healthErr   error
	lastDate    string
	messages    []ConversationMessage
	listErr     error
	deleteErr   error
	lastChannel string
	lastID      string
	deleteCalls int
	spaces      []ConversationSpace
	batch       ResponseBatch
}

func (f *fakeAgent) ListConversationSpaces(_ context.Context, channel, externalID string) ([]ConversationSpace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastChannel = channel
	f.lastID = externalID
	return append([]ConversationSpace(nil), f.spaces...), nil
}

func (f *fakeAgent) SendConversationMessage(_ context.Context, channel, externalID, conversationID, content, clientRequestID string) (ResponseBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastChannel = channel
	f.lastID = externalID
	return f.batch, nil
}

func (f *fakeAgent) ListConversationMessages(_ context.Context, channel, externalID, conversationID string) ([]ConversationMessage, error) {
	return f.ListMessages(context.Background(), channel, externalID)
}

func (f *fakeAgent) DeleteConversationMessages(_ context.Context, channel, externalID, conversationID string) error {
	return f.DeleteMessages(context.Background(), channel, externalID)
}

func (f *fakeAgent) Reply(_ context.Context, channel, externalID, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replyCalls++
	f.lastChannel = channel
	f.lastID = externalID
	return f.reply, nil
}

func (f *fakeAgent) RunDailyMaintenance(_ context.Context, targetDate string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastDate = targetDate
	return f.maintenance, nil
}

func (f *fakeAgent) ListMessages(_ context.Context, channel, externalID string) ([]ConversationMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastChannel = channel
	f.lastID = externalID
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]ConversationMessage(nil), f.messages...), nil
}

func (f *fakeAgent) DeleteMessages(_ context.Context, channel, externalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastChannel = channel
	f.lastID = externalID
	f.deleteCalls++
	return f.deleteErr
}

func (f *fakeAgent) Check(context.Context) error {
	return f.healthErr
}

func (f *fakeAgent) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.replyCalls
}

type fakeTokens struct {
	token string
}

func (f *fakeTokens) Get(context.Context) (string, error) {
	return f.token, nil
}

func (f *fakeTokens) Refresh(context.Context) (string, error) {
	f.token = "REFRESHED"
	return f.token, nil
}

type fakeLimiter struct {
	mu         sync.Mutex
	allow      bool
	err        error
	calls      int
	lastOpenID string
}

func (f *fakeLimiter) Allow(_ context.Context, openID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastOpenID = openID
	if f.err != nil {
		return false, f.err
	}
	return f.allow, nil
}

type fakeDeduper struct {
	seen bool
	err  error
}

func (f *fakeDeduper) SeenOrAdd(context.Context, string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.seen, nil
}

type fakePusher struct {
	mu       sync.Mutex
	sent     map[string]string
	failOnce bool
}

func (p *fakePusher) SendText(_ context.Context, token, openID, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failOnce {
		p.failOnce = false
		return &wechat.APIError{ErrCode: 42001, ErrMsg: "expired"}
	}
	if p.sent == nil {
		p.sent = map[string]string{}
	}
	p.sent[openID] = token + ":" + text
	return nil
}

func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
