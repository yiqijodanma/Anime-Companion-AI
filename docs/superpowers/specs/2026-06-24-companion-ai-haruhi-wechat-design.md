# 陪伴 AI（凉宫春日）微信公众号 — 设计文档

- **日期**：2026-06-24
- **状态**：已确认；2026-06-28 已做可执行性修订
- **目标**：做一个可接入微信公众号、随时陪用户聊天的陪伴型 AI。AI 性格固定为动漫人物「凉宫春日」，由后端代码固定。主要用于个人自娱。

---

## 0. 2026-06-28 可执行性修订（优先级最高）

以下修订覆盖本文后续同名旧描述：

1. **DeepSeek 接入**：OpenAI 兼容 Base URL 使用 `https://api.deepseek.com`。默认模型不要再写死为旧 `deepseek-chat`；部署时优先填写 DeepSeek 当前推荐模型，例如 `deepseek-v4-flash`。
2. **服务配置拆分**：Gateway 与 Agent 分别加载配置。Gateway 只校验微信与 Agent gRPC 配置；Agent 只校验 DeepSeek 与 PostgreSQL 配置，避免两个服务互相要求无关环境变量。
3. **每日维护日期窗口**：00:00 触发时必须处理“刚结束的一天”，不能用触发瞬间的 `time.Now()` 作为“今天”。Gateway 调 `RunDailyMaintenance` 时传 `target_date=YYYY-MM-DD`，Agent 按 `[target_date 00:00, target_date+1 00:00)` 汇总、删除工作记忆。
4. **REST 范围收敛**：本期 MVP 只实现 `POST /api/v1/chat` 与 `GET /healthz`。原计划中的 conversation 读写接口延期到 Phase 2，除非同时扩展 Agent gRPC 契约。
5. **健康检查必须真实**：Gateway `/healthz` 至少检查 Agent gRPC 连通性；Agent 暴露 gRPC health，并检查 PostgreSQL ping。
6. **微信可靠性**：`MsgId` 必须做短期去重；客服消息遇到 access token 失效错误（如 `40001`/`42001`）时刷新 token 并重试一次。

## 1. 需求与约束

| 维度 | 决定 |
|------|------|
| 接入渠道 | 微信公众号 |
| 公众号类型 | **微信公众平台测试号**（免费、秒注册、开放客服消息接口）。仅扫码关注测试号的人可聊，足够个人使用 |
| 人物性格 | **凉宫春日**：任性、自信、元气满满、强势、有领导欲、对平凡世界不感兴趣。固定在后端代码中（Go 常量 / 嵌入 system prompt） |
| 大模型 | **DeepSeek**（国内直连、中文与角色扮演表现好、性价比高）。Base URL 为 `https://api.deepseek.com`；具体 model id 在配置时按官方最新名称填写，默认示例用 `deepseek-v4-flash` |
| 记忆 | **双层记忆**：当日工作记忆（全量，不限条数）+ 记忆库（每日摘要，7 天滚动） |
| 语言 | **Go 1.21+** |
| Agent/LLM 框架 | **Eino**（CloudWeGo） |
| Web 框架 | **Gin** |
| 数据库 | **PostgreSQL**，通过 **GORM** 操作 |
| 服务内部通信 | **gRPC**（单向：Gateway 为 client，Agent 为 server） |
| 对外接口风格 | **RESTful**（用于本地测试与记忆管理） |
| 部署 | 公网服务器 `47.82.114.17`，监听 80 端口 |

### 关键技术约束

1. **微信被动回复 5 秒超时**：用户发消息后必须 5 秒内响应，但 LLM 生成常超过 5 秒。
   - **解法**：收到消息后立即返回 `success`，后台异步生成回复，再通过**客服消息接口**主动推送。测试号已开放客服消息接口。
2. **客服消息 48h 窗口**：客服消息只能在用户最后一次互动后 48 小时内推送。
   - 实时回复总在用户发消息后立即触发 → 必在窗口内。
   - 00:00「晚安啦！」只发给当天聊过的用户 → 必在窗口内。
