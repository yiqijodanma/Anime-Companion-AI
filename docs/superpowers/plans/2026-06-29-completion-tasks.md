# Completion Tasks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the remaining local-testable work after PostgreSQL/Redis containers are healthy: RESTful memory management APIs, Redis-backed gateway protection, and documentation consistency.

**Architecture:** Keep the existing split: Gateway owns HTTP/WeChat concerns, Agent owns conversation behavior and memory access, PostgreSQL remains the source of truth for chat memory. Redis is a gateway-side operational cache only: message dedupe, WeChat token cache, and open_id rate limiting.

**Tech Stack:** Go, Gin, gRPC/protobuf, GORM/PostgreSQL, Redis via `github.com/redis/go-redis/v9`, tests with existing Go test stack and optional `github.com/alicebob/miniredis/v2` for Redis adapters.

---

## File Structure

- `api/proto/agent.proto`: extend AgentService with list/delete conversation message RPCs.
- `gen/agentv1/agent.pb.go`, `gen/agentv1/agent_grpc.pb.go`: regenerated protobuf output.
- `internal/agent/server.go`: implement the new gRPC methods.
- `internal/agent/server_test.go`: test agent-side list/delete behavior.
- `internal/gateway/agent_client.go`: hide proto details behind the Gateway `AgentCaller` interface.
- `internal/gateway/api_handler.go`: add RESTful `GET` and `DELETE` routes for messages.
- `internal/gateway/api_handler_test.go`, `internal/gateway/test_helpers_test.go`: test REST behavior and update fakes.
- `internal/integration/e2e_test.go`: verify gRPC client/server list/delete through bufconn.
- `internal/config/config.go`, `internal/config/config_test.go`: add Redis configuration for Gateway.
- `internal/gateway/dedupe.go`, `internal/gateway/wechat_handler.go`, `internal/gateway/wechat_handler_test.go`: generalize dedupe, add optional rate limiting.
- `internal/wechat/token.go`, `internal/wechat/token_test.go`: add optional shared token cache.
- `internal/redisstore/redis.go`, `internal/redisstore/redis_test.go`: Redis adapters for dedupe, token cache, fixed-window limiter.
- `cmd/gateway/main.go`: wire Redis client into Gateway.
- `.env.example`, `README.md`: document Redis and local smoke-test limits.
- `docs/superpowers/specs/*.md`, `docs/superpowers/plans/*.md`: restore tracked documentation referenced by README.

---

### Task 1: Phase 2 RESTful Conversation Messages

**Files:**
- Modify: `api/proto/agent.proto`
- Regenerate: `gen/agentv1/agent.pb.go`
- Regenerate: `gen/agentv1/agent_grpc.pb.go`
- Modify: `internal/agent/server.go`
- Modify: `internal/agent/server_test.go`
- Modify: `internal/gateway/agent_client.go`
- Modify: `internal/gateway/api_handler.go`
- Modify: `internal/gateway/api_handler_test.go`
- Modify: `internal/gateway/test_helpers_test.go`
- Modify: `internal/integration/e2e_test.go`

- [ ] **Step 1: Write failing Agent server tests**

Add tests to `internal/agent/server_test.go`:

```go
func TestListConversationMessagesReturnsTodayMessages(t *testing.T) {
	srv, repo := newTestServer(t, "unused")
	now := time.Now()
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u1", Role: memory.RoleUser, Content: "今天1", CreatedAt: now.Add(-2 * time.Hour)}).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u1", Role: memory.RoleAssistant, Content: "今天2", CreatedAt: now.Add(-time.Hour)}).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u2", Role: memory.RoleUser, Content: "别人", CreatedAt: now}).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u1", Role: memory.RoleUser, Content: "昨天", CreatedAt: now.AddDate(0, 0, -1)}).Error)

	resp, err := srv.ListConversationMessages(context.Background(), &agentv1.ListConversationMessagesRequest{OpenId: "u1"})
	require.NoError(t, err)
	require.Len(t, resp.Messages, 2)
	require.Equal(t, "user", resp.Messages[0].Role)
	require.Equal(t, "今天1", resp.Messages[0].Content)
	require.Equal(t, "assistant", resp.Messages[1].Role)
	require.Equal(t, "今天2", resp.Messages[1].Content)
	require.NotNil(t, resp.Messages[0].CreatedAt)
}

func TestDeleteConversationMessagesDeletesOnlyTodayForOpenID(t *testing.T) {
	srv, repo := newTestServer(t, "unused")
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u1", Role: memory.RoleUser, Content: "今天", CreatedAt: now}).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u1", Role: memory.RoleUser, Content: "昨天", CreatedAt: yesterday}).Error)
	require.NoError(t, repo.DB().Create(&memory.Message{OpenID: "u2", Role: memory.RoleUser, Content: "别人", CreatedAt: now}).Error)

	_, err := srv.DeleteConversationMessages(context.Background(), &agentv1.DeleteConversationMessagesRequest{OpenId: "u1"})
	require.NoError(t, err)

	u1Today, err := repo.TodayMessages("u1")
	require.NoError(t, err)
	require.Empty(t, u1Today)
	u1Yesterday, err := repo.MessagesForDate("u1", yesterday)
	require.NoError(t, err)
	require.Len(t, u1Yesterday, 1)
	u2Today, err := repo.TodayMessages("u2")
	require.NoError(t, err)
	require.Len(t, u2Today, 1)
}

func TestConversationMessageMethodsRequireOpenID(t *testing.T) {
	srv, _ := newTestServer(t, "unused")

	_, err := srv.ListConversationMessages(context.Background(), &agentv1.ListConversationMessagesRequest{})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.DeleteConversationMessages(context.Background(), &agentv1.DeleteConversationMessagesRequest{})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
```

