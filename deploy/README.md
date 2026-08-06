# Anime Companion AI 单节点 k3s 运维手册

此目录是公网部署的声明式配置。发布脚本固定构建 `linux/amd64`，PostgreSQL/Redis 运行时镜像使用不可变 digest，先完成数据服务就绪，再依次运行 migration、幂等管理员初始化，最后滚动 Agent/Gateway。所有 Service 都是 ClusterIP；只有 k3s 自带 Traefik 使用公网 80/443。

## 1. 外部前置资源

上线前由运营方在控制台完成：

- 将域名状态设为“正常”，并使 apex A 记录指向目标服务器公网 IP。
- ECS 安全组只公开 22（按来源限制）、80、443；5432、6379、9090 不公开。
- k3s 已启用 Traefik 和 `local-path`。目标服务器实测为 k3s/Kubernetes `v1.36.2+k3s1`，cert-manager 固定使用已测试支持 Kubernetes 1.36 的 `v1.21.0`（或经审阅的更新 1.21 patch），不能使用只支持到 Kubernetes 1.35 的 1.20。
- ACR 私有 namespace 及 Gateway、Agent、migration、backup 四个仓库已创建，并在服务能力允许时启用 tag 不可变策略；发布脚本也会拒绝覆盖已存在的 tag。
- 私有 OSS Bucket 和仅允许备份前缀 `PutObject`、`ListParts`、`AbortMultipartUpload` 的 RAM 身份已创建；对 `anime-companion/postgres/` 应用 [7 天生命周期规则](oss-lifecycle.example.xml)（同时清理 1 天前的未完成分片），并把 Bucket 默认服务端加密设为 SSE-OSS/AES256。
- 阿里云邮件推送的发信域名和触发邮件发信地址已验证，发信地址已设置 SMTP 密码；DeepSeek key 和余额可用。不要把这些凭据写入仓库、命令行历史或聊天记录。

当前域名仍在审核时，可以完成镜像和 manifests 验证，但不要确认域名门禁，也不要申请生产证书。

首次安装 cert-manager：

```powershell
pwsh -NoProfile -File scripts/release/Install-CertManager.ps1 -Version v1.21.0 -KubeContext <context>
```

脚本使用 cert-manager 官方 release manifest，并等待 controller、webhook、cainjector 全部 Ready；preflight 会拒绝非 1.21 镜像线。

## 2. 首次 Secret 初始化

使用 PowerShell 7。脚本通过隐藏输入读取凭据，直接把 JSON 送给 Kubernetes API，并使用 server-side apply 避免生成包含明文 `stringData` 的 last-applied annotation；它不会在屏幕、参数列表或仓库文件中输出 Secret 值：

```powershell
pwsh -NoProfile -File scripts/release/Initialize-K3sSecrets.ps1 -KubeContext <context>
```

仓库中的 [secrets.example.yaml](k3s/secrets.example.yaml)只有空 key，用于审阅所需字段，不能直接用于生产。`Initialize-K3sSecrets.ps1` 只适合首次初始化或有计划的整体轮换；再次运行会生成新的 `AUTH_PEPPER` 并使现有会话失效。Secret 轮换后应重新滚动 Gateway/Agent。

## 3. 本地和集群 dry-run

离线渲染与策略检查（`kubectl apply --dry-run=client` 仍会做 API discovery，因此离线入口只使用 `kubectl kustomize`）：

```powershell
pwsh -NoProfile -File scripts/release/Test-Manifests.ps1
```

连接目标集群并完成 Secret 初始化后，执行 API Server dry-run：

```powershell
pwsh -NoProfile -File scripts/release/Test-Manifests.ps1 -Environment staging -KubeContext <context> -ServerValidation
```

preflight 会检查单节点 Linux/AMD64、Kubernetes 版本、Traefik 80/443、local-path、cert-manager/Traefik CRD、至少 35 GiB allocatable storage、Secret key、公网 DNS、ACR/OSS/阿里云邮件推送/DeepSeek 连通性，以及 PostgreSQL/Redis 公网端口不可达。提供 `-SshTarget <user>@<server-public-ip>` 时还会检查 k3s 磁盘实际剩余空间和主机 80/443 冲突。

## 4. 先 staging、后 production

两个源仓库必须无未提交变更。ACR 登录应提前在本机 Docker credential store 完成，脚本不会接收或打印 Registry 密码。

首次 staging 发布：

```powershell
pwsh -NoProfile -File scripts/release/Release-K3s.ps1 `
  -Registry '<acr-host>/<namespace>' `
  -AcmeEmail '<acme-contact-email>' `
  -SmtpFrom '<verified Alibaba Cloud DirectMail sender>' `
  -OssEndpoint '<region>.aliyuncs.com' `
  -Domain '<your-public-domain>' `
  -ExpectedPublicIP '<your-server-public-ip>' `
  -Environment staging `
  -KubeContext <context> `
  -SshTarget '<user>@<server-public-ip>' `
  -DomainStatusNormal
```

脚本生成包含 UTC 时间和两个 commit 短 SHA 的不可变 tag；也可显式传 `-ReleaseTag`。它执行 buildx/push、分阶段 apply、migration、seed、rollout 和公开冒烟，任一门禁失败立即停止。发布身份写入 `anime-companion-release` ConfigMap，其中包含本次与上次 Gateway/Agent image reference，不含凭据。

确认 staging `Certificate` 为 Ready、浏览器证书链正常且 HTTP 自动跳 HTTPS 后，切生产 issuer：

