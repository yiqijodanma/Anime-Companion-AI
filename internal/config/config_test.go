package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadGatewayReadsEnv(t *testing.T) {
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
	t.Setenv("COOKIE_SECURE", "true")

	cfg, err := LoadGateway()
	require.NoError(t, err)
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
	require.True(t, cfg.CookieSecure)
}

func TestLoadGatewayDefaultsRedisAddr(t *testing.T) {
	t.Setenv("WECHAT_TOKEN", "tok")
	t.Setenv("WECHAT_APPID", "appid")
	t.Setenv("WECHAT_APPSECRET", "secret")
	t.Setenv("PG_DSN", "postgres://localhost/companion")
	t.Setenv("AUTH_PEPPER", "test-pepper")

	cfg, err := LoadGateway()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:6379", cfg.RedisAddr)
}

func TestLoadGatewayMissingRequiredFails(t *testing.T) {
	t.Setenv("WECHAT_TOKEN", "")
	t.Setenv("WECHAT_APPID", "appid")
	t.Setenv("WECHAT_APPSECRET", "secret")

	_, err := LoadGateway()
	require.Error(t, err)
}

func TestLoadAgentReadsEnv(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "dskey")
	t.Setenv("DEEPSEEK_MODEL", "deepseek-v4-flash")
	t.Setenv("PG_DSN", "postgres://localhost/db")
	t.Setenv("AGENT_GRPC_ADDR", "127.0.0.1:9090")
	t.Setenv("REDIS_ADDR", "redis:6380")

	cfg, err := LoadAgent()
	require.NoError(t, err)
	require.Equal(t, "dskey", cfg.DeepSeekAPIKey)
	require.Equal(t, "deepseek-v4-flash", cfg.DeepSeekModel)
	require.Equal(t, "postgres://localhost/db", cfg.PgDSN)
	require.Equal(t, "127.0.0.1:9090", cfg.AgentGRPCAddr)
	require.Equal(t, "redis:6380", cfg.RedisAddr)
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
