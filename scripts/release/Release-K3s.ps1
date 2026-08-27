[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Registry,
    [Parameter(Mandatory)][string]$AcmeEmail,
    [Parameter(Mandatory)][string]$SmtpFrom,
    [Parameter(Mandatory)][string]$OssEndpoint,
    [Parameter(Mandatory)][string]$Domain,
    [Parameter(Mandatory)][string]$ExpectedPublicIP,
    [ValidateSet('staging', 'production')]
    [string]$Environment = 'staging',
    [string]$ReleaseTag,
    [string]$FrontendPath = (Join-Path $PSScriptRoot '..\..\..\Anime-Companion-ai-sos-chat-fronted'),
    [string]$KubeContext,
    [string]$SshTarget,
    [switch]$DomainStatusNormal,
    [switch]$ConfirmStagingCertificateValidated,
    [switch]$SkipIngress,
    [switch]$SkipBuild,
    [switch]$SkipSmoke,
    [switch]$SkipExternalNetworkChecks,
    [switch]$ContinuousDeployment
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'K3s.Common.ps1')

foreach ($command in @('git', 'docker', 'kubectl')) {
    Assert-CommandAvailable $command
}
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$frontendRoot = (Resolve-Path $FrontendPath).Path
$Registry = $Registry.Trim().TrimEnd('/')
$OssEndpoint = ($OssEndpoint.Trim() -replace '^https?://', '').TrimEnd('/')
if ($Registry -match '^https?://' -or $Registry -notmatch '^[A-Za-z0-9.-]+(?::\d+)?/[A-Za-z0-9._/-]+$') {
    throw 'Registry must be an ACR namespace path without a URL scheme, for example registry.example.com/team.'
}
if ($OssEndpoint -notmatch '^[A-Za-z0-9.-]+$') {
    throw 'OssEndpoint must be an OSS hostname without a URL scheme or path.'
}
if (-not $ContinuousDeployment -and -not $SshTarget) {
    throw 'SshTarget is required unless -ContinuousDeployment is used.'
}
if ($SshTarget -and $SshTarget -notmatch '^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+$') {
    throw 'SshTarget must use the safe user@host form.'
}
if ($ContinuousDeployment -and $Environment -ne 'production') {
    throw '-ContinuousDeployment is restricted to the production environment.'
}
if ($AcmeEmail -notmatch '^[^@\s]+@[^@\s]+\.[^@\s]+$') {
    throw 'AcmeEmail is not a valid contact email address.'
}
if (-not $SmtpFrom.Trim() -or $SmtpFrom -match 'placeholder|CHANGE_ME|[\r\n]') {
    throw 'SmtpFrom must identify the verified Alibaba Cloud DirectMail sender.'
}
if ($Environment -eq 'production' -and -not $SkipIngress -and -not $ConfirmStagingCertificateValidated) {
    throw 'Production certificate rollout requires -ConfirmStagingCertificateValidated after the staging certificate has been verified.'
}

Assert-CleanRepositoryPath $repoRoot
Assert-CleanRepositoryPath $frontendRoot
$backendCommit = Get-RepositoryCommit $repoRoot
$frontendCommit = Get-RepositoryCommit $frontendRoot
if (-not $ReleaseTag) {
    $ReleaseTag = '{0}-{1}-{2}' -f (Get-Date -AsUTC -Format 'yyyyMMddHHmmss'), $backendCommit.Substring(0, 8), $frontendCommit.Substring(0, 8)
}
if ($ReleaseTag -notmatch '^[A-Za-z0-9_][A-Za-z0-9_.-]{5,127}$' -or $ReleaseTag -eq 'latest') {
    throw "ReleaseTag '$ReleaseTag' is not a valid immutable Docker tag."
}

$images = [ordered]@{
    Gateway = "$Registry/gateway:$ReleaseTag"
    Agent = "$Registry/agent:$ReleaseTag"
    Migrate = "$Registry/migrate:$ReleaseTag"
    Backup = "$Registry/backup:$ReleaseTag"
}
foreach ($image in $images.Values) { Assert-ImmutableImageReference $image }