3. **微信回调只能是 HTTP**：微信服务器只会向 `/wechat` 发送 HTTP/XML，无法改为 gRPC。因此 gRPC 仅用于自有服务之间。

---

## 2. 整体架构

两个服务，内部单向 gRPC 通信（Gateway = client，Agent = server，无双向耦合）：

```
微信服务器 ──HTTP/XML──▶ [Gateway 服务] ──gRPC──▶ [Agent 服务] ──▶ DeepSeek
                            │  Gin                     │  Eino chain
                            │  微信协议/access_token    │  记忆(GORM/Pg)
                            │  客服消息推送              │  当日摘要
                            │  REST API                 └──▶ PostgreSQL
                            │  定时任务(cron)
```

### 职责划分

- **Gateway 服务**：拥有微信协议（签名校验、XML 解析、access_token 管理、客服消息推送）、对外 RESTful API、定时任务（cron）。是 Agent 的 gRPC client。**不知道 DeepSeek / 春日 / 数据库的存在。**
- **Agent 服务**：拥有春日人设、Eino 对话链、记忆（GORM/Pg）、当日摘要、DeepSeek 调用。是 gRPC server。**不知道微信协议的存在。**

---

## 3. 消息流

### 3.1 实时聊天

```
用户发消息
 → 微信 POST /wechat (XML)
 → Gateway: 校验签名 → 解析消息 → 立即返回 "success"（< 5 秒，避免微信重试）
 → Gateway 起 goroutine，gRPC 调 Agent.Reply(open_id, text)
       Agent:
         读记忆 = 最近 7 天摘要 + 当天全部对话
         Eino 链执行：[春日人设 system prompt] + [记忆] + [本条消息] → DeepSeek
         将「用户消息」与「春日回复」存入 Pg（messages 表）
         返回回复文本
 → Gateway 通过客服消息接口把回复推送给用户
```

### 3.2 每天 00:00 定时维护

```
Gateway cron(每天 00:00) → gRPC 调 Agent.RunDailyMaintenance()
   Agent: 对每个「当天聊过」的用户：
     1. 调 DeepSeek 把当天对话整理成摘要（提炼主要的事）
     2. 摘要写入记忆库（memory_summaries 表）
     3. 删除该用户当天原始消息（淘汰工作记忆）
     清除所有「7 天前」的摘要（滚动 7 天长期记忆）
     返回「需要道晚安」的 open_id 列表（即当天聊过的用户）
   Gateway: 给列表中每个 open_id 推送「晚安啦！」（客服消息）
```

> 说明：当日工作记忆每天 0 点被淘汰；长期连续性由滚动 7 天的每日摘要承载。拼上下文时同时使用「7 天摘要 + 当天全量对话」。

---

## 4. gRPC 契约

`api/proto/agent.proto`：

```protobuf
syntax = "proto3";
package agent.v1;

service AgentService {
  // 生成一条春日回复（读写记忆在内部完成）
  rpc Reply(ReplyRequest) returns (ReplyResponse);
  // 每日 0 点维护：摘要 + 淘汰工作记忆 + 清理 7 天前摘要，返回道晚安名单
  rpc RunDailyMaintenance(MaintenanceRequest) returns (MaintenanceResult);
}

message ReplyRequest  { string open_id = 1; string text = 2; }
message ReplyResponse { string reply_text = 1; }

message MaintenanceRequest { string target_date = 1; } // YYYY-MM-DD，本次维护要处理的自然日
message MaintenanceResult  { repeated string greet_open_ids = 1; }
```

gRPC 为单向调用（Gateway→Agent），避免双向依赖。

---

## 5. 对外 RESTful API（Gateway）

### 5.1 微信回调（协议固定，非 REST）

| 方法 | 路径 | 用途 |
|------|------|------|
| GET | `/wechat` | 微信 URL 接入校验（返回 echostr） |
| POST | `/wechat` | 接收用户消息（XML）→ 立即 ack → 异步处理 |

