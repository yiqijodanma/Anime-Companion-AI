# Anime-Companion-AI

陪伴型微信公众号 AI。Gateway 接微信公众号测试号 HTTP/XML 与客服消息，Agent 负责凉宫春日人设、DeepSeek 对话链和 PostgreSQL 记忆。

当前已整理的文档：

- 设计文档：[docs/superpowers/specs/2026-06-24-companion-ai-haruhi-wechat-design.md](docs/superpowers/specs/2026-06-24-companion-ai-haruhi-wechat-design.md)
- 本地基础设施设计：[docs/superpowers/specs/2026-06-29-local-infra-compose-design.md](docs/superpowers/specs/2026-06-29-local-infra-compose-design.md)
- 实现计划：[docs/superpowers/plans/2026-06-24-companion-ai-haruhi-wechat.md](docs/superpowers/plans/2026-06-24-companion-ai-haruhi-wechat.md)

2026-06-28 已补充可执行性修订：配置拆分、DeepSeek 最新接入说明、每日维护日期窗口、REST MVP 范围、healthz 真实检查、微信消息去重与 token 失效重试。

## 架构

`Gateway(Gin, :80) -> gRPC -> Agent(Eino + DeepSeek) -> PostgreSQL`

## 本地测试

```powershell
docker compose up -d postgres redis
go test ./...
go build -o bin/agent.exe ./cmd/agent
go build -o bin/gateway.exe ./cmd/gateway
```

`docker-compose.yml` 只启动本地测试基础设施，不容器化 Go 服务：

- PostgreSQL: `localhost:5432`
- Redis: `localhost:6379`

PostgreSQL 默认连接串：

```powershell
$env:PG_DSN="postgres://companion:companion@localhost:5432/companion?sslmode=disable"
```

Redis 目前只作为本地基础设施预留，后续再接入 MsgId 去重、access token 缓存或限流。

## 配置

复制 `.env.example` 并填写环境变量，或在启动服务前直接设置环境变量：

- Gateway：`WECHAT_TOKEN`、`WECHAT_APPID`、`WECHAT_APPSECRET`、`AGENT_GRPC_ADDR`、`GATEWAY_HTTP_ADDR`
- Agent：`DEEPSEEK_API_KEY`、`DEEPSEEK_MODEL`、`PG_DSN`、`AGENT_GRPC_ADDR`

DeepSeek OpenAI 兼容 Base URL 固定为 `https://api.deepseek.com`，模型示例为 `deepseek-v4-flash`。

## 启动

先启动 Agent：

```powershell
bin/agent.exe
```

再启动 Gateway：

```powershell
bin/gateway.exe
```

日志写入项目根目录 `log/agent.log` 和 `log/gateway.log`。

## 微信测试号配置

1. 打开微信公众平台接口测试号页面，获取 appID/appSecret。
2. 接口 URL 填 `http://47.82.114.17/wechat`，Token 填 `WECHAT_TOKEN`。
3. 扫码关注测试号后即可私聊。

本地联调不走微信：

```powershell
curl.exe -X POST http://localhost:80/api/v1/chat -H "Content-Type: application/json" -d "{\"open_id\":\"u1\",\"text\":\"你好\"}"
```
