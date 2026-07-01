package testdb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func Open(t testing.TB) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN is required for PostgreSQL-backed tests")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get postgres test db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if err := db.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Exec("DROP SCHEMA " + schema + " CASCADE").Error
	})
	if err := db.Exec("SET search_path TO " + schema).Error; err != nil {
		t.Fatalf("set test schema search_path: %v", err)
	}
	ApplyMigrations(t, sqlDB)
	Truncate(t, sqlDB)
	return db
}

func ApplyMigrations(t testing.TB, db *sql.DB) {
	t.Helper()
	root := repoRoot(t)
	upSQL, err := os.ReadFile(filepath.Join(root, "db", "migrations", "000001_init.up.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(string(upSQL)); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
}

func Truncate(t testing.TB, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("TRUNCATE TABLE messages, memory_summaries RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate test tables: %v", err)
	}
}

func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found while walking from %s", dir)
		}
		dir = parent
	}
}
