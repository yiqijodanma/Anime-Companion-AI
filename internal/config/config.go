package config

import (
	"fmt"
	"os"
)

type GatewayConfig struct {
	WechatToken     string
	WechatAppID     string
	WechatAppSecret string
	AgentGRPCAddr   string
	GatewayHTTPAddr string
	RedisAddr       string
}

type AgentConfig struct {
	DeepSeekAPIKey string
	DeepSeekModel  string
	PgDSN          string
	AgentGRPCAddr  string
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
	return cfg, nil
}

func LoadAgent() (*AgentConfig, error) {
	cfg := &AgentConfig{
		DeepSeekAPIKey: os.Getenv("DEEPSEEK_API_KEY"),
		DeepSeekModel:  env("DEEPSEEK_MODEL", "deepseek-v4-flash"),
		PgDSN:          os.Getenv("PG_DSN"),
		AgentGRPCAddr:  env("AGENT_GRPC_ADDR", "127.0.0.1:9090"),
	}
	if cfg.DeepSeekAPIKey == "" {
		return nil, fmt.Errorf("DEEPSEEK_API_KEY is required")
	}
	if cfg.PgDSN == "" {
		return nil, fmt.Errorf("PG_DSN is required")
	}
	return cfg, nil
}
