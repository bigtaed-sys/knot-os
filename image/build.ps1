# Build a flashable KnotOS image from Windows via WSL2.
#
# pi-gen needs Linux + root + chroot + loop devices. None of that
# works directly on Windows. This script:
#   1. Verifies WSL2 is installed and a usable distro exists.
#   2. Syncs the repo into WSL's native filesystem (~/.knot-os-build/),
#      because pi-gen breaks on /mnt/* mounts (9P does not support
#      every operation pi-gen needs - chmod g+s, mknod, etc.).
#   3. Runs `sudo bash image/build.sh` inside WSL.
#   4. Copies the produced .img.xz back to image/deploy/ on Windows.
#
# Usage:
#   .\image\build.ps1                   # default distro (auto-pick)
#   .\image\build.ps1 -Distro Ubuntu    # explicit distro
#   .\image\build.ps1 -Clean            # wipe WSL build dir first

[CmdletBinding()]
param(
    [string]$Distro = '',
    [switch]$Clean
)

$ErrorActionPreference = 'Stop'
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
Write-Host "Repo root: $repoRoot"

# ---- 1. WSL pre-flight -----------------------------------------------------

try {
    wsl.exe --status | Out-Null
} catch {
    Write-Host ''
    Write-Host 'WSL2 is not installed.' -ForegroundColor Red
    Write-Host 'Install it once with (Admin PowerShell):'
    Write-Host '    wsl --install -d Ubuntu' -ForegroundColor Yellow
    Write-Host 'Reboot, finish Ubuntu setup, then re-run this script.'
    exit 1
}

# ---- 2. Pick a distro ------------------------------------------------------

# wsl.exe --list outputs UTF-16 LE. On a non-Unicode Windows console
# (cp1251 etc.) PowerShell decodes those bytes as ANSI, which leaves
# embedded NUL bytes inside what looks like an ASCII distro name -
# "Ubuntu" then fails to match ^Ubuntu and gets misclassified.
#
# Fix: set the console output encoding to Unicode for the duration of
# the wsl call, then scrub any remaining stray control / format chars
# (NUL, BOM, RTL marks) from each line.
function Invoke-WslListQuiet {
    $prevEnc = [Console]::OutputEncoding
    [Console]::OutputEncoding = [System.Text.Encoding]::Unicode
    try {
        return (& wsl.exe --list --quiet)
    } finally {
        [Console]::OutputEncoding = $prevEnc
    }
}

$rawList = Invoke-WslListQuiet
$distros = $rawList |
    ForEach-Object { ($_ -replace '\p{C}', '').Trim() } |
    Where-Object { $_ -ne '' -and $_ -notmatch '^docker-desktop' }

if ($distros.Count -eq 0) {
    Write-Host ''
    Write-Host 'No usable WSL distro found.' -ForegroundColor Red
    Write-Host 'docker-desktop alone will not work - pi-gen needs a real distro (Ubuntu 22.04+ recommended).'
    Write-Host 'Install one with:'
    Write-Host '    wsl --install -d Ubuntu' -ForegroundColor Yellow
    exit 1
}

if (-not $Distro) {
    # Default: Ubuntu. Prefer the bare "Ubuntu" distro (it tracks the
    # latest LTS); fall back to the newest "Ubuntu-XX.YY" available.
    $ubuntus = @($distros | Where-Object { $_ -match '^Ubuntu' })
    if ($ubuntus -contains 'Ubuntu') {
        $Distro = 'Ubuntu'
    } elseif ($ubuntus.Count -gt 0) {
        $Distro = ($ubuntus | Sort-Object -Descending | Select-Object -First 1)
    } else {
        Write-Host ''
        Write-Host 'No Ubuntu distro is installed in WSL.' -ForegroundColor Red
        Write-Host 'KnotOS image builds require Ubuntu (22.04 or newer).'
        Write-Host ''
        Write-Host 'Install it with:' -ForegroundColor Yellow
        Write-Host '    wsl --install -d Ubuntu'
        Write-Host ''
        Write-Host 'After install, finish first-run setup (create a Linux user) and re-run this script.'
        Write-Host ''
        if ($distros.Count -gt 0) {
            Write-Host 'Other (non-Ubuntu) distros detected on this machine:'
            $distros | ForEach-Object { Write-Host "  - $_" }
            Write-Host 'Use them explicitly with -Distro <name> at your own risk; only Ubuntu is tested.'
        }
        exit 1
    }
}
elseif ($distros -notcontains $Distro) {
    Write-Host "Distro '$Distro' is not installed. Available:" -ForegroundColor Red
    $distros | ForEach-Object { Write-Host "  - $_" }
    exit 1
}

Write-Host "Using WSL distro: $Distro"

function wsl-run([string]$cmd) {
    # -e bash -lc "..."   keeps argument parsing predictable across distros.
    & wsl.exe -d $Distro -e bash -lc $cmd
    if ($LASTEXITCODE -ne 0) { throw "wsl command failed (exit $LASTEXITCODE): $cmd" }
}

function wsl-run-as-user([string]$cmd) {
    # Same as wsl-run but ensures we run as the default Linux user, not root.
    & wsl.exe -d $Distro --user (wsl-default-user) -e bash -lc $cmd
    if ($LASTEXITCODE -ne 0) { throw "wsl command failed (exit $LASTEXITCODE): $cmd" }
}

function wsl-default-user() {
    return (& wsl.exe -d $Distro -e bash -lc 'echo -n $USER').Trim()
}

