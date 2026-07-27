[CmdletBinding()]
param(
    [string]$Domain = 'animecompanion.icu',
    [switch]$AuthenticatedSmoke,
    [Security.SecureString]$SessionCookie,
    [switch]$RealChat,
    [switch]$AllowUntrustedTls,
    [string]$ConversationId = 'sos-group'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ($PSVersionTable.PSVersion.Major -lt 7) {
    throw 'PowerShell 7 or newer is required.'
}
$httpOrigin = "http://$Domain"
$httpsOrigin = "https://$Domain"

$redirect = Invoke-WebRequest -Uri "$httpOrigin/" -MaximumRedirection 0 -SkipHttpErrorCheck -ErrorAction Ignore
if ($null -eq $redirect) {
    throw 'HTTP origin did not return a response.'
}
if ([int]$redirect.StatusCode -notin @(301, 302, 307, 308)) {
    throw "HTTP origin returned $([int]$redirect.StatusCode) instead of a redirect."
}
$location = [string]$redirect.Headers.Location
if (-not $location.StartsWith($httpsOrigin, [StringComparison]::OrdinalIgnoreCase)) {
    throw "HTTP redirect target '$location' is not the canonical HTTPS origin."
}

$root = Invoke-WebRequest -Uri "$httpsOrigin/" -SkipCertificateCheck:$AllowUntrustedTls
if ([int]$root.StatusCode -ne 200 -or $root.Content -notmatch '<div\s+id=["'']root["'']') {
    throw 'HTTPS root did not return the React application shell.'
}
foreach ($path in @('/livez', '/readyz', '/healthz')) {
    $response = Invoke-WebRequest -Uri "$httpsOrigin$path" -SkipHttpErrorCheck -SkipCertificateCheck:$AllowUntrustedTls
    if ([int]$response.StatusCode -ne 200) {
        throw "$path returned HTTP $([int]$response.StatusCode)."
    }
}
$unauthenticated = Invoke-WebRequest -Uri "$httpsOrigin/api/v1/auth/session" -SkipHttpErrorCheck -SkipCertificateCheck:$AllowUntrustedTls
if ([int]$unauthenticated.StatusCode -ne 401) {
    throw 'Unauthenticated session endpoint did not return HTTP 401.'
}

if ($AuthenticatedSmoke) {
    if ($null -eq $SessionCookie) {
        $SessionCookie = Read-Host 'Paste a temporary sos_session cookie (input is hidden)' -AsSecureString
    }
    $plainCookie = [Net.NetworkCredential]::new('', $SessionCookie).Password
    try {
        $webSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
        $webSession.Cookies.Add([Net.Cookie]::new('sos_session', $plainCookie, '/', $Domain))
        $sessionResponse = Invoke-RestMethod -Uri "$httpsOrigin/api/v1/auth/session" -WebSession $webSession -SkipCertificateCheck:$AllowUntrustedTls
        if ($null -eq $sessionResponse.user) { throw 'Authenticated session response has no user.' }
        $conversations = Invoke-RestMethod -Uri "$httpsOrigin/api/v1/conversations" -WebSession $webSession -SkipCertificateCheck:$AllowUntrustedTls
        if ($null -eq $conversations.conversations) { throw 'Conversation listing response is missing conversations.' }

        if ($RealChat) {
            $requestId = [Guid]::NewGuid().ToString()
            $body = @{ content = '请用一句话确认生产对话链路正常。'; client_request_id = $requestId } | ConvertTo-Json -Compress
            $chat = Invoke-RestMethod -Uri "$httpsOrigin/api/v1/conversations/$ConversationId/messages" -Method Post -WebSession $webSession -ContentType 'application/json' -Body $body -SkipCertificateCheck:$AllowUntrustedTls
            if ($null -eq $chat.batch -or @($chat.batch.character_messages).Count -lt 1) {
                throw 'Real chat smoke did not return a character reply.'
            }
        }
    }
    finally {
        $plainCookie = $null
    }
}

Write-Host 'Public smoke checks passed: redirect, TLS, React root, health routes, and auth boundary.'
if ($AllowUntrustedTls) { Write-Warning 'TLS chain trust was intentionally skipped for the Let''s Encrypt staging certificate; production smoke must not use this switch.' }
if ($AuthenticatedSmoke) { Write-Host 'Authenticated session and conversation listing checks passed.' }
if ($RealChat) { Write-Host 'One real DeepSeek conversation check passed and consumed quota.' }
