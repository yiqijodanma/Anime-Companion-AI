package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/chat"
	"companion-ai/internal/memory"
	"companion-ai/internal/summarize"
)

type fakeModel struct{ reply string }

func (f *fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(f.reply, nil), nil
}

func newTestServer(t *testing.T, reply string) (*Server, *memory.Repo) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	repo, err := memory.NewRepo(db)
	require.NoError(t, err)
	fm := &fakeModel{reply: reply}
	return NewServer(repo, chat.NewReplier(fm), summarize.NewSummarizer(fm)), repo
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	out, err := time.ParseInLocation("2006-01-02 15:04", value, time.Local)
	require.NoError(t, err)
	return out
}

func TestReplyPersistsBothMessages(t *testing.T) {
	srv, repo := newTestServer(t, "哼，知道了！")
	resp, err := srv.Reply(context.Background(), &agentv1.ReplyRequest{OpenId: "u1", Text: "你好"})
	require.NoError(t, err)
	require.Equal(t, "哼，知道了！", resp.ReplyText)

	msgs, err := repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, memory.RoleUser, msgs[0].Role)
	require.Equal(t, memory.RoleAssistant, msgs[1].Role)
	require.Equal(t, "哼，知道了！", msgs[1].Content)
}

func TestListConversationMessagesReturnsOnlyTodayMessagesForOpenID(t *testing.T) {
	srv, repo := newTestServer(t, "unused")
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)
	u1First := memory.Message{OpenID: "u1", Role: memory.RoleUser, Content: "今天第一条", CreatedAt: today.Add(time.Minute)}
	u1Second := memory.Message{OpenID: "u1", Role: memory.RoleAssistant, Content: "今天第二条", CreatedAt: today.Add(2 * time.Minute)}
	require.NoError(t, repo.DB().Create(&u1First).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u2", Role: memory.RoleUser, Content: "别人今天", CreatedAt: today.Add(time.Minute)}).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u1", Role: memory.RoleUser, Content: "昨天", CreatedAt: today.AddDate(0, 0, -1)}).Error)
	require.NoError(t, repo.DB().Create(&u1Second).Error)

	resp, err := srv.ListConversationMessages(context.Background(), &agentv1.ListConversationMessagesRequest{OpenId: "u1"})
	require.NoError(t, err)
	require.Len(t, resp.Messages, 2)
	require.Equal(t, uint64(u1First.ID), resp.Messages[0].Id)
	require.Equal(t, memory.RoleUser, resp.Messages[0].Role)
	require.Equal(t, "今天第一条", resp.Messages[0].Content)
	require.NotNil(t, resp.Messages[0].CreatedAt)
	require.Equal(t, uint64(u1Second.ID), resp.Messages[1].Id)
	require.Equal(t, memory.RoleAssistant, resp.Messages[1].Role)
	require.Equal(t, "今天第二条", resp.Messages[1].Content)
	require.NotNil(t, resp.Messages[1].CreatedAt)
}

func TestDeleteConversationMessagesDeletesOnlyTodayMessagesForOpenIDAndIsIdempotent(t *testing.T) {
	srv, repo := newTestServer(t, "unused")
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)
	yesterday := today.AddDate(0, 0, -1)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u1", Role: memory.RoleUser, Content: "删掉", CreatedAt: today.Add(time.Minute)}).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u2", Role: memory.RoleUser, Content: "保留别人", CreatedAt: today.Add(time.Minute)}).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u1", Role: memory.RoleUser, Content: "保留昨天", CreatedAt: yesterday}).Error)

	resp, err := srv.DeleteConversationMessages(context.Background(), &agentv1.DeleteConversationMessagesRequest{OpenId: "u1"})
	require.NoError(t, err)
	require.NotNil(t, resp)
	resp, err = srv.DeleteConversationMessages(context.Background(), &agentv1.DeleteConversationMessagesRequest{OpenId: "u1"})
	require.NoError(t, err)
	require.NotNil(t, resp)

	u1Today, err := repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Empty(t, u1Today)
	u2Today, err := repo.TodayMessages("u2")
	require.NoError(t, err)
	require.Len(t, u2Today, 1)
	u1Yesterday, err := repo.MessagesForDate("u1", yesterday)
	require.NoError(t, err)
	require.Len(t, u1Yesterday, 1)
}

func TestConversationMessagesRequireOpenID(t *testing.T) {
	srv, _ := newTestServer(t, "unused")

	_, err := srv.ListConversationMessages(context.Background(), &agentv1.ListConversationMessagesRequest{})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.DeleteConversationMessages(context.Background(), &agentv1.DeleteConversationMessagesRequest{})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestRunDailyMaintenanceUsesTargetDate(t *testing.T) {
	srv, repo := newTestServer(t, "今天的摘要内容")
	target := mustTime(t, "2026-06-27 00:00")
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u1", Role: memory.RoleUser, Content: "我喜欢棒球", CreatedAt: mustTime(t, "2026-06-27 12:00")}).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u2", Role: memory.RoleUser, Content: "在吗", CreatedAt: mustTime(t, "2026-06-27 23:00")}).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u3", Role: memory.RoleUser, Content: "新一天", CreatedAt: mustTime(t, "2026-06-28 00:01")}).Error)

	res, err := srv.RunDailyMaintenance(context.Background(), &agentv1.MaintenanceRequest{TargetDate: "2026-06-27"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"u1", "u2"}, res.GreetOpenIds)

	msgs, err := repo.MessagesForDate("u1", target)
	require.NoError(t, err)
	require.Empty(t, msgs)
	nextDay, err := repo.MessagesForDate("u3", mustTime(t, "2026-06-28 00:00"))
	require.NoError(t, err)
	require.Len(t, nextDay, 1)

	sums, err := repo.RecentSummaries("u1")
	require.NoError(t, err)
	require.Len(t, sums, 1)
	require.Equal(t, "今天的摘要内容", sums[0].Content)
}
