[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidatePattern('^oss://')][string]$ObjectUrl,
    [string]$KubeContext,
    [string]$OssutilCommand = 'ossutil',
    [switch]$KeepAfterVerification,
    [ValidateRange(1, 100)][int]$MinimumFreeSpaceGiB = 2
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'K3s.Common.ps1')

$TemporaryDatabase = "companion_restore_" + [Guid]::NewGuid().ToString('N')
if ($TemporaryDatabase -notmatch '^[a-z][a-z0-9_]{2,62}$') {
    throw 'TemporaryDatabase must be a safe lowercase PostgreSQL identifier.'
}
Assert-CommandAvailable 'kubectl'
Assert-CommandAvailable $OssutilCommand

function Remove-TemporaryRestoreDatabase {
    param(
        [Parameter(Mandatory)][string]$PodName,
        [Parameter(Mandatory)][string]$DatabaseName
    )

    $dropCommand = 'export PGPASSWORD="$POSTGRES_PASSWORD"; dropdb --host=127.0.0.1 --username="$POSTGRES_USER" --if-exists --force ' + $DatabaseName
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'exec', $PodName, '--', 'sh', '-ec', $dropCommand)
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$latestMigration = Get-ChildItem (Join-Path $repoRoot 'db\migrations\*.up.sql') |
    ForEach-Object { if ($_.BaseName -match '^(\d+)_') { [int]$Matches[1] } } |
    Measure-Object -Maximum |
    Select-Object -ExpandProperty Maximum
$temporaryDirectory = New-TemporaryDirectory
$localDump = Join-Path $temporaryDirectory 'restore.dump'
$remoteDump = '/tmp/anime-companion-restore.dump'
$pod = $null
$databaseCreated = $false
$databaseCreationAttempted = $false
$verificationPassed = $false

