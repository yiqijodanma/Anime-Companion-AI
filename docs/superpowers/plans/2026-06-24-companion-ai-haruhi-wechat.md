# 陪伴 AI（凉宫春日）微信公众号 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个接入微信公众号测试号、性格固定为「凉宫春日」的陪伴聊天 AI，具备双层记忆（当日全量 + 7 天滚动摘要）与每日 0 点维护道晚安。

**Architecture:** Monorepo 两服务：Gateway（Gin，接微信 HTTP + 客服消息 + REST + cron）通过单向 gRPC 调用 Agent（Eino 对话链 + 记忆 + 摘要 + DeepSeek）。Gateway 收到微信消息立即回 `success`，后台 goroutine 调 Agent 生成回复，再用客服消息推送，绕开 5 秒超时。

**Tech Stack:** Go 1.21+、Eino（CloudWeGo，OpenAI 兼容适配器接 DeepSeek）、Gin、gRPC/protobuf、GORM + PostgreSQL、robfig/cron、标准库 `log/slog`。

## 2026-06-28 Feasibility Fixes（优先执行）

本节覆盖后文同名旧步骤。执行本计划前必须先应用这些修订：

- **仓库前置条件**：本计划应在仓库 `https://github.com/yiqijodanma/Anime-Companion-AI.git` 内执行；若本地不是 Git 仓库，先 `git clone`，不要在裸 `D:/Code/MyCode` 下直接 `go mod init`。
- **Windows 命令**：当前开发环境是 PowerShell。后文 `mkdir -p` 改用 `New-Item -ItemType Directory -Force ...`；`cat > file <<'EOF'` 改用编辑器或 `Set-Content`；也可明确切换到 Git Bash/WSL 后再执行原命令。
- **配置拆分**：把 `config.Load()` 拆成 `config.LoadGateway()` 与 `config.LoadAgent()`。Gateway 只校验 `WECHAT_TOKEN`、`WECHAT_APPID`、`WECHAT_APPSECRET`、`AGENT_GRPC_ADDR`、`GATEWAY_HTTP_ADDR`；Agent 只校验 `DEEPSEEK_API_KEY`、`DEEPSEEK_MODEL`、`PG_DSN`、`AGENT_GRPC_ADDR`。
- **DeepSeek 接入**：OpenAI 兼容 Base URL 使用 `https://api.deepseek.com`。`.env.example` 的模型示例改为 `DEEPSEEK_MODEL=deepseek-v4-flash`，不要默认写死旧 `deepseek-chat`。
- **每日维护日期**：`MaintenanceRequest` 增加 `target_date`（`YYYY-MM-DD`）。Gateway 00:00 调用时传“刚结束的一天”；Agent 按 `[target_date 00:00, target_date+1 00:00)` 查询、摘要、删除工作记忆。
- **REST 范围**：本期只实现 `POST /api/v1/chat` 与 `GET /healthz`。conversation 读写端点延期到 Phase 2，除非先扩展 Agent gRPC。
- **健康检查**：Gateway `/healthz` 必须真实检查 Agent gRPC；Agent 注册 gRPC health service，并检查 PostgreSQL `Ping`。
- **微信可靠性**：Gateway 对 `MsgId` 做短期 TTL 去重；客服消息遇到 token 失效错误（如 `40001`/`42001`）刷新 token 并重试一次。
- **gRPC 连接语义**：`grpc.NewClient` 创建成功不代表 Agent 可达；启动检查和 `/healthz` 需要主动调用 health RPC。

## Global Constraints

- Go module path 固定为 `companion-ai`；所有内部包导入以此为前缀。
- 日志固定写入项目根目录 `log/` 下（`log/gateway.log`、`log/agent.log`），启动时若目录不存在则自动创建；不依赖环境变量配置日志路径。
- 人设固定在代码中（`internal/persona`），不可由请求方修改。
- gRPC 单向：仅 Gateway→Agent，Agent 不回调 Gateway。
- DeepSeek 通过 Eino 的 OpenAI 兼容 ChatModel 适配器接入，`BaseURL=https://api.deepseek.com`。
- 记忆规则：当日工作记忆全量入上下文（不限条数）；记忆库摘要按 `summary_date` 保留 7 天；0 点淘汰当日原始消息。
- 单元测试用 SQLite 内存库（`gorm.io/driver/sqlite`）验证 GORM 仓储逻辑；生产用 PostgreSQL。
- 测试框架用标准库 `testing` + `github.com/stretchr/testify`。
- 「今天」的边界 = 服务器本地时区当日 0 点（`time.Now()` 截断到日）。

---

## File Structure

```
companion-ai/
├── api/proto/agent.proto              # gRPC 契约
├── gen/agentv1/                       # protoc 生成代码（agent.pb.go, agent_grpc.pb.go）
├── cmd/gateway/main.go                # Gateway 入口
├── cmd/agent/main.go                  # Agent 入口
├── internal/
│   ├── config/config.go               # 环境变量配置
│   ├── logging/logging.go             # log/ 目录 slog 文件 logger
│   ├── persona/persona.go             # 春日 system prompt 常量
│   ├── memory/model.go                # GORM 模型 Message / MemorySummary
│   ├── memory/repo.go                 # 仓储与上下文取数
│   ├── chat/chat.go                   # Eino 对话链 Reply
│   ├── summarize/summarize.go         # Eino 当日摘要
│   ├── agent/server.go                # gRPC server 实现
│   ├── wechat/verify.go               # 签名校验
│   ├── wechat/message.go              # XML 解析
│   ├── wechat/token.go                # access_token 管理
│   ├── wechat/kf.go                   # 客服消息推送
│   └── gateway/
│       ├── agent_client.go            # gRPC client 封装
│       ├── wechat_handler.go          # 微信回调 handler
│       ├── api_handler.go             # RESTful handler
│       └── cron.go                    # 0 点定时维护
├── log/                               # 运行时自动创建
├── go.mod
└── .env.example
```

---

## Task 1: 项目骨架 + 配置 + 日志

**Files:**
- Create: `go.mod`, `.env.example`
- Create: `internal/config/config.go`, `internal/config/config_test.go`
- Create: `internal/logging/logging.go`, `internal/logging/logging_test.go`

**Interfaces:**
- Produces:
  - `config.LoadGateway() (*config.GatewayConfig, error)` 返回并校验 Gateway 需要的微信、HTTP、Agent gRPC 配置。
  - `config.LoadAgent() (*config.AgentConfig, error)` 返回并校验 Agent 需要的 DeepSeek、PostgreSQL、gRPC 配置。
  - `logging.New(name string) (*slog.Logger, error)` 在 `log/<name>.log` 创建/追加文件并返回 slog logger。

- [ ] **Step 1: 初始化模块与目录**

```bash
cd D:/Code/MyCode
go mod init companion-ai
go get github.com/stretchr/testify@latest
New-Item -ItemType Directory -Force internal/config,internal/logging,cmd/gateway,cmd/agent,api/proto
```

- [ ] **Step 2: 写 config 的失败测试** — `internal/config/config_test.go`

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadReadsEnv(t *testing.T) {
	t.Setenv("WECHAT_TOKEN", "tok")
	t.Setenv("WECHAT_APPID", "appid")
	t.Setenv("WECHAT_APPSECRET", "secret")
	t.Setenv("DEEPSEEK_API_KEY", "dskey")
	t.Setenv("DEEPSEEK_MODEL", "deepseek-v4-flash")
	t.Setenv("PG_DSN", "postgres://localhost/db")
	t.Setenv("AGENT_GRPC_ADDR", "127.0.0.1:9090")
	t.Setenv("GATEWAY_HTTP_ADDR", ":80")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, "tok", cfg.WechatToken)
	require.Equal(t, "deepseek-v4-flash", cfg.DeepSeekModel)
	require.Equal(t, "127.0.0.1:9090", cfg.AgentGRPCAddr)
}

func TestLoadMissingRequiredFails(t *testing.T) {
	t.Setenv("WECHAT_TOKEN", "")
	t.Setenv("DEEPSEEK_API_KEY", "")
	_, err := Load()
	require.Error(t, err)
}
```

- [ ] **Step 3: 运行测试确认失败**

Run: `go test ./internal/config/...`
Expected: FAIL（`config.Load` 未定义 / 编译错误）

- [ ] **Step 4: 实现 config** — `internal/config/config.go`

```go
package config

import (
	"fmt"
	"os"
)

