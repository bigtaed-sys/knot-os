# Build a flashable KnotOS image from Windows via WSL2.
#
# The image is built by modifying the official Raspberry Pi OS Lite arm64
# image (losetup + mount + chroot); that needs Linux + root + loop
# devices, none of which work directly on Windows. This script:
#   1. Verifies WSL2 is installed and a usable distro exists.
#   2. Syncs the repo into WSL's native filesystem (~/.knot-os-build/),
#      because the chroot/loop steps break on /mnt/* mounts (9P does not
#      support every operation they need - chmod g+s, mknod, etc.).
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
    Write-Host 'docker-desktop alone will not work - the image build needs a real distro (Ubuntu 22.04+ recommended).'
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

# Derive the image version from git on the Windows side. The build dir
# synced into WSL excludes .git/, so build.sh can't read the tag itself -
# we compute it here and pass it through as $VERSION. Strip the leading
# 'v' (v2026.07.9 -> 2026.07.9). Falls back to a dev marker outside a
# git checkout.
$imgVersion = ''
try {
    $desc = (& git -C $repoRoot describe --tags --always 2>$null | Out-String).Trim()
    if ($desc) { $imgVersion = ($desc -replace '^v', '') }
} catch { }
if (-not $imgVersion) { $imgVersion = '0.0.0-dev' }
Write-Host "Image version: $imgVersion"

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
    # Toolchains for the UI / Go build steps.
    'golang-go', 'nodejs', 'npm',
    # qemu-user-static for arm64 binary execution under chroot.
    'qemu-user-static', 'binfmt-support',
    # losetup, mount, fdisk live in util-linux/mount/fdisk packages
    # that are present by default on Ubuntu, but be explicit.
    'util-linux', 'mount', 'fdisk',
    # Image manipulation.
    'parted', 'e2fsprogs', 'dosfstools', 'xz-utils',
    # Misc.
    'rsync', 'curl', 'ca-certificates', 'openssl'
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

# Verify binfmt_misc is registered for arm64. Without this, every
# command we try to run inside the qemu chroot fails with "Exec
# format error" — the kernel sees the arm64 ELF magic but doesn't
# know to route execs through /usr/bin/qemu-aarch64-static.
# WSL2 loses these registrations on every `wsl --shutdown`, so even
# a previously-working machine can hit this after a reboot.
function Test-Binfmt {
    $check = & wsl.exe -d $Distro -e bash -lc 'test -e /proc/sys/fs/binfmt_misc/qemu-aarch64 && head -1 /proc/sys/fs/binfmt_misc/qemu-aarch64 | grep -q enabled && echo OK || echo MISSING'
    return ($check | Out-String).Trim() -eq 'OK'
}

if (-not (Test-Binfmt)) {
    Write-Host ''
    Write-Host 'binfmt_misc for qemu-aarch64 is not registered in WSL.' -ForegroundColor Yellow
    Write-Host '  This usually means WSL was restarted since qemu-user-static was set up,'
    Write-Host '  or WSL boots without systemd so the binfmt-support service never ran.'
    Write-Host '  Re-registering now…'

    # Strategy chain (each attempt only fires if the previous didn't fix it):
    #   1. systemctl restart systemd-binfmt            (systemd-on-WSL)
    #   2. update-binfmts --enable qemu-aarch64        (any sysv-friendly layout)
    #   3. binfmt_misc /register direct write          (works EVERYWHERE,
    #                                                   including non-systemd
    #                                                   WSL where 1+2 both
    #                                                   fail silently).
    # Format string is the canonical aarch64 ELF magic / mask. Single-quoted
    # heredoc on the bash side so PowerShell doesn't try to interpret \xNN
    # escapes as its own.
    $regCmds = @"
sudo systemctl restart systemd-binfmt 2>/dev/null && exit 0
sudo update-binfmts --enable qemu-aarch64 2>/dev/null && exit 0
test -x /usr/bin/qemu-aarch64-static || exit 7
sudo bash -c 'echo ":qemu-aarch64:M::\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\xb7\x00:\xff\xff\xff\xff\xff\xff\xff\xfc\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff:/usr/bin/qemu-aarch64-static:OCF" > /proc/sys/fs/binfmt_misc/register'
"@
    & wsl.exe -d $Distro -e bash -lc $regCmds | Out-Null

    if (-not (Test-Binfmt)) {
        Write-Host ''
        Write-Host 'Still no binfmt for qemu-aarch64 after auto-fix.' -ForegroundColor Red
        Write-Host 'Run inside WSL:' -ForegroundColor Yellow
        Write-Host '    sudo apt-get install --reinstall qemu-user-static binfmt-support'
        Write-Host ''
        Write-Host 'For a permanent fix, enable systemd in WSL:' -ForegroundColor Yellow
        Write-Host '    sudo nano /etc/wsl.conf      # add:  [boot]\n                                 systemd=true'
        Write-Host '    exit && wsl --shutdown        # then re-open WSL'
        exit 1
    }
    Write-Host '  binfmt registered.' -ForegroundColor Green
}