Run: `go test ./internal/agent`

Expected: compile failure because proto types and server methods do not exist yet.

- [ ] **Step 2: Extend proto and regenerate code**

Update `api/proto/agent.proto`:

```proto
import "google/protobuf/timestamp.proto";

service AgentService {
  rpc Reply(ReplyRequest) returns (ReplyResponse);
  rpc RunDailyMaintenance(MaintenanceRequest) returns (MaintenanceResult);
  rpc ListConversationMessages(ListConversationMessagesRequest) returns (ListConversationMessagesResponse);
  rpc DeleteConversationMessages(DeleteConversationMessagesRequest) returns (DeleteConversationMessagesResponse);
}

message ConversationMessage {
  uint64 id = 1;
  string role = 2;
  string content = 3;
  google.protobuf.Timestamp created_at = 4;
}

message ListConversationMessagesRequest {
  string open_id = 1;
}

message ListConversationMessagesResponse {
  repeated ConversationMessage messages = 1;
}

message DeleteConversationMessagesRequest {
  string open_id = 1;
}

message DeleteConversationMessagesResponse {}
```

Regenerate:

```powershell
$gopath = go env GOPATH
protoc --plugin=protoc-gen-go="$gopath\bin\protoc-gen-go.exe" --plugin=protoc-gen-go-grpc="$gopath\bin\protoc-gen-go-grpc.exe" --go_out=. --go_opt=module=companion-ai --go-grpc_out=. --go-grpc_opt=module=companion-ai api/proto/agent.proto
```

- [ ] **Step 3: Implement Agent gRPC methods**

In `internal/agent/server.go`, add imports:

```go
"google.golang.org/grpc/codes"
"google.golang.org/grpc/status"
"google.golang.org/protobuf/types/known/timestamppb"
```

Add methods:

```go
func (s *Server) ListConversationMessages(ctx context.Context, req *agentv1.ListConversationMessagesRequest) (*agentv1.ListConversationMessagesResponse, error) {
	if req.OpenId == "" {
		return nil, status.Error(codes.InvalidArgument, "open_id is required")
	}
	msgs, err := s.repo.TodayMessages(req.OpenId)
	if err != nil {
		return nil, err
	}
	out := make([]*agentv1.ConversationMessage, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, &agentv1.ConversationMessage{
			Id:        uint64(msg.ID),
			Role:      msg.Role,
			Content:   msg.Content,
			CreatedAt: timestamppb.New(msg.CreatedAt),
		})
	}
	return &agentv1.ListConversationMessagesResponse{Messages: out}, nil
}

func (s *Server) DeleteConversationMessages(ctx context.Context, req *agentv1.DeleteConversationMessagesRequest) (*agentv1.DeleteConversationMessagesResponse, error) {
	if req.OpenId == "" {
		return nil, status.Error(codes.InvalidArgument, "open_id is required")
	}
	if err := s.repo.DeleteTodayMessages(req.OpenId); err != nil {
		return nil, err
	}
	return &agentv1.DeleteConversationMessagesResponse{}, nil
}
```

Run: `go test ./internal/agent`

Expected: PASS.

- [ ] **Step 4: Write failing Gateway REST tests**

Update `internal/gateway/test_helpers_test.go` with fake storage for listed messages and delete calls:

```go
messages    []ConversationMessage
deleteCalls int
listErr     error
deleteErr   error
```

Add `ListMessages` and `DeleteMessages` to fakeAgent.

Add tests to `internal/gateway/api_handler_test.go`:

