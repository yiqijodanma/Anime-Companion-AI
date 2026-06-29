package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"companion-ai/internal/config"
	"companion-ai/internal/gateway"
	"companion-ai/internal/logging"
	"companion-ai/internal/redisstore"
	"companion-ai/internal/wechat"
)

func main() {
	log, err := logging.New("gateway")
	if err != nil {
		panic(err)
	}
	cfg, err := config.LoadGateway()
	if err != nil {
		log.Error("config load failed", "err", err)
		panic(err)
	}

	conn, err := grpc.NewClient(cfg.AgentGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("grpc client failed", "err", err)
		panic(err)
	}
	defer conn.Close()

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer redisClient.Close()

	httpClient := &http.Client{Timeout: 10 * time.Second}
	tokens := wechat.NewTokenManager(cfg.WechatAppID, cfg.WechatAppSecret, httpClient).
		WithCache(redisstore.NewTokenCache(redisClient, "gateway:wechat:access_token"))
	pusher := wechat.NewKFClient(httpClient)
	agentClient := gateway.NewAgentClient(conn)

	h := &gateway.Handlers{
		Token:  cfg.WechatToken,
		Agent:  agentClient,
		Tokens: tokens,
		Pusher: pusher,
		Log:    log,
		Dedupe: redisstore.NewMessageDeduper(redisClient, "gateway:wechat:msg:", 72*time.Hour),
		Limiter: redisstore.NewFixedWindowLimiter(
			redisClient,
			"gateway:open_id:",
			30,
			time.Minute,
		),
	}

	cronInst := gateway.StartCron(agentClient, tokens, pusher, log)
	defer cronInst.Stop()

	r := gin.New()
	r.Use(gin.Recovery())
	h.RegisterRoutes(r)

	log.Info("gateway http serving", "addr", cfg.GatewayHTTPAddr)
	if err := r.Run(cfg.GatewayHTTPAddr); err != nil {
		log.Error("http serve failed", "err", err)
		panic(err)
	}
}
