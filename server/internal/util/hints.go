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
		case "ffmpeg", "mpv", "yt-dlp":
			return "brew install " + tool + " (Homebrew, brew.sh)"
		case "ani-cli":
			return "brew tap pystardust/ani-cli https://github.com/pystardust/ani-cli.git, then brew install ani-cli (Homebrew, brew.sh)"
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
