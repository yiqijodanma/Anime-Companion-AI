# 凉宫春日微信公众号陪伴 AI 实现计划

## 阶段 1：基础链路，已完成

- 建立 Gateway / Agent 两进程架构，Gateway 用 Gin 暴露 HTTP，Agent 用 gRPC 提供对话能力。
- 接入微信公众号测试号 `/wechat` GET 校验和 POST 文本消息接收。
- 接入微信客服消息发送，支持 access_token 失效后的刷新重试。
- 建立凉宫春日人设提示词、DeepSeek 对话调用、PostgreSQL 消息记忆。
- 加入日志、配置加载、基础单元测试和 bufconn 集成测试。

## 阶段 2：本地可测能力，已完成

- 增加 REST MVP：`POST /api/v1/chat`、`GET /api/v1/conversations/:open_id/messages`、`DELETE /api/v1/conversations/:open_id/messages`。
- 增加 `/healthz`，用于验证 Gateway 到 Agent 的 gRPC 连通性。
- 增加 Docker Compose 本地 PostgreSQL / Redis 依赖。
- 增加 Redis-backed Gateway 保护：MsgId 去重、access_token 缓存、open_id 限流，默认 `30 次/分钟/open_id`。
- 补齐 `.env.example` 和 README 的本地启动、smoke test、外部凭据边界。

## 阶段 3：仍需真实外部环境验证

- 使用真实 DeepSeek key 和可访问外网验证 `/api/v1/chat` 成功回复。
- 使用公网 URL、微信测试号 appID/appSecret/token 验证 `/wechat` 签名校验、文本回调和客服消息推送。
- 观察真实微信重试、access_token 过期、限流命中后的日志和用户体验。
- 根据真实流量再决定部署形态、监控告警和更细粒度的运维配置。
