# Build a fresh knotd, then push it to the running KnotOS device.
# Two transports:
#   - HTTP (default, fast)         : POST to /api/system/update over the LAN.
#   - Serial (-Serial COMx, slow)  : base64 the binary into a knot-update
#                                    receiver running on the device's COM port.
#
# Usage examples (from PowerShell at the repo root):
#
#   .\scripts\update-knot.ps1 -Address knot.local -Password mypass
#   .\scripts\update-knot.ps1 -Address 192.168.42.1 -Password mypass -SkipBuild
#   .\scripts\update-knot.ps1 -Serial COM4
#
# The serial path requires you to be logged into the Pi over the
# USB-OTG / UART console first, with the `knot-update-receive` helper
# running. The script tells you what to type if not.
#
# Note: -Address (not -Host) - PowerShell's $Host is a built-in
# read-only variable, so we cannot use that as a parameter name.

[CmdletBinding(DefaultParameterSetName = 'Http')]
param(
    [Parameter(ParameterSetName = 'Http', Mandatory = $true)]
    [string]$Address,

    [Parameter(ParameterSetName = 'Http')]
    [string]$Password,

    [Parameter(ParameterSetName = 'Serial', Mandatory = $true)]
    [string]$Serial,

    [Parameter(ParameterSetName = 'Serial')]
    [int]$BaudRate = 115200,

    [switch]$SkipBuild,
    [string]$Version = '0.1.0-dev'
)

$ErrorActionPreference = 'Stop'
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
$binPath = Join-Path $repoRoot 'dist\arm64\knotd'

# ---- 1. Build (unless -SkipBuild) ---------------------------------------

if (-not $SkipBuild) {
    Write-Host '==> Cross-compiling knotd for linux/arm64'
    Push-Location $repoRoot
    try {
        $env:GOOS = 'linux'
        $env:GOARCH = 'arm64'
        $ldflags = "-s -w -X main.Version=$Version"
        & go build -trimpath -ldflags $ldflags -o $binPath './core/cmd/knotd'
        if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
    } finally {
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
        Pop-Location
    }
}

if (-not (Test-Path $binPath)) {
    throw "Binary not found at $binPath - run without -SkipBuild first."
}
$size = (Get-Item $binPath).Length
Write-Host ("    knotd: {0:N0} bytes" -f $size)

# ---- 2a. HTTP transport -------------------------------------------------

if ($PSCmdlet.ParameterSetName -eq 'Http') {
    $base = "http://$Host"
    if ($Host -notmatch '^https?://') { $base = "http://$Host" }
    else { $base = $Host.TrimEnd('/') }

    if (-not $Password) {
        $Password = Read-Host -AsSecureString 'Admin password' |
            ForEach-Object { [Runtime.InteropServices.Marshal]::PtrToStringAuto(
                [Runtime.InteropServices.Marshal]::SecureStringToBSTR($_)) }
    }

    Write-Host "==> Logging in to $base"
    $session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
    $loginBody = @{ password = $Password } | ConvertTo-Json
    $login = Invoke-WebRequest -Uri "$base/api/auth/login" `
        -Method POST -Body $loginBody -ContentType 'application/json' `
        -WebSession $session -ErrorAction Stop
    if ($login.StatusCode -ne 200) {
        throw "login failed: $($login.StatusCode)"
    }

    Write-Host '==> Uploading new knotd binary'
    $bytes = [System.IO.File]::ReadAllBytes($binPath)
    try {
        $resp = Invoke-WebRequest -Uri "$base/api/system/update" `
            -Method POST -Body $bytes `
            -ContentType 'application/octet-stream' `
            -WebSession $session -TimeoutSec 60
        Write-Host ('==> Done. Server accepted ' + ($bytes.Length) + ' bytes; knotd will restart.')
    } catch {
        $err = $_.Exception.Response
        if ($err) {
            $reader = New-Object System.IO.StreamReader($err.GetResponseStream())
            $body = $reader.ReadToEnd()
            Write-Host "Server returned $($err.StatusCode):`n$body" -ForegroundColor Red
        }
        throw
    }
    return
}

# ---- 2b. Serial transport -----------------------------------------------

Write-Host "==> Sending knotd over serial ($Serial @ $BaudRate)"
Write-Host '    This is slow (~10 KB/s) - expect ~12 minutes for a 7 MB binary.'
Write-Host '    On the device, log in via the same COM port and run:'
Write-Host '        sudo knot-update-receive' -ForegroundColor Yellow
Write-Host '    Then disconnect the terminal and re-run this script.'
Write-Host ''

$port = New-Object System.IO.Ports.SerialPort $Serial, $BaudRate, None, 8, One
$port.NewLine = "`n"
$port.ReadTimeout = 30000
$port.WriteTimeout = 30000
$port.Open()
try {
    # Magic header so the receiver knows we're pushing a binary now.
    $port.WriteLine('KNOTUPDATE-BEGIN')
    Start-Sleep -Milliseconds 500

    # Stream as base64 in 4 KB chunks. The receiver decodes line-by-line.
    $bytes = [System.IO.File]::ReadAllBytes($binPath)
    $chunkSize = 3072  # 3072 raw bytes -> 4096 base64 chars per line
    $total = $bytes.Length
    $sent = 0
    $lines = 0

    while ($sent -lt $total) {
        $take = [Math]::Min($chunkSize, $total - $sent)
        $slice = New-Object byte[] $take
        [Array]::Copy($bytes, $sent, $slice, 0, $take)
        $b64 = [Convert]::ToBase64String($slice)
        $port.WriteLine($b64)
        $sent += $take
        $lines += 1
        if ($lines % 50 -eq 0) {
            $pct = [Math]::Round(($sent * 100.0) / $total, 1)
            Write-Host ("    {0,6:N1}%  ({1:N0} / {2:N0} bytes)" -f $pct, $sent, $total)
            Start-Sleep -Milliseconds 50
        }
    }

    $port.WriteLine('KNOTUPDATE-END')
    Write-Host '==> Sent. Watching for completion (up to 60s)…'

    $deadline = (Get-Date).AddSeconds(60)
    while ((Get-Date) -lt $deadline) {
        try {
            $line = $port.ReadLine()
            if ($line -match 'KNOTUPDATE-OK') {
                Write-Host '==> Receiver confirmed install. knotd is restarting.' -ForegroundColor Green
                return
            }
            if ($line -match 'KNOTUPDATE-FAIL: (.+)') {
                throw "Receiver reported failure: $($Matches[1])"
            }
            if ($line) { Write-Host "    $line" }
        } catch [System.TimeoutException] {
            # keep polling
        }
    }
    Write-Host 'Timed out waiting for confirmation. Check the device console.' -ForegroundColor Yellow
} finally {
    $port.Close()
}
