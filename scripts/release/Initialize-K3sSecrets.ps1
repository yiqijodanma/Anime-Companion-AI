[CmdletBinding()]
param(
    [string]$KubeContext,
    [string]$Namespace = 'anime-companion'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'K3s.Common.ps1')

function Read-HiddenValue {
    param([Parameter(Mandatory)][string]$Prompt, [switch]$AllowEmpty)

    $secure = Read-Host $Prompt -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
        $value = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
        if (-not $AllowEmpty -and [string]::IsNullOrWhiteSpace($value)) {
            throw "$Prompt cannot be empty."
        }
        return $value
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
    }
}

function Apply-SecretObject {
    param([Parameter(Mandatory)][hashtable]$Secret)

    $encodedData = @{}
    foreach ($entry in $Secret.stringData.GetEnumerator()) {
        $encodedData[[string]$entry.Key] = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes([string]$entry.Value))
    }
    $Secret.Remove('stringData')
    $Secret.data = $encodedData
    $json = $Secret | ConvertTo-Json -Depth 8 -Compress
    $arguments = @()
    if ($KubeContext) { $arguments += @('--context', $KubeContext) }
    $arguments += @('apply', '--server-side', '--field-manager=anime-companion-secret-bootstrap', '-f', '-')
    $json | & kubectl @arguments
    if ($LASTEXITCODE -ne 0) { throw "Failed to apply Secret '$($Secret.metadata.name)'." }
    $json = $null
    $encodedData = $null
}

Assert-CommandAvailable 'kubectl'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Invoke-Kubectl -Context $KubeContext -ArgumentList @('apply', '-f', (Join-Path $repoRoot 'deploy\k3s\base\namespace.yaml'))

$postgresPassword = Read-HiddenValue 'PostgreSQL password'
if ($postgresPassword.Length -lt 24) { throw 'PostgreSQL password must contain at least 24 characters.' }
$authPepperBytes = [byte[]]::new(48)
[Security.Cryptography.RandomNumberGenerator]::Fill($authPepperBytes)
$authPepper = [Convert]::ToBase64String($authPepperBytes)
$adminEmail = (Read-Host 'Administrator email').Trim()
$adminPassword = Read-HiddenValue 'Administrator password'
if ($adminPassword.Length -lt 12 -or $adminPassword.Length -gt 128) { throw 'Administrator password must contain 12 to 128 characters.' }
if ($adminPassword -eq $postgresPassword) { throw 'Administrator and PostgreSQL passwords must be different.' }
$deepSeekKey = Read-HiddenValue 'DeepSeek API key'
$smtpUsername = (Read-Host 'Alibaba Cloud DirectMail sender address (also SMTP username)').Trim()
$smtpPassword = Read-HiddenValue 'Alibaba Cloud DirectMail SMTP password'
$encodedPostgresPassword = [Uri]::EscapeDataString($postgresPassword)
$pgDsn = "postgres://companion:$encodedPostgresPassword@postgres:5432/companion?sslmode=disable"

$ossAccessKeyId = Read-HiddenValue 'OSS RAM AccessKey ID'
$ossAccessKeySecret = Read-HiddenValue 'OSS RAM AccessKey Secret'
$ossSessionToken = Read-HiddenValue 'OSS STS session token (leave empty for long-lived RAM key)' -AllowEmpty
$ossBucket = (Read-Host 'Private OSS bucket name').Trim()
$ossEndpoint = ((Read-Host 'OSS endpoint hostname (no https://)').Trim() -replace '^https?://', '').TrimEnd('/')
$ossRegion = (Read-Host 'OSS region ID (for example ap-southeast-1)').Trim()
$ossPrefix = 'anime-companion/postgres'
if ($ossRegion -notmatch '^[a-z0-9-]+$') { throw 'OSS region ID has an invalid format.' }

$acrServer = ((Read-Host 'ACR registry hostname').Trim() -replace '^https?://', '').TrimEnd('/')
$acrUsername = (Read-Host 'ACR pull username').Trim()
$acrPassword = Read-HiddenValue 'ACR pull password'
$acrAuth = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes("$acrUsername`:$acrPassword"))
$dockerConfig = @{ auths = @{ $acrServer = @{ username = $acrUsername; password = $acrPassword; auth = $acrAuth } } } | ConvertTo-Json -Depth 6 -Compress

try {
    Apply-SecretObject @{
        apiVersion = 'v1'; kind = 'Secret';
        metadata = @{ name = 'anime-companion-secrets'; namespace = $Namespace };
        type = 'Opaque';
        stringData = @{
            POSTGRES_PASSWORD = $postgresPassword; PG_DSN = $pgDsn; AUTH_PEPPER = $authPepper;
            ADMIN_EMAIL = $adminEmail; ADMIN_PASSWORD = $adminPassword; DEEPSEEK_API_KEY = $deepSeekKey;
            SMTP_USERNAME = $smtpUsername; SMTP_PASSWORD = $smtpPassword
        }
    }
    $ossData = @{
        OSS_ACCESS_KEY_ID = $ossAccessKeyId; OSS_ACCESS_KEY_SECRET = $ossAccessKeySecret;
        OSS_BUCKET = $ossBucket; OSS_ENDPOINT = $ossEndpoint; OSS_REGION = $ossRegion; OSS_PREFIX = $ossPrefix
    }
    if ($ossSessionToken) { $ossData.OSS_SESSION_TOKEN = $ossSessionToken }
    Apply-SecretObject @{
        apiVersion = 'v1'; kind = 'Secret';
        metadata = @{ name = 'oss-backup-secrets'; namespace = $Namespace };
        type = 'Opaque'; stringData = $ossData
    }
    Apply-SecretObject @{
        apiVersion = 'v1'; kind = 'Secret';
        metadata = @{ name = 'acr-pull'; namespace = $Namespace };
        type = 'kubernetes.io/dockerconfigjson'; stringData = @{ '.dockerconfigjson' = $dockerConfig }
    }
}
finally {
    $postgresPassword = $null; $pgDsn = $null; $authPepper = $null; $adminPassword = $null
    $deepSeekKey = $null; $smtpPassword = $null; $ossAccessKeyId = $null
    $ossAccessKeySecret = $null; $ossSessionToken = $null; $acrPassword = $null
    $acrAuth = $null; $dockerConfig = $null; $ossData = $null
    [Array]::Clear($authPepperBytes, 0, $authPepperBytes.Length)
}

Write-Host 'Namespace-scoped runtime, OSS, and ACR pull Secrets were applied without printing their values.'
