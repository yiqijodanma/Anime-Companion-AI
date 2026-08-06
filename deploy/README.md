# k3s 运维速查

本目录包含 Anime Companion AI 的 k3s 清单和发布脚本。完整发布说明在仓库根目录的 [README.md](../README.md#部署)；这里仅保留日常操作需要的检查清单和命令。以下命令均从仓库根目录执行。

## 发布前检查

| 项目 | 要求 |
| --- | --- |
| 集群 | 单节点 k3s，已启用 Traefik 和 `local-path`；开放 80/443，数据库与 Agent 端口不对公网开放 |
| 域名 | 域名状态正常，A 记录指向目标服务器公网 IP |
| 镜像仓库 | 已创建 Gateway、Agent、migration、backup 仓库，并完成本机 Docker 登录 |
| 外部服务 | 可用的 DeepSeek、邮件发送、OSS Bucket 与最小权限访问凭据 |
| 部署清单 | ingress host/TLS 域名和 `PUBLIC_ORIGIN` 已替换为真实域名 |
| 源码 | 后端与相邻前端仓库均没有未提交变更 |

不要把 Secret、DSN、邮件密码或云服务凭据提交到仓库。

## 首次初始化

```powershell
pwsh -NoProfile -File scripts/release/Install-CertManager.ps1 -Version v1.21.0 -KubeContext <context>
pwsh -NoProfile -File scripts/release/Initialize-K3sSecrets.ps1 -KubeContext <context>
pwsh -NoProfile -File scripts/release/Test-Manifests.ps1 -Environment staging -KubeContext <context> -ServerValidation
```

Secret 初始化脚本会以隐藏输入读取所有凭据。再次执行会轮换 `AUTH_PEPPER` 并使现有会话失效，完成后需滚动 Gateway 和 Agent。

## 发布与验证

先使用根 README 中的 `Release-K3s.ps1` staging 命令发布和验收，再使用 production 命令发布。发布时必须传入 `-Domain`、`-ExpectedPublicIP`、`-SshTarget` 和所有外部服务参数。

```powershell
pwsh -NoProfile -File scripts/release/Smoke-K3s.ps1 -Domain '<your-public-domain>'
kubectl -n anime-companion get pod,job,cronjob,ingress,certificate,pvc
kubectl -n anime-companion logs deployment/gateway --tail=200
kubectl -n anime-companion logs deployment/agent --tail=200
```

当域名尚未就绪时，可用 `-SkipIngress -SkipSmoke` 验证内部栈；这不等于完成上线。

## 备份与回滚

`postgres-backup` CronJob 负责定时 OSS 备份。手动恢复演练使用：

```powershell
pwsh -NoProfile -File scripts/release/Restore-Drill.ps1 `
  -ObjectUrl 'oss://<bucket>/anime-companion/postgres/<backup-file>.dump' `
  -KubeContext <context>
```

应用回滚使用 `Rollback-K3s.ps1`，并传入 previous Gateway/Agent 镜像、`-Domain` 和 `-KubeContext`。它不会回滚数据库 migration；不兼容时应实施经过审阅的 forward fix。
