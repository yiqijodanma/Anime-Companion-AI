# 凉宫春日微信公众号陪伴 AI 设计

## 目标

- 通过微信公众号测试号接收用户文本消息，并用客服消息异步回复。
- 保持 Gateway 与 Agent 分层：Gateway 处理 HTTP、微信协议和运行保护；Agent 处理人设、DeepSeek 调用和记忆。
- 用 PostgreSQL 保存用户当天对话与长期摘要，用 Redis 承担 Gateway 侧短期缓存和限流。

## 非目标

- 不容器化 Go 服务，生产部署方式另行决定。
- 不在本地 smoke test 中伪造真实微信公网回调或 DeepSeek 成功回复。
- 不支持图片、语音、菜单等非文本微信能力。

## 架构

`微信测试号 / REST 客户端 -> Gateway(Gin) -> gRPC -> Agent(Eino + DeepSeek) -> PostgreSQL`

- Gateway：暴露 `/wechat`、`/healthz`、`/api/v1/chat`、`/api/v1/conversations/:open_id/messages`；校验微信签名；做 MsgId 去重、open_id 限流、access_token 缓存；通过客服消息发送回复。
- Agent：实现 gRPC `Reply`、`RunDailyMaintenance`、会话消息查询和删除，并注册标准 gRPC health service；组装凉宫春日人设提示词；调用 DeepSeek；读写记忆。
- PostgreSQL：保存消息和每日摘要，是记忆数据源。
- Redis：仅作为 Gateway 运行缓存，不保存业务真相。

## 微信消息流程

1. 微信向 `/wechat` 发 GET 校验，Gateway 用 `WECHAT_TOKEN` 校验签名并回显 `echostr`。
2. 微信向 `/wechat` 发 POST 文本 XML，Gateway 校验签名、解析消息、用 Redis 按 MsgId 去重。
3. Gateway 对 `FromUserName` 做固定窗口限流，超过默认 `30 次/分钟/open_id` 时仍向微信 ACK `success`，但不调用 Agent。
4. Gateway 立即 ACK `success`，后台调用 Agent 生成回复。
5. Gateway 通过微信客服消息接口把回复发给用户；access_token 失效时刷新后重试。

## 客服消息发送

客服消息依赖真实 `WECHAT_APPID`、`WECHAT_APPSECRET` 和关注测试号后的 `open_id`。本地 REST smoke test 不验证这条外部链路；真实验证需要公网 URL、微信测试号配置和微信可回调到 Gateway。

## DeepSeek

Agent 使用 DeepSeek 的 OpenAI 兼容接口，Base URL 固定为 `https://api.deepseek.com`，模型由 `DEEPSEEK_MODEL` 配置。`/healthz` 只检查 Gateway 到 Agent 的 gRPC 连通性；`/api/v1/chat` 成功回复需要真实 `DEEPSEEK_API_KEY` 和可访问外网。

## 记忆模型

- 当天消息按 `open_id` 追加保存，参与下一轮回复上下文。
- 每日维护按目标日期汇总历史消息，生成摘要后写回 PostgreSQL。
- REST 接口可查看或清理某个 `open_id` 当天消息：`GET/DELETE /api/v1/conversations/:open_id/messages`。

## 配置

- Gateway：`WECHAT_TOKEN`、`WECHAT_APPID`、`WECHAT_APPSECRET`、`AGENT_GRPC_ADDR`、`GATEWAY_HTTP_ADDR`、`REDIS_ADDR`
- Agent：`DEEPSEEK_API_KEY`、`DEEPSEEK_MODEL`、`PG_DSN`、`AGENT_GRPC_ADDR`

本地手动启动 Gateway 建议用 `GATEWAY_HTTP_ADDR=:8080`，避免占用 80 端口。

## healthz

`GET /healthz` 由 Gateway 调 Agent 的标准 gRPC health service。只要 Agent 进程启动且配置通过，就可以用非空占位 DeepSeek key 做连通性 smoke test；它不会实际请求 DeepSeek。

## 测试策略

- 单元测试覆盖配置加载、Gateway HTTP/微信处理、Token 缓存、Redis 去重/限流、Agent gRPC 方法。
- 集成测试用 bufconn 覆盖 Gateway gRPC 客户端到 Agent 服务的关键路径。
- 本地 smoke test 覆盖 PostgreSQL/Redis compose、Agent/Gateway 启动、`/healthz`、REST 会话管理；真实 DeepSeek 和微信链路单独凭据验证。