try {
    Invoke-NativeCommand -FilePath $OssutilCommand -ArgumentList @('cp', $ObjectUrl, $localDump, '--force')
    if (-not [IO.File]::Exists($localDump) -or (Get-Item $localDump).Length -eq 0) {
        throw 'Downloaded OSS backup is empty.'
    }

    $pod = (Invoke-Kubectl -Context $KubeContext -ArgumentList @(
        '-n', 'anime-companion', 'get', 'pod', '-l', 'app.kubernetes.io/name=postgres',
        '-o', 'jsonpath={.items[0].metadata.name}'
    ) -CaptureOutput).Trim()
    if (-not $pod) { throw 'PostgreSQL Pod was not found.' }

    $databaseSizeCommand = 'export PGPASSWORD="$POSTGRES_PASSWORD"; psql --host=127.0.0.1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -Atc "SELECT pg_database_size(current_database())"'
    $sourceDatabaseBytesText = (Invoke-Kubectl -Context $KubeContext -ArgumentList @(
        '-n', 'anime-companion', 'exec', $pod, '--', 'sh', '-ec', $databaseSizeCommand
    ) -CaptureOutput).Trim()
    if ($sourceDatabaseBytesText -notmatch '^\d+$') {
        throw "PostgreSQL returned an invalid database size: '$sourceDatabaseBytesText'."
    }

    $freeSpaceCommand = "df -Pk /var/lib/postgresql/data | awk 'NR == 2 { print `$4 }'"
    $freeKilobytesText = (Invoke-Kubectl -Context $KubeContext -ArgumentList @(
        '-n', 'anime-companion', 'exec', $pod, '--', 'sh', '-ec', $freeSpaceCommand
    ) -CaptureOutput).Trim()
    if ($freeKilobytesText -notmatch '^\d+$') {
        throw "PostgreSQL volume returned an invalid free-space value: '$freeKilobytesText'."
    }

    $dumpBytes = [int64](Get-Item $localDump).Length
    $sourceDatabaseBytes = [int64]$sourceDatabaseBytesText
    $freeBytes = [int64]([decimal]$freeKilobytesText * 1KB)
    $minimumFreeBytes = [int64]$MinimumFreeSpaceGiB * 1GB
    $estimatedRestoreBytes = [int64][Math]::Ceiling(([double]$sourceDatabaseBytes * 2.0) + [double]$dumpBytes)
    $requiredFreeBytes = [Math]::Max($minimumFreeBytes, $estimatedRestoreBytes)
    $freeGiB = [Math]::Round($freeBytes / 1GB, 2)
    $requiredGiB = [Math]::Round($requiredFreeBytes / 1GB, 2)
    if ($freeBytes -lt $requiredFreeBytes) {
        throw "Restore drill requires at least $requiredGiB GiB free on the PostgreSQL volume, but only $freeGiB GiB is available."
    }
    Write-Host "Restore capacity check passed: $freeGiB GiB free; at least $requiredGiB GiB required."

    $databaseExistsCommand = ('export PGPASSWORD="$POSTGRES_PASSWORD"; psql --host=127.0.0.1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -Atc "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = ''{0}'')"' -f $TemporaryDatabase)
    $databaseExistsState = (Invoke-Kubectl -Context $KubeContext -ArgumentList @(
        '-n', 'anime-companion', 'exec', $pod, '--', 'sh', '-ec', $databaseExistsCommand
    ) -CaptureOutput).Trim()
    if ($databaseExistsState -eq 't') {
        throw "Temporary restore database '$TemporaryDatabase' already exists; choose a unique name."
    }
    if ($databaseExistsState -ne 'f') {
        throw "PostgreSQL returned an invalid database-existence result: '$databaseExistsState'."
    }

    $createCommand = 'export PGPASSWORD="$POSTGRES_PASSWORD"; createdb --host=127.0.0.1 --username="$POSTGRES_USER" ' + $TemporaryDatabase
    $databaseCreationAttempted = $true
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'exec', $pod, '--', 'sh', '-ec', $createCommand)
    $databaseCreated = $true
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'cp', $localDump, "$pod`:$remoteDump")

    $restoreCommand = 'export PGPASSWORD="$POSTGRES_PASSWORD"; pg_restore --host=127.0.0.1 --username="$POSTGRES_USER" --exit-on-error --no-owner --no-privileges --dbname=' + $TemporaryDatabase + ' ' + $remoteDump
    Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'exec', $pod, '--', 'sh', '-ec', $restoreCommand)

    $migrationQuery = 'export PGPASSWORD="$POSTGRES_PASSWORD"; psql --host=127.0.0.1 --username="$POSTGRES_USER" --dbname=' + $TemporaryDatabase + ' -Atc "SELECT version::text || '':'' || dirty::text FROM schema_migrations ORDER BY version DESC LIMIT 1"'
    $migrationState = (Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'exec', $pod, '--', 'sh', '-ec', $migrationQuery) -CaptureOutput).Trim()
    if ($migrationState -ne "$latestMigration`:false") {
        throw "Restored migration state '$migrationState' does not match expected '$latestMigration`:false'."
    }
    $accountQuery = 'export PGPASSWORD="$POSTGRES_PASSWORD"; psql --host=127.0.0.1 --username="$POSTGRES_USER" --dbname=' + $TemporaryDatabase + ' -Atc "SELECT COUNT(*)::text || '':'' || COUNT(*) FILTER (WHERE is_admin)::text FROM users"'
    $accountState = (Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'exec', $pod, '--', 'sh', '-ec', $accountQuery) -CaptureOutput).Trim()
    if ($accountState -notmatch '^(\d+):(\d+)$' -or [int64]$Matches[1] -lt 1 -or [int64]$Matches[2] -lt 1) {
        throw 'Representative account verification requires at least one user and one administrator.'
    }
    Write-Host "Restore drill passed: migration $migrationState; representative user/admin counts: $accountState."
    $verificationPassed = $true

    if ($KeepAfterVerification) {
        Write-Host "Temporary restore database retained for inspection: $TemporaryDatabase"
    }
    else {
        Remove-TemporaryRestoreDatabase -PodName $pod -DatabaseName $TemporaryDatabase
        $databaseCreated = $false
        $databaseCreationAttempted = $false
        Write-Host "Dropped temporary restore database $TemporaryDatabase."
    }
}
finally {
    if ($pod -and $databaseCreationAttempted -and (-not $verificationPassed -or -not $KeepAfterVerification)) {
        try {
            Remove-TemporaryRestoreDatabase -PodName $pod -DatabaseName $TemporaryDatabase
            $databaseCreated = $false
            $databaseCreationAttempted = $false
            Write-Host "Dropped temporary restore database $TemporaryDatabase during cleanup."
        }
        catch {
            Write-Warning "Failed to drop temporary restore database '$TemporaryDatabase' during cleanup: $($_.Exception.Message)"
        }
    }
    if ($pod) {
        try { Invoke-Kubectl -Context $KubeContext -ArgumentList @('-n', 'anime-companion', 'exec', $pod, '--', 'rm', '-f', $remoteDump) } catch { }
    }
    if ([IO.Directory]::Exists($temporaryDirectory)) {
        [IO.Directory]::Delete($temporaryDirectory, $true)
    }
}
