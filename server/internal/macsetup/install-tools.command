#!/bin/bash
# Kumo setup for macOS: installs the programs Kumo uses with Homebrew
# (brew.sh). Kumo opens it in Terminal from Settings > App > Programs
# (server/internal/macsetup).
#
#   ffmpeg   the in-app player (most files) and episode downloads
#   mpv      the external player
#   yt-dlp   faster, more reliable episode downloads
#   ani-cli  sub/dub streaming and downloads
#
# Programs that are there already are left alone. Homebrew is installed
# first when it's missing: its installer asks for your password.
#
# Kumo sees the setup running while <this file>.pid holds its process ID.

echo $$ >"$0.pid"
trap 'rm -f "$0.pid"' EXIT

title() { printf '\n\033[1m%s\033[0m\n' "$*"; }

finish() {
	echo
	read -r -p "Press Return to close this window. " _
	exit "$1"
}

# find_brew puts Homebrew on the PATH (Apple silicon, then Intel).
find_brew() {
	command -v brew >/dev/null 2>&1 && return 0
	for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do
		if [ -x "$b" ]; then
			eval "$("$b" shellenv)"
			return 0
		fi
	done
	return 1
}

clear
title "Kumo setup"
echo "Installs the programs Kumo uses with Homebrew: ffmpeg, mpv, yt-dlp and ani-cli."

if ! find_brew; then
	title "Installing Homebrew"
	echo "Homebrew (brew.sh) installs the programs. Its installer asks for your password,"
	echo "and may install Apple's Command Line Tools first; it takes a few minutes."
	/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
	if ! find_brew; then
		title "Homebrew couldn't be installed, so Kumo's programs weren't either."
		echo "Install Homebrew as https://brew.sh says, then run this again from Kumo's Settings."
		finish 1
	fi
fi

missing=""
for tool in ffmpeg mpv yt-dlp ani-cli; do
	command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
if [ -z "$missing" ]; then
	title "Everything Kumo uses is installed already."
	finish 0
fi

title "Installing:$missing"
# One word per program.
# shellcheck disable=SC2086
if ! brew install $missing; then
	title "Some programs couldn't be installed: see above."
	finish 1
fi
title "Done: Kumo finds the programs by itself, go back to it."
finish 0
