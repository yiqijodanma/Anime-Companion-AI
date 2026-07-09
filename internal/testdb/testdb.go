package testdb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
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
	schema := uniqueSchemaName("test")
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

var schemaCounter uint64

func uniqueSchemaName(prefix string) string {
	return fmt.Sprintf("%s_%d_%d_%d", prefix, os.Getpid(), time.Now().UnixNano(), atomic.AddUint64(&schemaCounter, 1))
}

func ApplyMigrations(t testing.TB, db *sql.DB) {
	t.Helper()
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, "db", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no migrations found")
	}
	sort.Strings(paths)
	for _, path := range paths {
		upSQL, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", filepath.Base(path), err)
		}
		if _, err := db.Exec(string(upSQL)); err != nil {
			t.Fatalf("apply migration %s: %v", filepath.Base(path), err)
		}
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
