package main

import (
	"context"
	"log"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"companion-ai/internal/auth"
)

func main() {
	dsn := requiredEnv("PG_DSN")
	email := requiredEnv("ADMIN_EMAIL")
	password := requiredEnv("ADMIN_PASSWORD")

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("access postgres connection: %v", err)
	}
	defer sqlDB.Close()

	admin, err := auth.EnsureAdmin(context.Background(), db, email, password)
	if err != nil {
		log.Fatalf("ensure administrator: %v", err)
	}
	log.Printf("administrator ensured: %s", admin.Email)
}

func requiredEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}
