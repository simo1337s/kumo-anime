<p align="center"><img src="packaging/icons/kumo-128.png" width="96" alt="Kumo"></p>

<h1 align="center">Kumo</h1>

<p align="center">An anime app for Arch Linux,Windows,Mac os: your AniList, your local library, ani-cli streaming, torrents and an extension marketplace — in one desktop app that only ever talks to your own computer and home network.</p>

---

## Features

**Library**
- Scans your anime folders (plus extra folders), parses every file name and **matches it to AniList automatically**: seasons, parts, absolute numbering (e.g. `One Piece - 1050`, `JJK - 30` → season 2 ep 6), movies, OVAs/specials, NCOP/NCED.
- Works with **every video format**: mkv, mp4, avi, webm, mov, wmv, flv, ts/m2ts, ogm, rmvb, vob, 3gp, divx and more.
- **Auto refresh**: watches the folders and rescans when files appear, and refreshes on startup.
- **Local library** page with only the anime you have on disk: recently added, filters by list status, sort by added/unwatched/size, grid or list view with downloaded episode ranges, size, resolution and release group, and one-click "play next".
- **Library sharing** between the Kumo apps on your computers at home: they find each other by themselves (or by address). On the computer with the anime, turn on **Share my library** for the others in **Settings › Local Anime Library › Library sharing**: its anime show up on theirs (a tab on the Local library page, and on each anime's page) and play like local files, converted by the computer that has them when needed. Watching updates the list (AniList or local) of the computer you watch on, never the other's. On the same AniList account, Continue watching carries over to the second (live to the computer whose files you watch), and episodes watched on any device come first.
- Library tools to fix matches by hand, change episode numbers, lock files and ignore junk.
- Safe with removable drives: an unplugged or unmounted library folder keeps its matches; symlinked folders are followed.
- **Artwork & metadata**: covers, banners, fanart, descriptions, genres, studios, characters, relations, recommendations, **episode titles, summaries and thumbnails** (AniList + ani.zip). All artwork is **downloaded to a local cache**, so it loads instantly and still shows offline.

**Watching**
- **mpv** (desktop media player, controlled over IPC) or the **in-app player**.
- In-app player plays any file: direct play when the browser can, otherwise ffmpeg **remuxes or transcodes on the fly** (VA-API / NVENC / QSV supported). Embedded and external subtitles (ASS/SRT/VTT) and audio-track switching are supported.
- **Resume exactly where you stopped**: the position is saved every few seconds, in both players.
- **Remembers your audio and subtitle track per anime.** Pick "Japanese + English Full Subs" once and every next episode opens with the same tracks, matched by language and title, so it works even when the track order changes.
- Auto skip openings (AniSkip), auto play next, "Continue watching" row, now-playing bar for mpv.
- Progress syncs to AniList automatically after an episode passes the completion threshold you set.

**Streaming & downloads (ani-cli)**
- Sub **and** dub streaming through your installed **ani-cli**. Kumo drives ani-cli non-interactively (no fzf pop-ups), so updating ani-cli (`ani-cli -U` / AUR) keeps it working when sites change.
- Per-anime sub/dub choice, quality, manual "wrong match?" fixing.
- **Download episodes** (single or ranges) with yt-dlp or ffmpeg straight into your library; they're matched instantly.
- Plus any **online-streaming extension** from the marketplace (HiAnime, AnimeAV1, …).

**Torrents**
- **qBittorrent** (Web UI API, v4 and v5) and **Transmission** sync: list, pause/resume, remove (with data), open folder, active count in the sidebar. Kumo can launch the client for you.
- Smart search on **Nyaa** and **AnimeTosho** (or extension providers): episode, batch, resolution, release group, seeders, dual-audio badges.
- **Auto downloader**: RSS rules per anime (release groups, resolutions, keywords, min seeders).

**AniList**
- Log in with AniList (client `13985`). The desktop window captures the token automatically, or you can paste it.
- Lists, progress, status, scores, rewatches; discover, search with filters, airing schedule.
- Works without an account too: your list is stored locally.

**Extensions & plugins**
- Built-in browser for the **[extension marketplace](https://raw.githubusercontent.com/Bas1874/Seanime-Marketplace/refs/heads/main/Marketplace/Main.json)** (any compatible index URL works): install, update, configure and uninstall.
- Runs **online-streaming, torrent and manga providers** in a sandboxed JavaScript runtime with the APIs marketplace extensions use (`fetch`, `LoadDoc`, `CryptoJS`, `$store`, `$storage`, `$habari`, user config…).
- **UI plugins**: trays, anime-page buttons, webviews, toasts, storage, AniList access, `$ui.register`, `ctx.state/effect/fieldRef`, … All 15 most-starred marketplace plugins load. Plugins that rewrite the app's own pages (`ctx.dom`) or need file/command access only partly work, because Kumo deliberately doesn't allow either.
- **Manga** reader (long strip, single pages or two-page spreads, LTR / RTL) using manga provider extensions.

**Extras**: can keep running in the tray (or the Dock) when its window is closed, so streaming to other devices and downloads go on, and start in the background when you log in (Settings › App › Desktop app, both off unless you turn them on); updates itself from the Home page, Discord rich presence (local IPC), accent colours, spoiler blur, Ctrl+K quick search, server logs and cache viewer.

## Closed network by design

| Who | Access |
|---|---|
| The desktop window | Always (authenticated with a per-run token) |
| A browser on this PC | Only when **Settings › App › Web UI** is on (`http://127.0.0.1:43211`) |
| Devices on your home network | Only when **Allow devices on my network** is on. They can be password protected, and can't change paths/programs or browse folders. |
| Kumo on your other computers | With **Library sharing** on: they see it, and only those you turn on get the library, and nothing else (each Kumo has its own key pair; requests are signed, and play only files of the library). |
| Anything on the internet | **Never** (connections from public IP addresses are refused) |

The server also rejects other websites' requests (Origin check) and DNS rebinding tricks (Host check). Extensions and the stream/image proxies can't connect to localhost or your LAN. Plugins get no file or command access. Nothing is sent anywhere except the services you use (AniList, ani.zip, AniSkip, the torrent and stream sites you search).

## Install (Arch Linux)

```bash
sudo pacman -S --needed base-devel go nodejs npm electron mpv ffmpeg
yay -S ani-cli            # sub/dub streaming & downloads (recommended)
sudo pacman -S yt-dlp     # optional, better downloads
git clone https://github.com/v0-0x/kumo-anime kumo && cd kumo
cd packaging/arch && makepkg -si
```

No git? Download the source from GitHub (**Code › Download ZIP**), unpack it (`bsdtar -xf kumo-anime-*.zip`) and run `makepkg -si` in its `packaging/arch` folder.

Start **Kumo** from your app menu (or run `kumo`).

### Updates
When GitHub has a newer Kumo, the **Home** page shows it with an **Update** button: Kumo downloads the new source, builds it with makepkg and installs the package with pacman (a password dialog asks for your password); **Restart Kumo** then starts the new version. **Settings › About & updates** shows the version and the last check, and checks again right away.

Update checks need no GitHub account. Without a password dialog (e.g. when Kumo runs as the `kumo-server` service), Kumo builds the update and shows the `sudo pacman -U …` command to run.

You can still update by hand: `cd kumo && git pull && cd packaging/arch && makepkg -sif` (`-f` rebuilds; without it makepkg reinstalls the package it built last time).

### First run
1. **Settings › Local Anime Library**: pick your folder (e.g. `/mnt/big/Media/Anime`) and save. The first scan starts on its own.
2. Click your avatar, then **Log in with AniList** (optional).
3. **Settings › Torrent Client**: enter your qBittorrent Web UI details (host `127.0.0.1`, port, user, password, and the executable, e.g. `/var/lib/flatpak/exports/bin/org.qbittorrent.qBittorrent`).
4. **Extensions › Marketplace**: add providers or plugins.

### Headless / other devices
```bash
systemctl --user enable --now kumo-server   # server only, web UI on 127.0.0.1:43211
```
Turn on **Allow devices on my network** (with a password) to use Kumo from your phone or TV at `http://<pc-name>.local:43211`. The desktop app attaches to the running server.

## Windows

Kumo also runs on Windows 10/11 (64-bit), released separately from the Arch package: get the installer (`Kumo-Setup-X.Y.Z-windows-x64.exe`) or the portable zip from the newest **Kumo X.Y.Z for Windows** [release](https://github.com/v0-0x/kumo-anime/releases) (tag `windows-vX.Y.Z`). Those files are Windows-only; on Arch, use the PKGBUILD above. The installer also offers to install the programs Kumo uses (Git, ani-cli, ffmpeg, mpv, yt-dlp…) with Scoop, and the installed app updates itself from its Home page too.

What to install alongside it (ffmpeg, mpv, yt-dlp, ani-cli, qBittorrent), where it keeps its data, how to build it and how releases are made: [packaging/windows/README.md](packaging/windows/README.md).

## macOS

Kumo runs on macOS 13 Ventura or newer, on Apple silicon and Intel Macs (one universal app): get the disk image (`Kumo-X.Y.Z-macos-universal.dmg`) from the newest **Kumo X.Y.Z for macOS** [release](https://github.com/v0-0x/kumo-anime/releases) (tag `macos-vX.Y.Z`) and drag Kumo to Applications. It isn't notarized by Apple: the first time, allow it in System Settings › Privacy & Security (**Open Anyway**). Kumo downloads the programs it uses (ffmpeg, yt-dlp, ani-cli) from its Settings, with no Homebrew needed, and updates itself from its Home page.

How to open it the first time, where it keeps its data, how to build it: [packaging/macos/README.md](packaging/macos/README.md).

## Android / Fire TV

Kumo for Android is made for TVs (a Fire TV Stick, Android TV) and plays the libraries the computers at home share, with the remote: get `Kumo-X.Y.Z-android.apk` from the newest **Kumo X.Y.Z for Android** [release](https://github.com/v0-0x/kumo-anime/releases) (tag `android-vX.Y.Z`). On a Fire TV, install the **Downloader** app, allow it in Settings › My Fire TV › Developer options › Install unknown apps, and open the APK's address in it. Then, on a computer with Kumo, turn on **Library sharing** (Settings › Local Anime Library): the TV shows up there. Turn on **Share my library** for it, and if you like **Share my AniList account** (the TV uses your account, with no login on the TV) and **Let it download here** (the TV's downloads and torrents go to that computer, and it becomes where the TV downloads by default). The app updates itself from its Home page; see [android/README.md](android/README.md).

## Development

```bash
make            # builds web/ and embeds it into dist/kumo
make test       # Go tests (library parser, ani-cli driver, extensions, security)
./dist/kumo --web-ui              # server + web UI at http://127.0.0.1:43211
cd web && npm run dev             # UI dev server on :43000 (proxies /api)
cd desktop && npm i && npx electron .   # desktop window (uses ../dist/kumo)
```

| Path | What |
|---|---|
| `server/` | Go server: AniList, scanner/matcher, mpv IPC, ani-cli driver, downloads, torrents, extension runtime (goja), API |
| `web/` | React + Tailwind UI (embedded into the binary) |
| `desktop/` | Electron shell (system `electron` on Arch; bundled by electron-builder for Windows and macOS) |
| `packaging/` | PKGBUILD, launcher, `.desktop`, icons, systemd user unit; `windows/`: Windows build script and notes; `macos/`: the app icon and notes |

Data lives in `~/.local/share/kumo` (SQLite database, artwork cache, extension storage; `%APPDATA%\Kumo` on Windows, `~/Library/Application Support/Kumo` on macOS).

## Notes
- AniList's API may rate-limit large first scans; Kumo waits and retries automatically.
- Image-based subtitles (PGS/VobSub) only render in mpv.
- Kumo is a personal project, unaffiliated with AniList or ani-cli. Licensed GPL-3.0.
