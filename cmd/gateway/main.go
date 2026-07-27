package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	authn "companion-ai/internal/auth"
	"companion-ai/internal/config"
	"companion-ai/internal/gateway"
	"companion-ai/internal/logging"
	"companion-ai/internal/quota"
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
	runCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	conn, err := grpc.NewClient(cfg.AgentGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("grpc client failed", "err", err)
		panic(err)
	}
	defer conn.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		DialTimeout:  500 * time.Millisecond,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
		MaxRetries:   -1,
	})
	defer redisClient.Close()
	db, err := gorm.Open(postgres.Open(cfg.PgDSN), &gorm.Config{TranslateError: true})
	if err != nil {
		log.Error("postgres client failed", "err", err)
		panic(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Error("postgres handle failed", "err", err)
		panic(err)
	}
	defer sqlDB.Close()
	authService, err := authn.NewService(db, redisClient, authn.SMTPMailer{
		Addr:        fmt.Sprintf("%s:%s", cfg.SMTPHost, cfg.SMTPPort),
		Host:        cfg.SMTPHost,
		Username:    cfg.SMTPUsername,
		Password:    cfg.SMTPPassword,
		From:        cfg.SMTPFrom,
		ImplicitTLS: cfg.SMTPImplicitTLS,
	}, authn.Config{Pepper: cfg.AuthPepper})
	if err != nil {
		log.Error("auth service failed", "err", err)
		panic(err)
	}

	quotaManager, err := quota.NewRedis(redisClient, "gateway:daily-quota:", cfg.DailyQuotaLimit)
	if err != nil {
		log.Error("quota service failed", "err", err)
		panic(err)
	}
	agentClient := gateway.NewAgentClient(conn)
	h := &gateway.Handlers{
		WechatEnabled: cfg.WechatEnabled,
		Agent:         agentClient,
		Log:           log,
		Limiter: redisstore.NewFixedWindowLimiter(
			redisClient,
			"gateway:open_id:",
			30,
			time.Minute,
		),
		Quota:        quotaManager,
		Auth:         authService,
		CookieSecure: cfg.CookieSecure,
	}
	if cfg.WechatEnabled {
		httpClient := &http.Client{Timeout: 10 * time.Second}
		tokens := wechat.NewTokenManager(cfg.WechatAppID, cfg.WechatAppSecret, httpClient).
			WithCache(redisstore.NewTokenCache(redisClient, "gateway:wechat:access_token"))
		h.Token = cfg.WechatToken
		h.Tokens = tokens
		h.Pusher = wechat.NewKFClient(httpClient)
		h.Dedupe = redisstore.NewMessageDeduper(redisClient, "gateway:wechat:msg:", 72*time.Hour)
		cronInstance := gateway.StartCron(agentClient, tokens, h.Pusher, log)
		defer cronInstance.Stop()
		log.Info("wechat integration enabled")
	} else {
		log.Info("wechat integration disabled")
	}

	router := gin.New()
	router.Use(gin.Recovery())
	h.RegisterRoutes(router)
	gateway.RegisterOperationalHealth(router,
		gateway.ReadinessCheck{Name: "agent", Check: agentClient.Check},
		gateway.ReadinessCheck{Name: "postgres", Check: sqlDB.PingContext},
		gateway.ReadinessCheck{Name: "redis", Check: func(ctx context.Context) error { return redisClient.Ping(ctx).Err() }},
	)

	server := &http.Server{
		Addr:              cfg.GatewayHTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		WriteTimeout:      90 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	log.Info("gateway http serving",
		"addr", cfg.GatewayHTTPAddr,
		"release_id", cfg.ReleaseID,
		"backend_commit", cfg.BackendCommit,
		"frontend_commit", cfg.FrontendCommit,
	)
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http serve failed", "err", err)
			panic(err)
		}
	case <-runCtx.Done():
		log.Info("gateway shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error("gateway graceful shutdown failed", "err", err)
			_ = server.Close()
		}
	}
}