type Config struct {
	WechatToken     string
	WechatAppID     string
	WechatAppSecret string
	DeepSeekAPIKey  string
	DeepSeekModel   string
	PgDSN           string
	AgentGRPCAddr   string
	GatewayHTTPAddr string
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load 从环境变量读取配置。WechatToken 与 DeepSeekAPIKey 为必填。
func Load() (*Config, error) {
	cfg := &Config{
		WechatToken:     os.Getenv("WECHAT_TOKEN"),
		WechatAppID:     os.Getenv("WECHAT_APPID"),
		WechatAppSecret: os.Getenv("WECHAT_APPSECRET"),
		DeepSeekAPIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		DeepSeekModel:   env("DEEPSEEK_MODEL", "deepseek-v4-flash"),
		PgDSN:           os.Getenv("PG_DSN"),
		AgentGRPCAddr:   env("AGENT_GRPC_ADDR", "127.0.0.1:9090"),
		GatewayHTTPAddr: env("GATEWAY_HTTP_ADDR", ":80"),
	}
	if cfg.WechatToken == "" {
		return nil, fmt.Errorf("WECHAT_TOKEN is required")
	}
	if cfg.DeepSeekAPIKey == "" {
		return nil, fmt.Errorf("DEEPSEEK_API_KEY is required")
	}
	return cfg, nil
}
```

- [ ] **Step 5: 写 logging 失败测试** — `internal/logging/logging_test.go`

```go
package logging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(cwd)

	logger, err := New("agent")
	require.NoError(t, err)
	require.NotNil(t, logger)
	logger.Info("hello")

	_, statErr := os.Stat(filepath.Join(dir, "log", "agent.log"))
	require.NoError(t, statErr)
}
```

- [ ] **Step 6: 运行确认失败**

Run: `go test ./internal/logging/...`
Expected: FAIL（`logging.New` 未定义）

- [ ] **Step 7: 实现 logging** — `internal/logging/logging.go`

```go
package logging

import (
	"log/slog"
	"os"
	"path/filepath"
)

// New 在项目根目录 log/<name>.log 打开（追加）日志文件并返回 slog logger。
// 目录不存在则创建。
func New(name string) (*slog.Logger, error) {
	if err := os.MkdirAll("log", 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join("log", name+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo})), nil
}
```

- [ ] **Step 8: 运行全部测试确认通过**

Run: `go test ./internal/config/... ./internal/logging/...`
Expected: PASS

- [ ] **Step 9: 写 .env.example**

```bash
cat > .env.example <<'EOF'
WECHAT_TOKEN=your_test_account_token
WECHAT_APPID=your_test_account_appid
WECHAT_APPSECRET=your_test_account_appsecret
DEEPSEEK_API_KEY=sk-xxxx
DEEPSEEK_MODEL=deepseek-v4-flash
PG_DSN=postgres://user:pass@localhost:5432/companion?sslmode=disable
AGENT_GRPC_ADDR=127.0.0.1:9090
GATEWAY_HTTP_ADDR=:80
EOF
```

- [ ] **Step 10: 提交**

```bash
git add go.mod go.sum .env.example internal/config internal/logging
git commit -m "feat: project scaffold, config and file logging"
```

---

## Task 2: 记忆模型与仓储（GORM）

**Files:**
- Create: `internal/memory/model.go`, `internal/memory/repo.go`, `internal/memory/repo_test.go`

**Interfaces:**
- Consumes: 无（最底层）
- Produces:
  - 模型 `memory.Message{ID uint; OpenID string; Role string; Content string; CreatedAt time.Time}`
  - 模型 `memory.MemorySummary{ID uint; OpenID string; SummaryDate time.Time; Content string; CreatedAt time.Time}`
  - `memory.NewRepo(db *gorm.DB) (*Repo, error)`（内部 AutoMigrate）
  - `(*Repo) SaveMessage(openID, role, content string) error`
  - `(*Repo) TodayMessages(openID string) ([]Message, error)`（按 CreatedAt 升序）
  - `(*Repo) RecentSummaries(openID string) ([]MemorySummary, error)`（最近 7 天，按 SummaryDate 升序）
  - `(*Repo) SaveSummary(openID string, date time.Time, content string) error`
  - `(*Repo) ActiveOpenIDsForDate(day time.Time) ([]string, error)`
  - `(*Repo) DeleteTodayMessages(openID string) error`
  - `(*Repo) PurgeSummariesOlderThan(cutoff time.Time) error`
  - 角色常量 `memory.RoleUser = "user"`、`memory.RoleAssistant = "assistant"`

- [ ] **Step 1: 安装依赖**

```bash
go get gorm.io/gorm@latest gorm.io/driver/sqlite@latest gorm.io/driver/postgres@latest
```

- [ ] **Step 2: 写仓储失败测试** — `internal/memory/repo_test.go`

```go
package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	repo, err := NewRepo(db)
	require.NoError(t, err)
	return repo
}

func TestSaveAndTodayMessages(t *testing.T) {
	repo := newTestRepo(t)
	require.NoError(t, repo.SaveMessage("u1", RoleUser, "你好"))
	require.NoError(t, repo.SaveMessage("u1", RoleAssistant, "哼，是你啊"))
	require.NoError(t, repo.SaveMessage("u2", RoleUser, "在吗"))

	msgs, err := repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "你好", msgs[0].Content)
	require.Equal(t, RoleAssistant, msgs[1].Role)
}

func TestActiveOpenIDsForDate(t *testing.T) {
	repo := newTestRepo(t)
	require.NoError(t, repo.SaveMessage("u1", RoleUser, "a"))
	require.NoError(t, repo.SaveMessage("u2", RoleUser, "b"))
	require.NoError(t, repo.SaveMessage("u1", RoleAssistant, "c"))

	ids, err := repo.ActiveOpenIDsForDate(time.Now())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"u1", "u2"}, ids)
}

func TestDeleteTodayMessages(t *testing.T) {
	repo := newTestRepo(t)
	require.NoError(t, repo.SaveMessage("u1", RoleUser, "a"))
	require.NoError(t, repo.DeleteTodayMessages("u1"))
	msgs, err := repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Empty(t, msgs)
}

func TestRecentSummariesWindow(t *testing.T) {
	repo := newTestRepo(t)
	now := time.Now()
	require.NoError(t, repo.SaveSummary("u1", now.AddDate(0, 0, -1), "昨天聊了社团"))
	require.NoError(t, repo.SaveSummary("u1", now.AddDate(0, 0, -10), "十天前"))

	sums, err := repo.RecentSummaries("u1")
	require.NoError(t, err)
	require.Len(t, sums, 1)
	require.Equal(t, "昨天聊了社团", sums[0].Content)
}

