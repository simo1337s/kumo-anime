#!/bin/sh
# Launches the Kumo desktop app with the system Electron.
# Extra Electron flags can go in ~/.config/kumo-flags.conf (one per line).
ELECTRON="${KUMO_ELECTRON:-electron}"
FLAGS=""
if [ -f "${XDG_CONFIG_HOME:-$HOME/.config}/kumo-flags.conf" ]; then
    FLAGS="$(grep -v '^#' "${XDG_CONFIG_HOME:-$HOME/.config}/kumo-flags.conf" | tr '\n' ' ')"
fi
# shellcheck disable=SC2086
exec "$ELECTRON" --ozone-platform-hint=auto $FLAGS /usr/lib/kumo/app "$@"
