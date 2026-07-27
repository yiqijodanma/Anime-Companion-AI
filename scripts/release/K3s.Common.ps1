Set-StrictMode -Version Latest

function Assert-CommandAvailable {
    param([Parameter(Mandatory)][string]$Name)

    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required command '$Name' is not available on PATH."
    }
}

function Invoke-NativeCommand {
    param(
        [Parameter(Mandatory)][string]$FilePath,
        [string[]]$ArgumentList = @(),
        [switch]$CaptureOutput
    )

    if ($CaptureOutput) {
        $output = @(& $FilePath @ArgumentList 2>&1)
        $exitCode = $LASTEXITCODE
        if ($exitCode -ne 0) {
            throw "Command '$FilePath' failed with exit code $exitCode.`n$($output -join [Environment]::NewLine)"
        }
        return ($output -join [Environment]::NewLine)
    }

    & $FilePath @ArgumentList
    if ($LASTEXITCODE -ne 0) {
        throw "Command '$FilePath' failed with exit code $LASTEXITCODE."
    }
}

function Invoke-Kubectl {
    param(
        [string]$Context,
        [Parameter(Mandatory)][string[]]$ArgumentList,
        [switch]$CaptureOutput
    )

    $allArguments = @()
    if ($Context) {
        $allArguments += @('--context', $Context)
    }
    $allArguments += $ArgumentList
    return Invoke-NativeCommand -FilePath 'kubectl' -ArgumentList $allArguments -CaptureOutput:$CaptureOutput
}

function Get-RepositoryCommit {
    param([Parameter(Mandatory)][string]$Path)

    return (Invoke-NativeCommand -FilePath 'git' -ArgumentList @('-C', $Path, 'rev-parse', 'HEAD') -CaptureOutput).Trim()
}

function Assert-CleanRepositoryPath {
    param([Parameter(Mandatory)][string]$Path)

    $status = Invoke-NativeCommand -FilePath 'git' -ArgumentList @('-C', $Path, 'status', '--porcelain', '--', '.') -CaptureOutput
    if ($status.Trim()) {
        throw "Repository path '$Path' has uncommitted changes. Commit or intentionally stash them before a traceable release."
    }
}

function Assert-ImmutableImageReference {
    param([Parameter(Mandatory)][string]$Image)

    if ($Image -notmatch '^[A-Za-z0-9._:/@-]+$' -or
        $Image -match '(?i)(:latest|release-placeholder)$' -or
        $Image -notmatch '(?:@sha256:[0-9a-f]{64}|:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})$') {
        throw "Image reference '$Image' is not an immutable release reference."
    }
}

function New-TemporaryDirectory {
    $path = Join-Path ([IO.Path]::GetTempPath()) ("anime-companion-" + [Guid]::NewGuid().ToString('N'))
    [IO.Directory]::CreateDirectory($path) | Out-Null
    return $path
}

function Render-ManifestFile {
    param(
        [Parameter(Mandatory)][string]$Source,
        [Parameter(Mandatory)][string]$Destination,
        [Parameter(Mandatory)][hashtable]$Replacements
    )

    $content = [IO.File]::ReadAllText((Resolve-Path $Source))
    foreach ($entry in $Replacements.GetEnumerator()) {
        $content = $content.Replace([string]$entry.Key, [string]$entry.Value)
    }
    if ($content -match 'release-(?:id-)?placeholder|(?:backend|frontend)-commit-placeholder|placeholder@animecompanion\.invalid') {
        throw "Manifest '$Source' still contains a release placeholder after rendering."
    }
    [IO.File]::WriteAllText($Destination, $content, [Text.UTF8Encoding]::new($false))
}

function Assert-SecretKeys {
    param(
        [string]$Context,
        [Parameter(Mandatory)][string]$Namespace,
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)][string[]]$Keys,
        [hashtable]$ExpectedValues = @{}
    )

    $secret = Invoke-Kubectl -Context $Context -ArgumentList @('-n', $Namespace, 'get', 'secret', $Name, '-o', 'json') -CaptureOutput | ConvertFrom-Json
    foreach ($key in $Keys) {
        $property = $secret.data.PSObject.Properties[$key]
        if ($null -eq $property -or [string]::IsNullOrWhiteSpace([string]$property.Value)) {
            throw "Secret '$Namespace/$Name' is missing required non-empty key '$key'."
        }
        try {
            $decodedBytes = [Convert]::FromBase64String([string]$property.Value)
            if ($decodedBytes.Length -eq 0) {
                throw "Secret '$Namespace/$Name' has an empty key '$key'."
            }
            if ($ExpectedValues.ContainsKey($key)) {
                $decodedValue = [Text.Encoding]::UTF8.GetString($decodedBytes)
                if ($decodedValue -cne [string]$ExpectedValues[$key]) {
                    throw "Secret '$Namespace/$Name' has an unexpected value for key '$key'."
                }
                $decodedValue = $null
            }
            $decodedBytes = $null
        }
        catch [FormatException] {
            throw "Secret '$Namespace/$Name' key '$key' is not valid Kubernetes Secret data."
        }
    }
}
