# Anime-Companion-AI

陪伴型 SOS 团 AI 后端。Gateway 提供 Web 与微信接入，Agent 负责编排多角色对话；PostgreSQL 保存长期记忆，Redis 保存短期上下文与缓存。

## 准备与配置

准备 Go、Docker Desktop（含 Compose）、GNU Make；如需开发 Web 界面，还需准备相邻的前端仓库和 Node.js。首次执行 `make db` 或 `make dev` 会从 `.env.example` 创建 `.env`。

| 用途 | 在哪里填写 | 需要填写的内容 |
| --- | --- | --- |
| 真实 AI 对话（必填） | `.env` | `DEEPSEEK_API_KEY` |
| 本地端口、数据库账户和模型 | `.env` | 按需修改 `DEV_*`、`POSTGRES_*`、`DEEPSEEK_MODEL` |
| 邮箱注册与密码重置 | `.env` | `AUTH_PEPPER`、`SMTP_*`、`ADMIN_EMAIL`、`ADMIN_PASSWORD` |
| 微信测试号（可选） | `.env` | 设置 `WECHAT_ENABLED=true`，并填写 `WECHAT_TOKEN`、`WECHAT_APPID`、`WECHAT_APPSECRET` |
| 集群 Secret | 运行 `scripts/release/Initialize-K3sSecrets.ps1` 时按提示输入 | 数据库、DeepSeek、邮件、OSS 与镜像仓库凭据；不要写入仓库文件 |
| 公网域名和服务器 IP | `deploy/k3s/overlays/{staging,production}/ingress.yaml`、`deploy/k3s/base/platform.yaml`，以及发布命令 | 将 ingress host/TLS 域名和 `PUBLIC_ORIGIN` 改为真实域名；执行发布时传入 `-Domain` 与 `-ExpectedPublicIP` |

## 本地启动

在本仓库根目录执行：

```powershell
make db
make dev
```

服务启动后，Gateway 默认为 `http://localhost:8080`，Mailpit 收件箱为 `http://localhost:8025`。运行后端测试：

```powershell
make test
```

前端本地启动与 API 地址配置见相邻仓库的 README。

## 部署

生产部署使用 k3s 发布脚本。先完成上表中的公网配置和 Secret 初始化，再按 [deploy/README.md](deploy/README.md) 的 staging、production 顺序执行 `Release-K3s.ps1`。发布和回滚的公开冒烟都必须显式传入 `-Domain '<your-public-domain>'`；发布还必须传入 `-ExpectedPublicIP '<your-server-public-ip>'`。
