[CmdletBinding()]
param(
    [ValidateSet('staging', 'production')]
    [string]$Environment = 'staging',
    [string]$KubeContext,
    [Parameter(Mandatory)][string]$Domain,
    [Parameter(Mandatory)][string]$ExpectedPublicIP,
    [string]$RegistryEndpoint,
    [string]$OssEndpoint,
    [string]$SshTarget,
    [switch]$DomainStatusNormal,
    [switch]$SkipDomainGates,
    [switch]$SkipExternalNetworkChecks,
    [switch]$SkipSecretChecks
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'K3s.Common.ps1')

function Convert-KubernetesQuantityToBytes {
    param([Parameter(Mandatory)][string]$Quantity)

    if ($Quantity -match '^(\d+)(Ki|Mi|Gi|Ti)$') {
        $powers = @{ Ki = 1; Mi = 2; Gi = 3; Ti = 4 }
        return [int64]$Matches[1] * [math]::Pow(1024, $powers[$Matches[2]])
    }
    if ($Quantity -match '^\d+$') {
        return [int64]$Quantity
    }
    throw "Unsupported Kubernetes storage quantity '$Quantity'."
}

function Test-TcpPort {
    param([Parameter(Mandatory)][string]$HostName, [Parameter(Mandatory)][int]$Port)

    $client = [Net.Sockets.TcpClient]::new()
    try {
        $task = $client.ConnectAsync($HostName, $Port)
        if (-not $task.Wait([TimeSpan]::FromSeconds(5))) { return $false }
        return $client.Connected
    }
    catch { return $false }
    finally { $client.Dispose() }
}

if ($PSVersionTable.PSVersion.Major -lt 7) {
    throw 'PowerShell 7 or newer is required.'
}
if ($Domain -notmatch '^(?=.{1,253}$)(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$') {
    throw "Domain '$Domain' is not a safe fully qualified domain name."
}
$parsedPublicIP = $null
if (-not [Net.IPAddress]::TryParse($ExpectedPublicIP, [ref]$parsedPublicIP) -or $parsedPublicIP.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) {
    throw "ExpectedPublicIP '$ExpectedPublicIP' is not a valid IPv4 address."
}
foreach ($endpoint in @($RegistryEndpoint, $OssEndpoint) | Where-Object { $_ }) {
    $endpointHost = (($endpoint -replace '^https?://', '').Split('/')[0])
    if ($endpointHost -notmatch '^[A-Za-z0-9.-]+(?::\d+)?$') {
        throw "Endpoint '$endpoint' is not a safe hostname."
    }
}
if ($SshTarget -and $SshTarget -notmatch '^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+$') {
    throw 'SshTarget must use the safe user@host form.'
}
foreach ($command in @('git', 'docker', 'kubectl')) {
    Assert-CommandAvailable $command
}
Invoke-NativeCommand -FilePath 'docker' -ArgumentList @('buildx', 'version') | Out-Null

$version = Invoke-Kubectl -Context $KubeContext -ArgumentList @('version', '-o', 'json') -CaptureOutput | ConvertFrom-Json
$minorVersion = [int]([string]$version.serverVersion.minor -replace '\D.*$', '')
if ([int]$version.serverVersion.major -lt 1 -or $minorVersion -lt 27) {
    throw "Kubernetes $($version.serverVersion.gitVersion) does not support CronJob timeZone; use Kubernetes 1.27 or newer."
}
if ([string]$version.serverVersion.gitVersion -notmatch '^v1\.36\.\d+\+k3s\d+$') {
    throw "This release profile is validated for the target k3s 1.36 patch line; found '$($version.serverVersion.gitVersion)'. Review manifests and cert-manager compatibility before changing the gate."
}

$nodes = Invoke-Kubectl -Context $KubeContext -ArgumentList @('get', 'nodes', '-o', 'json') -CaptureOutput | ConvertFrom-Json
if ($nodes.items.Count -ne 1) {
    throw "Expected one k3s node; found $($nodes.items.Count)."
}
$node = $nodes.items[0]
if ($node.status.nodeInfo.architecture -ne 'amd64' -or $node.status.nodeInfo.operatingSystem -ne 'linux') {
    throw "Release images target linux/amd64, but the node reports $($node.status.nodeInfo.operatingSystem)/$($node.status.nodeInfo.architecture)."
}
$ephemeralBytes = Convert-KubernetesQuantityToBytes ([string]$node.status.allocatable.'ephemeral-storage')
if ($ephemeralBytes -lt 35GB) {
    throw 'Node allocatable ephemeral storage is below the 35 GiB deployment floor.'
}

Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'kube-system', 'get', 'deployment', 'traefik', '-o', 'name') | Out-Null
Invoke-Kubectl -Context $KubeContext -ArgumentList @('get', 'storageclass', 'local-path', '-o', 'name') | Out-Null
Invoke-Kubectl -Context $KubeContext -ArgumentList @('get', 'customresourcedefinition', 'clusterissuers.cert-manager.io', '-o', 'name') | Out-Null
Invoke-Kubectl -Context $KubeContext -ArgumentList @('get', 'customresourcedefinition', 'middlewares.traefik.io', '-o', 'name') | Out-Null
$certManagerDeployment = Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'cert-manager', 'get', 'deployment', 'cert-manager', '-o', 'json') -CaptureOutput | ConvertFrom-Json
$certManagerImage = [string]$certManagerDeployment.spec.template.spec.containers[0].image
if ($certManagerImage -notmatch ':v1\.21\.\d+(?:@sha256:[0-9a-f]{64})?$') {
    throw "k3s v1.36 requires the tested cert-manager 1.21 line; found '$certManagerImage'. Install v1.21.0 or a reviewed newer 1.21 patch."
}
$traefikService = Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'kube-system', 'get', 'service', 'traefik', '-o', 'json') -CaptureOutput | ConvertFrom-Json
$traefikPorts = @($traefikService.spec.ports | ForEach-Object { [int]$_.port })
foreach ($port in @(80, 443)) {
    if ($port -notin $traefikPorts) { throw "Traefik Service does not expose required port $port." }
}

