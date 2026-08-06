# Anime-Companion-AI

陪伴型 SOS 团 AI。认证 Web 用户拥有一个五人群聊和五个独立单聊；微信公众号继续使用凉宫春日单聊。Gateway 负责 Web/微信传输，Agent 负责多角色编排、DeepSeek 对话链、Redis 当日上下文和 PostgreSQL 长期记忆。

## 架构

`Gateway(Gin + embedded Web UI) -> gRPC -> Agent(orchestration + Eino/DeepSeek) -> Redis + PostgreSQL`

## 本地测试

```powershell
make db
make test
```

`make db` 会在 `.env` 不存在时从 `.env.example` 复制生成，并使用 dev 端口启动 PostgreSQL / Redis，然后执行 SQL migration。数据库表结构不由 GORM 自动创建，schema 来源是 `db/migrations`。

默认 dev 基础设施：

- PostgreSQL: `localhost:5432`
- Redis: `localhost:6379`
- Mailpit SMTP: `localhost:1025`
- Mailpit 收件箱: `http://localhost:8025`

`make test` 复用 dev PostgreSQL，测试会创建临时 schema 并执行 migration，不依赖 SQLite 或 GORM `AutoMigrate`。

如需直接运行完整容器环境：

```powershell
make docker
```

`make docker` 使用 pro 端口配置构建并运行 Agent、Gateway、PostgreSQL、Redis、Mailpit 和 migration。

Redis 已用于 Gateway 侧 MsgId 去重、access_token 缓存和按 open_id 固定窗口限流，默认限流值为 `30 次/分钟/open_id`。

本地手动启动 Gateway 建议监听 8080，避免占用 80 端口：

```powershell
$env:GATEWAY_HTTP_ADDR=":8080"
```

## 配置

参考 `.env.example` 填写单个 `.env` 文件。Makefile 会从 `.env` 读取 dev/pro 两套端口配置，并导出程序实际使用的 `PG_DSN`、`REDIS_ADDR`、`GATEWAY_HTTP_ADDR`、`AGENT_GRPC_ADDR`。

- dev：`DEV_POSTGRES_PORT`、`DEV_REDIS_PORT`、`DEV_GATEWAY_HTTP_PORT`、`DEV_AGENT_GRPC_PORT`
- pro：`PRO_POSTGRES_PORT`、`PRO_REDIS_PORT`、`PRO_GATEWAY_HTTP_PORT`、`PRO_AGENT_GRPC_PORT`
- Gateway：`WECHAT_ENABLED`（默认 `false`）、`WECHAT_TOKEN`、`WECHAT_APPID`、`WECHAT_APPSECRET`、`AUTH_PEPPER`、`COOKIE_SECURE`
- 邮件：`SMTP_HOST`、`SMTP_PORT`、`SMTP_IMPLICIT_TLS`、`SMTP_USERNAME`、`SMTP_PASSWORD`、`SMTP_FROM`
- Agent：`DEEPSEEK_API_KEY`、`DEEPSEEK_MODEL`
- PostgreSQL：`POSTGRES_DB`、`POSTGRES_USER`、`POSTGRES_PASSWORD`

DeepSeek OpenAI 兼容 Base URL 固定为 `https://api.deepseek.com`，模型示例为 `deepseek-v4-flash`。

生产邮件使用阿里云邮件推送：`smtpdm.aliyun.com:465` 配合隐式 TLS。`SMTP_USERNAME` 必须与邮件推送控制台中已验证的发信地址一致，`SMTP_PASSWORD` 使用该发信地址单独设置的 SMTP 密码；`SMTP_FROM` 可以为同一地址增加显示名。本地 Docker 环境仍使用 Mailpit，不启用 TLS。

## 启动

开发环境：

```powershell
make db
make dev
```

`make dev` 会启动 PostgreSQL、Redis、Mailpit，执行 migration，构建二进制，并在当前终端并行运行 Agent 与 Gateway。Gateway 默认监听 `8080`：