# ---- 4. Sync source into WSL native fs ------------------------------------

# Convert E:\routerOS to /mnt/e/routerOS for rsync source.
$drive = $repoRoot.Path.Substring(0, 1).ToLower()
$rest  = $repoRoot.Path.Substring(2).Replace('\', '/')
$src   = "/mnt/$drive$rest/"
$dst   = "/home/$linuxUser/.knot-os-build/"

if ($Clean) {
    Write-Host "Cleaning $dst (WSL)..."
    # build.sh runs as root and leaves root-owned files (work/, deploy/,
    # cache/) all over the build dir. The user-level wsl-run can't delete
    # them; use root explicitly.
    & wsl.exe -d $Distro -u root -e bash -lc "rm -rf '$dst'"
    if ($LASTEXITCODE -ne 0) { throw "clean failed" }
}

Write-Host "Syncing source: $src -> $dst (WSL)"
# Excludes are split into two categories:
#   - Editor/IDE noise we never want to sync (.git, node_modules, ...)
#   - Build outputs that end up root-owned in WSL (cache, staged
#     binaries/plugins, work/deploy/). Without excluding these,
#     a second sync as the unprivileged user fails with EACCES trying
#     to --delete root-owned files left by the previous build.
$rsyncCmd = "mkdir -p '$dst' && rsync -a --delete " +
    "--exclude='.git/' " +
    "--exclude='node_modules/' " +
    "--exclude='ui/build/' " +
    "--exclude='ui/.svelte-kit/' " +
    "--exclude='dist/' " +
    "--exclude='tmp/' " +
    "--exclude='core/internal/web/dist/' " +
    "--exclude='image/deploy/' " +
    "--exclude='image/work/' " +
    "--exclude='image/cache/' " +
    "--exclude='image/stage-knot/00-install-knotd/files/knotd' " +
    "--exclude='image/stage-knot/00-install-knotd/files/knotctl' " +
    "--exclude='image/stage-knot/01-install-plugins/files/' " +
    # build.sh stages these binaries as root (install -m 755), so a
    # later user-run rsync --delete can't remove them ("Permission
    # denied"). They're build outputs, never in source — exclude them
    # from the sync entirely. (02-recovery/files IS committed source,
    # so it is deliberately NOT excluded.)
    "--exclude='image/stage-knot/03-singbox/files/' " +
    "--exclude='image/stage-knot/04-xray/files/' " +
    "--exclude='image/stage-knot/05-zapret/files/' " +
    "'$src' '$dst'"
wsl-run-as-user $rsyncCmd

# ---- 5. Run the build ------------------------------------------------------

Write-Host ''
Write-Host '==> Building the image in WSL (modifying Pi OS Lite). First run downloads ~500 MB and takes several minutes.'
Write-Host ''

# Run as root so build.sh sees EUID 0; SUDO_USER is set so it can drop
# back for `go build` / `npm` calls per the script. VERSION is derived
# from git above and passed through since the build dir has no .git.
& wsl.exe -d $Distro -u root -e bash -lc "cd '$dst' && SUDO_USER='$linuxUser' VERSION='$imgVersion' bash image/build.sh"
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
# Wipe Windows-side deploy/*.img.xz first so the directory only ever
# contains the most recent build. The WSL side already pruned old
# images before producing the new one, so we are not nuking anything
# we still have on disk elsewhere.
Get-ChildItem $winDeploy -Filter '*.img.xz' -ErrorAction SilentlyContinue | Remove-Item -Force
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