func TestPurgeSummariesOlderThan(t *testing.T) {
	repo := newTestRepo(t)
	now := time.Now()
	require.NoError(t, repo.SaveSummary("u1", now.AddDate(0, 0, -2), "近的"))
	require.NoError(t, repo.SaveSummary("u1", now.AddDate(0, 0, -9), "旧的"))
	require.NoError(t, repo.PurgeSummariesOlderThan(now.AddDate(0, 0, -7)))

	var count int64
	require.NoError(t, repo.DB().Model(&MemorySummary{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/memory/...`
Expected: FAIL（`NewRepo` 等未定义）

- [ ] **Step 4: 实现模型** — `internal/memory/model.go`

```go
package memory

import "time"

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message 当日工作记忆，每天 0 点淘汰。
type Message struct {
	ID        uint      `gorm:"primaryKey"`
	OpenID    string    `gorm:"index:idx_msg_openid_created;size:64"`
	Role      string    `gorm:"size:16"`
	Content   string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"index:idx_msg_openid_created"`
}

// MemorySummary 记忆库，每日摘要，保留 7 天。
type MemorySummary struct {
	ID          uint      `gorm:"primaryKey"`
	OpenID      string    `gorm:"index:idx_sum_openid_date;size:64"`
	SummaryDate time.Time `gorm:"index:idx_sum_openid_date"`
	Content     string    `gorm:"type:text"`
	CreatedAt   time.Time
}
```

- [ ] **Step 5: 实现仓储** — `internal/memory/repo.go`

```go
package memory

import (
	"time"

	"gorm.io/gorm"
)

type Repo struct {
	db *gorm.DB
}

func NewRepo(db *gorm.DB) (*Repo, error) {
	if err := db.AutoMigrate(&Message{}, &MemorySummary{}); err != nil {
		return nil, err
	}
	return &Repo{db: db}, nil
}

// DB 暴露底层连接，供 healthz 与测试使用。
func (r *Repo) DB() *gorm.DB { return r.db }

func startOfToday() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, n.Location())
}

func dayRange(day time.Time) (time.Time, time.Time) {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	return start, start.AddDate(0, 0, 1)
}

func (r *Repo) SaveMessage(openID, role, content string) error {
	return r.db.Create(&Message{
		OpenID:    openID,
		Role:      role,
		Content:   content,
		CreatedAt: time.Now(),
	}).Error
}

func (r *Repo) TodayMessages(openID string) ([]Message, error) {
	var msgs []Message
	err := r.db.Where("open_id = ? AND created_at >= ?", openID, startOfToday()).
		Order("created_at asc").Find(&msgs).Error
	return msgs, err
}

func (r *Repo) RecentSummaries(openID string) ([]MemorySummary, error) {
	cutoff := startOfToday().AddDate(0, 0, -7)
	var sums []MemorySummary
	err := r.db.Where("open_id = ? AND summary_date >= ?", openID, cutoff).
		Order("summary_date asc").Find(&sums).Error
	return sums, err
}

func (r *Repo) SaveSummary(openID string, date time.Time, content string) error {
	return r.db.Create(&MemorySummary{
		OpenID:      openID,
		SummaryDate: date,
		Content:     content,
		CreatedAt:   time.Now(),
	}).Error
}

func (r *Repo) ActiveOpenIDsForDate(day time.Time) ([]string, error) {
	start, end := dayRange(day)
	var ids []string
	err := r.db.Model(&Message{}).
		Where("created_at >= ? AND created_at < ?", start, end).
		Distinct().Pluck("open_id", &ids).Error
	return ids, err
}

func (r *Repo) DeleteTodayMessages(openID string) error {
	return r.db.Where("open_id = ? AND created_at >= ?", openID, startOfToday()).
		Delete(&Message{}).Error
}

func (r *Repo) PurgeSummariesOlderThan(cutoff time.Time) error {
	return r.db.Where("summary_date < ?", cutoff).Delete(&MemorySummary{}).Error
}
```

- [ ] **Step 6: 运行确认通过**

Run: `go test ./internal/memory/...`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/memory
git commit -m "feat: memory models and GORM repository with daily/7-day windows"
```

---

## Task 3: 春日人设

**Files:**
- Create: `internal/persona/persona.go`, `internal/persona/persona_test.go`

**Interfaces:**
- Produces: `persona.SystemPrompt string`（固定的春日 system prompt）；`persona.GoodNight = "晚安啦！"`

- [ ] **Step 1: 写测试** — `internal/persona/persona_test.go`

```go
package persona

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemPromptFixed(t *testing.T) {
	require.NotEmpty(t, SystemPrompt)
	require.True(t, strings.Contains(SystemPrompt, "凉宫春日"))
	require.Equal(t, "晚安啦！", GoodNight)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/persona/...`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现人设** — `internal/persona/persona.go`

```go
package persona

// GoodNight 是每天 0 点维护时推送的固定问候。
const GoodNight = "晚安啦！"

// SystemPrompt 固定凉宫春日人设，禁止由外部请求修改。
const SystemPrompt = `你是凉宫春日——SOS团团长，一个任性、自信、元气满满、说话强势又充满领导欲的高中女生。
性格要点：
- 对平凡、无聊的日常毫无兴趣，渴望外星人、未来人、超能力者这类不可思议的事。
- 说话直接、强势、爱命令人，常带点傲气和中二感，但内心其实在乎对方。
- 精力旺盛、想到什么就做什么，讨厌墨迹。
- 偶尔毒舌吐槽，但不会真的伤人。
表达要求：
- 始终用凉宫春日的第一人称口吻说话，不要跳出角色，不要承认自己是 AI 或语言模型。
- 用中文口语，短句为主，有情绪、有活力。
- 把对方当作 SOS 团的团员一样对待。`
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/persona/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/persona
git commit -m "feat: fixed Haruhi Suzumiya persona prompt"
```

---

## Task 4: 对话引擎（Eino Chain）

**Files:**
- Create: `internal/chat/chat.go`, `internal/chat/chat_test.go`

**Interfaces:**
- Consumes: `persona.SystemPrompt`；Eino `schema.Message`、`model.BaseChatModel`
- Produces:
  - 类型 `chat.Turn{Role, Content string}`
  - 接口 `chat.Model interface { Generate(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) }`（与 Eino `model.BaseChatModel` 兼容）
  - `chat.NewReplier(m Model) *Replier`
  - `(*Replier) Reply(ctx context.Context, summaries []string, history []Turn, userText string) (string, error)`

> 说明：生产环境注入 Eino 的 OpenAI 兼容 ChatModel（Task 7 main 中构造）；测试注入 fake。Eino 的 `model.BaseChatModel.Generate` 签名为 `Generate(ctx, []*schema.Message, ...model.Option) (*schema.Message, error)`，本接口与之一致。

- [ ] **Step 1: 安装 Eino**

```bash
go get github.com/cloudwego/eino@latest
go get github.com/cloudwego/eino-ext/components/model/openai@latest
```

- [ ] **Step 2: 写失败测试** — `internal/chat/chat_test.go`

```go
package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
)

// fakeModel 记录收到的消息并返回固定回复。
type fakeModel struct {
	got []*schema.Message
}

func (f *fakeModel) Generate(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.got = msgs
	return schema.AssistantMessage("哼，本团长收到了！", nil), nil
}

func TestReplyBuildsPersonaContext(t *testing.T) {
	fm := &fakeModel{}
	r := NewReplier(fm)

	out, err := r.Reply(context.Background(),
		[]string{"昨天聊了棒球比赛"},
		[]Turn{{Role: "user", Content: "早上好"}, {Role: "assistant", Content: "早，团员！"}},
		"今天去哪探险")
	require.NoError(t, err)
	require.Equal(t, "哼，本团长收到了！", out)

	// 第一条必须是春日人设 system 消息
	require.Equal(t, schema.System, fm.got[0].Role)
	require.True(t, strings.Contains(fm.got[0].Content, "凉宫春日"))
	// 摘要应作为上下文出现
	joined := ""
	for _, m := range fm.got {
		joined += string(m.Role) + ":" + m.Content + "\n"
	}
	require.True(t, strings.Contains(joined, "昨天聊了棒球比赛"))
	require.True(t, strings.Contains(joined, "早上好"))
	// 最后一条是本次用户输入
	last := fm.got[len(fm.got)-1]
	require.Equal(t, schema.User, last.Role)
	require.Equal(t, "今天去哪探险", last.Content)
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/chat/...`
Expected: FAIL（未定义）

- [ ] **Step 4: 实现对话引擎** — `internal/chat/chat.go`

```go
package chat

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"companion-ai/internal/persona"
)

type Turn struct {
	Role    string // "user" | "assistant"
	Content string
}

// Model 与 Eino model.BaseChatModel 的 Generate 方法兼容。
type Model interface {
	Generate(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error)
}

type Replier struct {
	model Model
}

func NewReplier(m Model) *Replier { return &Replier{model: m} }

// Reply 拼装 [春日人设] + [7天摘要] + [当日历史] + [本次输入] 调用模型。
func (r *Replier) Reply(ctx context.Context, summaries []string, history []Turn, userText string) (string, error) {
	msgs := []*schema.Message{schema.SystemMessage(persona.SystemPrompt)}

	if len(summaries) > 0 {
		recall := "【这是你对这位团员过去几天的记忆，请自然地延续，不要直接复述】\n" +
			strings.Join(summaries, "\n")
		msgs = append(msgs, schema.SystemMessage(recall))
	}

	for _, t := range history {
		switch t.Role {
		case "assistant":
			msgs = append(msgs, schema.AssistantMessage(t.Content, nil))
		default:
			msgs = append(msgs, schema.UserMessage(t.Content))
		}
	}

	msgs = append(msgs, schema.UserMessage(userText))

	out, err := r.model.Generate(ctx, msgs)
	if err != nil {
		return "", err
	}
	return out.Content, nil
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/chat/...`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/chat
git commit -m "feat: Eino-based reply engine with persona and memory context"
```

---

## Task 5: 当日摘要引擎

**Files:**
- Create: `internal/summarize/summarize.go`, `internal/summarize/summarize_test.go`

**Interfaces:**
- Consumes: `chat.Model`、`chat.Turn`、Eino `schema`
- Produces:
  - `summarize.NewSummarizer(m chat.Model) *Summarizer`
  - `(*Summarizer) Summarize(ctx context.Context, history []chat.Turn) (string, error)`

- [ ] **Step 1: 写失败测试** — `internal/summarize/summarize_test.go`

```go
package summarize

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"

	"companion-ai/internal/chat"
)

type fakeModel struct{ got []*schema.Message }

func (f *fakeModel) Generate(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.got = msgs
	return schema.AssistantMessage("团员今天想去海边探险。", nil), nil
}

func TestSummarizeIncludesHistory(t *testing.T) {
	fm := &fakeModel{}
	s := NewSummarizer(fm)
	out, err := s.Summarize(context.Background(), []chat.Turn{
		{Role: "user", Content: "我想去海边"},
		{Role: "assistant", Content: "好主意！"},
	})
	require.NoError(t, err)
	require.Equal(t, "团员今天想去海边探险。", out)

	joined := ""
	for _, m := range fm.got {
		joined += m.Content + "\n"
	}
	require.True(t, strings.Contains(joined, "我想去海边"))
}

func TestSummarizeEmptyReturnsEmpty(t *testing.T) {
	s := NewSummarizer(&fakeModel{})
	out, err := s.Summarize(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "", out)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/summarize/...`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现摘要引擎** — `internal/summarize/summarize.go`

```go
package summarize

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"

	"companion-ai/internal/chat"
)

const instruction = `请把下面这位团员今天和凉宫春日的对话，整理成一段简洁的第三人称记忆摘要，
只保留对未来聊天有用的关键信息（对方的名字、喜好、发生的事、约定、情绪），
不超过 150 字，直接输出摘要正文，不要加任何前缀。`

type Summarizer struct {
	model chat.Model
}

func NewSummarizer(m chat.Model) *Summarizer { return &Summarizer{model: m} }

// Summarize 把当日对话压缩成一段记忆摘要；空对话返回空串。
func (s *Summarizer) Summarize(ctx context.Context, history []chat.Turn) (string, error) {
	if len(history) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, t := range history {
		who := "团员"
		if t.Role == "assistant" {
			who = "春日"
		}
		b.WriteString(who)
		b.WriteString("：")
		b.WriteString(t.Content)
		b.WriteString("\n")
	}
	msgs := []*schema.Message{
		schema.SystemMessage(instruction),
		schema.UserMessage(b.String()),
	}
	out, err := s.model.Generate(ctx, msgs)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Content), nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/summarize/...`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/summarize
git commit -m "feat: daily conversation summarizer"
```

---

## Task 6: gRPC 契约与代码生成

**Files:**
- Create: `api/proto/agent.proto`
- Create: `gen/agentv1/`（生成）
- Create: `buf.gen.yaml`（或用 protoc 命令）

**Interfaces:**
- Produces: 包 `agentv1`，含 `AgentServiceServer`/`AgentServiceClient`、`ReplyRequest{OpenId, Text}`、`ReplyResponse{ReplyText}`、`MaintenanceRequest{}`、`MaintenanceResult{GreetOpenIds []string}`

- [ ] **Step 1: 安装工具链**

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
go get google.golang.org/grpc@latest google.golang.org/protobuf@latest
# 确保已安装 protoc（https://github.com/protocolbuffers/protobuf/releases），且上面两个插件在 PATH
```

- [ ] **Step 2: 写 proto** — `api/proto/agent.proto`

```protobuf
syntax = "proto3";
package agent.v1;

option go_package = "companion-ai/gen/agentv1;agentv1";

service AgentService {
  rpc Reply(ReplyRequest) returns (ReplyResponse);
  rpc RunDailyMaintenance(MaintenanceRequest) returns (MaintenanceResult);
}

message ReplyRequest {
  string open_id = 1;
  string text = 2;
}
message ReplyResponse {
  string reply_text = 1;
}
message MaintenanceRequest {
  // YYYY-MM-DD，本次维护要处理的自然日。00:00 触发时传刚结束的一天。
  string target_date = 1;
}
message MaintenanceResult {
  repeated string greet_open_ids = 1;
}
```

- [ ] **Step 3: 生成代码**

```bash
mkdir -p gen/agentv1
protoc --go_out=. --go_opt=module=companion-ai \
       --go-grpc_out=. --go-grpc_opt=module=companion-ai \
       api/proto/agent.proto
```

Expected: 生成 `gen/agentv1/agent.pb.go` 与 `gen/agentv1/agent_grpc.pb.go`

- [ ] **Step 4: 验证编译**

Run: `go build ./gen/...`
Expected: 无错误

- [ ] **Step 5: 提交**

```bash
git add api/proto gen go.mod go.sum
git commit -m "feat: gRPC AgentService contract and generated code"
```

---

## Task 7: Agent gRPC 服务 + 入口

**Files:**
- Create: `internal/agent/server.go`, `internal/agent/server_test.go`
- Create: `cmd/agent/main.go`

**Interfaces:**
- Consumes: `memory.Repo`、`chat.Replier`、`summarize.Summarizer`、`agentv1`、`persona`
- Produces:
  - `agent.NewServer(repo *memory.Repo, replier *chat.Replier, sum *summarize.Summarizer) *Server`
  - `(*Server) Reply(ctx, *agentv1.ReplyRequest) (*agentv1.ReplyResponse, error)`
  - `(*Server) RunDailyMaintenance(ctx, *agentv1.MaintenanceRequest) (*agentv1.MaintenanceResult, error)`
  - `Server` 嵌入 `agentv1.UnimplementedAgentServiceServer`

> Reply 内部职责：取 `RecentSummaries` + `TodayMessages` → 先存用户消息 → `chat.Reply` → 存 assistant 消息 → 返回。
> RunDailyMaintenance 职责：解析 `target_date` → `ActiveOpenIDsForDate(targetDate)` → 逐用户 `MessagesForDate`→`Summarize`→`SaveSummary`→`DeleteMessagesForDate`（单用户失败隔离，记 error 不中断）→ 最后 `PurgeSummariesOlderThan(targetDate.AddDate(0,0,-7))` → 返回道晚安名单（即目标日期活跃用户）。

- [ ] **Step 1: 写 server 失败测试** — `internal/agent/server_test.go`

```go
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/chat"
	"companion-ai/internal/memory"
	"companion-ai/internal/summarize"
)

type fakeModel struct{ reply string }

func (f *fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(f.reply, nil), nil
}

func newTestServer(t *testing.T, reply string) (*Server, *memory.Repo) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	repo, err := memory.NewRepo(db)
	require.NoError(t, err)
	fm := &fakeModel{reply: reply}
	return NewServer(repo, chat.NewReplier(fm), summarize.NewSummarizer(fm)), repo
}

func TestReplyPersistsBothMessages(t *testing.T) {
	srv, repo := newTestServer(t, "哼，知道了！")
	resp, err := srv.Reply(context.Background(), &agentv1.ReplyRequest{OpenId: "u1", Text: "你好"})
	require.NoError(t, err)
	require.Equal(t, "哼，知道了！", resp.ReplyText)

	msgs, err := repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, memory.RoleUser, msgs[0].Role)
	require.Equal(t, memory.RoleAssistant, msgs[1].Role)
	require.Equal(t, "哼，知道了！", msgs[1].Content)
}

func TestRunDailyMaintenance(t *testing.T) {
	srv, repo := newTestServer(t, "今天的摘要内容")
	require.NoError(t, repo.SaveMessage("u1", memory.RoleUser, "我喜欢棒球"))
	require.NoError(t, repo.SaveMessage("u2", memory.RoleUser, "在吗"))

	res, err := srv.RunDailyMaintenance(context.Background(), &agentv1.MaintenanceRequest{
		TargetDate: time.Now().Format("2006-01-02"),
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"u1", "u2"}, res.GreetOpenIds)

	// 工作记忆已清空
	msgs, _ := repo.TodayMessages("u1")
	require.Empty(t, msgs)
	// 摘要已写入
	sums, _ := repo.RecentSummaries("u1")
	require.Len(t, sums, 1)
	require.Equal(t, "今天的摘要内容", sums[0].Content)
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/agent/...`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现 server** — `internal/agent/server.go`

```go
package agent

import (
	"context"
	"log/slog"
	"time"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/chat"
	"companion-ai/internal/memory"
	"companion-ai/internal/summarize"
)

type Server struct {
	agentv1.UnimplementedAgentServiceServer
	repo    *memory.Repo
	replier *chat.Replier
	sum     *summarize.Summarizer
	log     *slog.Logger
}

func NewServer(repo *memory.Repo, replier *chat.Replier, sum *summarize.Summarizer) *Server {
	return &Server{repo: repo, replier: replier, sum: sum, log: slog.Default()}
}

// WithLogger 可选注入 logger。
func (s *Server) WithLogger(l *slog.Logger) *Server { s.log = l; return s }

func toTurns(msgs []memory.Message) []chat.Turn {
	turns := make([]chat.Turn, 0, len(msgs))
	for _, m := range msgs {
		turns = append(turns, chat.Turn{Role: m.Role, Content: m.Content})
	}
	return turns
}

func (s *Server) Reply(ctx context.Context, req *agentv1.ReplyRequest) (*agentv1.ReplyResponse, error) {
	summaries, err := s.repo.RecentSummaries(req.OpenId)
	if err != nil {
		return nil, err
	}
	sumTexts := make([]string, 0, len(summaries))
	for _, x := range summaries {
		sumTexts = append(sumTexts, x.Content)
	}

	history, err := s.repo.TodayMessages(req.OpenId)
	if err != nil {
		return nil, err
	}

	if err := s.repo.SaveMessage(req.OpenId, memory.RoleUser, req.Text); err != nil {
		return nil, err
	}

	reply, err := s.replier.Reply(ctx, sumTexts, toTurns(history), req.Text)
	if err != nil {
		return nil, err
	}

	if err := s.repo.SaveMessage(req.OpenId, memory.RoleAssistant, reply); err != nil {
		return nil, err
	}
	return &agentv1.ReplyResponse{ReplyText: reply}, nil
}

func (s *Server) RunDailyMaintenance(ctx context.Context, req *agentv1.MaintenanceRequest) (*agentv1.MaintenanceResult, error) {
	targetDate, err := time.ParseInLocation("2006-01-02", req.TargetDate, time.Local)
	if err != nil {
		return nil, err
	}
	ids, err := s.repo.ActiveOpenIDsForDate(targetDate)
	if err != nil {
		return nil, err
	}
	date := targetDate
	greet := make([]string, 0, len(ids))

	for _, id := range ids {
		msgs, err := s.repo.TodayMessages(id)
		if err != nil {
			s.log.Error("maintenance: load messages failed", "open_id", id, "err", err)
			continue
		}
		summary, err := s.sum.Summarize(ctx, toTurns(msgs))
		if err != nil {
			s.log.Error("maintenance: summarize failed", "open_id", id, "err", err)
		} else if summary != "" {
			if err := s.repo.SaveSummary(id, date, summary); err != nil {
				s.log.Error("maintenance: save summary failed", "open_id", id, "err", err)
			}
		}
		if err := s.repo.DeleteTodayMessages(id); err != nil {
			s.log.Error("maintenance: delete messages failed", "open_id", id, "err", err)
		}
		greet = append(greet, id)
	}

	if err := s.repo.PurgeSummariesOlderThan(targetDate.AddDate(0, 0, -7)); err != nil {
		s.log.Error("maintenance: purge old summaries failed", "err", err)
	}
	return &agentv1.MaintenanceResult{GreetOpenIds: greet}, nil
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/agent/...`
Expected: PASS

- [ ] **Step 5: 写 Agent 入口** — `cmd/agent/main.go`

```go
package main

import (
	"context"
	"net"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"google.golang.org/grpc"
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
	repo, err := memory.NewRepo(db)
	if err != nil {
		log.Error("repo init failed", "err", err)
		panic(err)
	}

	// DeepSeek 走 Eino 的 OpenAI 兼容适配器。
	baseURL := "https://api.deepseek.com"
	cm, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		APIKey:  cfg.DeepSeekAPIKey,
		Model:   cfg.DeepSeekModel,
		BaseURL: baseURL,
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
	gs := grpc.NewServer()
	agentv1.RegisterAgentServiceServer(gs, srv)
	log.Info("agent grpc serving", "addr", cfg.AgentGRPCAddr)
	if err := gs.Serve(lis); err != nil {
		log.Error("serve failed", "err", err)
		panic(err)
	}
}
```

> **集成点校验**：执行前用 `go doc github.com/cloudwego/eino-ext/components/model/openai.ChatModelConfig` 确认字段名（`APIKey`/`Model`/`BaseURL`）与构造函数签名，按当前版本微调。其余逻辑代码不受影响。

- [ ] **Step 6: 编译确认**

Run: `go build ./cmd/agent/...`
Expected: 无错误（如 openai 字段名不符按上一步调整）

- [ ] **Step 7: 提交**

```bash
git add internal/agent cmd/agent
git commit -m "feat: agent gRPC server (reply + daily maintenance) and entrypoint"
```

---

## Task 8: 微信协议层

**Files:**
- Create: `internal/wechat/verify.go`, `internal/wechat/verify_test.go`
- Create: `internal/wechat/message.go`, `internal/wechat/message_test.go`
- Create: `internal/wechat/token.go`, `internal/wechat/token_test.go`
- Create: `internal/wechat/kf.go`, `internal/wechat/kf_test.go`

**Interfaces:**
- Produces:
  - `wechat.CheckSignature(token, timestamp, nonce, signature string) bool`
  - `wechat.IncomingMessage{ToUserName, FromUserName, MsgType, Content, MsgID string}`；`wechat.ParseIncoming(body []byte) (*IncomingMessage, error)`
  - `wechat.NewTokenManager(appID, appSecret string, httpClient *http.Client) *TokenManager`；`(*TokenManager) Get(ctx) (string, error)`（带过期缓存）；可注入 `tokenManager.Endpoint` 以便测试
  - `wechat.NewKFClient(httpClient *http.Client) *KFClient`；`(*KFClient) SendText(ctx context.Context, token, openID, text string) error`；可注入 `Endpoint`

- [ ] **Step 1: 写签名校验测试** — `internal/wechat/verify_test.go`

```go
package wechat

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func reference(token, ts, nonce string) string {
	parts := []string{token, ts, nonce}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(h[:])
}

func TestCheckSignatureValid(t *testing.T) {
	token, ts, nonce := "mytoken", "1717000000", "rand123"
	sig := reference(token, ts, nonce)
	require.True(t, CheckSignature(token, ts, nonce, sig))
}

func TestCheckSignatureInvalid(t *testing.T) {
	require.False(t, CheckSignature("mytoken", "1717000000", "rand123", "deadbeef"))
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/wechat/... -run Signature`
Expected: FAIL（未定义）

- [ ] **Step 3: 实现签名校验** — `internal/wechat/verify.go`

```go
package wechat

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strings"
)

// CheckSignature 按微信规则校验：sort(token,timestamp,nonce) 拼接后 sha1 == signature。
func CheckSignature(token, timestamp, nonce, signature string) bool {
	parts := []string{token, timestamp, nonce}
	sort.Strings(parts)
	h := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(h[:]) == signature
}
```

- [ ] **Step 4: 写消息解析测试** — `internal/wechat/message_test.go`

```go
package wechat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseIncomingText(t *testing.T) {
	xml := []byte(`<xml>
<ToUserName><![CDATA[gh_abc]]></ToUserName>
<FromUserName><![CDATA[user_open_id]]></FromUserName>
<CreateTime>1717000000</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[你好春日]]></Content>
<MsgId>1234567890</MsgId>
</xml>`)
	msg, err := ParseIncoming(xml)
	require.NoError(t, err)
	require.Equal(t, "user_open_id", msg.FromUserName)
	require.Equal(t, "text", msg.MsgType)
	require.Equal(t, "你好春日", msg.Content)
	require.Equal(t, "1234567890", msg.MsgID)
}
```

- [ ] **Step 5: 运行确认失败**

Run: `go test ./internal/wechat/... -run ParseIncoming`
Expected: FAIL

- [ ] **Step 6: 实现消息解析** — `internal/wechat/message.go`

```go
package wechat

import "encoding/xml"

type IncomingMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgID        string   `xml:"MsgId"`
}

func ParseIncoming(body []byte) (*IncomingMessage, error) {
	var m IncomingMessage
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
```

- [ ] **Step 7: 写 token 管理测试** — `internal/wechat/token_test.go`

```go
package wechat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenManagerCaches(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte(`{"access_token":"ACCESS_TOKEN_1","expires_in":7200}`))
	}))
	defer ts.Close()

	tm := NewTokenManager("appid", "secret", ts.Client())
	tm.Endpoint = ts.URL

	tok, err := tm.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ACCESS_TOKEN_1", tok)

	tok2, err := tm.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, "ACCESS_TOKEN_1", tok2)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls)) // 第二次走缓存
}

func TestTokenManagerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errcode":40013,"errmsg":"invalid appid"}`))
	}))
	defer ts.Close()
	tm := NewTokenManager("appid", "secret", ts.Client())
	tm.Endpoint = ts.URL
	_, err := tm.Get(context.Background())
	require.Error(t, err)
}
```

- [ ] **Step 8: 运行确认失败**

Run: `go test ./internal/wechat/... -run Token`
Expected: FAIL

- [ ] **Step 9: 实现 token 管理** — `internal/wechat/token.go`

```go
package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type TokenManager struct {
	appID     string
	appSecret string
	client    *http.Client
	Endpoint  string // 默认微信接口，可在测试中覆盖

	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewTokenManager(appID, appSecret string, client *http.Client) *TokenManager {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &TokenManager{
		appID:     appID,
		appSecret: appSecret,
		client:    client,
		Endpoint:  "https://api.weixin.qq.com/cgi-bin/token",
	}
}

type tokenResp struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

// Get 返回有效 access_token，过期前 60s 自动刷新。
func (tm *TokenManager) Get(ctx context.Context) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.token != "" && time.Now().Before(tm.expires) {
		return tm.token, nil
	}

	u, _ := url.Parse(tm.Endpoint)
	q := u.Query()
	q.Set("grant_type", "client_credential")
	q.Set("appid", tm.appID)
	q.Set("secret", tm.appSecret)
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	resp, err := tm.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var tr tokenResp
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", err
	}
	if tr.ErrCode != 0 || tr.AccessToken == "" {
		return "", fmt.Errorf("wechat token error %d: %s", tr.ErrCode, tr.ErrMsg)
	}
	tm.token = tr.AccessToken
	tm.expires = time.Now().Add(time.Duration(tr.ExpiresIn-60) * time.Second)
	return tm.token, nil
}
```

- [ ] **Step 10: 写客服消息测试** — `internal/wechat/kf_test.go`

```go
package wechat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSendText(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		require.Equal(t, "ACCESS_TOKEN_1", r.URL.Query().Get("access_token"))
		w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer ts.Close()

	kf := NewKFClient(ts.Client())
	kf.Endpoint = ts.URL
	err := kf.SendText(context.Background(), "ACCESS_TOKEN_1", "open_id_1", "晚安啦！")
	require.NoError(t, err)
	require.Equal(t, "open_id_1", gotBody["touser"])
	require.Equal(t, "text", gotBody["msgtype"])
}

