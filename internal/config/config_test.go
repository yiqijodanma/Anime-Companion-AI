package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadGatewayReadsEnv(t *testing.T) {
	t.Setenv("WECHAT_ENABLED", "true")
	t.Setenv("WECHAT_TOKEN", "tok")
	t.Setenv("WECHAT_APPID", "appid")
	t.Setenv("WECHAT_APPSECRET", "secret")
	t.Setenv("AGENT_GRPC_ADDR", "127.0.0.1:9090")
	t.Setenv("GATEWAY_HTTP_ADDR", ":8080")
	t.Setenv("REDIS_ADDR", "redis:6380")
	t.Setenv("PG_DSN", "postgres://localhost/companion")
	t.Setenv("AUTH_PEPPER", "test-pepper")
	t.Setenv("SMTP_HOST", "mailpit")
	t.Setenv("SMTP_PORT", "1025")
	t.Setenv("SMTP_IMPLICIT_TLS", "true")
	t.Setenv("SMTP_USERNAME", "sender@example.com")
	t.Setenv("SMTP_PASSWORD", "smtp-password")
	t.Setenv("SMTP_FROM", "SOS 团 <sender@example.com>")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("DAILY_QUOTA_LIMIT", "37")
	t.Setenv("QUOTA_TIME_ZONE", "Asia/Shanghai")
	t.Setenv("RELEASE_ID", "release-20260723")
	t.Setenv("BACKEND_COMMIT", "backend-sha")
	t.Setenv("FRONTEND_COMMIT", "frontend-sha")

	cfg, err := LoadGateway()
	require.NoError(t, err)
	require.True(t, cfg.WechatEnabled)
	require.Equal(t, "tok", cfg.WechatToken)
	require.Equal(t, "appid", cfg.WechatAppID)
	require.Equal(t, "secret", cfg.WechatAppSecret)
	require.Equal(t, "127.0.0.1:9090", cfg.AgentGRPCAddr)
	require.Equal(t, ":8080", cfg.GatewayHTTPAddr)
	require.Equal(t, "redis:6380", cfg.RedisAddr)
	require.Equal(t, "postgres://localhost/companion", cfg.PgDSN)
	require.Equal(t, "test-pepper", cfg.AuthPepper)
	require.Equal(t, "mailpit", cfg.SMTPHost)
	require.Equal(t, "1025", cfg.SMTPPort)
	require.True(t, cfg.SMTPImplicitTLS)
	require.True(t, cfg.CookieSecure)
	require.Equal(t, 37, cfg.DailyQuotaLimit)
	require.Equal(t, "Asia/Shanghai", cfg.QuotaTimeZone)
	require.Equal(t, "release-20260723", cfg.ReleaseID)
	require.Equal(t, "backend-sha", cfg.BackendCommit)
	require.Equal(t, "frontend-sha", cfg.FrontendCommit)
}

func TestLoadGatewayDefaultsRedisAddr(t *testing.T) {
	t.Setenv("PG_DSN", "postgres://localhost/companion")
	t.Setenv("AUTH_PEPPER", "test-pepper")

	cfg, err := LoadGateway()
	require.NoError(t, err)
	require.False(t, cfg.WechatEnabled)
	require.Equal(t, "127.0.0.1:6379", cfg.RedisAddr)
	require.False(t, cfg.SMTPImplicitTLS)
	require.Equal(t, 20, cfg.DailyQuotaLimit)
	require.Equal(t, "Asia/Shanghai", cfg.QuotaTimeZone)
}

func TestLoadGatewayRejectsInvalidQuotaConfiguration(t *testing.T) {
	t.Setenv("PG_DSN", "postgres://localhost/companion")
	t.Setenv("AUTH_PEPPER", "test-pepper")
	t.Setenv("DAILY_QUOTA_LIMIT", "zero")
	_, err := LoadGateway()
	require.ErrorContains(t, err, "DAILY_QUOTA_LIMIT")

	t.Setenv("DAILY_QUOTA_LIMIT", "20")
	t.Setenv("QUOTA_TIME_ZONE", "UTC")
	_, err = LoadGateway()
	require.ErrorContains(t, err, "QUOTA_TIME_ZONE")
}

func TestLoadGatewayRequiresMatchingSenderForImplicitTLS(t *testing.T) {
	t.Setenv("PG_DSN", "postgres://localhost/companion")
	t.Setenv("AUTH_PEPPER", "test-pepper")
	t.Setenv("SMTP_IMPLICIT_TLS", "true")
	t.Setenv("SMTP_USERNAME", "sender@example.com")
	t.Setenv("SMTP_PASSWORD", "smtp-password")
	t.Setenv("SMTP_FROM", "Other <other@example.com>")

	_, err := LoadGateway()
	require.ErrorContains(t, err, "SMTP_USERNAME must match")
}

func TestLoadGatewayWebOnlyDoesNotRequireWechatCredentials(t *testing.T) {
	t.Setenv("WECHAT_ENABLED", "false")
	t.Setenv("WECHAT_TOKEN", "")
	t.Setenv("WECHAT_APPID", "")
	t.Setenv("WECHAT_APPSECRET", "")
	t.Setenv("PG_DSN", "postgres://localhost/companion")
	t.Setenv("AUTH_PEPPER", "test-pepper")

	cfg, err := LoadGateway()
	require.NoError(t, err)
	require.False(t, cfg.WechatEnabled)
}

func TestLoadGatewayWechatModeRequiresCredentials(t *testing.T) {
	t.Setenv("WECHAT_ENABLED", "true")
	t.Setenv("WECHAT_TOKEN", "")
	t.Setenv("WECHAT_APPID", "appid")
	t.Setenv("WECHAT_APPSECRET", "secret")
	t.Setenv("PG_DSN", "postgres://localhost/companion")
	t.Setenv("AUTH_PEPPER", "test-pepper")

	_, err := LoadGateway()
	require.ErrorContains(t, err, "WECHAT_TOKEN")
}

func TestLoadAgentReadsEnv(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "dskey")
	t.Setenv("DEEPSEEK_MODEL", "deepseek-v4-flash")
	t.Setenv("PG_DSN", "postgres://localhost/db")
	t.Setenv("AGENT_GRPC_ADDR", "127.0.0.1:9090")
	t.Setenv("REDIS_ADDR", "redis:6380")
	t.Setenv("RELEASE_ID", "release-20260723")
	t.Setenv("BACKEND_COMMIT", "backend-sha")

	cfg, err := LoadAgent()
	require.NoError(t, err)
	require.Equal(t, "dskey", cfg.DeepSeekAPIKey)
	require.Equal(t, "deepseek-v4-flash", cfg.DeepSeekModel)
	require.Equal(t, "postgres://localhost/db", cfg.PgDSN)
	require.Equal(t, "127.0.0.1:9090", cfg.AgentGRPCAddr)
	require.Equal(t, "redis:6380", cfg.RedisAddr)
	require.Equal(t, "release-20260723", cfg.ReleaseID)
	require.Equal(t, "backend-sha", cfg.BackendCommit)
}

func TestLoadAgentDefaultsRedisAddr(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "dskey")
	t.Setenv("PG_DSN", "postgres://localhost/db")

	cfg, err := LoadAgent()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:6379", cfg.RedisAddr)
}

func TestLoadAgentMissingRequiredFails(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("PG_DSN", "postgres://localhost/db")

	_, err := LoadAgent()
	require.Error(t, err)
}
