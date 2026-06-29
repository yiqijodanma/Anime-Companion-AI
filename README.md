# Anime-Companion-AI

陪伴型微信公众号 AI。Gateway 接微信公众号测试号 HTTP/XML 与客服消息，Agent 负责凉宫春日人设、DeepSeek 对话链和 PostgreSQL 记忆。

## 架构

`Gateway(Gin) -> gRPC -> Agent(Eino + DeepSeek) -> PostgreSQL`

## 本地测试

```powershell
docker compose up -d postgres redis
go test ./...
New-Item -ItemType Directory -Force bin
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

Redis 已用于 Gateway 侧 MsgId 去重、access_token 缓存和按 open_id 固定窗口限流，默认限流值为 `30 次/分钟/open_id`。

本地手动启动 Gateway 建议监听 8080，避免占用 80 端口：

```powershell
$env:GATEWAY_HTTP_ADDR=":8080"
```

## 配置

参考 `.env.example` 填写环境变量，并在启动服务前设置到当前 shell；程序只读取系统环境变量，不会自动加载 `.env` 文件。

- Gateway：`WECHAT_TOKEN`、`WECHAT_APPID`、`WECHAT_APPSECRET`、`AGENT_GRPC_ADDR`、`GATEWAY_HTTP_ADDR`、`REDIS_ADDR`
- Agent：`DEEPSEEK_API_KEY`、`DEEPSEEK_MODEL`、`PG_DSN`、`AGENT_GRPC_ADDR`

DeepSeek OpenAI 兼容 Base URL 固定为 `https://api.deepseek.com`，模型示例为 `deepseek-v4-flash`。

## 启动

先启动 Agent：

```powershell
bin/agent.exe
```

再启动 Gateway：

```powershell
$env:GATEWAY_HTTP_ADDR=":8080"
bin/gateway.exe
```

日志写入项目根目录 `log/agent.log` 和 `log/gateway.log`。

## 本地 smoke test

`/healthz` 只检查 Gateway 到 Agent 的 gRPC 连通性。Agent 启动时要求 `DEEPSEEK_API_KEY` 非空，因此可以用非空占位值验证 `/healthz`；它不会实际请求 DeepSeek。

```powershell
curl.exe http://localhost:8080/healthz
```

REST 对话成功路径需要真实 DeepSeek key 和可访问外网：

```powershell
curl.exe -X POST http://localhost:8080/api/v1/chat -H "Content-Type: application/json" -d '{"open_id":"u1","text":"你好"}'
```

当天记忆管理接口：

```powershell
curl.exe http://localhost:8080/api/v1/conversations/u1/messages
curl.exe -X DELETE http://localhost:8080/api/v1/conversations/u1/messages
```

## 微信测试号配置

1. 打开微信公众平台接口测试号页面，获取 appID/appSecret。
2. 接口 URL 填 `http://<public-host>/wechat`，Token 填 `WECHAT_TOKEN` 环境变量对应的实际值。
3. 扫码关注测试号后即可私聊。

真实 `/wechat` 回调需要公网 URL、微信测试号凭据和微信可访问的 Gateway。仅本地启动服务时，请使用上面的 REST smoke test。