if (-not $SkipSecretChecks) {
    Assert-SecretKeys -Context $KubeContext -Namespace 'anime-companion' -Name 'anime-companion-secrets' -Keys @(
        'POSTGRES_PASSWORD', 'PG_DSN', 'AUTH_PEPPER', 'ADMIN_EMAIL', 'ADMIN_PASSWORD',
        'DEEPSEEK_API_KEY', 'SMTP_USERNAME', 'SMTP_PASSWORD'
    )
    Assert-SecretKeys -Context $KubeContext -Namespace 'anime-companion' -Name 'oss-backup-secrets' -Keys @(
        'OSS_ACCESS_KEY_ID', 'OSS_ACCESS_KEY_SECRET', 'OSS_BUCKET', 'OSS_ENDPOINT', 'OSS_REGION', 'OSS_PREFIX'
    ) -ExpectedValues @{ OSS_PREFIX = 'anime-companion/postgres' }
    Assert-SecretKeys -Context $KubeContext -Namespace 'anime-companion' -Name 'acr-pull' -Keys @('.dockerconfigjson')
}

if ($SshTarget) {
    Assert-CommandAvailable 'ssh'
    $diskOutput = Invoke-NativeCommand -FilePath 'ssh' -ArgumentList @($SshTarget, 'df -Pk /var/lib/rancher/k3s') -CaptureOutput
    $diskLine = @($diskOutput -split "`r?`n" | Where-Object { $_.Trim() })[-1]
    $diskFields = @($diskLine -split '\s+' | Where-Object { $_ })
    if ($diskFields.Count -lt 6 -or ([int64]$diskFields[3] * 1KB) -lt 35GB) {
        throw 'The k3s data filesystem has less than 35 GiB free.'
    }
    $listeners = Invoke-NativeCommand -FilePath 'ssh' -ArgumentList @($SshTarget, "sudo -n ss -H -ltnp '( sport = :80 or sport = :443 )'") -CaptureOutput
    $unexpected = @($listeners -split "`r?`n" | Where-Object { $_ -and $_ -notmatch '(?i)k3s|traefik|svclb' })
    if ($unexpected.Count -gt 0) {
        throw 'A non-k3s host process appears to be listening on port 80 or 443.'
    }
    $remoteHttpsHosts = @('api.deepseek.com')
    if ($RegistryEndpoint) { $remoteHttpsHosts += (($RegistryEndpoint -replace '^https?://', '').Split('/')[0]) }
    if ($OssEndpoint) { $remoteHttpsHosts += (($OssEndpoint -replace '^https?://', '').Split('/')[0]) }
    foreach ($remoteHost in $remoteHttpsHosts | Select-Object -Unique) {
        Invoke-NativeCommand -FilePath 'ssh' -ArgumentList @(
            $SshTarget,
            "curl --silent --show-error --output /dev/null --connect-timeout 5 https://$remoteHost/"
        )
    }
    Invoke-NativeCommand -FilePath 'ssh' -ArgumentList @(
        $SshTarget,
        "timeout 5 bash -c ': >/dev/tcp/smtpdm.aliyun.com/465'"
    )
}

if (-not $SkipDomainGates) {
    if (-not $DomainStatusNormal) {
        throw 'Domain status must be confirmed Normal before creating an ACME Ingress. Pass -DomainStatusNormal only after checking Alibaba Cloud.'
    }
    foreach ($resolver in @('1.1.1.1', '8.8.8.8')) {
        $answers = @(Resolve-DnsName -Name $Domain -Type A -Server $resolver -ErrorAction Stop | Where-Object Type -eq 'A' | ForEach-Object IPAddress)
        if ($ExpectedPublicIP -notin $answers) {
            throw "Resolver $resolver does not return $ExpectedPublicIP for $Domain."
        }
    }
}

if (-not $SkipExternalNetworkChecks) {
    if ($RegistryEndpoint -and -not (Test-TcpPort -HostName ($RegistryEndpoint -replace '^https?://', '').Split('/')[0] -Port 443)) {
        throw "Cannot reach ACR endpoint '$RegistryEndpoint' on TCP 443."
    }
    if ($OssEndpoint -and -not (Test-TcpPort -HostName ($OssEndpoint -replace '^https?://', '').Split('/')[0] -Port 443)) {
        throw "Cannot reach OSS endpoint '$OssEndpoint' on TCP 443."
    }
    if (-not (Test-TcpPort -HostName 'smtpdm.aliyun.com' -Port 465)) {
        throw 'Cannot reach Alibaba Cloud DirectMail SMTP on TCP 465.'
    }
    if (-not (Test-TcpPort -HostName 'api.deepseek.com' -Port 443)) {
        throw 'Cannot reach DeepSeek API on TCP 443.'
    }
    foreach ($privatePort in @(5432, 6379, 9090)) {
        if (Test-TcpPort -HostName $ExpectedPublicIP -Port $privatePort) {
            throw "Public port $ExpectedPublicIP`:$privatePort is reachable; close it in the Alibaba Cloud security group."
        }
    }
}

Write-Host "Preflight passed for $Environment on node $($node.metadata.name) ($($version.serverVersion.gitVersion))."
Write-Warning 'Manual gates remain: verify the Alibaba Cloud DirectMail sender/domain and SMTP password, DeepSeek key/balance, ACR pull authorization, and OSS lifecycle policy in their provider consoles.'
