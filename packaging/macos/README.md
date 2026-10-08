# Kumo for macOS

Kumo runs on macOS 13 Ventura or newer, on Apple silicon and Intel Macs: one
universal app for both. It's the same Kumo as on Linux and Windows: a local
server with the app's window on top of it.

## Install

1. Download `Kumo-X.Y.Z-macos-universal.dmg` from the newest **Kumo X.Y.Z for
   macOS** [release](https://github.com/simo1337s/kumo-anime/releases) (tag
   `macos-vX.Y.Z`).
2. Open it and drag **Kumo** onto **Applications**.
3. Open Kumo from your Applications folder.

Kumo isn't notarized by Apple (that takes a paid Apple Developer account), so
the first time macOS says it can't check Kumo for malicious software:

- **macOS 15 Sequoia and newer:** click **Done**, then go to **System Settings
  › Privacy & Security**, scroll down to the message about Kumo and click
  **Open Anyway** (macOS asks for your password).
- **macOS 13 and 14:** Control-click Kumo in Applications, choose **Open**, then
  **Open** again.

Or, in Terminal, once: `xattr -dr com.apple.quarantine /Applications/Kumo.app`

## Programs Kumo uses

| Program | For |
|---|---|
| ffmpeg | The in-app player (most files) and episode downloads |
| ani-cli | Sub/dub streaming and downloads |
| mpv | The external player |
| yt-dlp | Faster, more reliable episode downloads |

**Settings › App › Programs › Install missing programs** installs them with
[Homebrew](https://brew.sh) in a Terminal window, and Homebrew itself first if
it's missing (its installer asks for your password). By hand:

```bash
brew install ffmpeg mpv yt-dlp ani-cli
```

Kumo finds programs in Homebrew's folders (`/opt/homebrew/bin` on Apple
silicon, `/usr/local/bin` on Intel) even when it's opened from the Dock, which
doesn't give apps those folders.

For torrents, Kumo talks to qBittorrent's Web UI (or Transmission): install
[qBittorrent](https://www.qbittorrent.org), turn on its Web UI, and set the
address and login in **Settings › Torrent Client**.

## Where Kumo keeps its files

| What | Where |
|---|---|
| Settings, database, artwork, extensions | `~/Library/Application Support/Kumo` |
| The window's own data (cookies of the AniList login) | `~/Library/Application Support/Kumo/electron` |
| Updates being downloaded | `~/Library/Caches/kumo/update` |
| The local library, by default | `~/Movies/Anime` |

## Updates

Kumo checks GitHub for a newer macOS release, and the **Update** button on its
Home page installs it: Kumo downloads the new app, puts it in place of the old
one, closes and opens again. That works once Kumo is in a folder you can write
to (your Applications folder): not from the disk image, nor from your Downloads
folder before you've moved it. If macOS says Kumo was prevented from modifying
apps, allow it in **System Settings › Privacy & Security › App Management**, or
download the new version from its release page.

## Build it yourself

On a Mac with Go, Node.js and the Xcode command line tools:

```bash
make macos                 # web UI, then the servers for Intel and Apple silicon
cd desktop && npm ci
npx electron-builder --mac --universal -c.directories.output=../dist/macos/release
```

The app, the disk image and the zip land in `dist/macos/release`.
`make macos` works on Linux too (the servers cross-compile); the app itself
needs a Mac.

## How releases are made

The **macOS release** workflow (`.github/workflows/macos.yml`) builds, tests
and packages Kumo on a GitHub-hosted Mac for every push to the default branch,
checks the app (both architectures, its signature, the zip as an update
unpacks it, and that it starts), and publishes the release
`macos-v<version>`. The version is `1.0.<number of commits>`, like the Windows
release's. The newest 10 macOS releases are kept.
