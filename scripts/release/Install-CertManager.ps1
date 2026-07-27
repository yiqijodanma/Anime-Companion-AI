[CmdletBinding()]
param(
    [ValidatePattern('^v1\.21\.\d+$')]
    [string]$Version = 'v1.21.0',
    [string]$KubeContext
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'K3s.Common.ps1')

Assert-CommandAvailable 'kubectl'
$manifest = "https://github.com/cert-manager/cert-manager/releases/download/$Version/cert-manager.yaml"
Invoke-Kubectl -Context $KubeContext -ArgumentList @('apply', '-f', $manifest)
foreach ($deployment in @('cert-manager', 'cert-manager-webhook', 'cert-manager-cainjector')) {
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'cert-manager', 'rollout', 'status', "deployment/$deployment", '--timeout=10m')
}
Invoke-Kubectl -Context $KubeContext -ArgumentList @('get', 'customresourcedefinition', 'clusterissuers.cert-manager.io', '-o', 'name') | Out-Null
Write-Host "cert-manager $Version is installed and ready."
