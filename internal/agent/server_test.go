package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/chat"
	"companion-ai/internal/conversation"
	"companion-ai/internal/memory"
	"companion-ai/internal/summarize"
	"companion-ai/internal/testdb"
)

type fakeModel struct {
	reply string
	err   error
	seen  []*schema.Message
}

func (f *fakeModel) Generate(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.seen = append([]*schema.Message(nil), msgs...)
	if f.err != nil {
		return nil, f.err
	}
	return schema.AssistantMessage(f.reply, nil), nil
}

func newTestServer(t *testing.T, reply string) (*Server, *memory.Repo, *conversation.RedisStore) {
	t.Helper()
	db := testdb.Open(t)
	repo, err := memory.NewRepo(db)
	require.NoError(t, err)
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})
	conv := conversation.NewRedisStore(client, "test:", 72*time.Hour)
	fm := &fakeModel{reply: reply}
	return NewServer(repo, conv, chat.NewReplier(fm), summarize.NewSummarizer(fm)), repo, conv
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	out, err := time.ParseInLocation("2006-01-02 15:04", value, time.Local)
	require.NoError(t, err)
	return out
}

func TestReplyPersistsBothMessages(t *testing.T) {
	srv, repo, conv := newTestServer(t, "哼，知道了！")
	resp, err := srv.Reply(context.Background(), &agentv1.ReplyRequest{OpenId: "u1", Text: "你好"})
	require.NoError(t, err)
	require.Equal(t, "哼，知道了！", resp.ReplyText)

	msgs, err := repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Empty(t, msgs)

	turns, err := conv.RecentTurns(context.Background(), conversation.Identity{Channel: "wechat", ExternalID: "u1"}, 0)
	require.NoError(t, err)
	require.Len(t, turns, 2)
	require.Equal(t, memory.RoleUser, turns[0].Role)
	require.Equal(t, memory.RoleAssistant, turns[1].Role)
	require.Equal(t, "哼，知道了！", turns[1].Content)
}

func TestReplyModelFailureKeepsOnlyUserTurn(t *testing.T) {
	db := testdb.Open(t)
	repo, err := memory.NewRepo(db)
	require.NoError(t, err)
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	conv := conversation.NewRedisStore(client, "test:", 72*time.Hour)
	fm := &fakeModel{err: errors.New("model down")}
	srv := NewServer(repo, conv, chat.NewReplier(fm), summarize.NewSummarizer(fm))

	_, err = srv.Reply(context.Background(), &agentv1.ReplyRequest{OpenId: "u1", Text: "你好"})
	require.Error(t, err)

	turns, err := conv.RecentTurns(context.Background(), conversation.Identity{Channel: "wechat", ExternalID: "u1"}, 0)
	require.NoError(t, err)
	require.Len(t, turns, 1)
	require.Equal(t, memory.RoleUser, turns[0].Role)
	require.Equal(t, "你好", turns[0].Content)
}

func TestListConversationMessagesReturnsOnlyTodayMessagesForOpenID(t *testing.T) {
	srv, _, conv := newTestServer(t, "unused")
	identity := conversation.Identity{Channel: "wechat", ExternalID: "u1"}
	other := conversation.Identity{Channel: "wechat", ExternalID: "u2"}
	_, err := conv.AddTurn(context.Background(), identity, memory.RoleUser, "今天第一条")
	require.NoError(t, err)
	_, err = conv.AddTurn(context.Background(), other, memory.RoleUser, "别人今天")
	require.NoError(t, err)
	_, err = conv.AddTurn(context.Background(), identity, memory.RoleAssistant, "今天第二条")
	require.NoError(t, err)

	resp, err := srv.ListConversationMessages(context.Background(), &agentv1.ListConversationMessagesRequest{OpenId: "u1"})
	require.NoError(t, err)
	require.Len(t, resp.Messages, 2)
	require.Equal(t, memory.RoleUser, resp.Messages[0].Role)
	require.Equal(t, "今天第一条", resp.Messages[0].Content)
	require.NotNil(t, resp.Messages[0].CreatedAt)
	require.NotZero(t, resp.Messages[0].Id)
	require.Equal(t, memory.RoleAssistant, resp.Messages[1].Role)
	require.Equal(t, "今天第二条", resp.Messages[1].Content)
	require.NotNil(t, resp.Messages[1].CreatedAt)
	require.NotZero(t, resp.Messages[1].Id)
	require.NotEqual(t, resp.Messages[0].Id, resp.Messages[1].Id)
}

