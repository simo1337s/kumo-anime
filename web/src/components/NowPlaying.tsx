import { Loader2, Pause, Play, RotateCcw, RotateCw, Square } from "lucide-react"
import { Link } from "react-router-dom"
import { api } from "@/lib/api"
import { playbackStore, scanStore, useStore } from "@/lib/store"
import { formatDuration } from "@/lib/utils"
import { Progress } from "./ui"

// Floating pill controlling the external mpv window.
export function NowPlaying() {
    const s = useStore(playbackStore)
    if (!s || !s.active || s.player !== "mpv") return null
    const cmd = (c: string, value = 0) => api.post("/api/playback/command", { cmd: c, value }).catch(() => {})
    const pct = s.duration > 0 ? s.position / s.duration : 0
    return (
        <div className="glass fixed bottom-5 left-1/2 z-50 w-[min(92vw,560px)] -translate-x-1/2 rounded-2xl p-3 shadow-2xl rise-in">
            <div className="flex items-center gap-3">
                <div className="min-w-0 flex-1">
                    <p className="text-[11px] font-semibold tracking-wider text-brand-strong uppercase">Playing in mpv</p>
                    <Link to={`/entry?id=${s.mediaId}`} className="block truncate text-sm font-semibold hover:underline">
                        {s.title}
                    </Link>
                </div>
                <div className="flex items-center gap-1">
                    <button className="grid size-9 place-items-center rounded-full hover:bg-white/10" onClick={() => cmd("seek", -10)} title="Back 10s">
                        <RotateCcw className="size-4" />
                    </button>
                    <button className="grid size-10 place-items-center rounded-full bg-white text-black hover:bg-white/90" onClick={() => cmd("toggle")} title="Play/Pause">
                        {s.paused ? <Play className="ml-0.5 size-4 fill-black" /> : <Pause className="size-4 fill-black" />}
                    </button>
                    <button className="grid size-9 place-items-center rounded-full hover:bg-white/10" onClick={() => cmd("seek", 85)} title="Skip 85s">
                        <RotateCw className="size-4" />
                    </button>
                    <button className="grid size-9 place-items-center rounded-full hover:bg-white/10" onClick={() => cmd("stop")} title="Stop">
                        <Square className="size-3.5 fill-current" />
                    </button>
                </div>
            </div>
            <div className="mt-2.5 flex items-center gap-3 text-[11px] text-muted tabular-nums">
                <span>{formatDuration(s.position)}</span>
                <Progress value={pct} className="h-1" />
                <span>{formatDuration(s.duration)}</span>
            </div>
        </div>
    )
}

export function ScanIndicator() {
    const s = useStore(scanStore)
    if (!s.running) return null
    return (
        <div className="glass fixed right-5 bottom-5 z-50 w-80 rounded-2xl p-4 shadow-2xl rise-in">
            <div className="flex items-center gap-3">
                <Loader2 className="size-5 animate-spin text-brand" />
                <div className="min-w-0 flex-1">
                    <p className="text-sm font-semibold">Scanning library</p>
                    <p className="truncate text-xs text-muted">{s.message}</p>
                </div>
            </div>
            {s.total > 0 && <Progress value={s.done / s.total} className="mt-3" />}
        </div>
    )
}
