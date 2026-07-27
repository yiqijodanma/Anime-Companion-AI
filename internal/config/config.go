package config

import (
	"fmt"
	"net/mail"
	"os"
	"strconv"
	"strings"
)

type GatewayConfig struct {
	WechatEnabled   bool
	WechatToken     string
	WechatAppID     string
	WechatAppSecret string
	AgentGRPCAddr   string
	GatewayHTTPAddr string
	RedisAddr       string
	PgDSN           string
	AuthPepper      string
	SMTPHost        string
	SMTPPort        string
	SMTPUsername    string
	SMTPPassword    string
	SMTPFrom        string
	SMTPImplicitTLS bool
	CookieSecure    bool
	DailyQuotaLimit int
	QuotaTimeZone   string
	ReleaseID       string
	BackendCommit   string
	FrontendCommit  string
}

type AgentConfig struct {
	DeepSeekAPIKey string
	DeepSeekModel  string
	PgDSN          string
	AgentGRPCAddr  string
	RedisAddr      string
	ReleaseID      string
	BackendCommit  string
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func LoadGateway() (*GatewayConfig, error) {
	dailyQuotaLimit, err := strconv.Atoi(env("DAILY_QUOTA_LIMIT", "20"))
	if err != nil || dailyQuotaLimit <= 0 {
		return nil, fmt.Errorf("DAILY_QUOTA_LIMIT must be a positive integer")
	}
	quotaTimeZone := env("QUOTA_TIME_ZONE", "Asia/Shanghai")
	if quotaTimeZone != "Asia/Shanghai" {
		return nil, fmt.Errorf("QUOTA_TIME_ZONE must be Asia/Shanghai")
	}
	cfg := &GatewayConfig{
		WechatEnabled:   envBool("WECHAT_ENABLED", false),
		WechatToken:     os.Getenv("WECHAT_TOKEN"),
		WechatAppID:     os.Getenv("WECHAT_APPID"),
		WechatAppSecret: os.Getenv("WECHAT_APPSECRET"),
		AgentGRPCAddr:   env("AGENT_GRPC_ADDR", "127.0.0.1:9090"),
		GatewayHTTPAddr: env("GATEWAY_HTTP_ADDR", ":80"),
		RedisAddr:       env("REDIS_ADDR", "127.0.0.1:6379"),
		PgDSN:           os.Getenv("PG_DSN"),
		AuthPepper:      os.Getenv("AUTH_PEPPER"),
		SMTPHost:        env("SMTP_HOST", "127.0.0.1"),
		SMTPPort:        env("SMTP_PORT", "1025"),
		SMTPUsername:    os.Getenv("SMTP_USERNAME"),
		SMTPPassword:    os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:        env("SMTP_FROM", "SOS Brigade <noreply@sos.local>"),
		SMTPImplicitTLS: envBool("SMTP_IMPLICIT_TLS", false),
		CookieSecure:    envBool("COOKIE_SECURE", false),
		DailyQuotaLimit: dailyQuotaLimit,
		QuotaTimeZone:   quotaTimeZone,
		ReleaseID:       os.Getenv("RELEASE_ID"),
		BackendCommit:   os.Getenv("BACKEND_COMMIT"),
		FrontendCommit:  os.Getenv("FRONTEND_COMMIT"),
	}
	if cfg.WechatEnabled {
		if cfg.WechatToken == "" {
			return nil, fmt.Errorf("WECHAT_TOKEN is required when WECHAT_ENABLED=true")
		}
		if cfg.WechatAppID == "" {
			return nil, fmt.Errorf("WECHAT_APPID is required when WECHAT_ENABLED=true")
		}
		if cfg.WechatAppSecret == "" {
			return nil, fmt.Errorf("WECHAT_APPSECRET is required when WECHAT_ENABLED=true")
		}
	}
	if cfg.PgDSN == "" {
		return nil, fmt.Errorf("PG_DSN is required")
	}
	if cfg.AuthPepper == "" {
		return nil, fmt.Errorf("AUTH_PEPPER is required")
	}
	if cfg.SMTPImplicitTLS {
		if cfg.SMTPUsername == "" {
			return nil, fmt.Errorf("SMTP_USERNAME is required when SMTP_IMPLICIT_TLS=true")
		}
		if cfg.SMTPPassword == "" {
			return nil, fmt.Errorf("SMTP_PASSWORD is required when SMTP_IMPLICIT_TLS=true")
		}
		sender, err := mail.ParseAddress(cfg.SMTPFrom)
		if err != nil {
			return nil, fmt.Errorf("SMTP_FROM must be a valid email address: %w", err)
		}
		if !strings.EqualFold(strings.TrimSpace(cfg.SMTPUsername), sender.Address) {
			return nil, fmt.Errorf("SMTP_USERNAME must match the SMTP_FROM address when SMTP_IMPLICIT_TLS=true")
		}
	}
	return cfg, nil
}

func envBool(key string, def bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return def
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return def
	}
	return parsed
}

func LoadAgent() (*AgentConfig, error) {
	cfg := &AgentConfig{
		DeepSeekAPIKey: os.Getenv("DEEPSEEK_API_KEY"),
		DeepSeekModel:  env("DEEPSEEK_MODEL", "deepseek-v4-flash"),
		PgDSN:          os.Getenv("PG_DSN"),
		AgentGRPCAddr:  env("AGENT_GRPC_ADDR", "127.0.0.1:9090"),
		RedisAddr:      env("REDIS_ADDR", "127.0.0.1:6379"),
		ReleaseID:      os.Getenv("RELEASE_ID"),
		BackendCommit:  os.Getenv("BACKEND_COMMIT"),
	}
	if cfg.DeepSeekAPIKey == "" {
		return nil, fmt.Errorf("DEEPSEEK_API_KEY is required")
	}
	if cfg.PgDSN == "" {
		return nil, fmt.Errorf("PG_DSN is required")
	}
	return cfg, nil
}
