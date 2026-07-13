-include .env

export DEEPSEEK_API_KEY

DEV_POSTGRES_PORT ?= 5432
DEV_REDIS_PORT ?= 6379
DEV_GATEWAY_HTTP_PORT ?= 8080
DEV_AGENT_GRPC_PORT ?= 9090

PRO_POSTGRES_PORT ?= 15432
PRO_REDIS_PORT ?= 16379
PRO_GATEWAY_HTTP_PORT ?= 80
PRO_AGENT_GRPC_PORT ?= 9090

POSTGRES_DB ?= companion
POSTGRES_USER ?= companion
POSTGRES_PASSWORD ?= companion
DEEPSEEK_MODEL ?= deepseek-v4-flash

AGENT_BIN := bin/agent.exe
GATEWAY_BIN := bin/gateway.exe

DEV_PG_DSN := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(DEV_POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
PRO_PG_DSN := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(PRO_POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable

COMPOSE_DEV := POSTGRES_PORT=$(DEV_POSTGRES_PORT) REDIS_PORT=$(DEV_REDIS_PORT) GATEWAY_HTTP_PORT=$(DEV_GATEWAY_HTTP_PORT) AGENT_GRPC_PORT=$(DEV_AGENT_GRPC_PORT) docker compose
COMPOSE_PRO := POSTGRES_PORT=$(PRO_POSTGRES_PORT) REDIS_PORT=$(PRO_REDIS_PORT) GATEWAY_HTTP_PORT=$(PRO_GATEWAY_HTTP_PORT) AGENT_GRPC_PORT=$(PRO_AGENT_GRPC_PORT) docker compose

ifeq ($(OS),Windows_NT)
AGENT_BIN := bin\agent.exe
GATEWAY_BIN := bin\gateway.exe
COMPOSE_DEV := set POSTGRES_PORT=$(DEV_POSTGRES_PORT)&& set REDIS_PORT=$(DEV_REDIS_PORT)&& set GATEWAY_HTTP_PORT=$(DEV_GATEWAY_HTTP_PORT)&& set AGENT_GRPC_PORT=$(DEV_AGENT_GRPC_PORT)&& docker compose
COMPOSE_PRO := set POSTGRES_PORT=$(PRO_POSTGRES_PORT)&& set REDIS_PORT=$(PRO_REDIS_PORT)&& set GATEWAY_HTTP_PORT=$(PRO_GATEWAY_HTTP_PORT)&& set AGENT_GRPC_PORT=$(PRO_AGENT_GRPC_PORT)&& docker compose
ENV_CREATE_CMD := if not exist .env (copy .env.example .env >NUL && echo Created .env from .env.example. Fill WECHAT_* and DEEPSEEK_API_KEY before running app targets.)
MKDIR_BIN_CMD := if not exist bin mkdir bin
TEST_ENV := set PG_DSN=$(DEV_PG_DSN)&&
DEV_AGENT_ENV := set PG_DSN=$(DEV_PG_DSN)&& set AGENT_GRPC_ADDR=127.0.0.1:$(DEV_AGENT_GRPC_PORT)&& set REDIS_ADDR=127.0.0.1:$(DEV_REDIS_PORT)&& set DEEPSEEK_MODEL=$(DEEPSEEK_MODEL)&&
DEV_GATEWAY_ENV := set AGENT_GRPC_ADDR=127.0.0.1:$(DEV_AGENT_GRPC_PORT)&& set GATEWAY_HTTP_ADDR=:$(DEV_GATEWAY_HTTP_PORT)&& set REDIS_ADDR=127.0.0.1:$(DEV_REDIS_PORT)&& set PG_DSN=$(DEV_PG_DSN)&& set AUTH_PEPPER=$(AUTH_PEPPER)&& set SMTP_HOST=$(SMTP_HOST)&& set SMTP_PORT=$(SMTP_PORT)&& set SMTP_USERNAME=$(SMTP_USERNAME)&& set SMTP_PASSWORD=$(SMTP_PASSWORD)&& set SMTP_FROM=$(SMTP_FROM)&& set COOKIE_SECURE=$(COOKIE_SECURE)&& set WECHAT_TOKEN=$(WECHAT_TOKEN)&& set WECHAT_APPID=$(WECHAT_APPID)&& set WECHAT_APPSECRET=$(WECHAT_APPSECRET)&&
PRO_AGENT_ENV := set PG_DSN=$(PRO_PG_DSN)&& set AGENT_GRPC_ADDR=127.0.0.1:$(PRO_AGENT_GRPC_PORT)&& set REDIS_ADDR=127.0.0.1:$(PRO_REDIS_PORT)&& set DEEPSEEK_MODEL=$(DEEPSEEK_MODEL)&&
PRO_GATEWAY_ENV := set AGENT_GRPC_ADDR=127.0.0.1:$(PRO_AGENT_GRPC_PORT)&& set GATEWAY_HTTP_ADDR=:$(PRO_GATEWAY_HTTP_PORT)&& set REDIS_ADDR=127.0.0.1:$(PRO_REDIS_PORT)&& set WECHAT_TOKEN=$(WECHAT_TOKEN)&& set WECHAT_APPID=$(WECHAT_APPID)&& set WECHAT_APPSECRET=$(WECHAT_APPSECRET)&&
else
ENV_CREATE_CMD := if [ ! -f .env ]; then cp .env.example .env; echo "Created .env from .env.example. Fill WECHAT_* and DEEPSEEK_API_KEY before running app targets."; fi
MKDIR_BIN_CMD := mkdir -p bin
TEST_ENV := PG_DSN="$(DEV_PG_DSN)"
DEV_AGENT_ENV := PG_DSN="$(DEV_PG_DSN)" AGENT_GRPC_ADDR="127.0.0.1:$(DEV_AGENT_GRPC_PORT)" REDIS_ADDR="127.0.0.1:$(DEV_REDIS_PORT)" DEEPSEEK_MODEL="$(DEEPSEEK_MODEL)"
DEV_GATEWAY_ENV := AGENT_GRPC_ADDR="127.0.0.1:$(DEV_AGENT_GRPC_PORT)" GATEWAY_HTTP_ADDR=":$(DEV_GATEWAY_HTTP_PORT)" REDIS_ADDR="127.0.0.1:$(DEV_REDIS_PORT)" PG_DSN="$(DEV_PG_DSN)" AUTH_PEPPER="$(AUTH_PEPPER)" SMTP_HOST="$(SMTP_HOST)" SMTP_PORT="$(SMTP_PORT)" SMTP_USERNAME="$(SMTP_USERNAME)" SMTP_PASSWORD="$(SMTP_PASSWORD)" SMTP_FROM="$(SMTP_FROM)" COOKIE_SECURE="$(COOKIE_SECURE)" WECHAT_TOKEN="$(WECHAT_TOKEN)" WECHAT_APPID="$(WECHAT_APPID)" WECHAT_APPSECRET="$(WECHAT_APPSECRET)"
PRO_AGENT_ENV := PG_DSN="$(PRO_PG_DSN)" AGENT_GRPC_ADDR="127.0.0.1:$(PRO_AGENT_GRPC_PORT)" REDIS_ADDR="127.0.0.1:$(PRO_REDIS_PORT)" DEEPSEEK_MODEL="$(DEEPSEEK_MODEL)"
PRO_GATEWAY_ENV := AGENT_GRPC_ADDR="127.0.0.1:$(PRO_AGENT_GRPC_PORT)" GATEWAY_HTTP_ADDR=":$(PRO_GATEWAY_HTTP_PORT)" REDIS_ADDR="127.0.0.1:$(PRO_REDIS_PORT)" WECHAT_TOKEN="$(WECHAT_TOKEN)" WECHAT_APPID="$(WECHAT_APPID)" WECHAT_APPSECRET="$(WECHAT_APPSECRET)"
endif

.PHONY: env db dev pro docker test migrate-up migrate-down logs ps clean build run-agent-dev run-gateway-dev run-agent-pro run-gateway-pro check-secrets

env:
	@$(ENV_CREATE_CMD)

check-secrets: env
ifeq ($(OS),Windows_NT)
	@if not defined DEEPSEEK_API_KEY (echo DEEPSEEK_API_KEY must be set in .env & exit /b 1)
	@if "%DEEPSEEK_API_KEY%"=="your_deepseek_key" (echo DEEPSEEK_API_KEY must be set in .env & exit /b 1)
else
	@if [ -z "$$DEEPSEEK_API_KEY" ] || [ "$$DEEPSEEK_API_KEY" = "your_deepseek_key" ]; then echo "DEEPSEEK_API_KEY must be set in .env"; exit 1; fi
endif

db: env
	$(COMPOSE_DEV) pull postgres redis mailpit migrate
	$(COMPOSE_DEV) up -d --wait postgres redis mailpit
	$(COMPOSE_DEV) run --rm migrate

migrate-up: env
	$(COMPOSE_DEV) run --rm migrate

migrate-down: env
	$(COMPOSE_DEV) run --rm migrate -path=/migrations -database "postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$(POSTGRES_DB)?sslmode=disable" down 1

build:
	$(MKDIR_BIN_CMD)
	go build -o bin/agent.exe ./cmd/agent
	go build -o bin/gateway.exe ./cmd/gateway

run-agent-dev: check-secrets
	@$(DEV_AGENT_ENV) $(AGENT_BIN)

run-gateway-dev: check-secrets
	@$(DEV_GATEWAY_ENV) $(GATEWAY_BIN)

dev: db build check-secrets
	@echo "Dev infrastructure is ready. Starting Agent and Gateway..."
	$(MAKE) --no-print-directory -j2 run-agent-dev run-gateway-dev

run-agent-pro: check-secrets
	@$(PRO_AGENT_ENV) $(AGENT_BIN)

run-gateway-pro: check-secrets
	@$(PRO_GATEWAY_ENV) $(GATEWAY_BIN)

pro: env build
	$(COMPOSE_PRO) up -d --wait postgres redis
	$(COMPOSE_PRO) run --rm migrate
	@echo "Pro database is ready and binaries are built."
	@echo "Run these in separate terminals:"
	@echo "  make run-agent-pro"
	@echo "  make run-gateway-pro"

docker: check-secrets
	$(COMPOSE_PRO) pull postgres redis migrate
	$(COMPOSE_PRO) up -d --build postgres redis migrate agent gateway

test: db
	$(TEST_ENV) go test ./...

logs:
	docker compose logs -f

ps:
	docker compose ps

clean:
	docker compose down