$manifestTests = @{
    Environment = $Environment
    KubeContext = $KubeContext
}
if (-not $ContinuousDeployment) {
    $manifestTests.ServerValidation = $true
}
& (Join-Path $PSScriptRoot 'Test-Manifests.ps1') @manifestTests
$preflight = @{
    Environment = $Environment
    KubeContext = $KubeContext
    Domain = $Domain
    ExpectedPublicIP = $ExpectedPublicIP
    RegistryEndpoint = $Registry.Split('/')[0]
    OssEndpoint = $OssEndpoint
    SshTarget = $SshTarget
    DomainStatusNormal = $DomainStatusNormal
    SkipDomainGates = $SkipIngress
    SkipExternalNetworkChecks = $SkipExternalNetworkChecks
    SkipSecretChecks = $ContinuousDeployment
}
& (Join-Path $PSScriptRoot 'Preflight-K3s.ps1') @preflight

function Test-RemoteImageExists {
    param([Parameter(Mandatory)][string]$Image)

    & docker buildx imagetools inspect $Image *> $null
    return $LASTEXITCODE -eq 0
}

foreach ($image in $images.Values) {
    $exists = Test-RemoteImageExists $image
    if ($SkipBuild -and -not $exists) {
        throw "-SkipBuild was requested, but remote image '$image' does not exist or cannot be read."
    }
    if (-not $SkipBuild -and $exists) {
        throw "Remote image '$image' already exists. Choose a new immutable ReleaseTag; existing tags are never overwritten."
    }
}

$buildTimestamp = Get-Date -AsUTC -Format 'yyyy-MM-ddTHH:mm:ssZ'
if (-not $SkipBuild) {
    $commonBuildArguments = @(
        'buildx', 'build', '--platform', 'linux/amd64',
        '--build-arg', "BACKEND_COMMIT=$backendCommit",
        '--build-arg', "BUILD_TIMESTAMP=$buildTimestamp",
        '--push'
    )
    Invoke-NativeCommand -FilePath 'docker' -ArgumentList @(
        $commonBuildArguments +
        @('--file', (Join-Path $repoRoot 'Dockerfile.gateway'), '--build-context', "frontend=$frontendRoot",
          '--build-arg', "FRONTEND_COMMIT=$frontendCommit", '--tag', $images.Gateway, $repoRoot)
    )
    Invoke-NativeCommand -FilePath 'docker' -ArgumentList @(
        $commonBuildArguments +
        @('--file', (Join-Path $repoRoot 'Dockerfile.agent'), '--tag', $images.Agent, $repoRoot)
    )
    Invoke-NativeCommand -FilePath 'docker' -ArgumentList @(
        $commonBuildArguments +
        @('--file', (Join-Path $repoRoot 'Dockerfile.migrate'), '--tag', $images.Migrate, $repoRoot)
    )
    Invoke-NativeCommand -FilePath 'docker' -ArgumentList @(
        $commonBuildArguments +
        @('--file', (Join-Path $repoRoot 'Dockerfile.backup'), '--tag', $images.Backup, $repoRoot)
    )
}

$replacements = @{
    'anime-companion-gateway:release-placeholder' = $images.Gateway
    'anime-companion-agent:release-placeholder' = $images.Agent
    'anime-companion-migrate:release-placeholder' = $images.Migrate
    'anime-companion-backup:release-placeholder' = $images.Backup
    'smtp-from-placeholder@animecompanion.invalid' = ($SmtpFrom | ConvertTo-Json -Compress)
    'acme-email-placeholder@animecompanion.invalid' = $AcmeEmail
    'release-id-placeholder' = $ReleaseTag
    'backend-commit-placeholder' = $backendCommit
    'frontend-commit-placeholder' = $frontendCommit
}

function Get-RenderedManifest {
    param([Parameter(Mandatory)][string]$RelativePath)

    $name = ($RelativePath -replace '[\\/]', '-')
    $destination = Join-Path $temporaryDirectory $name
    Render-ManifestFile -Source (Join-Path $repoRoot $RelativePath) -Destination $destination -Replacements $replacements
    return $destination
}

function Apply-Manifest {
    param([Parameter(Mandatory)][string]$RelativePath)
    $rendered = Get-RenderedManifest $RelativePath
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('apply', '-f', $rendered)
}

