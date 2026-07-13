package auth

import (
	"context"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type discardMailer struct{}

func (discardMailer) SendVerification(context.Context, string, string, string) error { return nil }

func TestPasswordHashRoundTrip(t *testing.T) {
	encoded, err := hashPassword("correct horse battery staple")
	require.NoError(t, err)
	require.True(t, verifyPassword(encoded, "correct horse battery staple"))
	require.False(t, verifyPassword(encoded, "wrong password"))
}

func TestPasswordPolicy(t *testing.T) {
	_, err := hashPassword("short")
	require.Error(t, err)
	_, err = hashPassword(strings.Repeat("x", 129))
	require.Error(t, err)
}

func TestCaptchaIsSingleUse(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	service, err := NewService(&gorm.DB{}, client, discardMailer{}, Config{Pepper: "test", CaptchaTTL: time.Minute})
	require.NoError(t, err)

	id, image, err := service.NewCaptcha(context.Background())
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(image, "data:image/svg+xml;base64,"))
	require.NoError(t, err)
	match := regexp.MustCompile(`letter-spacing="8">([A-Z2-9]{4})</text>`).FindSubmatch(raw)
	require.Len(t, match, 2)

	require.NoError(t, service.consumeCaptcha(context.Background(), id, string(match[1])))
	require.ErrorIs(t, service.consumeCaptcha(context.Background(), id, string(match[1])), ErrInvalidCaptcha)
}

func TestNormalizeEmail(t *testing.T) {
	email, err := normalizeEmail("  Member@Example.COM ")
	require.NoError(t, err)
	require.Equal(t, "member@example.com", email)
	_, err = normalizeEmail("not-an-email")
	require.Error(t, err)
}
