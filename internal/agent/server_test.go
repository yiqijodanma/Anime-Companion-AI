package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
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
