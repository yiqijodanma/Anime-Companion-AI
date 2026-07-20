package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"companion-ai/internal/testdb"
)

func TestEnsureAdminCreatesAndUpdatesOneVerifiedAdmin(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()

	created, err := EnsureAdmin(ctx, db, " Admin@Example.com ", "first-password")
	require.NoError(t, err)
	require.Equal(t, "admin@example.com", created.Email)
	require.True(t, created.IsAdmin)
	require.False(t, created.VerifiedAt.IsZero())
	require.True(t, verifyPassword(created.PasswordHash, "first-password"))

	updated, err := EnsureAdmin(ctx, db, "admin@example.com", "second-password")
	require.NoError(t, err)
	require.Equal(t, created.ID, updated.ID)
	require.True(t, updated.IsAdmin)
	require.False(t, verifyPassword(updated.PasswordHash, "first-password"))
	require.True(t, verifyPassword(updated.PasswordHash, "second-password"))

	var count int64
	require.NoError(t, db.Model(&User{}).Where("email = ?", "admin@example.com").Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestEnsureAdminRejectsMissingDatabase(t *testing.T) {
	_, err := EnsureAdmin(context.Background(), nil, "admin@example.com", "valid-password")
	require.EqualError(t, err, "database is required")
}