### 5.2 RESTful 管理/测试 API（前缀 `/api/v1`，JSON）

| 方法 | 路径 | 用途 |
|------|------|------|
| POST | `/api/v1/chat` | **本地测试核心**：body `{"open_id":"u1","text":"你好"}` → 同步调用 Agent.Reply 返回回复（绕过微信，开发期联调） |
| GET | `/healthz` | 健康检查（Gateway 到 Agent gRPC 连通性；Agent 自检 DB） |

> Phase 2 再补记忆管理端点：`GET /api/v1/conversations/{open_id}/messages?limit=20`、`DELETE /api/v1/conversations/{open_id}/messages`。补这些端点前必须先在 Agent gRPC 契约中加入对应 RPC，Gateway 不直接访问数据库。

**RESTful 约定**：资源名用复数名词；HTTP 方法表达动作；统一 JSON 响应；正确状态码（200/201/400/404/500）；统一错误体 `{"error":{"code":"...","message":"..."}}`。

---

## 6. 数据模型（GORM / PostgreSQL）

```go
// 当日工作记忆：每天 00:00 淘汰
type Message struct {
    ID        uint      `gorm:"primaryKey"`
    OpenID    string    `gorm:"index:idx_msg_openid_created;size:64"`
    Role      string    `gorm:"size:16"`   // "user" | "assistant"
    Content   string    `gorm:"type:text"`
    CreatedAt time.Time `gorm:"index:idx_msg_openid_created"`
}

// 记忆库：每日摘要，保留 7 天后清除
type MemorySummary struct {
    ID          uint      `gorm:"primaryKey"`
    OpenID      string    `gorm:"index:idx_sum_openid_date;size:64"`
    SummaryDate time.Time `gorm:"index:idx_sum_openid_date"` // 摘要对应的日期
    Content     string    `gorm:"type:text"`
    CreatedAt   time.Time
}
```

- 复合索引 `(open_id, created_at)` / `(open_id, summary_date)` 保证按用户取历史/摘要高效。
- 拼上下文：`memory` 包提供 `BuildContext(open_id)` → 返回「最近 7 天摘要 + 当天全部消息」组成的消息序列。

---

## 7. 包结构（monorepo，两个 cmd 入口）

```
companion-ai/
├── api/proto/agent.proto         # gRPC 契约
├── gen/                          # protoc 生成代码
├── cmd/
│   ├── gateway/main.go           # Gin + 微信 + cron + gRPC client
│   └── agent/main.go             # gRPC server
├── internal/
│   ├── config/                   # 环境变量配置集中管理
│   ├── wechat/                   # [gateway] 微信协议层（独立，仅懂微信）
│   │   ├── verify.go             #   GET 校验 + POST 消息签名校验
│   │   ├── message.go            #   XML 解析/构建
│   │   ├── token.go              #   access_token 获取+内存缓存+刷新
│   │   └── kf.go                 #   客服消息接口：主动推送文本
│   ├── gateway/                  # [gateway] gin handlers + cron + grpc client
│   │   ├── wechat_handler.go
│   │   ├── api_handler.go
│   │   ├── cron.go
│   │   └── agent_client.go
│   ├── persona/                  # [agent] 凉宫春日 system prompt 常量（固定在代码）
│   ├── chat/                     # [agent] Eino 链：Reply(ctx, ctxMsgs, userText)
│   ├── summarize/                # [agent] 当日对话摘要（Eino/DeepSeek）
│   ├── memory/                   # [agent] GORM models + repo + BuildContext
│   │   ├── model.go
│   │   └── repo.go
│   └── agent/                    # [agent] grpc server 实现（编排 chat+memory+summarize）
├── log/                          # 固定日志目录（两个服务的日志都写在这里）
│   ├── gateway.log
│   └── agent.log
├── go.mod
└── .env.example                  # 配置示例
```