$previousGatewayImage = ''
$previousAgentImage = ''
$previousReleaseTag = ''
$previousBackendCommit = ''
$previousFrontendCommit = ''
$recordedGatewayImage = ''
$recordedAgentImage = ''
$hasPreviousReleaseMetadata = $false
try {
    $previousGatewayImage = (Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'get', 'deployment', 'gateway', '-o', 'jsonpath={.spec.template.spec.containers[0].image}') -CaptureOutput).Trim()
    $previousAgentImage = (Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'get', 'deployment', 'agent', '-o', 'jsonpath={.spec.template.spec.containers[0].image}') -CaptureOutput).Trim()
}
catch {
    # First deployment has no previous application images.
}
try {
    $previousRelease = Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'get', 'configmap', 'anime-companion-release', '-o', 'json') -CaptureOutput | ConvertFrom-Json
    $previousReleaseTag = [string]$previousRelease.data.'release-tag'
    $previousBackendCommit = [string]$previousRelease.data.'backend-commit'
    $previousFrontendCommit = [string]$previousRelease.data.'frontend-commit'
    $recordedGatewayImage = [string]$previousRelease.data.'gateway-image'
    $recordedAgentImage = [string]$previousRelease.data.'agent-image'
    $hasPreviousReleaseMetadata = $true
}
catch {
    # First deployment has no previous release identity.
}

function Restore-RecordedApplicationImages {
    param(
        [Parameter(Mandatory)][string]$GatewayImage,
        [Parameter(Mandatory)][string]$AgentImage
    )

    Assert-ImmutableImageReference $GatewayImage
    Assert-ImmutableImageReference $AgentImage
    Invoke-Kubectl -Context $KubeContext -ArgumentList @(
        '-n', 'anime-companion', 'set', 'image', 'deployment/gateway', "gateway=$GatewayImage"
    )
    Invoke-Kubectl -Context $KubeContext -ArgumentList @(
        '-n', 'anime-companion', 'set', 'image', 'deployment/agent', "agent=$AgentImage"
    )
    Invoke-Kubectl -Context $KubeContext -ArgumentList @(
        '-n', 'anime-companion', 'rollout', 'status', 'deployment/agent', '--timeout=10m'
    )
    Invoke-Kubectl -Context $KubeContext -ArgumentList @(
        '-n', 'anime-companion', 'rollout', 'status', 'deployment/gateway', '--timeout=10m'
    )
}

if ($hasPreviousReleaseMetadata -and
    (($recordedGatewayImage -and $recordedGatewayImage -ne $previousGatewayImage) -or
     ($recordedAgentImage -and $recordedAgentImage -ne $previousAgentImage))) {
    if (-not $ContinuousDeployment) {
        throw 'Deployment images differ from anime-companion-release metadata. Reconcile the manual drift before creating another release.'
    }
    if (-not $recordedGatewayImage -or -not $recordedAgentImage) {
        throw 'Continuous deployment cannot recover drift because the recorded application images are incomplete.'
    }
    Write-Warning 'Application images differ from the last recorded release; restoring the recorded images before retrying deployment.'
    Restore-RecordedApplicationImages -GatewayImage $recordedGatewayImage -AgentImage $recordedAgentImage
    $previousGatewayImage = $recordedGatewayImage
    $previousAgentImage = $recordedAgentImage
}

