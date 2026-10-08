#!/bin/bash
# Runs Kumo's macOS programs setup (server/internal/macsetup/
# install-tools.command) like a user would, without Homebrew, into a fresh
# folder, then checks that the programs it put there run on this Mac. The
# macOS release workflow runs it on Apple silicon and on Intel.

bin="${RUNNER_TEMP:-/tmp}/kumo-bin"
script="${RUNNER_TEMP:-/tmp}/kumo-setup.command"
rm -rf "$bin"
sed "s|'@KUMO_BIN@'|'$bin'|" server/internal/macsetup/install-tools.command >"$script"
chmod 755 "$script"
echo "== $(uname -m), macOS $(sw_vers -productVersion)"
KUMO_SETUP_NO_BREW=1 TERM=dumb "$script" </dev/null
echo "== the setup ended with $?"

failed=""
check() {
	name=$1
	shift
	out=$("$@" 2>&1)
	code=$?
	first=$(printf '%s\n' "$out" | head -1)
	if [ $code -eq 0 ] && [ -n "$first" ]; then
		echo "$name: $first | $(file -b "$bin/$name" | cut -c1-90)"
	else
		echo "::error::$name doesn't run (exit $code): $first"
		failed="$failed $name"
	fi
}
check ffmpeg "$bin/ffmpeg" -hide_banner -version
check ffprobe "$bin/ffprobe" -hide_banner -version
check yt-dlp "$bin/yt-dlp" --version
# Kumo gives ani-cli a stand-in player; without one it stops right away.
check ani-cli env ANI_CLI_PLAYER=true PATH="$bin:/usr/bin:/bin" "$bin/ani-cli" --version
if [ -n "$failed" ]; then
	echo "::warning::The programs setup didn't install:$failed"
	exit 1
fi
