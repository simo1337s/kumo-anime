// The computer Kumo runs on (status.platform: Go's GOOS), for install hints
// and examples. The server's error messages use the same commands
// (server/internal/util/hints.go).

export type Tool = "ffmpeg" | "mpv" | "yt-dlp" | "ani-cli"

export function platformName(platform?: string) {
    return platform === "windows" ? "Windows" : platform === "darwin" ? "macOS" : "Linux"
}

export function installHint(platform: string | undefined, tool: Tool) {
    if (platform === "windows") {
        return {
            ffmpeg: "winget install Gyan.FFmpeg",
            mpv: "scoop install mpv",
            "yt-dlp": "winget install yt-dlp.yt-dlp",
            "ani-cli": "scoop install ani-cli",
        }[tool]
    }
    return tool === "ani-cli" ? "yay -S ani-cli" : `sudo pacman -S ${tool}`
}

// Where the hint comes from, said before it.
export function installSource(platform: string | undefined, tool: Tool) {
    if (platform === "windows") return tool === "ani-cli" ? "With Git for Windows installed (winget install Git.Git), get it with Scoop (extras bucket)" : "Install it with"
    return tool === "ani-cli" ? "Install it from the AUR with" : "Install it with"
}

export function exampleMpvPath(platform?: string) {
    return platform === "windows" ? "C:\\Program Files\\mpv\\mpv.exe" : "/usr/bin/mpv"
}
