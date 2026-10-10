import Hls from "hls.js"
import {
    ArrowLeft,
    Captions,
    FastForward,
    Gauge,
    Languages,
    Loader2,
    Maximize,
    Minimize,
    MonitorPlay,
    Pause,
    Play,
    RotateCcw,
    RotateCw,
    SkipForward,
    Volume1,
    Volume2,
    VolumeX,
} from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { toast } from "@/lib/toast"
import { api, qs } from "@/lib/api"
import { prefersHls, randomId, videoCaps } from "@/lib/playback"
import { useSettings } from "@/lib/queries"
import { playerStore, type PlayerRequest } from "@/lib/store"
import type { EntryView, Probe, StreamSource, TrackPrefs } from "@/lib/types"
import { android, androidApp, move, tv } from "@/lib/tv"
import { cn, formatDuration, title as animeTitle } from "@/lib/utils"
import { parseVTT, type Cue } from "@/lib/vtt"
import { Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger } from "../ui"

type SubOption = { key: string; label: string; lang: string; title: string; index: number; url: string; bitmap?: boolean }
type Skip = { type: string; start: number; end: number }
// The AniSkip intervals the player skips: openings and endings. Recaps play
// (AniSkip calls the next episode's preview one too).
const SKIPPED = ["op", "mixed-op", "ed", "mixed-ed"]
const skipName = (type: string) => (type === "ed" || type === "mixed-ed" ? "ending" : "opening")

const langNames: Record<string, string> = {
    jpn: "Japanese", ja: "Japanese", eng: "English", en: "English", spa: "Spanish", es: "Spanish", fre: "French", fra: "French", fr: "French",
    ger: "German", deu: "German", de: "German", ita: "Italian", it: "Italian", por: "Portuguese", pt: "Portuguese", rus: "Russian", ru: "Russian",
    chi: "Chinese", zho: "Chinese", zh: "Chinese", kor: "Korean", ko: "Korean", ara: "Arabic", ar: "Arabic", und: "Unknown",
}
const langName = (l?: string) => (l ? langNames[l.toLowerCase()] ?? l.toUpperCase() : "")
const normLang = (l?: string) => {
    const x = (l ?? "").toLowerCase()
    const map: Record<string, string> = { ja: "jpn", jp: "jpn", en: "eng", es: "spa", fr: "fre", fra: "fre", de: "ger", deu: "ger", it: "ita", pt: "por", ru: "rus", zh: "chi", zho: "chi", ko: "kor", ar: "ara" }
    return map[x] ?? x
}
const langIn = (lang: string, list: string) => list.split(",").map(s => normLang(s.trim())).filter(Boolean).includes(normLang(lang))

export function PlayerOverlay() {
    const [req, setReq] = useState<PlayerRequest | null>(playerStore.get())
    useEffect(
        () =>
            playerStore.subscribe(() => {
                // The episode that's playing asked for again: it goes on
                // playing (a new request object would reload the stream).
                const next = playerStore.get()
                setReq(prev => (prev && next && JSON.stringify(prev) === JSON.stringify(next) ? prev : next))
            }),
        [],
    )
    if (!req) return null
    return <Player key={JSON.stringify(req)} req={req} onClose={() => playerStore.set(null)} />
}

