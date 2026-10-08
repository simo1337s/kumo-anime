package util

import "runtime"

// InstallHint says how to install a program Kumo uses, for messages.
func InstallHint(tool string) string {
	if runtime.GOOS == "windows" {
		switch tool {
		case "ffmpeg":
			return "winget install Gyan.FFmpeg"
		case "mpv":
			return "scoop install mpv (extras bucket), or get it from mpv.io"
		case "yt-dlp":
			return "winget install yt-dlp.yt-dlp"
		case "ani-cli":
			return "install Git for Windows (winget install Git.Git), then scoop install ani-cli (extras bucket)"
		}
		return "install " + tool
	}
	if runtime.GOOS == "darwin" {
		switch tool {
		case "ffmpeg", "yt-dlp", "ani-cli":
			return "Settings › App › Programs › Install downloads it"
		case "mpv":
			return "get it from mpv.io, or brew install mpv on an Apple silicon Mac"
		}
		return "install " + tool
	}
	switch tool {
	case "ffmpeg", "mpv", "yt-dlp":
		return "sudo pacman -S " + tool
	case "ani-cli":
		return "yay -S ani-cli (AUR)"
	}
	return "install " + tool
}
