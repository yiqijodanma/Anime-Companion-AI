package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func EnsureAdmin(ctx context.Context, db *gorm.DB, email, password string) (User, error) {
	if db == nil {
		return User{}, errors.New("database is required")
	}

	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return User{}, fmt.Errorf("normalize admin email: %w", err)
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return User{}, fmt.Errorf("hash admin password: %w", err)
	}
	id, err := randomHex(16)
	if err != nil {
		return User{}, fmt.Errorf("generate admin id: %w", err)
	}

	now := time.Now().UTC()
	seed := User{
		ID:           id,
		Email:        normalizedEmail,
		PasswordHash: passwordHash,
		IsAdmin:      true,
		VerifiedAt:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	var admin User
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "email"}},
			DoUpdates: clause.Assignments(map[string]any{
				"password_hash": passwordHash,
				"is_admin":      true,
				"verified_at":   now,
				"updated_at":    now,
			}),
		}).Create(&seed).Error; err != nil {
			return fmt.Errorf("upsert admin: %w", err)
		}
		if err := tx.Where("email = ?", normalizedEmail).First(&admin).Error; err != nil {
			return fmt.Errorf("read admin after upsert: %w", err)
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return admin, nil
}