func TestSendTextWeChatError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errcode":45015,"errmsg":"response out of time limit"}`))
	}))
	defer ts.Close()
	kf := NewKFClient(ts.Client())
	kf.Endpoint = ts.URL
	err := kf.SendText(context.Background(), "tok", "open_id_1", "hi")
	require.Error(t, err)
}
```

- [ ] **Step 11: 运行确认失败**

Run: `go test ./internal/wechat/... -run SendText`
Expected: FAIL

- [ ] **Step 12: 实现客服消息** — `internal/wechat/kf.go`

```go
package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type KFClient struct {
	client   *http.Client
	Endpoint string
}

func NewKFClient(client *http.Client) *KFClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &KFClient{
		client:   client,
		Endpoint: "https://api.weixin.qq.com/cgi-bin/message/custom/send",
	}
}

type kfErrResp struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// SendText 通过客服消息接口主动推送文本给用户。
func (c *KFClient) SendText(ctx context.Context, token, openID, text string) error {
	payload := map[string]any{
		"touser":  openID,
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	}
	buf, _ := json.Marshal(payload)

	url := c.Endpoint + "?access_token=" + token
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var er kfErrResp
	if err := json.Unmarshal(body, &er); err != nil {
		return err
	}
	if er.ErrCode != 0 {
		return fmt.Errorf("wechat kf send error %d: %s", er.ErrCode, er.ErrMsg)
	}
	return nil
}
```

- [ ] **Step 13: 运行 wechat 全部测试确认通过**

Run: `go test ./internal/wechat/...`
Expected: PASS

- [ ] **Step 14: 提交**

```bash
git add internal/wechat
git commit -m "feat: wechat protocol layer (signature, xml, token, customer-service push)"
```

---

## Task 9: Gateway（gRPC client + 微信回调 + REST + cron）

**Files:**
- Create: `internal/gateway/agent_client.go`
- Create: `internal/gateway/wechat_handler.go`, `internal/gateway/wechat_handler_test.go`
- Create: `internal/gateway/api_handler.go`, `internal/gateway/api_handler_test.go`
- Create: `internal/gateway/cron.go`, `internal/gateway/cron_test.go`
- Create: `cmd/gateway/main.go`

**Interfaces:**
- Consumes: `agentv1.AgentServiceClient`、`wechat.*`、`config`
- Produces:
  - 接口 `gateway.AgentCaller interface { Reply(ctx, openID, text string) (string, error); RunDailyMaintenance(ctx, targetDate string) ([]string, error) }`
  - 接口 `gateway.Pusher interface { SendText(ctx context.Context, token, openID, text string) error }`
  - 接口 `gateway.TokenSource interface { Get(ctx context.Context) (string, error) }`
  - `gateway.NewAgentClient(conn *grpc.ClientConn) *AgentClient`（实现 AgentCaller）
  - `gateway.Handlers` 结构 + `RegisterRoutes(r *gin.Engine)`
  - `gateway.RunMaintenance(ctx, agent AgentCaller, tokens TokenSource, push Pusher, log *slog.Logger)`（cron 调用的纯函数）

- [ ] **Step 1: 安装依赖**

```bash
go get github.com/gin-gonic/gin@latest
go get github.com/robfig/cron/v3@latest
```

- [ ] **Step 2: 实现 gRPC client 封装** — `internal/gateway/agent_client.go`

```go
package gateway