> **日志位置固定**：所有日志写入本项目根目录下的 `log/` 文件夹（Gateway → `log/gateway.log`，Agent → `log/agent.log`），不写系统目录、不写控制台为主。程序启动时若 `log/` 不存在则自动创建。

### 依赖方向（单向，无循环）

```
cmd/gateway → internal/gateway → wechat, config, gen(grpc client)
cmd/agent   → internal/agent   → chat, memory, summarize, persona, config, gen(grpc server)
chat        → persona, eino
summarize   → eino
memory      → gorm
wechat      → 标准库 + http 客户端（独立）
```

每个单元可独立回答「做什么 / 怎么用 / 依赖谁」。例如 `chat.Reply(ctx, contextMsgs, userText)` 给定上下文与新消息返回春日回复，依赖 Eino + persona，**不知道微信和数据库**，可单独 mock 测试。

---

## 8. 错误处理

| 场景 | 处理 |
|------|------|
| 微信签名校验失败 | 返回 403，不处理 |
| 微信重试（未及时回 success） | 收到即回 `success`，基本不触发；用 `MsgId` 去重防极端重复处理 |
| DeepSeek 超时/报错 | 推送春日口吻兜底语（如「哼，本小姐突然走神了，再说一遍！」），记日志 |
| access_token 失效（errcode 40001/42001） | 自动刷新 token 重试一次 |
| 客服消息 48h 窗口 | 实时回复与 0 点晚安均在窗口内，无需特殊处理 |
| gRPC 调用失败（Agent 不可达） | Gateway 记日志；实时场景推送兜底语；REST 场景返回 502 |
| DB 错误 | 记日志；REST 返回 500 |
| 异步任务超时 | DeepSeek 调用设 30s 超时（异步无 5 秒限制） |
| 0 点维护中单用户摘要失败 | 隔离单用户失败，不影响其他用户；记日志，该用户仍发晚安并淘汰工作记忆 |

---

## 9. 测试策略（实现阶段走 TDD，先写测试）

- **单元测试**：
  - `wechat`：签名校验（已知向量）、XML 解析/构建、token 缓存刷新（mock http）
  - `chat.Reply`：mock Eino 的 ChatModel，验证人设与上下文正确拼接
  - `summarize`：mock 模型，验证摘要输入构造
  - `memory.repo`：存取、当日历史取全量、7 天摘要范围、淘汰与清理逻辑
- **集成测试**：
  - `POST /api/v1/chat` 全链路（mock DeepSeek + 测试 Pg 库 + 真实 gRPC 本地回环），不依赖微信即可验证春日逻辑
  - `RunDailyMaintenance` 端到端：构造当天数据 → 触发 → 校验摘要写入、工作记忆清空、7 天前摘要被删、返回名单正确
- **手动验证**：部署到 `47.82.114.17`，微信扫测试号二维码实聊。

---

## 10. 配置项（环境变量）

| 变量 | 用途 |
|------|------|
| `WECHAT_TOKEN` | 测试号接口配置 Token（签名校验） |
| `WECHAT_APPID` / `WECHAT_APPSECRET` | 获取 access_token |
| `DEEPSEEK_API_KEY` | DeepSeek API Key |
| `DEEPSEEK_MODEL` | 模型 id（按官方最新名填；示例 `deepseek-v4-flash`） |
| `PG_DSN` | PostgreSQL 连接串 |
| `AGENT_GRPC_ADDR` | Agent gRPC 监听/连接地址 |
| `GATEWAY_HTTP_ADDR` | Gateway HTTP 监听地址（:80） |

> 日志路径不通过环境变量配置，**固定为项目根目录下的 `log/`**。

---

## 11. 范围与非目标（YAGNI）

- **本期范围**：文本聊天、双层记忆、0 点维护与道晚安、测试号接入、REST 测试接口、两服务 gRPC。
- **非目标（暂不做）**：图片/语音消息、多角色切换、用户级人设自定义、token 预算精确截断（先用「当天全量 + 7 天摘要」）、Web 前端、鉴权与多租户、容器编排。
