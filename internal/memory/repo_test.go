package memory

import (
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"companion-ai/internal/testdb"
)

func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	db := testdb.Open(t)
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

func TestNewRepoDoesNotCreateTables(t *testing.T) {
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN is required for PostgreSQL-backed tests")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	schema := fmt.Sprintf("repo_nomigrate_%d", time.Now().UnixNano())
	require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		_ = db.Exec("DROP SCHEMA " + schema + " CASCADE").Error
	})
	require.NoError(t, db.Exec("SET search_path TO "+schema).Error)

	_, err = NewRepo(db)
	require.NoError(t, err)

	var table sql.NullString
	require.NoError(t, db.Raw("SELECT to_regclass('messages')::text").Scan(&table).Error)
	require.False(t, table.Valid, "NewRepo must not create database tables; run SQL migrations instead")
}
