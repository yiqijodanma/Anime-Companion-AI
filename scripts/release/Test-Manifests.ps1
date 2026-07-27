[CmdletBinding()]
param(
    [ValidateSet('staging', 'production', 'both')]
    [string]$Environment = 'both',
    [string]$KubeContext,
    [switch]$ServerValidation
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'K3s.Common.ps1')

Assert-CommandAvailable 'kubectl'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$overlayRoot = Join-Path $repoRoot 'deploy\k3s\overlays'
$environments = if ($Environment -eq 'both') { @('staging', 'production') } else { @($Environment) }
$temporaryDirectory = New-TemporaryDirectory
$serverValidationRoot = ''

if ($ServerValidation) {
    Copy-Item -LiteralPath (Join-Path $repoRoot 'deploy\k3s') -Destination $temporaryDirectory -Recurse
    $serverValidationRoot = Join-Path $temporaryDirectory 'k3s'
    $baseKustomizationPath = Join-Path $serverValidationRoot 'base\kustomization.yaml'
    $baseKustomization = [IO.File]::ReadAllText($baseKustomizationPath)
    foreach ($jobResource in @('migration-job.yaml', 'seed-job.yaml')) {
        $pattern = "(?m)^[ \t]*-[ \t]*$([regex]::Escape($jobResource))[ \t]*\r?\n?"
        $withoutJob = [regex]::Replace($baseKustomization, $pattern, '')
        if ($withoutJob -eq $baseKustomization) {
            throw "Server-validation copy did not contain expected Job resource '$jobResource'."
        }
        $baseKustomization = $withoutJob
    }
    [IO.File]::WriteAllText($baseKustomizationPath, $baseKustomization, [Text.UTF8Encoding]::new($false))
}