```powershell
make dev
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

Gateway 和 Agent 日志写入标准输出/标准错误；容器运行时可用 `make logs` 或 `kubectl logs` 查看，不依赖容器内项目日志文件。

## 本地 smoke test

`/livez` 只证明 Gateway HTTP 进程仍能响应；`/readyz` 检查 Agent、PostgreSQL、Redis 是否都可用；`/healthz` 保持兼容语义，只检查 Gateway 到 Agent 的 gRPC 连通性。Agent 启动时要求 `DEEPSEEK_API_KEY` 非空，但这些探针不会实际请求 DeepSeek。

```powershell
curl.exe http://localhost:8080/healthz
```

浏览器入口为 `http://localhost:8080/`；`/app/` 仅保留兼容跳转。未登录时显示认证界面；登录后左侧固定显示 SOS 团群聊和五个成员单聊。注册、验证和密码找回继续使用现有 REST 流程。

Web REST 接口必须先完成邮箱注册并取得 `HttpOnly` 会话 Cookie；前端不会再提交或保存匿名 `external_id`。本地 Mailpit 收件箱位于 `http://localhost:8025`，生产环境必须替换 `AUTH_PEPPER` 并将 `COOKIE_SECURE=true`。

认证后的新会话接口需要真实 DeepSeek key 和可访问外网。每次发送都要使用新的 UUID `client_request_id`；网络重试必须复用原 UUID：

```powershell
curl.exe -b cookies.txt http://localhost:8080/api/v1/conversations
curl.exe -b cookies.txt -X POST http://localhost:8080/api/v1/conversations/sos-group/messages -H "Content-Type: application/json" -d '{"content":"有希、阿虚和团长怎么看？","client_request_id":"11111111-1111-4111-8111-111111111111"}'
```

当天消息和单空间清理：

```powershell
curl.exe -b cookies.txt http://localhost:8080/api/v1/conversations/sos-group/messages
curl.exe -b cookies.txt -X DELETE http://localhost:8080/api/v1/conversations/direct-yuki/messages
```

旧的认证路由 `/api/v1/conversations/messages` 暂时保留为 deprecated alias，固定映射到 `direct-haruhi`；新客户端不要再使用它。

真实模型的非 CI smoke 建议各发一轮：明确只问一名成员、邀请三名成员讨论、邀请全体五人发言。人工检查参与人数是否自然、角色口吻和串行延迟；不要把随机措辞写进自动测试。

## k3s 生产发布

生产镜像通过 Windows PowerShell 发布脚本构建：Gateway 从 sibling React 仓库的 named build context 执行 `npm ci`，将 `dist` 覆盖进 Go embed 目录，再和 Agent、migration、backup 镜像一起以不可变 tag 推送到 ACR。k3s manifests、Secret 初始化、preflight、staging/prod 证书切换、回滚、冒烟与 OSS 恢复演练命令见 [deploy/README.md](deploy/README.md)。

### 发布前填写的公网配置

`scripts/release` 不包含任何默认的生产域名或服务器公网 IP。开发者必须在每次运行下列脚本时自行填写真实值；不要把真实值重新写回脚本或提交到仓库：

- `Preflight-K3s.ps1`：`-Domain '<your-public-domain>'`、`-ExpectedPublicIP '<your-server-public-ip>'`
- `Smoke-K3s.ps1`：`-Domain '<your-public-domain>'`
- `Release-K3s.ps1`：`-Domain '<your-public-domain>'`、`-ExpectedPublicIP '<your-server-public-ip>'`
- `Rollback-K3s.ps1`：`-Domain '<your-public-domain>'`（若未使用 `-SkipSmoke`）

同时确认 `deploy/k3s/overlays/{staging,production}/ingress.yaml` 的 host/TLS 域名，以及 `deploy/k3s/base/platform.yaml` 的 `PUBLIC_ORIGIN` 已替换为同一公网域名。这些部署清单不由发布脚本自动改写。

本地只渲染并校验两套 manifests：

```powershell
make k3s-manifests
```

## 微信测试号配置

1. 打开微信公众平台接口测试号页面，获取 appID/appSecret。
2. 接口 URL 填 `http://<public-host>/wechat`，Token 填 `WECHAT_TOKEN` 环境变量对应的实际值。
3. 扫码关注测试号后即可私聊。

真实 `/wechat` 回调需要显式设置 `WECHAT_ENABLED=true`、公网 URL、微信测试号凭据和微信可访问的 Gateway。默认 Web-only 模式不会要求微信凭据、注册回调或启动微信定时任务。仅本地启动服务时，请使用上面的 REST smoke test。