```go
func TestAPIListConversationMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handlers{Agent: &fakeAgent{messages: []ConversationMessage{{ID: 1, Role: "user", Content: "你好", CreatedAt: "2026-06-29T10:00:00Z"}}}, Log: slogDiscard()}
	h.RegisterRoutes(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/conversations/u1/messages", nil))

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"messages"`)
	require.Contains(t, w.Body.String(), `"你好"`)
}

func TestAPIDeleteConversationMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agent := &fakeAgent{}
	h := &Handlers{Agent: agent, Log: slogDiscard()}
	h.RegisterRoutes(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/conversations/u1/messages", nil))

	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, 1, agent.DeleteCalls())
}
```

Run: `go test ./internal/gateway`

Expected: compile failure because route/client types do not exist yet.

- [ ] **Step 5: Implement Gateway REST and client methods**

In `internal/gateway/agent_client.go`, add:

```go
type ConversationMessage struct {
	ID        uint64 `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}
```

Extend `AgentCaller`:

```go
ListMessages(ctx context.Context, openID string) ([]ConversationMessage, error)
DeleteMessages(ctx context.Context, openID string) error
```

Implement `AgentClient.ListMessages` and `AgentClient.DeleteMessages` using the generated RPCs. Format timestamps with `time.RFC3339`.

In `internal/gateway/api_handler.go`, register:

```go
v1.GET("/conversations/:open_id/messages", h.apiListMessages)
v1.DELETE("/conversations/:open_id/messages", h.apiDeleteMessages)
```

Add handlers returning `200 {"messages":[...]}` and `204 No Content`. Map `codes.InvalidArgument` to HTTP 400 and other agent errors to HTTP 502.

Run: `go test ./internal/gateway`

Expected: PASS.

- [ ] **Step 6: Add integration coverage**

In `internal/integration/e2e_test.go`, add a test that seeds repo messages, connects through bufconn `gateway.NewAgentClient`, calls `ListMessages`, then calls `DeleteMessages`, then verifies the repo has no current-day messages for that open_id.

Run: `go test ./internal/integration`

Expected: PASS.

- [ ] **Step 7: Verify and commit Task 1**

Run:

```powershell
go test ./...
git diff --stat
git add api/proto gen internal
git commit -m "feat: add conversation message REST APIs"
```

Expected: all tests pass and commit succeeds.

---

### Task 2: Redis Gateway Protection

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/gateway/dedupe.go`
- Modify: `internal/gateway/wechat_handler.go`
- Modify: `internal/gateway/wechat_handler_test.go`
- Modify: `internal/gateway/api_handler.go`
- Modify: `internal/gateway/api_handler_test.go`
- Modify: `internal/gateway/test_helpers_test.go`
- Modify: `internal/wechat/token.go`
- Modify: `internal/wechat/token_test.go`
- Create: `internal/redisstore/redis.go`
- Create: `internal/redisstore/redis_test.go`
- Modify: `cmd/gateway/main.go`
- Modify: `.env.example`
- Modify: `README.md`

- [ ] **Step 1: Add failing config and Gateway limiter tests**

In `internal/config/config_test.go`, assert `LoadGateway` returns default `RedisAddr == "127.0.0.1:6379"` and honors `REDIS_ADDR`.

In `internal/gateway/api_handler_test.go`, add:

```go
func TestAPIChatRateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agent := &fakeAgent{reply: "unused"}
	h := &Handlers{Agent: agent, Limiter: denyLimiter{}, Log: slogDiscard()}
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"open_id":"u1","text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, 0, agent.Calls())
}
```

In `internal/gateway/wechat_handler_test.go`, add a matching test that a rate-limited WeChat POST still ACKs `success` and does not call Agent.

Run: `go test ./internal/config ./internal/gateway`

Expected: compile failure because config/limiter types do not exist.

- [ ] **Step 2: Generalize dedupe and add rate limiter seam**

In `internal/gateway/dedupe.go`, define:

```go
type MessageDeduper interface {
	SeenOrAdd(ctx context.Context, id string) (bool, error)
}

type RateLimiter interface {
	Allow(ctx context.Context, openID string) (bool, error)
}
```

Update in-memory dedupe to accept context and keep current map behavior.

In `internal/gateway/wechat_handler.go`, change `Dedupe` to `MessageDeduper`, add `Limiter RateLimiter`, and fail open on dedupe/limiter errors while logging. In `internal/gateway/api_handler.go`, enforce limiter before `Agent.Reply`, returning `429` when denied.

Run: `go test ./internal/gateway`

Expected: PASS.

- [ ] **Step 3: Add TokenCache tests and implementation seam**

In `internal/wechat/token_test.go`, add tests for cache hit, cache miss writes, and refresh replacing cache.

In `internal/wechat/token.go`, add:

```go
type TokenCache interface {
	Get(ctx context.Context) (token string, ok bool, err error)
	Set(ctx context.Context, token string, ttl time.Duration) error
	Delete(ctx context.Context) error
}

