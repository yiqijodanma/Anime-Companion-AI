# Anime-Companion-AI

陪伴型 SOS 团 AI 后端。Gateway 提供 Web 与微信接入，Agent 负责编排多角色对话；PostgreSQL 保存长期记忆，Redis 保存短期上下文与缓存。

前端仓库：[Anime-Companion-ai-sos-chat-fronted](https://github.com/yiqijodanma/Anime-Companion-ai-sos-chat-fronted)。

![alt text](image.png)

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

项目通过 k3s 发布脚本完成镜像构建、推送、清单校验和滚动发布。发布脚本会从相邻的前端仓库构建 `dist`，因此两个仓库都必须无未提交变更，并且已登录目标镜像仓库。

### 1. 准备发布参数

| 参数 | 说明 |
| --- | --- |
| `Registry` | 镜像仓库 namespace，例如 `<registry-host>/<namespace>` |
| `AcmeEmail` | TLS 证书申请联系邮箱 |
| `SmtpFrom` | 已验证的邮件发信地址 |
| `OssEndpoint` | OSS endpoint 主机名，不含 `https://` |
| `Domain` | 对外访问的公网域名 |
| `ExpectedPublicIP` | 服务器公网 IPv4 地址 |
| `SshTarget` | 服务器 SSH 地址，格式为 `<user>@<server-public-ip>` |
| `KubeContext` | 目标 Kubernetes context |

在发布前，将 `deploy/k3s/overlays/{staging,production}/ingress.yaml` 的 host 和 TLS 域名、以及 `deploy/k3s/base/platform.yaml` 中的 `PUBLIC_ORIGIN` 改为同一个 `Domain`。这些值不会被发布脚本自动替换。

### 2. 初始化集群

首次部署时，安装 cert-manager、通过隐藏输入创建 Secret，并检查渲染后的清单：

```powershell
pwsh -NoProfile -File scripts/release/Install-CertManager.ps1 -Version v1.21.0 -KubeContext <context>
pwsh -NoProfile -File scripts/release/Initialize-K3sSecrets.ps1 -KubeContext <context>
pwsh -NoProfile -File scripts/release/Test-Manifests.ps1 -Environment staging -KubeContext <context> -ServerValidation
```

`Initialize-K3sSecrets.ps1` 会交互式读取数据库、DeepSeek、邮件、OSS 和镜像仓库凭据；不要将这些值写入 YAML、命令历史或仓库。

### 3. 先发布 staging

```powershell
pwsh -NoProfile -File scripts/release/Release-K3s.ps1 `
  -Registry '<registry-host>/<namespace>' `
  -AcmeEmail '<acme-contact-email>' `
  -SmtpFrom '<verified-smtp-sender>' `
  -OssEndpoint '<oss-endpoint-host>' `
  -Domain '<your-public-domain>' `
  -ExpectedPublicIP '<your-server-public-ip>' `
  -Environment staging `
  -KubeContext <context> `
  -SshTarget '<user>@<server-public-ip>' `
  -DomainStatusNormal
```

脚本会运行 preflight、构建并推送不可变镜像、执行 migration/seed、完成滚动发布和公网冒烟。确认 staging 证书有效、HTTP 能跳转 HTTPS 后再发布 production。

### 4. 发布 production

```powershell
pwsh -NoProfile -File scripts/release/Release-K3s.ps1 `
  -Registry '<registry-host>/<namespace>' `
  -AcmeEmail '<acme-contact-email>' `
  -SmtpFrom '<verified-smtp-sender>' `
  -OssEndpoint '<oss-endpoint-host>' `
  -Domain '<your-public-domain>' `
  -ExpectedPublicIP '<your-server-public-ip>' `
  -Environment production `
  -KubeContext <context> `
  -SshTarget '<user>@<server-public-ip>' `
  -DomainStatusNormal `
  -ConfirmStagingCertificateValidated
```

### 5. 验收、备份与回滚

发布后执行公开冒烟；仅在 staging 证书未受信任时使用 `-AllowUntrustedTls`。`-RealChat` 会消耗一次模型额度。

```powershell
pwsh -NoProfile -File scripts/release/Smoke-K3s.ps1 -Domain '<your-public-domain>'
kubectl -n anime-companion get pod,job,cronjob,ingress,certificate,pvc
```

备份由 `postgres-backup` CronJob 负责；恢复演练使用 `scripts/release/Restore-Drill.ps1`。若需要回滚，先读取发布记录中的 previous image，再执行：

```powershell
pwsh -NoProfile -File scripts/release/Rollback-K3s.ps1 `
  -GatewayImage '<registry>/gateway:<previous-tag>' `
  -AgentImage '<registry>/agent:<previous-tag>' `
  -Domain '<your-public-domain>' `
  -KubeContext <context>
```

完整的运维速查见 [deploy/README.md](deploy/README.md)。
