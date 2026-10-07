# Kumo setup for Windows: installs the programs Kumo uses, for this Windows
# user (no administrator rights), with Scoop (https://scoop.sh):
#
#   git      Git for Windows: its bash runs ani-cli, with the curl, sed and
#            grep that ani-cli uses
#   ani-cli  sub/dub streaming and episode downloads (Scoop's extras bucket)
#   ffmpeg   the in-app player (most files) and episode downloads
#   mpv      the external player (extras bucket)
#   yt-dlp   episode downloads; aria2 too (ani-cli downloads with it)
#   fzf      ani-cli checks for it (Kumo answers ani-cli's menus itself)
#   gh       the GitHub CLI: signed in to GitHub, Kumo can check for and
#            download its updates while its repository is private
#
# Programs that are already installed, with Scoop or otherwise, are left
# alone, so it's safe to run again. The Kumo installer offers to run it, and
# Kumo runs it from Settings > App > Programs.
#
# Keep this file ASCII: Windows PowerShell reads a script without a byte
# order mark in the computer's code page.

param(
    # The GitHub repository Kumo updates from.
    [string]$Repo = "simo1337s/animetest",
    # Close the window at the end without waiting for Enter.
    [switch]$NoPause
)

# Scoop's own errors are reported by checking what got installed, not by
# stopping at the first one.
$ErrorActionPreference = "Continue"
# Windows PowerShell's download progress bar makes downloads very slow.
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
try { $Host.UI.RawUI.WindowTitle = "Kumo setup" } catch {}

$script:failed = @()
$scoopDir = if ($env:SCOOP) { $env:SCOOP } else { Join-Path $env:USERPROFILE "scoop" }

function Step($text) {
    Write-Host ""
    Write-Host "==> $text" -ForegroundColor Cyan
}

function Have($command) {
    return [bool](Get-Command $command -ErrorAction SilentlyContinue)
}

function Installed($app) {
    return Test-Path (Join-Path $scoopDir "apps\$app\current")
}

# Install installs a Scoop app, unless it's installed already: with Scoop,
# or (when command is given) anywhere on the PATH.
function Install($app, $command, $why) {
    if ((Installed $app) -or ($command -and (Have $command))) {
        Write-Host "$app is installed" -ForegroundColor DarkGray
        return
    }
    Step "Installing $app ($why)"
    scoop install $app
    if (-not (Installed $app)) {
        $script:failed += $app
    }
}

Write-Host "Kumo setup: installs the programs Kumo uses with Scoop, for your Windows user." -ForegroundColor Cyan
Write-Host "It takes a few minutes. Programs you already have are skipped."

try {
    if (-not (Have "scoop")) {
        Step "Installing Scoop"
        # Scoop needs PowerShell scripts allowed for this user; Windows
        # blocks them by default and RemoteSigned is what Scoop asks for.
        $policy = Get-ExecutionPolicy -Scope CurrentUser
        if ($policy -eq "Undefined" -or $policy -eq "Restricted") {
            try { Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser -Force -ErrorAction Stop } catch { Write-Host $_ -ForegroundColor Yellow }
        }
        # Scoop's installer, run as a script of its own: when it gives up it
        # ends its own PowerShell, not this one.
        $installer = Join-Path $env:TEMP "kumo-install-scoop.ps1"
        Invoke-RestMethod -Uri "https://get.scoop.sh" -OutFile $installer
        $principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
        $admin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
        $installerArgs = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $installer)
        if ($admin) { $installerArgs += "-RunAsAdmin" }
        & powershell.exe @installerArgs
        Remove-Item $installer -ErrorAction SilentlyContinue
        $env:PATH = (Join-Path $scoopDir "shims") + ";" + $env:PATH
        if (-not (Have "scoop")) {
            throw "Scoop didn't install (see above)."
        }
    }

    # Scoop needs git for its buckets: it comes first.
    Install "git" "git" "its bash runs ani-cli"
    if (-not (Test-Path (Join-Path $scoopDir "buckets\extras"))) {
        Step "Adding Scoop's extras bucket (ani-cli and mpv are in it)"
        scoop bucket add extras
        if (-not (Test-Path (Join-Path $scoopDir "buckets\extras"))) {
            $script:failed += "Scoop's extras bucket"
        }
    }
    Install "ffmpeg" "ffmpeg" "the in-app player and downloads"
    Install "yt-dlp" "yt-dlp" "downloads"
    Install "aria2" "aria2c" "ani-cli's downloads"
    Install "fzf" "fzf" "ani-cli checks for it"
    Install "mpv" "mpv" "the external player"
    # A bash script: Kumo finds it in Scoop's folder.
    Install "ani-cli" $null "sub/dub streaming"
    Install "gh" "gh" "signs Kumo in to GitHub for its updates"

    # Kumo checks GitHub for its updates. While its repository is private
    # that takes a GitHub sign-in, which the GitHub CLI keeps.
    if (Have "gh") {
        $public = $true
        try {
            Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo" -Headers @{ "User-Agent" = "Kumo-setup" } | Out-Null
        } catch {
            $public = $false
        }
        if (-not $public) {
            gh auth status --hostname github.com *> $null
            if ($LASTEXITCODE -ne 0) {
                Step "Signing in to GitHub"
                Write-Host "Kumo checks GitHub for its updates, and its repository is private:"
                Write-Host "sign in with the GitHub account that can see $Repo."
                $answer = Read-Host "Sign in now? [Y/n]"
                if ($answer -notmatch "^\s*[nN]") {
                    gh auth login --hostname github.com --git-protocol https --web
                    if ($LASTEXITCODE -ne 0) {
                        $script:failed += "the GitHub sign-in"
                    }
                }
            }
        }
    }
} catch {
    Write-Host ""
    Write-Host "Kumo setup stopped: $_" -ForegroundColor Red
    $script:failed += "the rest"
}

Write-Host ""
if ($script:failed.Count -eq 0) {
    Write-Host "All done. Kumo finds the new programs by itself." -ForegroundColor Green
} else {
    Write-Host ("Not installed: " + ($script:failed -join ", ") + ".") -ForegroundColor Yellow
    Write-Host "Run the setup again from Kumo (Settings > App > Programs), or see"
    Write-Host "https://github.com/$Repo/blob/HEAD/packaging/windows/README.md"
}
if (-not $NoPause) {
    Read-Host "Press Enter to close this window" | Out-Null
}
exit $script:failed.Count