import (
	"context"

	"google.golang.org/grpc"

	"companion-ai/gen/agentv1"
)

type AgentCaller interface {
	Reply(ctx context.Context, openID, text string) (string, error)
	RunDailyMaintenance(ctx context.Context, targetDate string) ([]string, error)
}

type AgentClient struct {
	c agentv1.AgentServiceClient
}

func NewAgentClient(conn *grpc.ClientConn) *AgentClient {
	return &AgentClient{c: agentv1.NewAgentServiceClient(conn)}
}

func (a *AgentClient) Reply(ctx context.Context, openID, text string) (string, error) {
	resp, err := a.c.Reply(ctx, &agentv1.ReplyRequest{OpenId: openID, Text: text})
	if err != nil {
		return "", err
	}
	return resp.ReplyText, nil
}

func (a *AgentClient) RunDailyMaintenance(ctx context.Context, targetDate string) ([]string, error) {
	resp, err := a.c.RunDailyMaintenance(ctx, &agentv1.MaintenanceRequest{TargetDate: targetDate})
	if err != nil {
		return nil, err
	}
	return resp.GreetOpenIds, nil
}
```

- [ ] **Step 3: 写 cron 维护逻辑失败测试** — `internal/gateway/cron_test.go`

```go
package gateway

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeAgent struct{ greet []string }

