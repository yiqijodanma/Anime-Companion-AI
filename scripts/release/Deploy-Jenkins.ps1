[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$ReleaseParametersPath,
    [Parameter(Mandatory)][string]$BackendPath,
    [Parameter(Mandatory)][string]$FrontendPath,
    [switch]$RegistryHostOnly
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'K3s.Common.ps1')

function Read-JenkinsReleaseParameters {
    param([Parameter(Mandatory)][string]$Path)

    $resolvedPath = (Resolve-Path -LiteralPath $Path -ErrorAction Stop).Path
    $file = Get-Item -LiteralPath $resolvedPath -Force
    if (-not $file.PSIsContainer -and ($file.Length -le 0 -or $file.Length -gt 16KB)) {
        throw 'Release parameters must be a non-empty JSON file no larger than 16 KiB.'
    }
    if ($file.PSIsContainer) {
        throw 'ReleaseParametersPath must identify a JSON file.'
    }

    $document = $null
    try {
        $document = [Text.Json.JsonDocument]::Parse([IO.File]::ReadAllText($resolvedPath))
        if ($document.RootElement.ValueKind -ne [Text.Json.JsonValueKind]::Object) {
            throw 'Release parameters JSON must contain one object.'
        }

        $requiredNames = @('registry', 'acmeEmail', 'smtpFrom', 'ossEndpoint', 'domain', 'expectedPublicIP')
        $properties = @($document.RootElement.EnumerateObject())
        $actualNames = @($properties | ForEach-Object Name)
        if ($actualNames.Count -ne $requiredNames.Count) {
            throw "Release parameters JSON must contain exactly: $($requiredNames -join ', ')."
        }
        foreach ($name in $requiredNames) {
            if (-not ($actualNames -ccontains $name)) {
                throw "Release parameters JSON is missing exact property '$name'."
            }
        }

        $values = @{}
        foreach ($property in $properties) {
            if ($property.Value.ValueKind -ne [Text.Json.JsonValueKind]::String) {
                throw "Release parameter '$($property.Name)' must be a string."
            }
            $value = $property.Value.GetString()
            if ([string]::IsNullOrWhiteSpace($value) -or $value -cne $value.Trim()) {
                throw "Release parameter '$($property.Name)' must be a non-empty, trimmed string."
            }
            $values[$property.Name] = $value
        }
    }
    catch [Text.Json.JsonException] {
        throw 'Release parameters file is not valid strict JSON.'
    }
    finally {
        if ($null -ne $document) { $document.Dispose() }
    }

    $registry = [string]$values.registry
    if ($registry.Length -gt 512 -or $registry -match '^https?://' -or
        $registry -notmatch '^[A-Za-z0-9.-]+(?::\d+)?/[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*$') {
        throw 'registry must be an ACR namespace path without a URL scheme.'
    }
    $registryHost = $registry.Split('/')[0]
    if ($registryHost -match ':(\d+)$') {
        $registryPort = [int]$Matches[1]
        if ($registryPort -lt 1 -or $registryPort -gt 65535) {
            throw 'registry contains an invalid TCP port.'
        }
    }

    $acmeEmail = [string]$values.acmeEmail
    if ($acmeEmail.Length -gt 254 -or $acmeEmail -notmatch '^[^@\s]+@[^@\s]+\.[^@\s]+$') {
        throw 'acmeEmail is not a valid contact email address.'
    }

    $smtpFrom = [string]$values.smtpFrom
    if ($smtpFrom.Length -gt 320 -or $smtpFrom -match 'placeholder|CHANGE_ME|[\x00-\x1F\x7F]') {
        throw 'smtpFrom must identify the verified Alibaba Cloud DirectMail sender on one line.'
    }

    $ossEndpoint = [string]$values.ossEndpoint
    if ($ossEndpoint.Length -gt 253 -or $ossEndpoint -notmatch '^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$' -or
        $ossEndpoint.Contains('..')) {
        throw 'ossEndpoint must be an OSS hostname without a URL scheme, port, or path.'
    }

    $domain = [string]$values.domain
    if ($domain -notmatch '^(?=.{1,253}$)(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$') {
        throw 'domain must be a safe fully qualified domain name.'
    }

    $expectedPublicIP = [string]$values.expectedPublicIP
    $parsedPublicIP = $null
    if (-not [Net.IPAddress]::TryParse($expectedPublicIP, [ref]$parsedPublicIP) -or
        $parsedPublicIP.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork -or
        $parsedPublicIP.ToString() -cne $expectedPublicIP) {
        throw 'expectedPublicIP must be a canonical IPv4 address.'
    }

    return [pscustomobject]@{
        Registry = $registry
        RegistryHost = $registryHost
        AcmeEmail = $acmeEmail
        SmtpFrom = $smtpFrom
        OssEndpoint = $ossEndpoint
        Domain = $domain
        ExpectedPublicIP = $expectedPublicIP
    }
}