$temporaryDirectory = New-TemporaryDirectory
$applicationsTouched = $false
try {
    if (-not $ContinuousDeployment) {
        Apply-Manifest 'deploy\k3s\base\namespace.yaml'
    }
    Apply-Manifest 'deploy\k3s\base\platform.yaml'
    Apply-Manifest 'deploy\k3s\base\network-policies.yaml'
    Apply-Manifest 'deploy\k3s\base\data.yaml'
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'rollout', 'status', 'statefulset/postgres', '--timeout=10m')
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'rollout', 'status', 'statefulset/redis', '--timeout=5m')

    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'delete', 'job', 'database-migrate', '--ignore-not-found=true', '--wait=true')
    Apply-Manifest 'deploy\k3s\base\migration-job.yaml'
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'wait', '--for=condition=complete', 'job/database-migrate', '--timeout=10m')

    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'delete', 'job', 'seed-admin', '--ignore-not-found=true', '--wait=true')
    Apply-Manifest 'deploy\k3s\base\seed-job.yaml'
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'wait', '--for=condition=complete', 'job/seed-admin', '--timeout=5m')

    $applicationsTouched = $true
    Apply-Manifest 'deploy\k3s\base\applications.yaml'
    Apply-Manifest 'deploy\k3s\base\backup.yaml'
    Apply-Manifest 'deploy\k3s\base\traefik-middleware.yaml'
    if (-not $SkipIngress) {
        if (-not $ContinuousDeployment) {
            Apply-Manifest 'deploy\k3s\cert-manager\issuers.yaml'
        }
        $issuerName = if ($Environment -eq 'staging') { 'letsencrypt-staging' } else { 'letsencrypt-production' }
        Invoke-Kubectl -Context $KubeContext -ArgumentList @('wait', '--for=condition=Ready', "clusterissuer/$issuerName", '--timeout=5m')
        Apply-Manifest "deploy\k3s\overlays\$Environment\ingress.yaml"
        $certificateName = "animecompanion-icu-$Environment-tls"
        Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'wait', '--for=condition=Ready', "certificate/$certificateName", '--timeout=15m')
    }

    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'rollout', 'status', 'deployment/agent', '--timeout=10m')
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'rollout', 'status', 'deployment/gateway', '--timeout=10m')

    if (-not $SkipSmoke -and -not $SkipIngress) {
        & (Join-Path $PSScriptRoot 'Smoke-K3s.ps1') -Domain $Domain -AllowUntrustedTls:($Environment -eq 'staging')
    }

    $releaseMetadata = Invoke-Kubectl -Context $KubeContext -ArgumentList @(
        '-n', 'anime-companion', 'create', 'configmap', 'anime-companion-release',
        "--from-literal=release-tag=$ReleaseTag",
        "--from-literal=backend-commit=$backendCommit",
        "--from-literal=frontend-commit=$frontendCommit",
        "--from-literal=gateway-image=$($images.Gateway)",
        "--from-literal=agent-image=$($images.Agent)",
        "--from-literal=previous-gateway-image=$previousGatewayImage",
        "--from-literal=previous-agent-image=$previousAgentImage",
        "--from-literal=previous-release-tag=$previousReleaseTag",
        "--from-literal=previous-backend-commit=$previousBackendCommit",
        "--from-literal=previous-frontend-commit=$previousFrontendCommit",
        "--from-literal=migrate-image=$($images.Migrate)",
        "--from-literal=backup-image=$($images.Backup)",
        "--from-literal=build-timestamp=$buildTimestamp",
        '--dry-run=client', '-o', 'yaml'
    ) -CaptureOutput
    $releaseMetadataPath = Join-Path $temporaryDirectory 'release-metadata.yaml'
    [IO.File]::WriteAllText($releaseMetadataPath, $releaseMetadata, [Text.UTF8Encoding]::new($false))
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('apply', '-f', $releaseMetadataPath)
}
catch {
    $releaseFailure = $_
    if ($ContinuousDeployment -and $applicationsTouched -and $previousGatewayImage -and $previousAgentImage) {
        try {
            Write-Warning 'Release failed after application deployment started; restoring the previously running application images.'
            Restore-RecordedApplicationImages -GatewayImage $previousGatewayImage -AgentImage $previousAgentImage
            Write-Warning 'Automatic application rollback completed; release metadata was not advanced.'
        }
        catch {
            throw "Release failed and automatic application rollback also failed. Release error: $($releaseFailure.Exception.Message) Rollback error: $($_.Exception.Message)"
        }
    }
    throw $releaseFailure
}
finally {
    if ([IO.Directory]::Exists($temporaryDirectory)) {
        [IO.Directory]::Delete($temporaryDirectory, $true)
    }
}

Write-Host "Release completed: $ReleaseTag"
Write-Host "Backend commit: $backendCommit"
Write-Host "Frontend commit: $frontendCommit"
Write-Host "Gateway image: $($images.Gateway)"
Write-Host "Agent image: $($images.Agent)"
