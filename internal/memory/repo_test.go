package memory

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	repo, err := NewRepo(db)
	require.NoError(t, err)
	return repo
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	out, err := time.ParseInLocation("2006-01-02 15:04", value, time.Local)
	require.NoError(t, err)
	return out
}

func TestSaveAndTodayMessages(t *testing.T) {
	repo := newTestRepo(t)
	require.NoError(t, repo.SaveMessage("u1", RoleUser, "你好"))
	require.NoError(t, repo.SaveMessage("u1", RoleAssistant, "哼，是你啊"))
	require.NoError(t, repo.SaveMessage("u2", RoleUser, "在吗"))

	msgs, err := repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "你好", msgs[0].Content)
	require.Equal(t, RoleAssistant, msgs[1].Role)
}

func TestMessagesForDateUsesRequestedDayWindow(t *testing.T) {
	repo := newTestRepo(t)
	target := mustTime(t, "2026-06-27 00:00")
	require.NoError(t, repo.DB().Create(&Message{OpenID: "u1", Role: RoleUser, Content: "昨天", CreatedAt: mustTime(t, "2026-06-27 23:59")}).Error)
	require.NoError(t, repo.DB().Create(&Message{OpenID: "u1", Role: RoleUser, Content: "今天", CreatedAt: mustTime(t, "2026-06-28 00:00")}).Error)

	msgs, err := repo.MessagesForDate("u1", target)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, "昨天", msgs[0].Content)
}

func TestActiveOpenIDsForDate(t *testing.T) {
	repo := newTestRepo(t)
	target := mustTime(t, "2026-06-27 00:00")
	require.NoError(t, repo.DB().Create(&Message{OpenID: "u1", Role: RoleUser, Content: "a", CreatedAt: mustTime(t, "2026-06-27 12:00")}).Error)
	require.NoError(t, repo.DB().Create(&Message{OpenID: "u2", Role: RoleUser, Content: "b", CreatedAt: mustTime(t, "2026-06-27 13:00")}).Error)
	require.NoError(t, repo.DB().Create(&Message{OpenID: "u3", Role: RoleUser, Content: "c", CreatedAt: mustTime(t, "2026-06-28 00:01")}).Error)

	ids, err := repo.ActiveOpenIDsForDate(target)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"u1", "u2"}, ids)
}

func TestDeleteMessagesForDate(t *testing.T) {
	repo := newTestRepo(t)
	target := mustTime(t, "2026-06-27 00:00")
	require.NoError(t, repo.DB().Create(&Message{OpenID: "u1", Role: RoleUser, Content: "old", CreatedAt: mustTime(t, "2026-06-27 12:00")}).Error)
	require.NoError(t, repo.DB().Create(&Message{OpenID: "u1", Role: RoleUser, Content: "new", CreatedAt: mustTime(t, "2026-06-28 12:00")}).Error)

	require.NoError(t, repo.DeleteMessagesForDate("u1", target))

	msgs, err := repo.MessagesForDate("u1", target)
	require.NoError(t, err)
	require.Empty(t, msgs)
	today, err := repo.MessagesForDate("u1", mustTime(t, "2026-06-28 00:00"))
	require.NoError(t, err)
	require.Len(t, today, 1)
}

func TestRecentSummariesWindow(t *testing.T) {
	repo := newTestRepo(t)
	now := time.Now()
	require.NoError(t, repo.SaveSummary("u1", now.AddDate(0, 0, -1), "昨天聊了社团"))
	require.NoError(t, repo.SaveSummary("u1", now.AddDate(0, 0, -10), "十天前"))

	sums, err := repo.RecentSummaries("u1")
	require.NoError(t, err)
	require.Len(t, sums, 1)
	require.Equal(t, "昨天聊了社团", sums[0].Content)
}

func TestPurgeSummariesOlderThan(t *testing.T) {
	repo := newTestRepo(t)
	now := time.Now()
	require.NoError(t, repo.SaveSummary("u1", now.AddDate(0, 0, -2), "近的"))
	require.NoError(t, repo.SaveSummary("u1", now.AddDate(0, 0, -9), "旧的"))
	require.NoError(t, repo.PurgeSummariesOlderThan(now.AddDate(0, 0, -7)))

	var count int64
	require.NoError(t, repo.DB().Model(&MemorySummary{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}
