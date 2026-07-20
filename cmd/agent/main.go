package main

import (
	"context"
	"net"
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
	if err := sqlDB.Ping(); err != nil {
		log.Error("db ping failed", "err", err)
		panic(err)
	}

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
	defer redisClient.Close()

	cm, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
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
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(grpcServer, healthServer)

	log.Info("agent grpc serving", "addr", cfg.AgentGRPCAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Error("serve failed", "err", err)
		panic(err)
	}
}
