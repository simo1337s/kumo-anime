#!/bin/bash
# Kumo setup for macOS: puts the programs Kumo uses in Kumo's own folder,
# ready-made for this Mac (Apple silicon or Intel), with no Homebrew and no
# password. Kumo opens it in Terminal from Settings > App > Programs
# (server/internal/macsetup), with KUMO_BIN set to that folder.
#
#   ffmpeg, ffprobe  the in-app player (most files) and episode downloads
#   yt-dlp           faster, more reliable episode downloads
#   ani-cli          sub/dub streaming and downloads (a shell script)
#   mpv              the external player: with Homebrew, on Apple silicon
#                    (Homebrew has no ready-made mpv for Intel Macs)
#
# Programs that are there already are left alone. Kumo sees the setup
# running while <this file>.pid holds its process ID.
#
# KUMO_SETUP_NO_BREW=1 leaves Homebrew out, programs it installed included
# (for the tests).

KUMO_BIN='@KUMO_BIN@'

echo $$ >"$0.pid"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"; rm -f "$0.pid"' EXIT

title() { printf '\n\033[1m%s\033[0m\n' "$*"; }

finish() {
	echo
	read -r -p "Press Return to close this window. " _
	exit "$1"
}

arch=$(uname -m) # arm64 or x86_64
brew=""
if [ "$KUMO_SETUP_NO_BREW" != 1 ]; then
	for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do
		if [ -x "$b" ]; then
			brew=$b
			break
		fi
	done
	export PATH="$KUMO_BIN:/opt/homebrew/bin:/usr/local/bin:$PATH"
else
	export PATH="$KUMO_BIN:/usr/bin:/bin:/usr/sbin:/sbin"
fi

have() { command -v "$1" >/dev/null 2>&1; }

fetch() {
	curl -fL --retry 3 --connect-timeout 20 --progress-bar -o "$2" "$1"
}

# put makes a downloaded program runnable and moves it to Kumo's folder.
put() {
	chmod 755 "$1" || return 1
	# Apple silicon only runs signed programs: sign it here if it isn't.
	codesign -v "$1" >/dev/null 2>&1 || codesign --force -s - "$1" >/dev/null 2>&1
	mv -f "$1" "$KUMO_BIN/$2"
}

# from_zip puts the program called name from a downloaded zip in Kumo's
# folder.
from_zip() {
	rm -rf "$tmp/x"
	unzip -o -q "$1" -d "$tmp/x" || return 1
	f=$(find "$tmp/x" -type f -name "$2" | head -1)
	[ -n "$f" ] && put "$f" "$2"
}

# ffmpeg_tool gets ffmpeg or ffprobe: Martin Riedl's static builds, for both
# kinds of Mac, then evermeet.cx's for Intel Macs.
ffmpeg_tool() {
	a=arm64
	[ "$arch" = x86_64 ] && a=amd64
	if fetch "https://ffmpeg.martin-riedl.de/redirect/latest/macos/$a/release/$1.zip" "$tmp/$1.zip" && from_zip "$tmp/$1.zip" "$1"; then
		return 0
	fi
	if [ "$arch" = x86_64 ]; then
		u=https://evermeet.cx/ffmpeg/getrelease/zip
		[ "$1" = ffprobe ] && u=https://evermeet.cx/ffmpeg/getrelease/ffprobe/zip
		fetch "$u" "$tmp/$1.zip" && from_zip "$tmp/$1.zip" "$1" && return 0
	fi
	return 1
}

clear 2>/dev/null
title "Kumo setup"
echo "Puts the programs Kumo uses in its folder:"
echo "$KUMO_BIN"
mkdir -p "$KUMO_BIN" || finish 1

failed=""

if have ffmpeg && have ffprobe; then
	echo "ffmpeg: there already"
else
	title "Downloading ffmpeg"
	{ have ffmpeg || ffmpeg_tool ffmpeg; } && { have ffprobe || ffmpeg_tool ffprobe; } || failed="$failed ffmpeg"
fi

if have yt-dlp; then
	echo "yt-dlp: there already"
else
	title "Downloading yt-dlp"
	fetch https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp_macos "$tmp/yt-dlp" && put "$tmp/yt-dlp" yt-dlp || failed="$failed yt-dlp"
fi

if have ani-cli; then
	echo "ani-cli: there already"
else
	title "Downloading ani-cli"
	if fetch https://raw.githubusercontent.com/pystardust/ani-cli/master/ani-cli "$tmp/ani-cli" && head -1 "$tmp/ani-cli" | grep -q '^#!'; then
		chmod 755 "$tmp/ani-cli" && mv -f "$tmp/ani-cli" "$KUMO_BIN/ani-cli" || failed="$failed ani-cli"
	else
		failed="$failed ani-cli"
	fi
fi

mpv_note=""
if have mpv; then
	echo "mpv: there already"
elif [ -n "$brew" ] && [ "$arch" = arm64 ]; then
	title "Installing mpv with Homebrew"
	"$brew" install mpv || failed="$failed mpv"
else
	mpv_note=1
fi

if [ -n "$failed" ]; then
	title "These couldn't be installed:$failed"
	echo "Check the internet connection and run the setup again from Kumo's Settings."
	finish 1
fi
title "Done: Kumo finds the programs by itself, go back to it."
if [ -n "$mpv_note" ]; then
	echo
	echo "mpv, the external player, isn't installed: Kumo's own player doesn't need it."
	echo "To use it, get mpv from https://mpv.io/installation/"
fi
finish 0