$releaseParameters = Read-JenkinsReleaseParameters -Path $ReleaseParametersPath
if ($RegistryHostOnly) {
    Write-Output $releaseParameters.RegistryHost
    return
}

if ([string]::IsNullOrWhiteSpace($env:KUBECONFIG) -or
    $env:KUBECONFIG.Contains([IO.Path]::PathSeparator) -or
    -not [IO.File]::Exists($env:KUBECONFIG)) {
    throw 'KUBECONFIG must identify exactly one Jenkins-managed kubeconfig file.'
}

$backendRoot = (Resolve-Path -LiteralPath $BackendPath -ErrorAction Stop).Path
$frontendRoot = (Resolve-Path -LiteralPath $FrontendPath -ErrorAction Stop).Path
foreach ($repositoryPath in @($backendRoot, $frontendRoot)) {
    if (-not [IO.Directory]::Exists((Join-Path $repositoryPath '.git'))) {
        throw "Repository path '$repositoryPath' is not a Git checkout."
    }
    Assert-CleanRepositoryPath $repositoryPath
}

$configuredServer = (Invoke-Kubectl -ArgumentList @('config', 'view', '--minify', '--raw', '-o', 'jsonpath={.clusters[0].cluster.server}') -CaptureOutput).Trim()
if ($configuredServer -cne 'https://127.0.0.1:16443') {
    throw 'Jenkins kubeconfig must connect only through https://127.0.0.1:16443.'
}

$backendCommit = Get-RepositoryCommit $backendRoot
$frontendCommit = Get-RepositoryCommit $frontendRoot
$currentReleaseJson = Invoke-Kubectl -ArgumentList @(
    '-n', 'anime-companion', 'get', 'configmap', 'anime-companion-release',
    '--ignore-not-found=true', '-o', 'json'
) -CaptureOutput
if ($currentReleaseJson.Trim()) {
    try {
        $currentRelease = $currentReleaseJson | ConvertFrom-Json -Depth 20
    }
    catch {
        throw 'Existing anime-companion-release ConfigMap is not valid JSON.'
    }
    $deployedBackendCommit = [string]$currentRelease.data.'backend-commit'
    $deployedFrontendCommit = [string]$currentRelease.data.'frontend-commit'
    if ($deployedBackendCommit -ceq $backendCommit -and $deployedFrontendCommit -ceq $frontendCommit) {
        try {
            $recordedGatewayImage = [string]$currentRelease.data.'gateway-image'
            $recordedAgentImage = [string]$currentRelease.data.'agent-image'
            Assert-ImmutableImageReference $recordedGatewayImage
            Assert-ImmutableImageReference $recordedAgentImage

            $actualGatewayImage = (Invoke-Kubectl -ArgumentList @(
                '-n', 'anime-companion', 'get', 'deployment', 'gateway',
                '-o', 'jsonpath={.spec.template.spec.containers[0].image}'
            ) -CaptureOutput).Trim()
            $actualAgentImage = (Invoke-Kubectl -ArgumentList @(
                '-n', 'anime-companion', 'get', 'deployment', 'agent',
                '-o', 'jsonpath={.spec.template.spec.containers[0].image}'
            ) -CaptureOutput).Trim()
            if ($actualGatewayImage -cne $recordedGatewayImage -or $actualAgentImage -cne $recordedAgentImage) {
                throw 'Application images differ from the recorded release metadata.'
            }

            Invoke-Kubectl -ArgumentList @(
                '-n', 'anime-companion', 'rollout', 'status', 'deployment/agent', '--timeout=5m'
            )
            Invoke-Kubectl -ArgumentList @(
                '-n', 'anime-companion', 'rollout', 'status', 'deployment/gateway', '--timeout=5m'
            )
            Invoke-Kubectl -ArgumentList @(
                '-n', 'anime-companion', 'wait', '--for=condition=Ready',
                'certificate/animecompanion-icu-production-tls', '--timeout=5m'
            )
            & (Join-Path $backendRoot 'scripts\release\Smoke-K3s.ps1') -Domain $releaseParameters.Domain

            Write-Host 'Production contains both requested commits and passed health checks; deployment is a safe no-op.'
            return
        }
        catch {
            Write-Warning "The recorded commits match, but production health verification failed; creating a recovery release. $($_.Exception.Message)"
        }
    }
}

$releaseArguments = @{
    Registry = $releaseParameters.Registry
    AcmeEmail = $releaseParameters.AcmeEmail
    SmtpFrom = $releaseParameters.SmtpFrom
    OssEndpoint = $releaseParameters.OssEndpoint
    Domain = $releaseParameters.Domain
    ExpectedPublicIP = $releaseParameters.ExpectedPublicIP
    Environment = 'production'
    FrontendPath = $frontendRoot
    DomainStatusNormal = $true
    ConfirmStagingCertificateValidated = $true
    ContinuousDeployment = $true
}
& (Join-Path $backendRoot 'scripts\release\Release-K3s.ps1') @releaseArguments
