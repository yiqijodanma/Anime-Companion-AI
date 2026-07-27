[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$GatewayImage,
    [Parameter(Mandatory)][string]$AgentImage,
    [string]$ReleaseTag,
    [string]$BackendCommit,
    [string]$FrontendCommit,
    [string]$KubeContext,
    [switch]$SkipSmoke
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'K3s.Common.ps1')

function Get-ReleaseValue {
    param([object]$Data, [Parameter(Mandatory)][string]$Name)

    if ($null -eq $Data) { return '' }
    $property = $Data.PSObject.Properties[$Name]
    if ($null -eq $property) { return '' }
    return [string]$property.Value
}

Assert-CommandAvailable 'kubectl'
Assert-ImmutableImageReference $GatewayImage
Assert-ImmutableImageReference $AgentImage
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$temporaryDirectory = New-TemporaryDirectory
$releaseData = $null

try {
    try {
        $release = Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'get', 'configmap', 'anime-companion-release', '-o', 'json') -CaptureOutput | ConvertFrom-Json
        $releaseData = $release.data
    }
    catch {
        if (-not ($ReleaseTag -and $BackendCommit -and $FrontendCommit)) {
            throw 'Release metadata is unavailable. Supply -ReleaseTag, -BackendCommit, and -FrontendCommit explicitly.'
        }
    }

    $recordedGatewayImage = Get-ReleaseValue $releaseData 'previous-gateway-image'
    $recordedAgentImage = Get-ReleaseValue $releaseData 'previous-agent-image'
    $usesRecordedPrevious = $GatewayImage -eq $recordedGatewayImage -and $AgentImage -eq $recordedAgentImage
    if ($usesRecordedPrevious) {
        if (-not $ReleaseTag) { $ReleaseTag = Get-ReleaseValue $releaseData 'previous-release-tag' }
        if (-not $BackendCommit) { $BackendCommit = Get-ReleaseValue $releaseData 'previous-backend-commit' }
        if (-not $FrontendCommit) { $FrontendCommit = Get-ReleaseValue $releaseData 'previous-frontend-commit' }
    }
    if (-not ($ReleaseTag -and $BackendCommit -and $FrontendCommit)) {
        throw 'The selected images are not the recorded immediately previous pair. Supply their exact -ReleaseTag, -BackendCommit, and -FrontendCommit.'
    }
    if ($ReleaseTag -notmatch '^[A-Za-z0-9_][A-Za-z0-9_.-]{5,127}$' -or $ReleaseTag -eq 'latest') {
        throw "ReleaseTag '$ReleaseTag' is not a valid immutable release identifier."
    }
    if ($BackendCommit -notmatch '^[0-9a-fA-F]{40}$' -or $FrontendCommit -notmatch '^[0-9a-fA-F]{40}$') {
        throw 'BackendCommit and FrontendCommit must be full 40-character Git commit IDs.'
    }

    $currentGatewayImage = (Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'get', 'deployment', 'gateway', '-o', 'jsonpath={.spec.template.spec.containers[0].image}') -CaptureOutput).Trim()
    $currentAgentImage = (Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'get', 'deployment', 'agent', '-o', 'jsonpath={.spec.template.spec.containers[0].image}') -CaptureOutput).Trim()
    $currentRecordedGatewayImage = Get-ReleaseValue $releaseData 'gateway-image'
    $currentRecordedAgentImage = Get-ReleaseValue $releaseData 'agent-image'
    if (($currentRecordedGatewayImage -and $currentRecordedGatewayImage -ne $currentGatewayImage) -or
        ($currentRecordedAgentImage -and $currentRecordedAgentImage -ne $currentAgentImage)) {
        throw 'Deployment images differ from anime-companion-release metadata. Reconcile the manual drift before rollback.'
    }
    $currentReleaseTag = Get-ReleaseValue $releaseData 'release-tag'
    $currentBackendCommit = Get-ReleaseValue $releaseData 'backend-commit'
    $currentFrontendCommit = Get-ReleaseValue $releaseData 'frontend-commit'

    $renderedPath = Join-Path $temporaryDirectory 'applications.yaml'
    Render-ManifestFile -Source (Join-Path $repoRoot 'deploy\k3s\base\applications.yaml') -Destination $renderedPath -Replacements @{
        'anime-companion-gateway:release-placeholder' = $GatewayImage
        'anime-companion-agent:release-placeholder' = $AgentImage
        'release-id-placeholder' = $ReleaseTag
        'backend-commit-placeholder' = $BackendCommit
        'frontend-commit-placeholder' = $FrontendCommit
    }
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('apply', '-f', $renderedPath)
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'rollout', 'status', 'deployment/agent', '--timeout=10m')
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'rollout', 'status', 'deployment/gateway', '--timeout=10m')

    $metadata = Invoke-Kubectl -Context $KubeContext -ArgumentList @(
        '-n', 'anime-companion', 'create', 'configmap', 'anime-companion-release',
        "--from-literal=release-tag=$ReleaseTag",
        "--from-literal=backend-commit=$BackendCommit",
        "--from-literal=frontend-commit=$FrontendCommit",
        "--from-literal=gateway-image=$GatewayImage",
        "--from-literal=agent-image=$AgentImage",
        "--from-literal=previous-gateway-image=$currentGatewayImage",
        "--from-literal=previous-agent-image=$currentAgentImage",
        "--from-literal=previous-release-tag=$currentReleaseTag",
        "--from-literal=previous-backend-commit=$currentBackendCommit",
        "--from-literal=previous-frontend-commit=$currentFrontendCommit",
        "--from-literal=migrate-image=$(Get-ReleaseValue $releaseData 'migrate-image')",
        "--from-literal=backup-image=$(Get-ReleaseValue $releaseData 'backup-image')",
        "--from-literal=rollback-timestamp=$(Get-Date -AsUTC -Format 'yyyy-MM-ddTHH:mm:ssZ')",
        '--dry-run=client', '-o', 'yaml'
    ) -CaptureOutput
    $metadataPath = Join-Path $temporaryDirectory 'release-metadata.yaml'
    [IO.File]::WriteAllText($metadataPath, $metadata, [Text.UTF8Encoding]::new($false))
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('apply', '-f', $metadataPath)

    if (-not $SkipSmoke) {
        & (Join-Path $PSScriptRoot 'Smoke-K3s.ps1')
    }
}
finally {
    if ([IO.Directory]::Exists($temporaryDirectory)) {
        [IO.Directory]::Delete($temporaryDirectory, $true)
    }
}

Write-Host "Rolled back to release $ReleaseTag."
Write-Warning 'Application images were rolled back. Database migrations were not changed; review compatibility and use a forward fix rather than an automatic down migration.'
