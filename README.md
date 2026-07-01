# Anime-Companion-AI

陪伴型微信公众号 AI。Gateway 接微信公众号测试号 HTTP/XML 与客服消息，Agent 负责凉宫春日人设、DeepSeek 对话链和 PostgreSQL 记忆。

## 架构

`Gateway(Gin) -> gRPC -> Agent(Eino + DeepSeek) -> PostgreSQL`

## 本地测试

```powershell
make db
make test
```

`make db` 会在 `.env` 不存在时从 `.env.example` 复制生成，并使用 dev 端口启动 PostgreSQL / Redis，然后执行 SQL migration。数据库表结构不由 GORM 自动创建，schema 来源是 `db/migrations`。

默认 dev 基础设施：

- PostgreSQL: `localhost:5432`
- Redis: `localhost:6379`

`make test` 复用 dev PostgreSQL，测试会创建临时 schema 并执行 migration，不依赖 SQLite 或 GORM `AutoMigrate`。

如需直接运行完整容器环境：

```powershell
make docker
```

`make docker` 使用 pro 端口配置构建并运行 Agent、Gateway、PostgreSQL、Redis 和 migration。

Redis 已用于 Gateway 侧 MsgId 去重、access_token 缓存和按 open_id 固定窗口限流，默认限流值为 `30 次/分钟/open_id`。

本地手动启动 Gateway 建议监听 8080，避免占用 80 端口：

```powershell
$env:GATEWAY_HTTP_ADDR=":8080"
```

## 配置

参考 `.env.example` 填写单个 `.env` 文件。Makefile 会从 `.env` 读取 dev/pro 两套端口配置，并导出程序实际使用的 `PG_DSN`、`REDIS_ADDR`、`GATEWAY_HTTP_ADDR`、`AGENT_GRPC_ADDR`。

- dev：`DEV_POSTGRES_PORT`、`DEV_REDIS_PORT`、`DEV_GATEWAY_HTTP_PORT`、`DEV_AGENT_GRPC_PORT`
- pro：`PRO_POSTGRES_PORT`、`PRO_REDIS_PORT`、`PRO_GATEWAY_HTTP_PORT`、`PRO_AGENT_GRPC_PORT`
- Gateway：`WECHAT_TOKEN`、`WECHAT_APPID`、`WECHAT_APPSECRET`
- Agent：`DEEPSEEK_API_KEY`、`DEEPSEEK_MODEL`
- PostgreSQL：`POSTGRES_DB`、`POSTGRES_USER`、`POSTGRES_PASSWORD`

DeepSeek OpenAI 兼容 Base URL 固定为 `https://api.deepseek.com`，模型示例为 `deepseek-v4-flash`。

## 启动

开发环境：

```powershell
make db
make dev
```

`make dev` 会构建二进制并打印两个启动命令。按提示分别在两个终端启动：

```powershell
make run-agent-dev
make run-gateway-dev
```

模拟生产配置但不使用项目镜像：

```powershell
make pro
make run-agent-pro
make run-gateway-pro
```

完整容器化运行：

```powershell
make docker
```

日志写入项目根目录 `log/agent.log` 和 `log/gateway.log`；容器运行时也可以用 `make logs` 查看 Docker 日志。

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