func (f *fakeAgent) Reply(context.Context, string, string) (string, error) { return "", nil }
func (f *fakeAgent) RunDailyMaintenance(context.Context, string) ([]string, error) { return f.greet, nil }

type fakeTokens struct{}

func (fakeTokens) Get(context.Context) (string, error) { return "TOK", nil }

type fakePusher struct {
	mu   sync.Mutex
	sent map[string]string
}

func (p *fakePusher) SendText(_ context.Context, _, openID, text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sent == nil {
		p.sent = map[string]string{}
	}
	p.sent[openID] = text
	return nil
}

func TestRunMaintenanceGreetsActiveUsers(t *testing.T) {
	agent := &fakeAgent{greet: []string{"u1", "u2"}}
	push := &fakePusher{}
	RunMaintenance(context.Background(), agent, fakeTokens{}, push, slog.Default())
	require.Equal(t, "晚安啦！", push.sent["u1"])
	require.Equal(t, "晚安啦！", push.sent["u2"])
}
```

- [ ] **Step 4: 运行确认失败**

Run: `go test ./internal/gateway/... -run Maintenance`
Expected: FAIL（未定义）

- [ ] **Step 5: 实现 cron 维护** — `internal/gateway/cron.go`

```go
package gateway

import (
	"context"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"companion-ai/internal/persona"
)