func TestDeleteConversationMessagesDeletesOnlyTodayMessagesForOpenIDAndIsIdempotent(t *testing.T) {
	srv, _, conv := newTestServer(t, "unused")
	identity := conversation.Identity{Channel: "wechat", ExternalID: "u1"}
	other := conversation.Identity{Channel: "wechat", ExternalID: "u2"}
	_, err := conv.AddTurn(context.Background(), identity, memory.RoleUser, "删掉")
	require.NoError(t, err)
	_, err = conv.AddTurn(context.Background(), other, memory.RoleUser, "保留别人")
	require.NoError(t, err)

	resp, err := srv.DeleteConversationMessages(context.Background(), &agentv1.DeleteConversationMessagesRequest{OpenId: "u1"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	resp, err = srv.DeleteConversationMessages(context.Background(), &agentv1.DeleteConversationMessagesRequest{OpenId: "u1"})
	require.NoError(t, err)
	require.NotNil(t, resp)

	u1Today, err := conv.RecentTurns(context.Background(), identity, 0)
	require.NoError(t, err)
	require.Empty(t, u1Today)
	u2Today, err := conv.RecentTurns(context.Background(), other, 0)
	require.NoError(t, err)
	require.Len(t, u2Today, 1)
}

func TestConversationMessagesRequireOpenID(t *testing.T) {
	srv, _, _ := newTestServer(t, "unused")

	_, err := srv.ListConversationMessages(context.Background(), &agentv1.ListConversationMessagesRequest{})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.DeleteConversationMessages(context.Background(), &agentv1.DeleteConversationMessagesRequest{})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestRunDailyMaintenanceUsesTargetDate(t *testing.T) {
	srv, repo, conv := newTestServer(t, "今天的摘要内容")
	target := mustTime(t, "2026-06-27 00:00")
	conv.SetClock(func() time.Time { return mustTime(t, "2026-06-27 12:00") })
	u1 := conversation.Identity{Channel: "wechat", ExternalID: "u1"}
	u2 := conversation.Identity{Channel: "wechat", ExternalID: "u2"}
	_, err := conv.AddTurn(context.Background(), u1, memory.RoleUser, "我喜欢棒球")
	require.NoError(t, err)
	_, err = conv.AddTurn(context.Background(), u2, memory.RoleUser, "在吗")
	require.NoError(t, err)

	res, err := srv.RunDailyMaintenance(context.Background(), &agentv1.MaintenanceRequest{TargetDate: "2026-06-27"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"u1", "u2"}, res.GreetOpenIds)

	msgs, err := repo.MessagesForDate("u1", target)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "我喜欢棒球", msgs[0].Content)
	turns, err := conv.TurnsForDate(context.Background(), u1, target)
	require.NoError(t, err)
	require.Empty(t, turns)

	var sums []memory.MemorySummary
	require.NoError(t, repo.DB().Where("channel = ? AND external_id = ? AND message_date = ?", "wechat", "u1", target.Format("2006-01-02")).Find(&sums).Error)
	require.Len(t, sums, 1)
	require.Equal(t, "今天的摘要内容", sums[0].Content)
}

func TestRunDailyMaintenanceArchivesRawTurnsWhenSummaryFails(t *testing.T) {
	db := testdb.Open(t)
	repo, err := memory.NewRepo(db)
	require.NoError(t, err)
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	conv := conversation.NewRedisStore(client, "test:", 72*time.Hour)
	conv.SetClock(func() time.Time { return mustTime(t, "2026-06-27 12:00") })
	fm := &fakeModel{err: errors.New("summarizer down")}
	srv := NewServer(repo, conv, chat.NewReplier(fm), summarize.NewSummarizer(fm))
	identity := conversation.Identity{Channel: "wechat", ExternalID: "u1"}
	target := mustTime(t, "2026-06-27 00:00")
	_, err = conv.AddTurn(context.Background(), identity, memory.RoleUser, "今天的原始消息")
	require.NoError(t, err)

	res, err := srv.RunDailyMaintenance(context.Background(), &agentv1.MaintenanceRequest{TargetDate: "2026-06-27"})
	require.NoError(t, err)
	require.Empty(t, res.GreetOpenIds)

	msgs, err := repo.MessagesForDate("u1", target)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "今天的原始消息", msgs[0].Content)
	sums, err := repo.RecentSummariesForIdentity("wechat", "u1")
	require.NoError(t, err)
	require.Empty(t, sums)
	turns, err := conv.TurnsForDate(context.Background(), identity, target)
	require.NoError(t, err)
	require.Len(t, turns, 1)
}

func TestRunDailyMaintenanceClearsEmptyActiveIdentity(t *testing.T) {
	db := testdb.Open(t)
	repo, err := memory.NewRepo(db)
	require.NoError(t, err)
	s := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: s.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	conv := conversation.NewRedisStore(client, "test:", 72*time.Hour)
	fm := &fakeModel{reply: "should not summarize"}
	srv := NewServer(repo, conv, chat.NewReplier(fm), summarize.NewSummarizer(fm))
	target := mustTime(t, "2026-06-27 00:00")
	require.NoError(t, client.SAdd(context.Background(), "test:conversation:active:2026-06-27", "wechat|u1").Err())

	res, err := srv.RunDailyMaintenance(context.Background(), &agentv1.MaintenanceRequest{TargetDate: "2026-06-27"})
	require.NoError(t, err)
	require.Empty(t, res.GreetOpenIds)
	require.Empty(t, fm.seen)

	active, err := conv.ActiveIdentities(context.Background(), target)
	require.NoError(t, err)
	require.Empty(t, active)
	sums, err := repo.RecentSummariesForIdentity("wechat", "u1")
	require.NoError(t, err)
	require.Empty(t, sums)
}