function Player({ req, onClose }: { req: PlayerRequest; onClose: () => void }) {
    const settings = useSettings()
    const videoRef = useRef<HTMLVideoElement>(null)
    const containerRef = useRef<HTMLDivElement>(null)
    const hlsRef = useRef<Hls | null>(null)
    const trackRef = useRef<TextTrack | null>(null)

    const [title, setTitle] = useState("")
    const [error, setError] = useState("")
    const [loading, setLoading] = useState(true)
    const [buffering, setBuffering] = useState(false)

    // local
    const [probe, setProbe] = useState<Probe | null>(null)
    const [method, setMethod] = useState<"direct" | "remux" | "transcode">("direct")
    const [offset, setOffset] = useState(0) // where the converted stream was asked to start
    const [audioIndex, setAudioIndex] = useState(0)
    // The converted stream: base is where it really starts (copied video can
    // only start at a keyframe), so subtitles and times stay in sync.
    const [stream, setStream] = useState<{ url: string; base: number; hls: boolean; id?: string } | null>(null)
    const [restart, setRestart] = useState(0)
    // stream
    const [sources, setSources] = useState<StreamSource[]>([])
    const [sourceIdx, setSourceIdx] = useState(0)

    const [subs, setSubs] = useState<SubOption[]>([])
    const [subKey, setSubKey] = useState<string>("off")
    const [cues, setCues] = useState<Cue[]>([])
    const [skips, setSkips] = useState<Skip[]>([])

    const [paused, setPaused] = useState(true)
    const [time, setTime] = useState(0) // absolute time
    const [duration, setDuration] = useState(0)
    const [buffered, setBuffered] = useState(0)
    const [volume, setVolume] = useState(() => Number(localStorage.getItem("kumo-volume") ?? 1))
    const [muted, setMuted] = useState(false)
    const [rate, setRate] = useState(1)
    const [fullscreen, setFullscreen] = useState(false)
    const [controls, setControls] = useState(true)
    const [hoverTime, setHoverTime] = useState<{ x: number; t: number } | null>(null)
    const [nextEp, setNextEp] = useState<{ path?: string; episode: number } | null>(null)

    const startAt = useRef(0)
    const lastReport = useRef(0)
    const ended = useRef(false)
    const skipped = useRef<Set<string>>(new Set())
    const prefsRef = useRef<TrackPrefs | null>(null)

    const mediaId = req.mediaId
    const episode = req.episode
    const source = req.kind === "local" ? "local" : req.provider === "ani-cli" ? "anicli" : "stream"
    // Safari and iPhone/iPad get converted files as HLS (see lib/playback).
    const hlsMode = useMemo(() => prefersHls(), [])
    const caps = useMemo(() => videoCaps(hlsMode && Hls.isSupported() ? "mse" : "element"), [hlsMode])
    const clientId = useMemo(() => randomId(), [])
    const base = req.kind === "local" && method !== "direct" && stream ? stream.base : offset
    const absTime = (v: HTMLVideoElement) => v.currentTime + base

    // ------------------------------------------------------------------ load
    useEffect(() => {
        let cancelled = false
        ;(async () => {
            try {
                setLoading(true)
                if (req.kind === "local") {
                    const res = await api.post<{ probe: Probe; title: string; resumeAt: number; tracks: TrackPrefs | null }>("/api/playback/local", {
                        path: req.path,
                        mediaId: req.mediaId,
                        episode: req.episode,
                        player: "builtin",
                        start: req.start,
                        caps,
                    })
                    if (cancelled) return
                    prefsRef.current = res.tracks
                    setTitle(res.title)
                    setProbe(res.probe)
                    setDuration(res.probe.duration)
                    const audio = (res.probe.audio ?? []).map((a, i) => ({ index: i, lang: a.language, title: a.title }))
                    let ai = res.probe.audio?.findIndex(a => a.default) ?? 0
                    if (ai < 0) ai = 0
                    const p = res.tracks
                    // Sub/dub chosen for this anime (or a "dub" default) picks the
                    // audio of dual-audio files, unless a track was picked by hand.
                    const mode = p?.streamMode || (settings?.aniCli.defaultMode === "dub" ? "dub" : "")
                    const handAudio = !!(settings?.playback.rememberTracks && p && (p.audioLang || p.audioTitle || p.audioIndex))
                    const handSub = !!(settings?.playback.rememberTracks && p && (p.subOff || p.subLang || p.subTitle || p.subIndex))
                    let modeAudio = false
                    if (handAudio && p) {
                        const m =
                            audio.find(a => normLang(a.lang) === normLang(p.audioLang) && a.title === p.audioTitle) ??
                            audio.find(a => p.audioTitle && a.title === p.audioTitle) ??
                            audio.find(a => p.audioLang && normLang(a.lang) === normLang(p.audioLang)) ??
                            (p.audioIndex ? audio[p.audioIndex - 1] : undefined)
                        if (m) ai = m.index
                    } else if (mode && audio.length > 1 && (mode === "dub" ? audio.some(a => isEnglishTrack(a.lang, a.title)) : true)) {
                        const m =
                            mode === "dub"
                                ? audio.find(a => isEnglishTrack(a.lang, a.title))
                                : (audio.find(a => isJapaneseTrack(a.lang, a.title)) ?? audio.find(a => !isEnglishTrack(a.lang, a.title)))
                        if (m) {
                            ai = m.index
                            modeAudio = true
                        }
                    } else if (settings?.playback.preferredAudioLang) {
                        const m = audio.find(a => langIn(a.lang, settings.playback.preferredAudioLang))
                        if (m) ai = m.index
                    }
                    setAudioIndex(ai)
                    const m = ai === 0 && res.probe.method === "direct" ? "direct" : res.probe.method === "direct" ? "remux" : res.probe.method
                    setMethod(m)
                    // Ignore saved positions past the end of this file.
                    startAt.current = res.resumeAt && res.resumeAt < res.probe.duration - 10 ? res.resumeAt : 0
                    if (m !== "direct") setOffset(startAt.current)

                    const subOpts: SubOption[] = [
                        ...(res.probe.subtitles ?? []).map(s => ({
                            key: `e${s.typeIndex}`,
                            label: [langName(s.language), s.title].filter(Boolean).join(" · ") || `Track ${s.typeIndex + 1}`,
                            lang: s.language,
                            title: s.title,
                            index: s.typeIndex,
                            bitmap: s.bitmap,
                            url: `/api/local/subtitle${qs({ path: req.path, index: s.typeIndex })}`,
                        })),
                        ...(res.probe.externalSubs ?? []).map((s, i) => ({
                            key: `x${i}`,
                            label: `${s.language || "External"} · ${s.name}`,
                            lang: s.language,
                            title: s.name,
                            index: 1000 + i,
                            url: `/api/local/subtitle${qs({ path: req.path, external: s.path })}`,
                        })),
                    ]
                    setSubs(subOpts)
                    if (modeAudio && !handSub && mode === "dub") {
                        // With a dub only signs & songs (on-screen text), if there are any.
                        const signs = subOpts.find(o => !o.bitmap && /sign|song|forced/i.test(o.title) && (!o.lang || langIn(o.lang, "eng,en")))
                        setSubKey(signs ? signs.key : "off")
                    } else if (modeAudio && !handSub && mode === "sub") {
                        const full = subOpts.find(o => !o.bitmap && (langIn(o.lang, "eng,en") || /english/i.test(o.title)) && !/sign|song|forced/i.test(o.title))
                        setSubKey(full ? full.key : pickSub(subOpts, null, settings?.playback.preferredSubLang ?? "", (res.probe.subtitles ?? []).find(s => s.default)?.typeIndex))
                    } else {
                        setSubKey(pickSub(subOpts, p, settings?.playback.preferredSubLang ?? "", (res.probe.subtitles ?? []).find(s => s.default)?.typeIndex))
                    }
                } else {
                    const res = await api.get<{ sources: StreamSource[]; resumeAt: number; tracks: TrackPrefs | null; title: string }>(
                        `/api/onlinestream/sources${qs({ provider: req.provider, mediaId: req.mediaId, episode: req.episode, dub: req.dub, server: req.server })}`,
                    )
                    if (cancelled) return
                    prefsRef.current = res.tracks
                    const media = await api.get<EntryView>(`/api/anime/${req.mediaId}`).catch(() => null)
                    setTitle(`${animeTitle(media?.media) || res.title || "Episode"} — Episode ${req.episode}${req.dub ? " (Dub)" : ""}`)
                    const sorted = [...res.sources].sort((a, b) => qualityRank(b.quality) - qualityRank(a.quality))
                    setSources(sorted)
                    setSourceIdx(0)
                    startAt.current = res.resumeAt || 0
                    applyStreamSubs(sorted[0])
                }
                // Next episode
                api.get<EntryView>(`/api/anime/${mediaId}`)
                    .then(e => {
                        if (cancelled) return
                        const n = e.episodes.find(x => x.number === episode + 1)
                        if (!n) return
                        if (req.kind === "local") n.file && setNextEp({ path: n.file.path, episode: n.number })
                        else if (n.aired) setNextEp({ episode: n.number })
                    })
                    .catch(() => {})
            } catch (e: any) {
                if (!cancelled) setError(e.message || String(e))
            } finally {
                if (!cancelled) setLoading(false)
            }
        })()
        return () => {
            cancelled = true
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [])

    const applyStreamSubs = (src?: StreamSource) => {
        const opts: SubOption[] = (src?.subtitles ?? []).map((s, i) => ({ key: `s${i}`, label: s.language || `Subtitle ${i + 1}`, lang: s.language, title: s.language, index: i, url: s.url }))
        setSubs(opts)
        const def = opts.find((_, i) => src?.subtitles[i].isDefault) ?? opts.find(o => /eng/i.test(o.lang))
        setSubKey(pickSub(opts, prefsRef.current, settings?.playback.preferredSubLang ?? "", undefined, def?.key))
    }

    // ------------------------------------------------------------ video src
    // A converted local file: ask the server where the stream will start
    // (or start an HLS session), then load it.
    useEffect(() => {
        if (req.kind !== "local" || !probe || method === "direct") {
            setStream(null)
            return
        }
        let cancelled = false
        ;(async () => {
            try {
                if (hlsMode) {
                    const s = await api.post<{ id: string; url: string; start: number }>("/api/local/hls", {
                        path: req.path,
                        start: offset,
                        audio: audioIndex,
                        method,
                        caps,
                        client: clientId,
                    })
                    if (!cancelled) setStream({ url: s.url, base: s.start, hls: true, id: s.id })
                } else {
                    const c = caps.join(",")
                    const sp = await api
                        .get<{ start: number }>(`/api/local/seekpoint${qs({ path: req.path, t: offset.toFixed(3), audio: audioIndex, method, caps: c })}`)
                        .catch(() => ({ start: offset }))
                    if (!cancelled)
                        setStream({ url: `/api/local/transcode${qs({ path: req.path, start: sp.start.toFixed(3), audio: audioIndex, method, caps: c })}`, base: sp.start, hls: false })
                }
            } catch (e: any) {
                if (!cancelled) setError(e.message || String(e))
            }
        })()
        return () => {
            cancelled = true
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [req, probe, method, offset, audioIndex, restart])

    // End the HLS session when the player closes.
    const sessionRef = useRef<string | undefined>(undefined)
    useEffect(() => {
        sessionRef.current = stream?.id
    }, [stream])
    useEffect(
        () => () => {
            if (sessionRef.current) api.del(`/api/local/hls/${sessionRef.current}`).catch(() => {})
        },
        [],
    )

    const videoSrc = useMemo(() => {
        if (req.kind === "local") {
            if (!probe) return ""
            if (method === "direct") return `/api/local/file${qs({ path: req.path })}`
            return stream?.url ?? ""
        }
        return sources[sourceIdx]?.url ?? ""
    }, [req, probe, method, stream, sources, sourceIdx])

    useEffect(() => {
        const v = videoRef.current
        if (!v || !videoSrc) return
        hlsRef.current?.destroy()
        hlsRef.current = null
        const src = sources[sourceIdx]
        const localHls = req.kind === "local" && method !== "direct" && !!stream?.hls
        // HLS can start right where asked; the progressive stream starts at
        // its keyframe (stream.base).
        const resume = localHls ? Math.max(0, offset - stream!.base) : req.kind === "local" ? (method === "direct" ? startAt.current : 0) : startAt.current
        const isHls = localHls || (req.kind === "stream" && (src?.type === "m3u8" || /m3u8/i.test(src?.originalUrl ?? "")))
        if (isHls && Hls.isSupported()) {
            // A local session's playlist grows while it's converted: give a
            // start, or players begin at its end like a live stream.
            // Memory: hls.js keeps everything already watched (the whole
            // episode by the end); keep the last 90s for going back, and up
            // to 30 MB (at least 60s) ahead.
            const hls = new Hls({
                startPosition: localHls ? resume : resume > 0 ? resume : -1,
                maxBufferLength: 60,
                maxBufferSize: 30 * 1000 * 1000,
                backBufferLength: 90,
            })
            hls.loadSource(videoSrc)
            hls.attachMedia(v)
            hls.on(Hls.Events.ERROR, (_, data) => {
                if (!data.fatal) return
                if (localHls) {
                    // The session ended (e.g. paused for a long time): start
                    // a new one here. Anything else: convert everything.
                    const at = v.currentTime + (stream?.base ?? 0)
                    startAt.current = 0
                    setOffset(at)
                    if (data.type === Hls.ErrorTypes.NETWORK_ERROR) setRestart(n => n + 1)
                    else if (method === "remux") setMethod("transcode")
                    else setError("This video could not be played (" + data.details + ")")
                    return
                }
                if (sourceIdx + 1 < sources.length) {
                    toast.warning(`Source failed, trying ${sources[sourceIdx + 1].quality || "the next one"}…`)
                    setSourceIdx(i => i + 1)
                } else setError("The stream could not be played (" + data.details + ")")
            })
            hlsRef.current = hls
        } else {
            v.src = videoSrc
            if (resume > 0) {
                const onMeta = () => {
                    v.currentTime = resume
                    v.removeEventListener("loadedmetadata", onMeta)
                }
                v.addEventListener("loadedmetadata", onMeta)
            }
        }
        v.volume = volume
        v.playbackRate = rate
        v.play().catch(() => {})
        return () => {
            hlsRef.current?.destroy()
            hlsRef.current = null
        }
        // A seek within one keyframe interval sets up the same stream again:
        // it still has to reload.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [videoSrc, stream])

    // ------------------------------------------------------------- subtitles
    useEffect(() => {
        const sub = subs.find(s => s.key === subKey)
        if (!sub) {
            setCues([])
            return
        }
        if (sub.bitmap) {
            toast.warning("Image-based subtitles (PGS/VobSub) can only be shown in mpv")
            setCues([])
            return
        }
        let cancelled = false
        fetch(sub.url)
            .then(r => {
                if (!r.ok) throw new Error("subtitle unavailable")
                return r.text()
            })
            .then(t => !cancelled && setCues(parseVTT(t)))
            .catch(() => {
                if (!cancelled) {
                    setCues([])
                    toast.error("Could not load these subtitles")
                }
            })
        return () => {
            cancelled = true
        }
    }, [subKey, subs])

    useEffect(() => {
        const v = videoRef.current
        if (!v) return
        if (!trackRef.current) {
            trackRef.current = v.addTextTrack("subtitles", "Kumo", "und")
        }
        const tr = trackRef.current
        tr.mode = "showing"
        while (tr.cues && tr.cues.length) tr.removeCue(tr.cues[0])
        for (const c of cues) {
            const s = c.start - base
            const e = c.end - base
            if (e <= 0) continue
            try {
                const cue = new VTTCue(Math.max(0, s), e, c.text.replace(/<[^>]+>/g, m => (/^<\/?(i|b|u)>$/i.test(m) ? m : "")))
                cue.line = -2
                tr.addCue(cue)
            } catch {
                /* ignore bad cue */
            }
        }
    }, [cues, base])

    // -------------------------------------------------------------- progress
    const report = useCallback(
        (force = false, didEnd = false) => {
            const v = videoRef.current
            if (!v || mediaId <= 0) return
            const now = Date.now()
            if (!force && now - lastReport.current < 5000) return
            lastReport.current = now
            const position = absTime(v)
            const dur = duration || v.duration || 0
            if (position < 3 && !didEnd) return
            api.post("/api/playback/progress", { mediaId, episode, position, duration: dur, source, ended: didEnd, title, paused: v.paused }).catch(() => {})
        },
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [mediaId, episode, source, duration, base, title],
    )

    const saveTrackPrefs = (next: { audio?: number; sub?: string }) => {
        if (!settings?.playback.rememberTracks || mediaId <= 0) return
        const a = next.audio ?? audioIndex
        const sk = next.sub ?? subKey
        const audio = probe?.audio?.[a]
        const sub = subs.find(s => s.key === sk)
        const body: TrackPrefs = {
            mediaId,
            audioLang: audio?.language ?? prefsRef.current?.audioLang ?? "",
            audioTitle: audio?.title ?? prefsRef.current?.audioTitle ?? "",
            audioIndex: audio ? a + 1 : prefsRef.current?.audioIndex ?? 0,
            subLang: sub?.lang ?? "",
            subTitle: sub?.title ?? "",
            subIndex: sub ? (sub.index >= 1000 ? 0 : sub.index + 1) : 0,
            subOff: sk === "off",
        }
        prefsRef.current = body
        api.put(`/api/playback/tracks/${mediaId}`, body).catch(() => {})
    }

    // --------------------------------------------------------------- actions
    const seekTo = (t: number) => {
        const v = videoRef.current
        if (!v) return
        const total = duration || v.duration || 0
        t = Math.max(0, Math.min(total > 0 ? total - 0.5 : t, t))
        if (req.kind === "local" && method !== "direct") {
            // HLS can seek within what's converted so far.
            if (stream?.hls && v.seekable.length) {
                const rel = t - stream.base
                if (rel >= 0 && rel < v.seekable.end(v.seekable.length - 1) - 1) {
                    v.currentTime = rel
                    return
                }
            }
            // Otherwise the stream starts over from there.
            v.pause()
            startAt.current = 0
            setOffset(t)
            setRestart(n => n + 1)
            setTime(t)
        } else {
            v.currentTime = t
        }
    }
    const seekBy = (d: number) => {
        const v = videoRef.current
        if (v) seekTo(absTime(v) + d)
    }
    const togglePlay = () => {
        const v = videoRef.current
        if (!v) return
        if (v.paused) v.play().catch(() => {})
        else v.pause()
    }
    const toggleFullscreen = () => {
        if (document.fullscreenElement) document.exitFullscreen().catch(() => {})
        else containerRef.current?.requestFullscreen().catch(() => {})
    }
    const changeAudio = (i: number) => {
        const v = videoRef.current
        const at = v ? absTime(v) : 0
        setAudioIndex(i)
        if (method === "direct") setMethod("remux")
        startAt.current = 0
        setOffset(at)
        saveTrackPrefs({ audio: i })
    }
    const changeSub = (key: string) => {
        setSubKey(key)
        saveTrackPrefs({ sub: key })
    }
    const changeSource = (i: number) => {
        const v = videoRef.current
        startAt.current = v ? v.currentTime : 0
        setSourceIdx(i)
        applyStreamSubs(sources[i])
    }
    const close = () => {
        report(true)
        if (document.fullscreenElement) document.exitFullscreen().catch(() => {})
        onClose()
    }
    const playNext = () => {
        if (!nextEp) return
        report(true)
        if (req.kind === "local" && nextEp.path) playerStore.set({ kind: "local", path: nextEp.path, mediaId, episode: nextEp.episode })
        else if (req.kind === "stream") playerStore.set({ ...req, episode: nextEp.episode })
    }
    const openInMpv = async () => {
        const v = videoRef.current
        const at = v ? absTime(v) : 0
        try {
            if (req.kind === "local") await api.post("/api/playback/local", { path: req.path, mediaId, episode, player: "mpv", start: at })
            else {
                const src = sources[sourceIdx]
                await api.post("/api/playback/stream", {
                    mediaId,
                    episode,
                    url: src.originalUrl,
                    headers: src.headers,
                    referrer: src.referrer,
                    subtitles: src.subtitles.map(s => s.original),
                    title,
                    source,
                    start: at,
                })
            }
            v?.pause()
            onClose()
        } catch (e: any) {
            toast.error(e.message)
        }
    }

    // AniSkip's times are recorded on one version of the episode and land on
    // random scenes in a longer or shorter one: ask for the ones that fit
    // this one's length, once it's known.
    const length = Number.isFinite(duration) ? Math.round(duration) : 0
    useEffect(() => {
        setSkips([])
        if (length <= 0 || mediaId <= 0 || episode <= 0) return
        let cancelled = false
        api.get<Skip[]>(`/api/playback/skips${qs({ mediaId, episode, length })}`)
            .then(s => !cancelled && setSkips(s ?? []))
            .catch(() => {})
        return () => {
            cancelled = true
        }
    }, [mediaId, episode, length])
    const skippable = skips.filter(s => SKIPPED.includes(s.type))
    const currentSkip = skippable.find(s => time >= s.start && time < s.end - 1)
    const skipWhat = currentSkip && skipName(currentSkip.type)
    const autoSkip = skipWhat === "ending" ? settings?.playback.skipOutroAniSkip : skipWhat ? settings?.playback.skipIntroAniSkip : false
    useEffect(() => {
        if (!currentSkip || !autoSkip || skipped.current.has(currentSkip.type)) return
        skipped.current.add(currentSkip.type)
        seekTo(currentSkip.end)
        toast.info(`Skipped ${skipWhat}`)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [currentSkip?.type, autoSkip])

    // -------------------------------------------------------------- keyboard
    // Keys go to the player, not to what had the focus on the page behind it:
    // the episode card that opened it plays it again on Space or Enter.
    useEffect(() => {
        containerRef.current?.focus({ preventScroll: true })
    }, [])
    useEffect(() => {
        // Typing, or in one of the player's menus: the keys are theirs.
        const theirs = (t: EventTarget | null) => !!(t as HTMLElement | null)?.closest?.("input, textarea, select, [contenteditable], [role=menu], [role=listbox]")
        const onKey = (e: KeyboardEvent) => {
            if (e.ctrlKey || e.metaKey || e.altKey || theirs(e.target)) return
            const v = videoRef.current
            const box = containerRef.current
            // TV (the remote): on the picture, Left/Right seek, OK plays or
            // pauses and Up goes to the controls; on those, the arrows move
            // between them (lib/tv) and Down goes back to the picture.
            const t = e.target as HTMLElement
            const onPicture = !box || t === box || !box.contains(t) || !!t.closest("[data-tv-skip]")
            if (tv && box && e.key.startsWith("Arrow") && (!onPicture || e.key === "ArrowUp" || e.key === "ArrowDown")) {
                poke()
                if (!onPicture) {
                    if (!move(e.key === "ArrowUp" ? "up" : e.key === "ArrowDown" ? "down" : e.key === "ArrowLeft" ? "left" : "right", box) && e.key === "ArrowDown") box.focus()
                } else if (e.key === "ArrowUp") {
                    box.querySelector<HTMLElement>('[data-tv-controls="bottom"] button')?.focus()
                }
                e.preventDefault()
                e.stopPropagation()
                return
            }
            let handled = true
            switch (e.key) {
                case " ":
                case "k":
                    if (!e.repeat) togglePlay()
                    break
                case "Enter":
                    if (tv && onPicture && !t.closest("[data-tv-skip]")) togglePlay()
                    else handled = false
                    break
                case "ArrowLeft":
                    seekBy(tv ? -10 : -5)
                    break
                case "ArrowRight":
                    seekBy(tv ? 10 : 5)
                    break
                case "j":
                    seekBy(-10)
                    break
                case "l":
                    seekBy(10)
                    break
                case "ArrowUp":
                    setVolume(x => Math.min(1, x + 0.05))
                    break
                case "ArrowDown":
                    setVolume(x => Math.max(0, x - 0.05))
                    break
                case "f":
                    toggleFullscreen()
                    break
                case "m":
                    setMuted(m => !m)
                    break
                case "s":
                    if (currentSkip) seekTo(currentSkip.end)
                    break
                case "n":
                    playNext()
                    break
                case "Escape":
                    if (document.fullscreenElement) handled = false
                    else close()
                    break
                default:
                    if (/^[0-9]$/.test(e.key) && v) seekTo(((duration || v.duration) * Number(e.key)) / 10)
                    else handled = false
            }
            if (handled) {
                // The player hears keys first (capture phase): not the page
                // behind it too, nor a focused button (Space would click it).
                e.preventDefault()
                e.stopPropagation()
            }
            poke()
        }
        // Firefox clicks a focused button when Space comes back up anyway.
        const onKeyUp = (e: KeyboardEvent) => {
            if (e.key === " " && !e.ctrlKey && !e.metaKey && !e.altKey && !theirs(e.target)) e.preventDefault()
        }
        window.addEventListener("keydown", onKey, true)
        window.addEventListener("keyup", onKeyUp, true)
        return () => {
            window.removeEventListener("keydown", onKey, true)
            window.removeEventListener("keyup", onKeyUp, true)
        }
    })

    useEffect(() => {
        const v = videoRef.current
        if (!v) return
        v.volume = volume
        v.muted = muted
        try {
            localStorage.setItem("kumo-volume", String(volume))
        } catch {
            /* ignore */
        }
    }, [volume, muted])
    useEffect(() => {
        if (videoRef.current) videoRef.current.playbackRate = rate
    }, [rate])
    useEffect(() => {
        const onFs = () => setFullscreen(!!document.fullscreenElement)
        document.addEventListener("fullscreenchange", onFs)
        return () => document.removeEventListener("fullscreenchange", onFs)
    }, [])

    // TV: Skip opening/ending takes the remote's focus when it shows (OK
    // skips; Left/Right still seek), unless it's on the controls.
    const skipRef = useRef<HTMLButtonElement>(null)
    const showingSkip = !!currentSkip
    useEffect(() => {
        if (!tv || !showingSkip) return
        const box = containerRef.current
        const a = document.activeElement
        if (!box || a === box || !box.contains(a)) skipRef.current?.focus({ preventScroll: true })
        return () => {
            if (document.activeElement === skipRef.current || document.activeElement === document.body) box?.focus({ preventScroll: true })
        }
    }, [showingSkip])
    // The Android app keeps the screen on while a video plays.
    useEffect(() => {
        android?.keepAwake?.(!paused)
        return () => android?.keepAwake?.(false)
    }, [paused])

    // auto-hide controls
    const hideTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
    const poke = () => {
        setControls(true)
        if (hideTimer.current) clearTimeout(hideTimer.current)
        hideTimer.current = setTimeout(() => {
            if (videoRef.current?.paused) return
            setControls(false)
            // TV: the remote's focus doesn't stay on hidden buttons.
            if (tv && containerRef.current?.querySelector("[data-tv-controls]:focus-within")) containerRef.current.focus()
        }, tv ? 4000 : 2600)
    }
    useEffect(() => () => void (hideTimer.current && clearTimeout(hideTimer.current)), [])
    useEffect(() => () => report(true), [report])

    const total = duration || 0
    const pct = total > 0 ? (time / total) * 100 : 0
    const VolumeIcon = muted || volume === 0 ? VolumeX : volume < 0.5 ? Volume1 : Volume2

    return (
        <div
            ref={containerRef}
            tabIndex={-1}
            data-tv-scope
            className={cn("fixed inset-0 z-[80] bg-black outline-none fade-in", !controls && !paused && "cursor-none")}
            onMouseMove={poke}
            onClick={e => e.target === e.currentTarget && togglePlay()}
        >
            <video
                ref={videoRef}
                className="absolute inset-0 size-full"
                playsInline
                crossOrigin="anonymous"
                onClick={togglePlay}
                onDoubleClick={toggleFullscreen}
                onPlay={() => {
                    setPaused(false)
                    poke()
                }}
                onPause={() => {
                    setPaused(true)
                    setControls(true)
                    report(true)
                }}
                onWaiting={() => setBuffering(true)}
                onPlaying={() => setBuffering(false)}
                onCanPlay={() => setBuffering(false)}
                onTimeUpdate={e => {
                    const v = e.currentTarget
                    setTime(absTime(v))
                    if (v.buffered.length) setBuffered(v.buffered.end(v.buffered.length - 1) + base)
                    report()
                }}
                onLoadedMetadata={e => {
                    const v = e.currentTarget
                    if (req.kind !== "local" || method === "direct") setDuration(v.duration || duration)
                }}
                onEnded={() => {
                    if (ended.current) return
                    ended.current = true
                    report(true, true)
                    if (settings?.playback.autoPlayNext && nextEp) playNext()
                }}
                onError={() => {
                    if (req.kind === "local" && method === "direct") {
                        toast.info("Switching to compatibility mode…")
                        const v = videoRef.current
                        startAt.current = 0
                        setOffset(v ? v.currentTime : 0)
                        setMethod("remux")
                    } else if (req.kind === "local" && method === "remux") {
                        const v = videoRef.current
                        setOffset(v ? absTime(v) : offset)
                        setMethod("transcode")
                    } else if (req.kind === "stream" && !hlsRef.current) {
                        if (sourceIdx + 1 < sources.length) setSourceIdx(i => i + 1)
                        else setError("This video could not be played.")
                    }
                }}
            />

            {(loading || buffering) && !error && (
                <div className="pointer-events-none absolute inset-0 grid place-items-center">
                    <Loader2 className="size-12 animate-spin text-white/80" />
                </div>
            )}
            {error && (
                <div className="absolute inset-0 grid place-items-center p-6">
                    <div className="max-w-md rounded-xl border border-line-strong bg-surface-1 p-6 text-center">
                        <p className="text-lg font-semibold">Playback failed</p>
                        <p className="mt-2 text-sm text-muted">{error}</p>
                        <div className="mt-5 flex justify-center gap-2">
                            <button className="h-10 rounded-xl bg-white/10 px-4 text-sm font-medium hover:bg-white/15" onClick={close}>
                                Close
                            </button>
                            {(req.kind === "local" || sources.length > 0) && !androidApp && (
                                <button className="h-10 rounded-xl bg-brand px-4 text-sm font-medium text-white" onClick={openInMpv}>
                                    Open in mpv
                                </button>
                            )}
                        </div>
                    </div>
                </div>
            )}

            {/* top bar */}
            <div data-tv-controls="top" className={cn("absolute inset-x-0 top-0 flex items-center gap-4 bg-gradient-to-b from-black/80 to-transparent px-6 pt-5 pb-14 transition-opacity duration-300", controls || paused ? "opacity-100" : "pointer-events-none opacity-0")}>
                <button onClick={close} className="grid size-10 place-items-center rounded-full bg-white/10 text-white backdrop-blur hover:bg-white/20" aria-label="Close player">
                    <ArrowLeft className="size-5" />
                </button>
                <div className="min-w-0">
                    <p className="truncate text-lg font-semibold text-white">{title}</p>
                    {req.kind === "local" && probe && (
                        <p className="text-xs text-white/60">
                            {method === "direct" ? "Direct play" : method === "remux" ? "Direct stream (remuxed)" : "Transcoding"} · {probe.video?.[0]?.codec?.toUpperCase()}{" "}
                            {probe.video?.[0]?.height ? `${probe.video[0].height}p` : ""}
                        </p>
                    )}
                    {req.kind === "stream" && sources[sourceIdx] && (
                        <p className="text-xs text-white/60">
                            {sources[sourceIdx].server} · {sources[sourceIdx].quality || "auto"}
                        </p>
                    )}
                </div>
            </div>

            {/* bottom controls */}
            <div data-tv-controls="bottom" className={cn("absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/90 via-black/50 to-transparent px-6 pt-20 pb-5 transition-opacity duration-300", controls || paused ? "opacity-100" : "pointer-events-none opacity-0")}>
                {/* scrubber */}
                <div
                    className="group/seek relative mb-3 h-5 cursor-pointer"
                    onMouseMove={e => {
                        const r = e.currentTarget.getBoundingClientRect()
                        setHoverTime({ x: e.clientX - r.left, t: ((e.clientX - r.left) / r.width) * total })
                    }}
                    onMouseLeave={() => setHoverTime(null)}
                    onClick={e => {
                        const r = e.currentTarget.getBoundingClientRect()
                        seekTo(((e.clientX - r.left) / r.width) * total)
                    }}
                >
                    <div className="absolute inset-x-0 top-1/2 h-1 -translate-y-1/2 overflow-hidden rounded-full bg-white/20 transition-all group-hover/seek:h-1.5">
                        <div className="absolute inset-y-0 left-0 bg-white/30" style={{ width: `${total ? Math.min(100, (buffered / total) * 100) : 0}%` }} />
                        {skippable.map(s => (
                            <div key={s.type} className="absolute inset-y-0 bg-amber-300/50" style={{ left: `${(s.start / total) * 100}%`, width: `${((s.end - s.start) / total) * 100}%` }} />
                        ))}
                        <div className="absolute inset-y-0 left-0 bg-brand" style={{ width: `${pct}%` }} />
                    </div>
                    <div className="absolute top-1/2 size-3.5 -translate-x-1/2 -translate-y-1/2 scale-0 rounded-full bg-white shadow transition-transform group-hover/seek:scale-100" style={{ left: `${pct}%` }} />
                    {hoverTime && (
                        <div className="absolute -top-8 -translate-x-1/2 rounded-md bg-black/80 px-2 py-1 text-xs font-medium text-white" style={{ left: hoverTime.x }}>
                            {formatDuration(hoverTime.t)}
                        </div>
                    )}
                </div>

                <div className="flex items-center gap-2 text-white">
                    <CtrlButton onClick={togglePlay} label={paused ? "Play (k)" : "Pause (k)"}>
                        {paused ? <Play className="size-6 fill-white" /> : <Pause className="size-6 fill-white" />}
                    </CtrlButton>
                    <CtrlButton onClick={() => seekBy(-10)} label="Back 10s (j)">
                        <RotateCcw className="size-5" />
                    </CtrlButton>
                    <CtrlButton onClick={() => seekBy(10)} label="Forward 10s (l)">
                        <RotateCw className="size-5" />
                    </CtrlButton>
                    {nextEp && (
                        <CtrlButton onClick={playNext} label="Next episode (n)">
                            <SkipForward className="size-5" />
                        </CtrlButton>
                    )}
                    <div className="group/vol flex items-center">
                        <CtrlButton onClick={() => setMuted(m => !m)} label="Mute (m)">
                            <VolumeIcon className="size-5" />
                        </CtrlButton>
                        <input
                            type="range"
                            min={0}
                            max={1}
                            step={0.01}
                            value={muted ? 0 : volume}
                            onChange={e => {
                                setVolume(Number(e.target.value))
                                setMuted(false)
                            }}
                            className="w-0 accent-white opacity-0 transition-all duration-200 group-hover/vol:w-24 group-hover/vol:opacity-100"
                        />
                    </div>
                    <span className="ml-2 text-sm font-medium text-white/85 tabular-nums">
                        {formatDuration(time)} <span className="text-white/45">/ {formatDuration(total)}</span>
                    </span>

                    <div className="ml-auto flex items-center gap-1">
                        {/* subtitles */}
                        <Dropdown>
                            <DropdownTrigger asChild>
                                <button className={cn("grid size-10 place-items-center rounded-full hover:bg-white/15", subKey !== "off" && "text-brand-strong")} aria-label="Subtitles">
                                    <Captions className="size-5" />
                                </button>
                            </DropdownTrigger>
                            <DropdownContent className="z-[90] max-h-80 overflow-y-auto">
                                <DropdownLabel>Subtitles</DropdownLabel>
                                <DropdownItem onSelect={() => changeSub("off")}>{subKey === "off" ? "● " : ""}Off</DropdownItem>
                                {subs.map(s => (
                                    <DropdownItem key={s.key} onSelect={() => changeSub(s.key)} disabled={s.bitmap}>
                                        {subKey === s.key ? "● " : ""}
                                        {s.label}
                                        {s.bitmap ? " (mpv only)" : ""}
                                    </DropdownItem>
                                ))}
                            </DropdownContent>
                        </Dropdown>
                        {/* audio */}
                        {req.kind === "local" && (probe?.audio?.length ?? 0) > 1 && (
                            <Dropdown>
                                <DropdownTrigger asChild>
                                    <button className="grid size-10 place-items-center rounded-full hover:bg-white/15" aria-label="Audio track">
                                        <Languages className="size-5" />
                                    </button>
                                </DropdownTrigger>
                                <DropdownContent className="z-[90]">
                                    <DropdownLabel>Audio</DropdownLabel>
                                    {(probe?.audio ?? []).map((a, i) => (
                                        <DropdownItem key={i} onSelect={() => changeAudio(i)}>
                                            {audioIndex === i ? "● " : ""}
                                            {[langName(a.language), a.title, a.codec?.toUpperCase(), a.channels ? `${a.channels}ch` : ""].filter(Boolean).join(" · ") || `Track ${i + 1}`}
                                        </DropdownItem>
                                    ))}
                                </DropdownContent>
                            </Dropdown>
                        )}
                        {/* quality / speed */}
                        <Dropdown>
                            <DropdownTrigger asChild>
                                <button className="grid size-10 place-items-center rounded-full hover:bg-white/15" aria-label="Settings">
                                    <Gauge className="size-5" />
                                </button>
                            </DropdownTrigger>
                            <DropdownContent className="z-[90] max-h-96 overflow-y-auto">
                                {req.kind === "stream" && sources.length > 1 && (
                                    <>
                                        <DropdownLabel>Source</DropdownLabel>
                                        {sources.map((s, i) => (
                                            <DropdownItem key={i} onSelect={() => changeSource(i)}>
                                                {sourceIdx === i ? "● " : ""}
                                                {[s.server, s.quality, s.label].filter(Boolean).join(" · ")}
                                            </DropdownItem>
                                        ))}
                                        <DropdownSeparator />
                                    </>
                                )}
                                {req.kind === "local" && method !== "transcode" && (
                                    <>
                                        <DropdownItem
                                            onSelect={() => {
                                                const v = videoRef.current
                                                setOffset(v ? absTime(v) : 0)
                                                setMethod("transcode")
                                            }}
                                        >
                                            Force transcoding
                                        </DropdownItem>
                                        <DropdownSeparator />
                                    </>
                                )}
                                <DropdownLabel>Speed</DropdownLabel>
                                {[0.75, 1, 1.25, 1.5, 2].map(r => (
                                    <DropdownItem key={r} onSelect={() => setRate(r)}>
                                        {rate === r ? "● " : ""}
                                        {r}×
                                    </DropdownItem>
                                ))}
                            </DropdownContent>
                        </Dropdown>
                        {!androidApp && (
                            <CtrlButton onClick={openInMpv} label="Continue in mpv">
                                <MonitorPlay className="size-5" />
                            </CtrlButton>
                        )}
                        {!tv && (
                            <CtrlButton onClick={toggleFullscreen} label="Fullscreen (f)">
                                {fullscreen ? <Minimize className="size-5" /> : <Maximize className="size-5" />}
                            </CtrlButton>
                        )}
                    </div>
                </div>
            </div>

            {/* skip (the next episode is in the controls, and n). Above the
                controls: their gradient reaches up behind the button and
                would otherwise take its clicks while the controls show. */}
            <div className="absolute right-8 bottom-32 z-10 flex flex-col items-end gap-3">
                {currentSkip && (
                    <button ref={skipRef} data-tv-skip onClick={() => seekTo(currentSkip.end)} className="flex h-11 items-center gap-2 rounded-xl bg-white px-5 text-sm font-semibold text-black shadow-2xl rise-in hover:bg-white/90">
                        <FastForward className="size-4 fill-black" /> Skip {skipWhat}
                    </button>
                )}
            </div>
        </div>
    )
}

function CtrlButton({ children, onClick, label }: { children: React.ReactNode; onClick: () => void; label: string }) {
    return (
        <button onClick={onClick} title={label} aria-label={label} className="grid size-10 place-items-center rounded-full transition hover:bg-white/15 active:scale-95">
            {children}
        </button>
    )
}

function qualityRank(q: string) {
    const m = /(\d{3,4})/.exec(q ?? "")
    if (m) return Number(m[1])
    if (/auto|default|best/i.test(q ?? "")) return 5000
    return 0
}

// An English (dub) or Japanese (original) track, by language tag or title —
// dual-audio releases often only name their tracks ("English 5.1").
function isEnglishTrack(lang?: string, title?: string) {
    const l = (lang ?? "").trim().toLowerCase()
    const t = (title ?? "").toLowerCase()
    if (t.includes("commentary")) return false
    return l === "eng" || l === "en" || t.includes("english") || t.includes("dub") || /\beng\b/.test(t)
}

function isJapaneseTrack(lang?: string, title?: string) {
    const l = (lang ?? "").trim().toLowerCase()
    const t = (title ?? "").toLowerCase()
    return l === "jpn" || l === "ja" || l === "jp" || t.includes("japanese") || /\b(jpn|jap)\b/.test(t)
}

function pickSub(opts: SubOption[], prefs: TrackPrefs | null, preferred: string, defaultIndex?: number, fallbackKey?: string): string {
    if (prefs?.subOff) return "off"
    if (prefs && (prefs.subLang || prefs.subTitle || prefs.subIndex)) {
        const m =
            opts.find(o => normLang(o.lang) === normLang(prefs.subLang) && o.title === prefs.subTitle) ??
            opts.find(o => prefs.subTitle && o.title === prefs.subTitle) ??
            opts.find(o => prefs.subLang && normLang(o.lang) === normLang(prefs.subLang) && !/sign|song/i.test(o.title)) ??
            (prefs.subIndex ? opts.find(o => o.index === prefs.subIndex! - 1) : undefined)
        if (m && !m.bitmap) return m.key
    }
    if (preferred) {
        const full = opts.find(o => langIn(o.lang, preferred) && !/sign|song|forced/i.test(o.title) && !o.bitmap)
        if (full) return full.key
        const any = opts.find(o => langIn(o.lang, preferred) && !o.bitmap)
        if (any) return any.key
    }
    if (fallbackKey) return fallbackKey
    if (defaultIndex !== undefined) {
        const d = opts.find(o => o.index === defaultIndex && !o.bitmap)
        if (d) return d.key
    }
    const first = opts.find(o => !o.bitmap)
    return first ? first.key : "off"
}