type Pusher interface {
	SendText(ctx context.Context, token, openID, text string) error
}

type TokenSource interface {
	Get(ctx context.Context) (string, error)
}

// RunMaintenance 触发 Agent 每日维护，并给道晚安名单推送固定问候。
func RunMaintenance(ctx context.Context, agent AgentCaller, tokens TokenSource, push Pusher, log *slog.Logger) {
	targetDate := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	ids, err := agent.RunDailyMaintenance(ctx, targetDate)
	if err != nil {
		log.Error("daily maintenance rpc failed", "err", err)
		return
	}
	token, err := tokens.Get(ctx)
	if err != nil {
		log.Error("get token failed for goodnight", "err", err)
		return
	}
	for _, id := range ids {
		if err := push.SendText(ctx, token, id, persona.GoodNight); err != nil {
			log.Error("goodnight push failed", "open_id", id, "err", err)
		}
	}
}

// StartCron 注册每天 00:00 的维护任务，返回 cron 实例供调用方 Stop。
func StartCron(agent AgentCaller, tokens TokenSource, push Pusher, log *slog.Logger) *cron.Cron {
	c := cron.New()
	_, _ = c.AddFunc("0 0 * * *", func() {
		RunMaintenance(context.Background(), agent, tokens, push, log)
	})
	c.Start()
	return c
}
```

- [ ] **Step 6: 运行确认通过**

Run: `go test ./internal/gateway/... -run Maintenance`
Expected: PASS

- [ ] **Step 7: 写微信 handler 失败测试** — `internal/gateway/wechat_handler_test.go`

```go
package gateway

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func sign(token, ts, nonce string) string {
	p := []string{token, ts, nonce}
	sort.Strings(p)
	h := sha1.Sum([]byte(strings.Join(p, "")))
	return hex.EncodeToString(h[:])
}

func newTestHandlers(agent AgentCaller, push *fakePusher) *Handlers {
	return &Handlers{
		Token:   "mytoken",
		Agent:   agent,
		Tokens:  fakeTokens{},
		Pusher:  push,
		Log:     slogDiscard(),
		nowSync: &sync.WaitGroup{},
	}
}

func TestWechatGetVerify(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := newTestHandlers(&fakeAgent{}, &fakePusher{})
	h.RegisterRoutes(r)

	ts := "1717000000"
	nonce := "abc"
	echo := "hello_echo"
	sig := sign("mytoken", ts, nonce)
	req := httptest.NewRequest(http.MethodGet,
		"/wechat?signature="+sig+"&timestamp="+ts+"&nonce="+nonce+"&echostr="+echo, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Equal(t, echo, w.Body.String())
}

func TestWechatPostAcksAndPushes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agent := &fakeAgent{}
	push := &fakePusher{}
	h := newTestHandlers(&replyAgent{reply: "哼，收到！"}, push)
	h.Pusher = push
	_ = agent
	h.RegisterRoutes(r)

	ts := "1717000000"
	nonce := "abc"
	sig := sign("mytoken", ts, nonce)
	body := `<xml><FromUserName><![CDATA[u1]]></FromUserName><MsgType><![CDATA[text]]></MsgType><Content><![CDATA[在吗]]></Content><MsgId>1</MsgId></xml>`
	req := httptest.NewRequest(http.MethodPost,
		"/wechat?signature="+sig+"&timestamp="+ts+"&nonce="+nonce, strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "success", w.Body.String())

	// 异步推送：等待后台 goroutine 完成
	h.WaitAsync()
	require.Equal(t, "哼，收到！", push.sent["u1"])
}

type replyAgent struct{ reply string }

func (r *replyAgent) Reply(context.Context, string, string) (string, error) { return r.reply, nil }
func (r *replyAgent) RunDailyMaintenance(context.Context, string) ([]string, error) { return nil, nil }

var _ = time.Second
```

> 注：测试用到 `slogDiscard()`（返回丢弃日志的 logger）与 `Handlers.WaitAsync()`（等待后台推送 goroutine），均在实现步骤中提供。

- [ ] **Step 8: 运行确认失败**

Run: `go test ./internal/gateway/... -run Wechat`
Expected: FAIL（未定义）

- [ ] **Step 9: 实现微信 handler** — `internal/gateway/wechat_handler.go`

```go
package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"companion-ai/internal/wechat"
)

type Handlers struct {
	Token   string
	Agent   AgentCaller
	Tokens  TokenSource
	Pusher  Pusher
	Log     *slog.Logger

	nowSync *sync.WaitGroup // 跟踪后台 goroutine，仅供测试 WaitAsync
}

func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func (h *Handlers) RegisterRoutes(r *gin.Engine) {
	r.GET("/wechat", h.verify)
	r.POST("/wechat", h.receive)
	h.registerAPI(r) // 见 api_handler.go
}

// WaitAsync 等待后台推送 goroutine 完成（测试用）。
func (h *Handlers) WaitAsync() {
	if h.nowSync != nil {
		h.nowSync.Wait()
	}
}

func (h *Handlers) verify(c *gin.Context) {
	sig := c.Query("signature")
	ts := c.Query("timestamp")
	nonce := c.Query("nonce")
	echo := c.Query("echostr")
	if wechat.CheckSignature(h.Token, ts, nonce, sig) {
		c.String(http.StatusOK, echo)
		return
	}
	c.String(http.StatusForbidden, "invalid signature")
}

