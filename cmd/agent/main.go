package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/agent"
	"companion-ai/internal/chat"
	"companion-ai/internal/config"
	"companion-ai/internal/conversation"
	"companion-ai/internal/logging"
	"companion-ai/internal/memory"
	"companion-ai/internal/orchestration"
	"companion-ai/internal/summarize"
)

func main() {
	log, err := logging.New("agent")
	if err != nil {
		panic(err)
	}
	cfg, err := config.LoadAgent()
	if err != nil {
		log.Error("config load failed", "err", err)
		panic(err)
	}
	runCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	db, err := gorm.Open(postgres.Open(cfg.PgDSN), &gorm.Config{})
	if err != nil {
		log.Error("db open failed", "err", err)
		panic(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Error("db handle failed", "err", err)
		panic(err)
	}
	defer func() { _ = sqlDB.Close() }()

	repo, err := memory.NewRepo(db)
	if err != nil {
		log.Error("repo init failed", "err", err)
		panic(err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		DialTimeout:  500 * time.Millisecond,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
		MaxRetries:   -1,
	})
	defer func() { _ = redisClient.Close() }()

	cm, err := openai.NewChatModel(runCtx, &openai.ChatModelConfig{
		APIKey:  cfg.DeepSeekAPIKey,
		Model:   cfg.DeepSeekModel,
		BaseURL: "https://api.deepseek.com",
		Timeout: 30 * time.Second,
	})
	if err != nil {
		log.Error("chat model init failed", "err", err)
		panic(err)
	}

	conversations := conversation.NewRedisStore(redisClient, "", 72*time.Hour)
	conversationApp := orchestration.NewApplication(conversations, repo, orchestration.NewDeepSeekAdapter(cm)).WithLogger(log)
	srv := agent.NewServer(repo, conversations, chat.NewReplier(cm), summarize.NewSummarizer(cm)).
		WithConversationApplication(conversationApp).
		WithLogger(log)
	lis, err := net.Listen("tcp", cfg.AgentGRPCAddr)
	if err != nil {
		log.Error("listen failed", "err", err)
		panic(err)
	}

	grpcServer := grpc.NewServer()
	agentv1.RegisterAgentServiceServer(grpcServer, srv)
	healthServer := health.NewServer()
	initializeAgentHealth(healthServer)
	healthv1.RegisterHealthServer(grpcServer, healthServer)
	checks := []dependencyCheck{
		func(ctx context.Context) error { return sqlDB.PingContext(ctx) },
		func(ctx context.Context) error { return redisClient.Ping(ctx).Err() },
	}
	checkDependencies := func() bool {
		ctx, cancel := context.WithTimeout(runCtx, 2*time.Second)
		defer cancel()
		return updateAgentServingStatus(ctx, healthServer, checks...)
	}
	ready := checkDependencies()
	if !ready {
		log.Warn("agent dependencies not ready")
	}
	go func(previous bool) {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				current := checkDependencies()
				if current != previous {
					log.Info("agent readiness changed", "ready", current)
					previous = current
				}
			}
		}
	}(ready)

	serveErr := make(chan error, 1)
	go func() { serveErr <- grpcServer.Serve(lis) }()
	log.Info("agent grpc serving", "addr", cfg.AgentGRPCAddr, "ready", ready, "release_id", cfg.ReleaseID, "backend_commit", cfg.BackendCommit)
	select {
	case err := <-serveErr:
		if err != nil {
			log.Error("serve failed", "err", err)
			panic(err)
		}
	case <-runCtx.Done():
		log.Info("agent shutting down")
		healthServer.Shutdown()
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(20 * time.Second):
			log.Warn("agent graceful shutdown timed out")
			grpcServer.Stop()
		}
	}
}
