# 本地基础设施 Docker Compose 设计

## 目标

`docker-compose.yml` 只启动本地依赖服务，供宿主机上的 Go Agent 和 Gateway 连接。当前不把 Go 服务放入 compose。

## 服务

- PostgreSQL：`postgres:16-alpine`，宿主端口 `5432`。
- Redis：`redis:7-alpine`，宿主端口 `6379`。

## 账号与连接串

PostgreSQL 默认配置：

- 数据库：`companion`
- 用户：`companion`
- 密码：`companion`
- DSN：`postgres://companion:companion@localhost:5432/companion?sslmode=disable`

Redis 默认地址：

- `127.0.0.1:6379`
- 对应环境变量：`REDIS_ADDR=127.0.0.1:6379`

## Volume

- `postgres_data` 持久化 PostgreSQL 数据。
- `redis_data` 持久化 Redis 数据。

清理 volume 会删除本地测试数据和 Redis 缓存。

## 启动与清理

启动依赖：

```powershell
docker compose up -d postgres redis
```

查看配置：

```powershell
docker compose config
docker compose ps
```

停止容器但保留数据：

```powershell
docker compose down
```

停止并清理本地数据：

```powershell
docker compose down -v
```

## Redis 当前用途

Redis 只服务 Gateway：

- MsgId 去重，避免微信重试导致重复处理。
- `access_token` 缓存，减少微信 token 请求。
- 按 `open_id` 固定窗口限流，默认 `30 次/分钟/open_id`。

Agent 不依赖 Redis；记忆数据仍以 PostgreSQL 为准。