# ---- 3. Pre-flight inside WSL ---------------------------------------------

Write-Host 'Checking WSL host...'
$linuxUser = wsl-default-user
Write-Host "  user: $linuxUser"

# Verify required tools exist; install on demand. Package names contain
# no whitespace, so we can leave them unquoted in the bash loop and
# avoid quote-escape gymnastics.
#
# This list mirrors pi-gen's own depends file plus the toolchains we
# need (Go, Node) for the cross-compile / UI build steps. Keep it in
# sync with upstream pi-gen/depends if a build fails citing missing
# packages.
$aptPackages = @(
    'quilt', 'parted',
    'qemu-user-static', 'qemu-utils',
    # NOTE: do not list qemu-user-binfmt here. On Ubuntu 24.04 it
    # was merged into qemu-user-static and the two now conflict —
    # asking apt for both forces a removal of one. pi-gen's stock
    # depends file still names qemu-user-binfmt; build.sh patches
    # that out after cloning pi-gen.
    'debootstrap', 'zerofree', 'zip', 'dosfstools',
    'libcap2-bin', 'libarchive-tools',
    'grep', 'rsync', 'xz-utils', 'file', 'git', 'curl', 'bc',
    'binfmt-support', 'kpartx', 'pigz', 'arch-test',
    'pv', 'xxd', 'coreutils', 'gnupg', 'ca-certificates',
    'golang-go', 'nodejs', 'npm'
) -join ' '
$checkCmd = 'for p in ' + $aptPackages + '; do dpkg -s $p >/dev/null 2>&1 || echo $p; done'
$missing = (& wsl.exe -d $Distro -e bash -lc $checkCmd | Out-String).Trim()
if ($missing) {
    Write-Host ''
    Write-Host 'Some required apt packages are missing in WSL:' -ForegroundColor Yellow
    $missing -split '\s+' | ForEach-Object { Write-Host "  - $_" }
    Write-Host 'Installing them now (you may be asked for your sudo password)...'
    $installList = ($missing -split '\s+') -join ' '
    & wsl.exe -d $Distro -e bash -lc "sudo apt-get update && sudo apt-get install -y $installList"
    if ($LASTEXITCODE -ne 0) { throw 'apt install failed' }
}

# ---- 4. Sync source into WSL native fs ------------------------------------

# Convert E:\routerOS to /mnt/e/routerOS for rsync source.
$drive = $repoRoot.Path.Substring(0, 1).ToLower()
$rest  = $repoRoot.Path.Substring(2).Replace('\', '/')
$src   = "/mnt/$drive$rest/"
$dst   = "/home/$linuxUser/.knot-os-build/"

if ($Clean) {
    Write-Host "Cleaning $dst (WSL)..."
    # pi-gen runs as root and leaves root-owned files (work/, deploy/)
    # all over the build dir. The user-level wsl-run can't delete
    # them; use root explicitly.
    & wsl.exe -d $Distro -u root -e bash -lc "rm -rf '$dst'"
    if ($LASTEXITCODE -ne 0) { throw "clean failed" }
}

Write-Host "Syncing source: $src -> $dst (WSL)"
$rsyncCmd = "mkdir -p '$dst' && rsync -a --delete " +
    "--exclude='.git/' " +
    "--exclude='node_modules/' " +
    "--exclude='ui/build/' " +
    "--exclude='ui/.svelte-kit/' " +
    "--exclude='dist/' " +
    "--exclude='tmp/' " +
    "--exclude='image/pi-gen/' " +
    "--exclude='image/deploy/' " +
    "--exclude='image/work/' " +
    "--exclude='core/internal/web/dist/' " +
    "'$src' '$dst'"
wsl-run-as-user $rsyncCmd

# ---- 5. Run the build ------------------------------------------------------

Write-Host ''
Write-Host '==> Starting pi-gen build in WSL. This takes 30-60 minutes the first time.'
Write-Host ''

# Run as root so build.sh sees EUID 0; SUDO_USER is set so it can drop
# back for `go build` / `npm` calls per the script.
& wsl.exe -d $Distro -u root -e bash -lc "cd '$dst' && SUDO_USER='$linuxUser' bash image/build.sh"
if ($LASTEXITCODE -ne 0) {
    Write-Host ''
    Write-Host 'Build failed.' -ForegroundColor Red
    Write-Host "WSL build dir is at $dst - re-run with -Clean to start over."
    exit $LASTEXITCODE
}

# ---- 6. Copy the image back to Windows ------------------------------------

$winDeploy = Join-Path $repoRoot 'image\deploy'
New-Item -ItemType Directory -Path $winDeploy -Force | Out-Null

Write-Host 'Copying built image back to Windows...'
$copyCmd = "cp -v '$dst'image/deploy/*.img.xz '/mnt/$drive$rest/image/deploy/' 2>&1"
wsl-run-as-user $copyCmd

Write-Host ''
Write-Host '==> Done.' -ForegroundColor Green
Get-ChildItem $winDeploy -Filter '*.img.xz' |
    Sort-Object LastWriteTime -Descending |
    Select-Object -First 3 |
    ForEach-Object {
        $sizeMB = [math]::Round($_.Length / 1MB, 1)
        Write-Host "  $($_.FullName)   $sizeMB MB"
    }

Write-Host ''
Write-Host 'Flash with Raspberry Pi Imager: Choose Device -> Pi Zero 2 W,'
Write-Host '                                Choose OS -> Use custom -> select the .img.xz.'