```powershell
pwsh -NoProfile -File scripts/release/Release-K3s.ps1 `
  -Registry '<acr-host>/<namespace>' `
  -AcmeEmail '<acme-contact-email>' `
  -SmtpFrom '<verified Alibaba Cloud DirectMail sender>' `
  -OssEndpoint '<region>.aliyuncs.com' `
  -Domain '<your-public-domain>' `
  -ExpectedPublicIP '<your-server-public-ip>' `
  -Environment production `
  -KubeContext <context> `
  -SshTarget '<user>@<server-public-ip>' `
  -DomainStatusNormal `
  -ConfirmStagingCertificateValidated
```

域名尚未就绪时可使用 `-SkipIngress -SkipSmoke` 部署内部栈，但这不算完成上线。HSTS 暂不启用；应在生产证书签发和至少一次续期演练后单独评审。

## 5. 上线验收与诊断

探针语义固定如下：`/livez` 只证明 Gateway HTTP 进程可响应；`/readyz` 检查 Agent、PostgreSQL、Redis 三个必需依赖；`/healthz` 保持旧兼容语义，仅检查 Gateway 到 Agent 的 gRPC 连通性。

公开只读冒烟：

```powershell
pwsh -NoProfile -File scripts/release/Smoke-K3s.ps1 -Domain '<your-public-domain>'
```

仅在 Let’s Encrypt staging 证书验证时添加 `-AllowUntrustedTls`；production 冒烟绝不能使用该开关，否则无法验证真实证书链。

带临时 `sos_session` 的认证冒烟会隐藏输入；`-RealChat` 会真实调用 DeepSeek 并消耗一次额度：

```powershell
pwsh -NoProfile -File scripts/release/Smoke-K3s.ps1 -Domain '<your-public-domain>' -AuthenticatedSmoke -RealChat
```

仍需人工完成注册图形验证码、阿里云邮件推送注册邮件和密码重置邮件、Secure/HttpOnly/SameSite Cookie、普通用户剩余额度、管理员无限、重启后数据持久性检查。常用状态命令：

```powershell
kubectl -n anime-companion get pod,job,cronjob,ingress,certificate,pvc
kubectl -n anime-companion logs deployment/gateway --tail=200
kubectl -n anime-companion logs deployment/agent --tail=200
kubectl -n anime-companion describe certificate animecompanion-icu-production-tls
```

不要把 `kubectl get secret -o yaml`、环境变量或含 DSN 的错误粘贴到工单。

## 6. 备份与恢复演练

CronJob 使用 `timeZone: Asia/Shanghai` 每天 03:00 执行 `pg_dump --format=custom`，再通过校验过 SHA-256 的官方 ossutil 2.3.0 以 V4 签名和 TLS 上传。超过 100 MiB 自动分片并重试，上传请求显式携带 `x-oss-server-side-encryption:AES256`；Bucket 默认加密是第二道保护。保留与未完成分片清理由 OSS 生命周期控制。临时 dump 最多占 20 GiB；任务退出后自动删除。手动触发并查看结果：

```powershell
$job = 'postgres-backup-manual-' + (Get-Date -Format 'yyyyMMddHHmmss')
kubectl -n anime-companion create job --from=cronjob/postgres-backup $job
kubectl -n anime-companion wait --for=condition=complete "job/$job" --timeout=30m
kubectl -n anime-companion logs "job/$job"
```

首次手工任务完成后，还需使用拥有只读权限的运维身份执行 `ossutil stat oss://<bucket>/<object>`，确认对象非零且响应中的 `X-Oss-Server-Side-Encryption` 为 `AES256`。备份 RAM 身份不需要 Bucket 管理或删除权限。

先用已配置凭据的 `ossutil ls oss://<bucket>/anime-companion/postgres/` 选择最新对象，再恢复到独立临时数据库：

```powershell
pwsh -NoProfile -File scripts/release/Restore-Drill.ps1 `
  -ObjectUrl 'oss://<bucket>/anime-companion/postgres/companion-<timestamp>-<random-suffix>.dump' `
  -KubeContext <context>
```

脚本在创建临时数据库前检查 PostgreSQL 卷剩余空间：要求至少保留 2 GiB，且不低于当前数据库大小的两倍加 dump 大小；空间不足会在写入前停止。随后检查 dump 非空、`pg_restore --exit-on-error`、最新 migration 版本，以及至少一条用户和管理员记录；它不读取或打印账号内容。临时数据库在成功或失败后都默认删除。只有确需继续人工检查时才显式添加 `-KeepAfterVerification`；检查结束后明确执行：

```powershell
kubectl -n anime-companion exec statefulset/postgres -- dropdb --username=companion <temporary-database>
```

只有成功完成一次上述恢复演练，才可把备份门禁标为通过。Redis AOF 只保护普通重启，不属于灾难恢复备份。

## 7. 回滚

读取发布记录中的 previous image，确认都是不可变 tag：

```powershell
kubectl -n anime-companion get configmap anime-companion-release -o jsonpath='{.data.previous-gateway-image}'
kubectl -n anime-companion get configmap anime-companion-release -o jsonpath='{.data.previous-agent-image}'
```

然后重新应用旧应用镜像：

```powershell
pwsh -NoProfile -File scripts/release/Rollback-K3s.ps1 `
  -GatewayImage '<acr>/gateway:<previous-tag>' `
  -AgentImage '<acr>/agent:<previous-tag>' `
  -Domain '<your-public-domain>' `
  -KubeContext <context>
```

对“紧邻上一版”，脚本会从发布 ConfigMap 自动恢复该版本的 release ID 和前后端 commit，使 Pod 内发布身份与回滚镜像一致。若回滚到更早的任意版本，还必须显式传入对应的 `-ReleaseTag`、完整 40 位 `-BackendCommit` 和 `-FrontendCommit`；不要伪造或沿用当前版本元数据。

回滚不会执行 down migration。migration 必须与紧邻旧版本向后兼容；不兼容时停止回滚并实施经过审阅的 forward fix。
