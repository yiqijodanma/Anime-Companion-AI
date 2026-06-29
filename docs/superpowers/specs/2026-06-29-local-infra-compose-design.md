# Local Infrastructure Docker Compose Design

- **Date**: 2026-06-29
- **Status**: Approved for first implementation

## Goal

Provide local PostgreSQL and Redis services through Docker Compose so the Go Agent and Gateway can still run directly on the host during development.

## Scope

This first version only manages infrastructure:

- PostgreSQL for the existing GORM memory store.
- Redis for local availability and future cache/queue work.

It does not containerize `cmd/agent` or `cmd/gateway`, and it does not change application code to use Redis yet.

## Architecture

Local development flow:

```text
Go Agent on host -> localhost:5432 -> postgres container
Go Gateway on host -> localhost:9090 -> Go Agent on host
Redis available at localhost:6379 for later MsgId/access-token/rate-limit work
```

PostgreSQL uses a named Docker volume so local test data survives container restarts. Redis also uses a named volume, but the application does not depend on Redis yet.

## Service Defaults

| Service | Host Port | Database/User/Password |
| --- | --- | --- |
| PostgreSQL | `5432` | `companion` / `companion` / `companion` |
| Redis | `6379` | no password, local development only |

`PG_DSN` for local Go processes:

```text
postgres://companion:companion@localhost:5432/companion?sslmode=disable
```

## Future Redis Use

Redis should be introduced deliberately in later code changes:

- `MsgId` dedupe across process restarts.
- Shared WeChat `access_token` cache for multiple Gateway instances.
- Rate limiting by `open_id`.
- Optional async job queue for LLM reply tasks.

Redis should not replace PostgreSQL as the source of truth for message history or memory summaries in the current project stage.
