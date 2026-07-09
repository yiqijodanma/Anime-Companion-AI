package testdb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestOpenAppliesAllMigrationsInOrder(t *testing.T) {
	db := Open(t)

	require.True(t, db.Migrator().HasColumn("messages", "channel"))
	require.True(t, db.Migrator().HasColumn("messages", "external_id"))
	require.True(t, db.Migrator().HasColumn("messages", "turn_id"))
	require.True(t, db.Migrator().HasColumn("messages", "message_date"))
	require.True(t, db.Migrator().HasColumn("messages", "archived_at"))
	require.True(t, db.Migrator().HasColumn("memory_summaries", "channel"))
	require.True(t, db.Migrator().HasColumn("memory_summaries", "external_id"))
	require.True(t, db.Migrator().HasColumn("memory_summaries", "message_date"))
	require.True(t, db.Migrator().HasColumn("memory_summaries", "archived_at"))
}

func TestConversationArchiveMigrationDedupesExistingSummaries(t *testing.T) {
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

	schema := uniqueSchemaName("migration_dedupe")
	require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		_ = db.Exec("DROP SCHEMA " + schema + " CASCADE").Error
	})
	require.NoError(t, db.Exec("SET search_path TO "+schema).Error)

	root := repoRoot(t)
	migration1 := mustReadMigration(t, root, "000001_init.up.sql")
	migration2 := mustReadMigration(t, root, "000002_conversation_archive.up.sql")
	require.NoError(t, db.Exec(migration1).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO memory_summaries (open_id, summary_date, content, created_at)
		VALUES
			('u1', '2026-06-27 12:00:00+08', '旧摘要', '2026-06-27 12:01:00+08'),
			('u1', '2026-06-27 13:00:00+08', '最新摘要', '2026-06-27 13:01:00+08')
	`).Error)

	require.NoError(t, db.Exec(migration2).Error)

	var count int64
	require.NoError(t, db.Table("memory_summaries").Where("channel = ? AND external_id = ? AND message_date = ?", "wechat", "u1", "2026-06-27").Count(&count).Error)
	require.Equal(t, int64(1), count)
	var content string
	require.NoError(t, db.Table("memory_summaries").Select("content").Where("channel = ? AND external_id = ?", "wechat", "u1").Scan(&content).Error)
	require.Equal(t, "最新摘要", content)
}

func mustReadMigration(t testing.TB, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "db", "migrations", name))
	require.NoError(t, err)
	return string(data)
}
