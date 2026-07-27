package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type unavailableMailer struct{}

func (unavailableMailer) SendVerification(context.Context, string, string, string) error {
	return errors.New("provider detail must stay private")
}

func TestCreatePendingClassifiesMailDeliveryFailure(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	service, err := NewService(&gorm.DB{}, client, unavailableMailer{}, Config{Pepper: "test-pepper"})
	require.NoError(t, err)

	err = service.createPending(context.Background(), "register", "member@example.com", "hash")
	require.ErrorIs(t, err, ErrMailUnavailable)
	require.NotContains(t, err.Error(), "provider detail")
	require.Empty(t, server.Keys())
}