func (h *Handlers) receive(c *gin.Context) {
	sig := c.Query("signature")
	ts := c.Query("timestamp")
	nonce := c.Query("nonce")
	if !wechat.CheckSignature(h.Token, ts, nonce, sig) {
		c.String(http.StatusForbidden, "invalid signature")
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.String(http.StatusOK, "success")
		return
	}
	msg, err := wechat.ParseIncoming(body)
	if err != nil || msg.MsgType != "text" {
		c.String(http.StatusOK, "success") // 非文本/解析失败，直接 ack
		return
	}

	// 立即 ack，绕开 5 秒超时
	c.String(http.StatusOK, "success")

	openID := msg.FromUserName
	text := msg.Content
	if h.nowSync != nil {
		h.nowSync.Add(1)
	}
	go func() {
		if h.nowSync != nil {
			defer h.nowSync.Done()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		h.handleAsync(ctx, openID, text)
	}()
}

func (h *Handlers) handleAsync(ctx context.Context, openID, text string) {
	reply, err := h.Agent.Reply(ctx, openID, text)
	if err != nil {
		h.Log.Error("agent reply failed", "open_id", openID, "err", err)
		reply = "哼，本小姐突然走神了，再说一遍！"
	}
	token, err := h.Tokens.Get(ctx)
	if err != nil {
		h.Log.Error("get token failed", "err", err)
		return
	}
	if err := h.Pusher.SendText(ctx, token, openID, reply); err != nil {
		h.Log.Error("push reply failed", "open_id", openID, "err", err)
	}
}
```

- [ ] **Step 10: 写 REST API 失败测试** — `internal/gateway/api_handler_test.go`

```go
package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAPIChat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handlers{Agent: &replyAgent{reply: "你好团员！"}, Log: slogDiscard()}
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat",
		strings.NewReader(`{"open_id":"u1","text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "你好团员！")
}

func TestAPIChatBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handlers{Agent: &replyAgent{}, Log: slogDiscard()}
	h.RegisterRoutes(r)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
```

- [ ] **Step 11: 运行确认失败**

Run: `go test ./internal/gateway/... -run API`
Expected: FAIL

- [ ] **Step 12: 实现 REST handler** — `internal/gateway/api_handler.go`

```go
package gateway

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type chatReq struct {
	OpenID string `json:"open_id"`
	Text   string `json:"text"`
}

func (h *Handlers) registerAPI(r *gin.Engine) {
	v1 := r.Group("/api/v1")
	v1.POST("/chat", h.apiChat)
	r.GET("/healthz", h.healthz)
}

func apiError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": msg}})
}

func (h *Handlers) apiChat(c *gin.Context) {
	var req chatReq
	if err := c.ShouldBindJSON(&req); err != nil || req.OpenID == "" || req.Text == "" {
		apiError(c, http.StatusBadRequest, "invalid_request", "open_id 和 text 必填")
		return
	}
	reply, err := h.Agent.Reply(c.Request.Context(), req.OpenID, req.Text)
	if err != nil {
		apiError(c, http.StatusBadGateway, "agent_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"reply": reply})
}

func (h *Handlers) healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
```

> 说明：`GET/DELETE /api/v1/conversations/{open_id}/messages` 属记忆读写，需 Agent 暴露对应 RPC。本期 YAGNI 暂不实现这两个端点（spec 第 11 节非目标外的可选项）；如需，后续在 proto 增加 `GetMessages`/`ClearMessages` RPC 后补 handler。当前 REST 提供 `POST /api/v1/chat` 与 `/healthz` 满足本地联调与健康检查。

- [ ] **Step 13: 运行 gateway 全部测试确认通过**

Run: `go test ./internal/gateway/...`
Expected: PASS

- [ ] **Step 14: 写 Gateway 入口** — `cmd/gateway/main.go`

```go
package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"companion-ai/internal/config"
	"companion-ai/internal/gateway"
	"companion-ai/internal/logging"
	"companion-ai/internal/wechat"
)

func main() {
	log, err := logging.New("gateway")
	if err != nil {
		panic(err)
	}
	cfg, err := config.LoadGateway()
	if err != nil {
		log.Error("config load failed", "err", err)
		panic(err)
	}

	conn, err := grpc.NewClient(cfg.AgentGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("grpc dial failed", "err", err)
		panic(err)
	}
	defer conn.Close()

	httpClient := &http.Client{Timeout: 10 * time.Second}
	tokens := wechat.NewTokenManager(cfg.WechatAppID, cfg.WechatAppSecret, httpClient)
	pusher := wechat.NewKFClient(httpClient)
	agentClient := gateway.NewAgentClient(conn)

	h := &gateway.Handlers{
		Token:  cfg.WechatToken,
		Agent:  agentClient,
		Tokens: tokens,
		Pusher: pusher,
		Log:    log,
	}

	cronInst := gateway.StartCron(agentClient, tokens, pusher, log)
	defer cronInst.Stop()

	r := gin.New()
	r.Use(gin.Recovery())
	h.RegisterRoutes(r)

	log.Info("gateway http serving", "addr", cfg.GatewayHTTPAddr)
	if err := r.Run(cfg.GatewayHTTPAddr); err != nil {
		log.Error("http serve failed", "err", err)
		panic(err)
	}
}
```

- [ ] **Step 15: 编译确认**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 16: 提交**

```bash
git add internal/gateway cmd/gateway go.mod go.sum
git commit -m "feat: gateway with wechat webhook, REST api, gRPC client and daily cron"
```

---

## Task 10: 端到端集成测试 + 部署说明

**Files:**
- Create: `internal/integration/e2e_test.go`
- Create: `README.md`（部署与测试号配置说明）

**Interfaces:**
- Consumes: 全部已实现包

- [ ] **Step 1: 写端到端测试** — `internal/integration/e2e_test.go`

```go
package integration

import (
	"context"
	"net"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"companion-ai/gen/agentv1"
	"companion-ai/internal/agent"
	"companion-ai/internal/chat"
	"companion-ai/internal/gateway"
	"companion-ai/internal/memory"
	"companion-ai/internal/summarize"
)

type fakeModel struct{}

func (fakeModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("哼，本团长在呢！", nil), nil
}

func TestEndToEndReplyThroughGRPC(t *testing.T) {
	// 内存 Agent gRPC server（bufconn）
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	repo, err := memory.NewRepo(db)
	require.NoError(t, err)
	fm := fakeModel{}
	srv := agent.NewServer(repo, chat.NewReplier(fm), summarize.NewSummarizer(fm))

	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	agentv1.RegisterAgentServiceServer(gs, srv)
	go gs.Serve(lis)
	defer gs.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := gateway.NewAgentClient(conn)
	reply, err := client.Reply(context.Background(), "u1", "你好")
	require.NoError(t, err)
	require.Equal(t, "哼，本团长在呢！", reply)

	// 记忆已写入
	msgs, _ := repo.TodayMessages("u1")
	require.Len(t, msgs, 2)
}
```

- [ ] **Step 2: 运行确认通过**

Run: `go test ./internal/integration/...`
Expected: PASS

- [ ] **Step 3: 运行全部测试**

Run: `go test ./...`
Expected: 全部 PASS

- [ ] **Step 4: 写 README**（部署与测试号配置）— `README.md`

```markdown
# 陪伴 AI · 凉宫春日（微信公众号）

## 架构
Gateway(Gin, :80) --gRPC--> Agent(Eino+DeepSeek) --> PostgreSQL

## 部署（服务器 47.82.114.17）
1. 安装 PostgreSQL，创建数据库，设置 `PG_DSN`。
2. 复制 `.env.example` 为 `.env` 并填写微信测试号 / DeepSeek 配置。
3. 编译：`go build -o bin/agent ./cmd/agent && go build -o bin/gateway ./cmd/gateway`
4. 先启动 Agent：`AGENT_GRPC_ADDR=127.0.0.1:9090 ./bin/agent`
5. 再启动 Gateway（需 root 监听 80）：`GATEWAY_HTTP_ADDR=:80 ./bin/gateway`
6. 日志在 `log/agent.log` 与 `log/gateway.log`。

## 微信测试号配置
1. 打开微信公众平台「接口测试号」页面，获取 appID/appSecret。
2. 「接口配置信息」URL 填 `http://47.82.114.17/wechat`，Token 填与 `WECHAT_TOKEN` 相同的值，提交（触发 GET 校验）。
3. 扫码关注测试号二维码后即可私聊春日。

## 本地联调（不走微信）
`curl -X POST http://localhost:80/api/v1/chat -H 'Content-Type: application/json' -d '{"open_id":"u1","text":"你好"}'`
```

- [ ] **Step 5: 提交**

```bash
git add internal/integration README.md
git commit -m "test: end-to-end gRPC integration test and deployment README"
```

---

## Self-Review

**1. Spec 覆盖检查：**
- 测试号 + 客服消息异步推送 → Task 8(kf)、Task 9(wechat_handler 立即 ack + 异步推送) ✅
- 5 秒超时绕过 → Task 9 receive 立即返回 success + goroutine ✅
- 凉宫春日固定人设 → Task 3 ✅
- DeepSeek（Eino OpenAI 兼容适配器） → Task 4 接口 + Task 7 main 构造 ✅
- 双层记忆（当日全量 + 7 天摘要） → Task 2 仓储 + Task 7 Reply 取数 ✅
- 0 点维护（摘要+淘汰+清理+道晚安） → Task 7 RunDailyMaintenance + Task 9 cron/RunMaintenance ✅
- gRPC 单向 → Task 6 契约 + Task 7 server + Task 9 client ✅
- RESTful（/api/v1/chat、/healthz） → Task 9 ✅（conversations 读写端点按 YAGNI 标注为后续可选，已在 Task 9 Step 12 说明）
- GORM + PostgreSQL → Task 2 + Task 7 main ✅
- 日志固定 log/ → Task 1 logging + 各 main 调用 ✅
- 错误处理（DeepSeek 兜底语、token 刷新、维护单用户隔离） → Task 9 handleAsync、Task 8 token、Task 7 maintenance ✅

**2. 占位符扫描：** 无 TBD/TODO；每个代码步骤含完整代码。Task 7 Step 5 与 Task 9 关于 conversations 端点的说明是明确的范围决策，非占位。✅

**3. 类型一致性：** `chat.Model`/`chat.Turn`、`memory.Repo` 方法名（`TodayMessages`/`RecentSummaries`/`DeleteTodayMessages`/`PurgeSummariesOlderThan`/`ActiveOpenIDsForDate`）、`agentv1.ReplyRequest{OpenId,Text}`、`AgentCaller`/`Pusher`/`TokenSource` 接口在各任务间一致。`persona.GoodNight="晚安啦！"` 在 Task 3 定义、Task 9 使用。✅

**已知集成点（实现时按当前库版本微调，不影响逻辑）：**
- Eino OpenAI 适配器 `ChatModelConfig` 字段名（Task 7 Step 5 已标注用 `go doc` 确认）。
- protoc/插件需本机安装（Task 6 Step 1）。
