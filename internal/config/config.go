package config

import (
	"fmt"
	"os"
	"strconv"
)

type GatewayConfig struct {
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
	CookieSecure    bool
}

type AgentConfig struct {
	DeepSeekAPIKey string
	DeepSeekModel  string
	PgDSN          string
	AgentGRPCAddr  string
	RedisAddr      string
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func LoadGateway() (*GatewayConfig, error) {
	cfg := &GatewayConfig{
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
		CookieSecure:    envBool("COOKIE_SECURE", false),
	}
	if cfg.WechatToken == "" {
		return nil, fmt.Errorf("WECHAT_TOKEN is required")
	}
	if cfg.WechatAppID == "" {
		return nil, fmt.Errorf("WECHAT_APPID is required")
	}
	if cfg.WechatAppSecret == "" {
		return nil, fmt.Errorf("WECHAT_APPSECRET is required")
	}
	if cfg.PgDSN == "" {
		return nil, fmt.Errorf("PG_DSN is required")
	}
	if cfg.AuthPepper == "" {
		return nil, fmt.Errorf("AUTH_PEPPER is required")
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
	}
	if cfg.DeepSeekAPIKey == "" {
		return nil, fmt.Errorf("DEEPSEEK_API_KEY is required")
	}
	if cfg.PgDSN == "" {
		return nil, fmt.Errorf("PG_DSN is required")
	}
	return cfg, nil
}
