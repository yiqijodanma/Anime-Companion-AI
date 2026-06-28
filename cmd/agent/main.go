package main

import (
	"context"
	"net"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/agent"
	"companion-ai/internal/chat"
	"companion-ai/internal/config"
	"companion-ai/internal/logging"
	"companion-ai/internal/memory"
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

	srv := agent.NewServer(repo, chat.NewReplier(cm), summarize.NewSummarizer(cm)).WithLogger(log)
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
