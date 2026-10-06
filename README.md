<p align="center"><img src="packaging/icons/kumo-128.png" width="96" alt="Kumo"></p>

<h1 align="center">Kumo</h1>

<p align="center">A Seanime-style anime app for Arch Linux: your AniList, your local library, ani-cli streaming, torrents and the Seanime extension marketplace — in one desktop app that only ever talks to your own computer and home network.</p>

---

## Features

**Library**
- Scans your anime folders (plus extra folders), parses every file name and **matches it to AniList automatically**: seasons, parts, absolute numbering (e.g. `One Piece - 1050`, `JJK - 30` → season 2 ep 6), movies, OVAs/specials, NCOP/NCED.
- Works with **every video format**: mkv, mp4, avi, webm, mov, wmv, flv, ts/m2ts, ogm, rmvb, vob, 3gp, divx and more.
- **Auto refresh**: watches the folders and rescans when files appear, and refreshes on startup.
- **Local library** page with only the anime you have on disk: recently added, filters by list status, sort by added/unwatched/size, grid or list view with downloaded episode ranges, size, resolution and release group, and one-click "play next".
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
- Built-in browser for the **[Seanime marketplace](https://raw.githubusercontent.com/Bas1874/Seanime-Marketplace/refs/heads/main/Marketplace/Main.json)** (any compatible index URL works): install, update, configure and uninstall.
- Runs **online-streaming, torrent and manga providers** in a sandboxed JavaScript runtime with Seanime-compatible APIs (`fetch`, `LoadDoc`, `CryptoJS`, `$store`, `$storage`, `$habari`, user config…).
- **UI plugins**: trays, anime-page buttons, webviews, toasts, storage, AniList access, `$ui.register`, `ctx.state/effect/fieldRef`, … All 15 most-starred marketplace plugins load. Plugins that rewrite the app's own pages (`ctx.dom`) or need file/command access only partly work, because Kumo deliberately doesn't allow either.
- **Manga** reader (long strip / paged, LTR / RTL) using manga provider extensions.

**Extras**: Discord rich presence (local IPC), accent colours, spoiler blur, Ctrl+K quick search, server logs and cache viewer.

## Closed network by design

| Who | Access |
|---|---|
| The desktop window | Always (authenticated with a per-run token) |
| A browser on this PC | Only when **Settings › App › Web UI** is on (`http://127.0.0.1:43211`) |
| Devices on your home network | Only when **Allow devices on my network** is on. They can be password protected, and can't change paths/programs or browse folders. |
| Anything on the internet | **Never** (connections from public IP addresses are refused) |

The server also rejects other websites' requests (Origin check) and DNS rebinding tricks (Host check). Extensions and the stream/image proxies can't connect to localhost or your LAN. Plugins get no file or command access. Nothing is sent anywhere except the services you use (AniList, ani.zip, AniSkip, the torrent and stream sites you search).

## Install (Arch Linux)

```bash
sudo pacman -S --needed base-devel go nodejs npm electron mpv ffmpeg
yay -S ani-cli            # sub/dub streaming & downloads (recommended)
sudo pacman -S yt-dlp     # optional, better downloads
sudo pacman -S --needed github-cli && gh auth login   # the repository is private
gh repo clone simo1337s/animetest kumo && cd kumo
cd packaging/arch && makepkg -si
```

Update later with `cd kumo && git pull && cd packaging/arch && makepkg -si`.

Start **Kumo** from your app menu (or run `kumo`).

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
| `desktop/` | Electron shell (system `electron` on Arch) |
| `packaging/` | PKGBUILD, launcher, `.desktop`, icons, systemd user unit |

Data lives in `~/.local/share/kumo` (SQLite database, artwork cache, extension storage).

## Notes
- AniList's API may rate-limit large first scans; Kumo waits and retries automatically.
- Image-based subtitles (PGS/VobSub) only render in mpv.
- Kumo is a personal project, unaffiliated with Seanime, AniList or ani-cli. Licensed GPL-3.0.