func (tm *TokenManager) WithCache(cache TokenCache) *TokenManager {
	tm.cache = cache
	return tm
}
```

Keep the existing in-process token cache. On external cache errors, fall back to current behavior. `Refresh` should delete external cache before fetching.

Run: `go test ./internal/wechat`

Expected: PASS.

- [ ] **Step 4: Add Redis adapters**

Add dependencies:

```powershell
go get github.com/redis/go-redis/v9 github.com/alicebob/miniredis/v2
```

Create `internal/redisstore/redis.go` implementing:

```go
func NewMessageDeduper(client *redis.Client, prefix string, ttl time.Duration) *MessageDeduper
func NewTokenCache(client *redis.Client, key string) *TokenCache
func NewFixedWindowLimiter(client *redis.Client, prefix string, limit int64, window time.Duration) *FixedWindowLimiter
```

Behavior:
- Message dedupe uses `SET key 1 NX EX ttl`; seen is `true` when Redis returns false.
- Token cache uses a single string key and TTL supplied by TokenManager.
- Limiter increments `ratelimit:<openID>`, sets expiry on first increment, allows while count <= limit.

Use miniredis tests in `internal/redisstore/redis_test.go`.

Run: `go test ./internal/redisstore`

Expected: PASS.

- [ ] **Step 5: Wire Redis into Gateway**

In `internal/config/config.go`, add `RedisAddr string` to `GatewayConfig`, defaulting to `127.0.0.1:6379`.

In `cmd/gateway/main.go`, create:

```go
redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
defer redisClient.Close()
```

Inject:
- `wechat.NewTokenManager(...).WithCache(redisstore.NewTokenCache(redisClient, "wechat:access_token"))`
- `Dedupe: redisstore.NewMessageDeduper(redisClient, "wechat:msg:", 72*time.Hour)`
- `Limiter: redisstore.NewFixedWindowLimiter(redisClient, "ratelimit:open_id:", 30, time.Minute)`

Update `.env.example` with `REDIS_ADDR=127.0.0.1:6379`.

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 6: Verify and commit Task 2**

Run:

```powershell
go test ./...
git diff --stat
git add go.mod go.sum cmd internal .env.example README.md
git commit -m "feat: add redis gateway protection"
```

Expected: all tests pass and commit succeeds.

---

### Task 3: Documentation and Local Smoke Test Guide

**Files:**
- Modify: `.gitignore`
- Create: `docs/superpowers/specs/2026-06-24-companion-ai-haruhi-wechat-design.md`
- Create: `docs/superpowers/specs/2026-06-29-local-infra-compose-design.md`
- Create: `docs/superpowers/plans/2026-06-24-companion-ai-haruhi-wechat.md`
- Modify: `README.md`

- [ ] **Step 1: Restore tracked docs**

Ensure `.gitignore` does not contain `/docs`. Keep `.worktrees/` ignored.

Create the three README-linked docs with concise content:
- design spec: architecture, goals, non-goals, flows, configuration, tests.
- local infra spec: Docker Compose only runs PostgreSQL/Redis for local Go programs.
- original implementation plan: status of completed and deferred work.

- [ ] **Step 2: Clarify local verification**

Update `README.md` to include:

```powershell
$env:GATEWAY_HTTP_ADDR=":8080"
curl.exe http://localhost:8080/healthz
```

Clarify:
- `/healthz` can be tested with non-empty placeholder DeepSeek key because it only checks Agent gRPC health.
- `/api/v1/chat` successful reply requires a real DeepSeek key and network access.
- Real `/wechat` callback requires public URL and real WeChat test account credentials.
- Redis is now used for MsgId dedupe, access_token cache, and open_id rate limiting.

- [ ] **Step 3: Verify docs links and commit**

Run:

```powershell
rg -n "docs/superpowers|REDIS_ADDR|healthz|api/v1/conversations" README.md docs .env.example
go test ./...
git add .gitignore README.md docs .env.example
git commit -m "docs: restore project docs and local verification guide"
```

Expected: docs are tracked, README links point to real files, tests pass.

---

## Final Verification

After all tasks and reviews:

```powershell
docker compose config
docker compose ps
go test ./...
go build -o bin/agent.exe ./cmd/agent
go build -o bin/gateway.exe ./cmd/gateway
git status --short --branch
```

Report clearly which checks were run locally and which still require external credentials:
- Real DeepSeek `/api/v1/chat` success.
- Real WeChat public callback and customer-service push.