try {
    foreach ($name in $environments) {
        $overlay = Join-Path $overlayRoot $name
        $rendered = Invoke-Kubectl -Context $KubeContext -ArgumentList @('kustomize', $overlay) -CaptureOutput

        if ($rendered -match '(?im)^\s*type:\s*(NodePort|LoadBalancer)\s*$') {
            throw "Overlay '$name' exposes a service outside ClusterIP."
        }
        if ($rendered -match '(?im)^\s*image:\s*\S+:latest\s*$') {
            throw "Overlay '$name' contains a latest image tag."
        }
        if ($rendered -match '(?im)^\s*privileged:\s*true\s*$') {
            throw "Overlay '$name' contains a privileged container."
        }
    foreach ($requiredText in @('storage: 20Gi', 'storage: 5Gi', 'timeZone: Asia/Shanghai', 'kind: NetworkPolicy', 'kind: ClusterIssuer')) {
            if (-not $rendered.Contains($requiredText)) {
                throw "Overlay '$name' is missing required rendered content '$requiredText'."
            }
        }

        $renderedPath = Join-Path $temporaryDirectory "$name.yaml"
        [IO.File]::WriteAllText($renderedPath, $rendered, [Text.UTF8Encoding]::new($false))
        if ($ServerValidation) {
            Invoke-Kubectl -Context $KubeContext -ArgumentList @('apply', '--dry-run=client', '-f', $renderedPath)

            $serverOverlay = Join-Path $serverValidationRoot "overlays\$name"
            $serverRendered = Invoke-Kubectl -Context $KubeContext -ArgumentList @('kustomize', $serverOverlay) -CaptureOutput
            if ($serverRendered -match '(?m)^\s*kind:\s*Job\s*$') {
                throw "Server-validation overlay '$name' still contains an immutable fixed-name Job."
            }
            $serverRenderedPath = Join-Path $temporaryDirectory "$name-server-validation.yaml"
            [IO.File]::WriteAllText($serverRenderedPath, $serverRendered, [Text.UTF8Encoding]::new($false))
            Invoke-Kubectl -Context $KubeContext -ArgumentList @('apply', '--dry-run=server', '-f', $serverRenderedPath)
        }
        Write-Host "Rendered and structurally checked k3s overlay: $name"
    }

    $dataManifestText = [IO.File]::ReadAllText((Join-Path $repoRoot 'deploy\k3s\base\data.yaml'))
    foreach ($runtimeImage in @('postgres:16-alpine', 'redis:7-alpine')) {
        $pinnedImagePattern = "(?m)^\s*image:\s*$([regex]::Escape($runtimeImage))@sha256:[a-f0-9]{64}\s*$"
        if ($dataManifestText -notmatch $pinnedImagePattern) {
            throw "Runtime image '$runtimeImage' must be pinned to an immutable sha256 digest."
        }
    }

    $secretTemplate = Join-Path $repoRoot 'deploy\k3s\secrets.example.yaml'
    $secretText = [IO.File]::ReadAllText($secretTemplate)
    foreach ($key in @(
        'POSTGRES_PASSWORD', 'PG_DSN', 'AUTH_PEPPER', 'ADMIN_EMAIL', 'ADMIN_PASSWORD',
        'DEEPSEEK_API_KEY', 'SMTP_USERNAME', 'SMTP_PASSWORD', 'OSS_ACCESS_KEY_ID',
        'OSS_ACCESS_KEY_SECRET', 'OSS_SESSION_TOKEN', 'OSS_BUCKET', 'OSS_ENDPOINT',
        'OSS_REGION', 'OSS_PREFIX', '.dockerconfigjson'
    )) {
        if ($secretText -notmatch "(?m)^\s*$([regex]::Escape($key)):\s*`"`"\s*$") {
            throw "Secret template key '$key' must remain empty."
        }
    }
    [xml]$lifecycle = [IO.File]::ReadAllText((Join-Path $repoRoot 'deploy\oss-lifecycle.example.xml'))
    if ([string]$lifecycle.LifecycleConfiguration.Rule.Expiration.Days -ne '7' -or
        [string]$lifecycle.LifecycleConfiguration.Rule.AbortMultipartUpload.Days -ne '1') {
        throw 'OSS lifecycle example must retain backups for 7 days and abort incomplete multipart uploads after 1 day.'
    }
    $canonicalOssPrefix = 'anime-companion/postgres'
    if ([string]$lifecycle.LifecycleConfiguration.Rule.Prefix -ne "$canonicalOssPrefix/") {
        throw "OSS lifecycle example must target the canonical backup prefix '$canonicalOssPrefix/'."
    }
    $secretInitializerText = [IO.File]::ReadAllText((Join-Path $repoRoot 'scripts\release\Initialize-K3sSecrets.ps1'))
    if ($secretInitializerText -match "Read-Host\s+'OSS backup prefix" -or
        -not $secretInitializerText.Contains("`$ossPrefix = '$canonicalOssPrefix'")) {
        throw "Secret initialization must use the canonical OSS backup prefix '$canonicalOssPrefix' without a customizable prompt."
    }
    $preflightText = [IO.File]::ReadAllText((Join-Path $repoRoot 'scripts\release\Preflight-K3s.ps1'))
    if (-not $preflightText.Contains("-ExpectedValues @{ OSS_PREFIX = '$canonicalOssPrefix' }")) {
        throw 'Preflight must reject an OSS Secret whose prefix does not match the lifecycle rule.'
    }
    $restoreDrillText = [IO.File]::ReadAllText((Join-Path $repoRoot 'scripts\release\Restore-Drill.ps1'))
    if ($restoreDrillText.Contains('[switch]$DropAfterVerification') -or
        -not $restoreDrillText.Contains('[switch]$KeepAfterVerification')) {
        throw 'Restore drills must drop temporary databases by default and require an explicit keep switch.'
    }
    if ($restoreDrillText.Contains('[string]$TemporaryDatabase') -or
        -not $restoreDrillText.Contains("[Guid]::NewGuid().ToString('N')")) {
        throw 'Restore drill database names must be generated internally with a GUID to prevent concurrent-name cleanup races.'
    }
    foreach ($capacityMarker in @(
        'pg_database_size(current_database())',
        'pg_database WHERE datname',
        'df -Pk /var/lib/postgresql/data',
        '$requiredFreeBytes',
        '$databaseCreated',
        '$databaseCreationAttempted'
    )) {
        if (-not $restoreDrillText.Contains($capacityMarker)) {
            throw "Restore drills are missing the capacity or cleanup guard '$capacityMarker'."
        }
    }

    $networkPolicyText = [IO.File]::ReadAllText((Join-Path $repoRoot 'deploy\k3s\base\network-policies.yaml'))
    foreach ($policyName in @(
        'default-deny', 'allow-cluster-dns', 'allow-postgres-callers', 'allow-redis-callers',
        'allow-agent-from-gateway', 'allow-gateway-from-traefik', 'allow-gateway-egress',
        'allow-agent-egress', 'allow-jobs-egress', 'allow-backup-egress',
        'allow-acme-solver-from-traefik'
    )) {
        if ($networkPolicyText -notmatch "(?m)^  name: $([regex]::Escape($policyName))$") {
            throw "Required NetworkPolicy '$policyName' is missing."
        }
    }
    foreach ($privateRange in @(
        '10.0.0.0/8', '100.64.0.0/10', '127.0.0.0/8', '169.254.0.0/16',
        '172.16.0.0/12', '192.168.0.0/16'
    )) {
        if ($networkPolicyText -notmatch [regex]::Escape($privateRange)) {
            throw "Public egress policies must exclude private range '$privateRange'."
        }
    }

    $platformText = [IO.File]::ReadAllText((Join-Path $repoRoot 'deploy\k3s\base\platform.yaml'))
    foreach ($mailSetting in @(
        '  SMTP_HOST: smtpdm.aliyun.com',
        '  SMTP_PORT: "465"',
        '  SMTP_IMPLICIT_TLS: "true"'
    )) {
        if (-not $platformText.Contains($mailSetting)) {
            throw "Alibaba Cloud DirectMail configuration is missing '$mailSetting'."
        }
    }
    if (-not $networkPolicyText.Contains('          port: 465')) {
        throw 'Gateway egress must allow Alibaba Cloud DirectMail SMTP on TCP 465.'
    }
    foreach ($identityKey in @('RELEASE_ID', 'BACKEND_COMMIT', 'FRONTEND_COMMIT')) {
        if ($platformText -match "(?m)^\s*${identityKey}:\s*") {
            throw "Release identity '$identityKey' must not live in the shared runtime ConfigMap."
        }
    }
    $applicationsText = [IO.File]::ReadAllText((Join-Path $repoRoot 'deploy\k3s\base\applications.yaml'))
    foreach ($identityPlaceholder in @('release-id-placeholder', 'backend-commit-placeholder', 'frontend-commit-placeholder')) {
        if ($applicationsText -notmatch "(?m)^\s*value:\s*`"?$([regex]::Escape($identityPlaceholder))`"?\s*$") {
            throw "Application pod templates must bind release identity placeholder '$identityPlaceholder' directly."
        }
    }

    $releaseText = [IO.File]::ReadAllText((Join-Path $repoRoot 'scripts\release\Release-K3s.ps1'))
    $rollbackText = [IO.File]::ReadAllText((Join-Path $repoRoot 'scripts\release\Rollback-K3s.ps1'))
    if ($rollbackText.Contains("'patch', 'configmap', 'anime-companion-config'")) {
        throw 'Rollback must not update shared release identity before application rollout succeeds.'
    }
    foreach ($identityReplacement in @(
        "'release-id-placeholder' = `$ReleaseTag",
        "'backend-commit-placeholder' = `$BackendCommit",
        "'frontend-commit-placeholder' = `$FrontendCommit"
    )) {
        if (-not $rollbackText.Contains($identityReplacement)) {
            throw "Rollback is missing atomic pod-template identity replacement: $identityReplacement"
        }
    }
    $manifestTestText = [IO.File]::ReadAllText($PSCommandPath)
    $unsafeServerValidationMarker = "@('apply', '--dry-run=server', '-f', `$renderedPath)"
    if ($manifestTestText.Contains($unsafeServerValidationMarker)) {
        throw 'Server validation must not apply the full overlay containing immutable fixed-name Jobs.'
    }
    $orderedReleaseMarkers = @(
        "Apply-Manifest 'deploy\k3s\base\network-policies.yaml'",
        "Apply-Manifest 'deploy\k3s\base\data.yaml'",
        "'rollout', 'status', 'statefulset/postgres'",
        "Apply-Manifest 'deploy\k3s\base\migration-job.yaml'",
        "'wait', '--for=condition=complete', 'job/database-migrate'",
        "Apply-Manifest 'deploy\k3s\base\seed-job.yaml'",
        "'wait', '--for=condition=complete', 'job/seed-admin'",
        "Apply-Manifest 'deploy\k3s\base\applications.yaml'"
    )
    $previousMarkerIndex = -1
    foreach ($marker in $orderedReleaseMarkers) {
        $markerIndex = $releaseText.IndexOf($marker, [StringComparison]::Ordinal)
        if ($markerIndex -lt 0) { throw "Release sequence marker is missing: $marker" }
        if ($markerIndex -le $previousMarkerIndex) {
            throw "Release sequence is unsafe near marker: $marker"
        }
        $previousMarkerIndex = $markerIndex
    }

    Assert-ImmutableImageReference 'registry.example.com/anime-companion/gateway:20260723-abcdef12-12345678'
    foreach ($unsafeImage in @(
        'registry.example.com/anime-companion/gateway:latest',
        "registry.example.com/anime-companion/gateway:safe`nkind: Secret"
    )) {
        $wasRejected = $false
        try { Assert-ImmutableImageReference $unsafeImage } catch { $wasRejected = $true }
        if (-not $wasRejected) { throw 'Immutable image guard accepted an unsafe reference.' }
    }

    Write-Host 'Secret template contains keys only; no credential values are present.'
    Write-Host 'Network isolation, release ordering, and immutable image guards passed.'
}
finally {
    if ([IO.Directory]::Exists($temporaryDirectory)) {
        [IO.Directory]::Delete($temporaryDirectory, $true)
    }
}
