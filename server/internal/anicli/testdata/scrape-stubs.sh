# Scraping stubs for ani-cli 5.x (hianime backend), used by Kumo's tests.
#
# They replace the functions of ani-cli that use the network (hianime_search,
# hianime_episodes, hianime_m3u8, update_script, time_until_next_ep), so the
# rest of the script (argument parsing, menus, playback) runs for real but
# hermetically. fake-ani-cli sources this file; the tests can also insert it
# into a real ani-cli script just before its "# MAIN" line.
#
# The data comes from the catalog file named by $KUMO_FAKE_CATALOG (a small
# default one is used when it is unset), one entry per line:
#
#   id <TAB> title <TAB> number of episodes [<TAB> modes with sources]
#
# Every run's arguments are appended to $KUMO_FAKE_LOG (when set), one run per
# line, tab separated.

if [ -n "$KUMO_FAKE_LOG" ]; then
    {
        printf 'run'
        for _arg in "$@"; do printf '\t%s' "$_arg"; done
        printf '\n'
    } >>"$KUMO_FAKE_LOG"
fi

fake_catalog() {
    if [ -n "$KUMO_FAKE_CATALOG" ]; then
        cat "$KUMO_FAKE_CATALOG"
    else
        printf '%s\t%s\t%s\n' \
            code-geass "Code Geass: Lelouch of the Rebellion" 25 \
            code-geass-r2 "Code Geass: Lelouch of the Rebellion R2" 25 \
            single-show "Single Result Show" 12
    fi
}

fake_log() {
    [ -n "$KUMO_FAKE_LOG" ] && printf '%s\n' "$*" >>"$KUMO_FAKE_LOG"
    return 0
}

# Fetches the search page like curl and a web server would: curl expands []
# and {} as URL globs (a title like "[Oshi no Ko]" is not a valid range), the
# #fragment is not sent, & starts the next parameter, + is a space and %
# starts an escape. Prints "id<TAB>title" lines like the real function.
hianime_search() {
    #shellcheck disable=SC2059
    _url="$(printf "$search_api" "$1")"
    case "$_url" in
        *[][{}]*) die "Connection error: could not fetch $_url (no HTTP response; curl exit 3)" ;;
    esac
    _kw="${_url#*keyword=}"
    _kw="${_kw%%#*}"
    _kw="${_kw%%&*}"
    case "$_kw" in
        *%*) die "Request failed: HTTP 400 from $_url" ;;
    esac
    _kw="$(printf "%s" "$_kw" | tr '+' ' ' | tr '[:upper:]' '[:lower:]')"
    fake_log "search	$_kw"
    [ -n "$(printf "%s" "$_kw" | tr -d '[:space:]')" ] || return 0
    fake_catalog | {
        set -f
        while IFS="	" read -r _id _title _eps _modes; do
            _lower="$(printf "%s" "$_title" | tr '[:upper:]' '[:lower:]')"
            _all=1
            for _word in $_kw; do
                case "$_lower" in
                    *"$_word"*) ;;
                    *) _all=0 ;;
                esac
            done
            if [ "$_all" = 1 ]; then
                printf "%s\t%s\n" "$_id" "$_title"
            fi
        done
    }
}

# Prints "episode id<TAB>episode number" lines like the real function.
hianime_episodes() {
    fake_catalog | while IFS="	" read -r _id _title _eps _modes; do
        if [ "$_id" = "$1" ]; then
            _n=1
            while [ "$_n" -le "$_eps" ]; do
                printf "%s\t%s\n" "$_id-$_n" "$_n"
                _n=$((_n + 1))
            done
        fi
    done
}

# Sets links, sub_link, refr and mal_id for episode $1 in mode $2.
hianime_m3u8() {
    _modes="$(fake_catalog | while IFS="	" read -r _id _title _eps _m; do
        if [ "$_id" = "$anime_id" ]; then
            printf "%s" "${_m:-sub dub}"
        fi
    done)"
    case " $_modes " in
        *" $2 "*) ;;
        *) return 1 ;;
    esac
    fake_log "resolve	$anime_id	$1	$2"
    refr="https://embed.example/"
    mal_id=""
    sub_link="https://subs.example/$1.vtt"
    links="$(printf "%s >https://cdn.example/%s/ep%s/%s.m3u8\n" \
        1080 "$2" "$1" 1080 \
        720 "$2" "$1" 720 \
        480 "$2" "$1" 480)"
}

update_script() {
    fake_log "update"
    exit 0
}

time_until_next_ep() {
    fake_log "nextep"
    exit 0
}
