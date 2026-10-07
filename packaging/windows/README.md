# Kumo for Windows

Kumo runs on Windows 10 and 11 (64-bit). The Windows build is released on its
own, as GitHub Releases named **Kumo X.Y.Z for Windows** (tag `windows-vX.Y.Z`).
None of it is for Linux: on Arch, keep installing Kumo from source with
[`packaging/arch`](../arch/PKGBUILD) as the [main README](../../README.md#install-arch-linux) says.

## Install

Open the [releases](https://github.com/simo1337s/animetest/releases), take the
newest **Kumo X.Y.Z for Windows** and download one of:

| File | What it is |
|---|---|
| `Kumo-Setup-X.Y.Z-windows-x64.exe` | **Installer** (recommended). Installs Kumo for your Windows user, without admin rights, in `%LOCALAPPDATA%\Programs\Kumo` (you can pick another folder). Adds Start menu and desktop shortcuts. Uninstall it from **Settings › Apps**. To update, run the newer installer. |
| `Kumo-X.Y.Z-windows-x64-portable.zip` | **Portable** build. Unzip it anywhere and run `Kumo.exe`; nothing gets installed. Your data still goes to the folders [below](#where-kumo-keeps-its-files). |

The builds aren't code-signed, so SmartScreen may show "Windows protected your
PC": click **More info**, then **Run anyway**.

## Programs Kumo uses

Some features use other programs. **Kumo installs them for you**: at the end of
the install, the installer asks whether to install them (also later from
**Settings › App › Programs › Install**, or the Install button Kumo shows on
the Home page while ffmpeg or ani-cli is missing). A PowerShell window then
installs, with [Scoop](https://scoop.sh) and for your Windows user only (no
administrator rights):

- Scoop itself and its extras bucket
- Git for Windows (its bash runs ani-cli) and ani-cli
- ffmpeg, mpv, yt-dlp, aria2 and fzf
- the GitHub CLI (`gh`). The repository is private, so the setup offers to
  sign you in to GitHub, which Kumo's update checks need.

Programs you already have are skipped, so you can run it again whenever
something is missing. Kumo picks newly installed programs up by itself. The
script is
[`server/internal/winsetup/install-tools.ps1`](../../server/internal/winsetup/install-tools.ps1).

To install them yourself instead, from a terminal (PowerShell or Windows
Terminal): **Settings** shows what Kumo found; for a program that isn't on
your `PATH`, enter its full path there.

| For | Install |
|---|---|
| The in-app player, for most files (MKV, HEVC, AC3/DTS, several tracks…), and episode downloads | ffmpeg: `winget install Gyan.FFmpeg` |
| Faster, more reliable episode downloads | yt-dlp: `winget install yt-dlp.yt-dlp` |
| mpv as the player (optional) | `scoop install mpv` ([Scoop](#scoop), extras bucket) or [mpv.io](https://mpv.io/installation/); then choose it in **Settings › Video Playback** |
| Sub/dub streaming with ani-cli | Git for Windows and ani-cli, [see below](#ani-cli) |
| Torrents | qBittorrent: `winget install qBittorrent.qBittorrent`, [then turn on its Web UI](#qbittorrent) |

Hardware encoding for the in-app player (**Settings › Transcoding / Direct
Play**): NVENC (NVIDIA) and Quick Sync (Intel) work on Windows; VA-API is
Linux only.

### Scoop

[Scoop](https://scoop.sh) installs command-line programs for your user. In
PowerShell:

```powershell
Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
Invoke-RestMethod -Uri https://get.scoop.sh | Invoke-Expression
scoop bucket add extras    # needs Git: winget install Git.Git
```

### ani-cli

ani-cli is a bash script, so it runs in the bash that comes with Git for
Windows (which also brings the curl, sed and grep it uses). With
[Scoop](#scoop) set up:

```powershell
winget install Git.Git
scoop install ani-cli
```

Without Scoop, save the `ani-cli` script from the
[ani-cli repository](https://github.com/pystardust/ani-cli) in a folder on your
`PATH`, or anywhere and put its full path in **Settings › Online Streaming ›
Executable**. Kumo finds Git's bash in its usual places (`C:\Program Files\Git`,
Scoop's `git`).

### qBittorrent

Kumo talks to qBittorrent through its Web UI, which is off by default. In
qBittorrent: **Tools › Options › Web UI**, tick **Web User Interface (Remote
control)**, and set a username and password. Then in Kumo, **Settings ›
Torrent Client**: host `127.0.0.1`, the same port as qBittorrent's (8080
unless you changed it), the same username and password. Kumo can start
qBittorrent when it's needed: the path (`C:\Program Files\qBittorrent\qbittorrent.exe`)
is found by itself.

## Where Kumo keeps its files

| Folder | What's in it |
|---|---|
| `%APPDATA%\Kumo` | Your data: the database (lists, library matches, settings, watch progress), downloaded artwork, extension storage |
| `%LOCALAPPDATA%\Kumo` | Cache and temporary files: the in-app player's segments (`hls`), the running server's address and token (`run`), the app window's browser data (`electron`) |
| `%LOCALAPPDATA%\Programs\Kumo` | The app, when installed with the installer |

Uninstalling keeps your data; delete the first two folders to remove it too.

When **Allow devices on my network** is turned on, Windows Firewall asks
whether `kumo.exe` may accept connections: allow it on **private** networks.

`kumo.exe --web-ui` (in the app's `resources` folder) runs the server alone,
without the window: the web UI is then at `http://127.0.0.1:43211`. The app
window uses that server when it's already running.

## Build it yourself

On Windows, with Go, Node.js, Git and the GitHub CLI (the repository is
private):

```powershell
winget install GoLang.Go
winget install OpenJS.NodeJS.LTS
winget install Git.Git
winget install GitHub.cli
gh auth login
gh repo clone simo1337s/animetest kumo
cd kumo
powershell -ExecutionPolicy Bypass -File packaging\windows\build.ps1
```

[`build.ps1`](build.ps1) does what the release workflow does: builds the web
UI, embeds it into the server, runs `go vet` and `go test` (the ani-cli tests
use Git's bash; `-SkipTests` skips the tests), builds `dist\windows\kumo.exe`,
then packages the app with electron-builder
([`desktop/electron-builder.yml`](../../desktop/electron-builder.yml)). The
installer and the zip land in `dist\windows\release`.

For development, `cd desktop; npm ci; npx electron .` starts the window with
`dist\windows\kumo.exe`. On Linux, `make windows` cross-compiles that
`kumo.exe` (the installer is built on Windows).

## How releases are made

The [Windows release](../../.github/workflows/windows.yml) workflow builds,
tests and packages everything on a Windows machine, then creates (or updates)
the release `windows-v<version>`, with the version from
`desktop/package.json`. Either:

- push a tag that matches that version:
  `git tag windows-v1.0.0` then `git push origin windows-v1.0.0`; or
- run **Windows release** from the repository's **Actions** tab. With
  **publish** ticked it creates or updates the release, replacing files of the
  same name; untick it to only get the files as workflow artifacts.

Keep the version in step with `AppVersion` in `server/internal/config/config.go`
and `pkgver` in `packaging/arch/PKGBUILD`.
