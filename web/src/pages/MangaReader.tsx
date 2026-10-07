import { useQuery, useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, BookOpen, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Loader2, Maximize, Minimize, RectangleVertical, Rows3, ZoomIn, ZoomOut } from "lucide-react"
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger, Tooltip } from "@/components/ui"
import { api, qs } from "@/lib/api"
import { usePersisted } from "@/lib/hooks"
import { useMangaChapters, useStatus } from "@/lib/queries"
import { toast } from "@/lib/toast"
import type { MangaPosition, Settings, Status } from "@/lib/types"
import { cn } from "@/lib/utils"

type Page = { url: string; index: number; headers: Record<string, string> }
type Mode = Settings["manga"]["readingMode"]

const MODES: [Mode, string][] = [
    ["double", "Two pages"],
    ["paged", "Single page"],
    ["long-strip", "Long strip"],
]

function proxied(p: Page) {
    const u = btoa(unescape(encodeURIComponent(p.url))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
    const h = Object.keys(p.headers ?? {}).length ? btoa(JSON.stringify(p.headers)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "") : ""
    return `/api/proxy?u=${u}${h ? `&h=${h}` : ""}`
}

// Pairs pages (their indexes) like a printed book: the first one (the cover)
// alone, then 2–3, 4–5… or, shifted by one, 1–2, 3–4… A page wider than it is
// tall is a double spread already and stays alone; pairing goes on after it.
function pairUp(ids: number[], wide: (i: number) => boolean, cover: boolean) {
    const out: number[][] = []
    for (let k = 0; k < ids.length; k += out[out.length - 1].length) {
        const i = ids[k]
        const j = ids[k + 1]
        out.push((k === 0 && cover) || j === undefined || wide(i) || wide(j) ? [i] : [i, j])
    }
    return out
}

// A strip rather than a page: a slice of one (some sites cut pages up), an
// ad or credits banner, or a webtoon's long image. Width over height.
const isStrip = (ratio: number) => ratio > 2.5 || ratio < 0.4

// Two pages side by side get too small on a screen taller than it is wide
// (phones): they are shown one at a time until it is turned or resized.
const portrait = window.matchMedia("(orientation: portrait)")
const onPortraitChange = (cb: () => void) => {
    portrait.addEventListener("change", cb)
    return () => portrait.removeEventListener("change", cb)
}

const onFullscreenChange = (cb: () => void) => {
    document.addEventListener("fullscreenchange", cb)
    return () => document.removeEventListener("fullscreenchange", cb)
}
const toggleFullscreen = () => (document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen()).catch(() => {})

// The last page of a chapter, before its pages are known: turning back from
// the first page of a chapter opens the previous one there.
const END = Number.MAX_SAFE_INTEGER

export default function MangaReaderPage() {
    const [params, setParams] = useSearchParams()
    const id = Number(params.get("id"))
    const provider = params.get("provider") ?? ""
    const chapterId = params.get("chapter") ?? ""
    const asked = /^\d+$/.test(params.get("page") ?? "") ? Number(params.get("page")) : undefined
    const { data: status } = useStatus()
    const qc = useQueryClient()
    // A mode picked here shows at once and becomes the default (Settings).
    const [picked, setPicked] = useState<Mode>()
    const saved = status?.settings.manga.readingMode
    const mode = picked ?? (saved === "paged" || saved === "long-strip" ? saved : "double")
    // Right to left (manga as printed) unless Settings say otherwise; a
    // direction picked here shows at once and becomes the default too.
    const [pickedDir, setPickedDir] = useState<"ltr" | "rtl">()
    const rtl = (pickedDir ?? status?.settings.manga.direction ?? "rtl") !== "ltr"
    const narrow = useSyncExternalStore(onPortraitChange, () => portrait.matches)
    const fullscreen = useSyncExternalStore(onFullscreenChange, () => !!document.fullscreenElement)
    const double = mode === "double" && !narrow
    // The chapter whose pairs are shifted by one.
    const [shifted, setShifted] = useState("")
    // The pages' width over height once loaded, by image URL.
    const [ratios, setRatios] = useState<Record<string, number>>({})
    // Chapters found to come in strips.
    const [stripChapters, setStripChapters] = useState<string[]>([])
    const navigate = useNavigate()
    const marked = useRef<string>("")
    // How big pages show: 1 fills the space below the buttons, less leaves
    // more room around them. Remembered by this browser.
    const [zoom, setZoom] = usePersisted("kumo-manga-zoom", 1)
    const zoomBy = (d: number) => setZoom(z => Math.round(Math.min(1, Math.max(0.5, z + d)) * 100) / 100)

    const { data: chapters } = useMangaChapters(id, provider)
    const list = chapters?.chapters ?? []
    const idx = list.findIndex(c => c.id === chapterId)
    const chapter = list[idx]
    const { data: pages, isLoading, error } = useQuery({
        queryKey: ["manga", "pages", provider, chapterId],
        queryFn: () => api.get<Page[]>(`/api/manga/pages${qs({ provider, chapterId })}`),
        enabled: !!chapterId,
    })
    // Where reading stopped last time, to start there again.
    const positionKey = ["manga", "position", id]
    const { data: position, isPending: positionPending } = useQuery({
        queryKey: positionKey,
        queryFn: () => api.get<MangaPosition | null>(`/api/manga/${id}/position`),
        enabled: id > 0,
    })

    // The page turned to in this chapter; before any turn, the one asked for
    // (?page=), else the one reading stopped at, else the first.
    const [pos, setPos] = useState<{ chapter: string; page: number } | null>(null)
    const resumeAt = position?.provider === provider && position.chapterId === chapterId ? position.page : 0
    const srcs = pages?.map(proxied) ?? []
    const total = srcs.length
    const wanted = pos?.chapter === chapterId ? pos.page : (asked ?? resumeAt)
    const page = total ? Math.min(wanted, total - 1) : 0

    // A chapter that comes in strips reads as one long strip, where they join
    // up again, whatever the mode. That's told from its first 8 pages, in
    // order, once all of them have loaded (most must be strips): small
    // banner strips load before whole pages, so the first images to arrive
    // would make a chapter of pages look like one of strips.
    const sample = srcs.slice(0, Math.min(8, total))
    const sampled = sample.length > 0 && sample.every(src => ratios[src])
    const inStrips = stripChapters.includes(chapterId) || (sampled && sample.filter(src => isStrip(ratios[src])).length * 2 > sample.length)
    useEffect(() => {
        if (inStrips && !stripChapters.includes(chapterId)) setStripChapters(l => [...l, chapterId])
    }, [inStrips, chapterId, stripChapters])
    const view: Mode = inStrips ? "long-strip" : mode

    // The pages on screen together: one, or two side by side. Strips in a
    // chapter of pages (ad and credits banners) are left out.
    const ids = srcs.map((_, i) => i).filter(i => !isStrip(ratios[srcs[i]] ?? 1))
    const spreads = double ? pairUp(ids, i => (ratios[srcs[i]] ?? 0) > 1, shifted !== chapterId) : ids.map(i => [i])
    // The spread with the page, or the next one when it was left out.
    const found = spreads.findIndex(s => s[s.length - 1] >= page)
    const at = found < 0 ? Math.max(0, spreads.length - 1) : found
    const spread = spreads[at] ?? []
    const last = spread[spread.length - 1] ?? -1
    // Pages near the ones on screen stay mounted, hidden: the next ones load
    // before they are turned to (telling which are double spreads before they
    // get paired) and turning back is instant. Page images aren't cacheable
    // (the proxy sends no-store): only the element that loaded one shows it
    // without downloading it again. The first pages are mounted too: they
    // tell whether the chapter comes in strips.
    const from = Math.max(0, (spread[0] ?? 0) - 2)
    const mounted = srcs.map((_, i) => i).filter(i => i < sample.length || (i >= from && i < last + 5))

    // The top page on screen in the long strip, once scrolled (until then,
    // the page it opened at).
    const [stripPage, setStripPage] = useState<number | null>(null)
    const restored = useRef("")

    const go = (i: number, at = 0) => {
        const c = list[i]
        if (!c) return
        setPos({ chapter: c.id, page: at })
        setStripPage(null)
        setParams({ id: String(id), provider, chapter: c.id })
        document.getElementById("reader")?.scrollTo({ top: 0 })
    }

    const turn = (forward: boolean) => {
        if (forward) {
            if (at < spreads.length - 1) setPos({ chapter: chapterId, page: spreads[at + 1][0] })
            else if (spreads.length && idx < list.length - 1) go(idx + 1)
        } else if (at > 0) setPos({ chapter: chapterId, page: spreads[at - 1][0] })
        else if (idx > 0) go(idx - 1, END)
    }

    // Opening a chapter puts the manga on the Reading list (the server leaves
    // it where it is when it's there already, or rereading, or completed).
    const started = useRef(0)
    useEffect(() => {
        if (!(id > 0) || started.current === id) return
        started.current = id
        api.post<{ added: boolean }>(`/api/manga/${id}/reading`)
            .then(r => r?.added && toast.success("Added to Reading"))
            .catch(() => {})
    }, [id])

    const canBack = at > 0 || idx > 0
    const canForward = at < spreads.length - 1 || idx < list.length - 1

    const markRead = () => {
        if (!chapter || marked.current === chapter.id) return
        marked.current = chapter.id
        api.post(`/api/manga/${id}/progress`, { chapter: chapter.chapter })
            .then(() => {
                toast.success(`Chapter ${chapter.chapter} marked as read`)
                qc.invalidateQueries({ queryKey: ["manga", "media", id] })
                qc.invalidateQueries({ queryKey: ["list"] })
            })
            .catch(() => {})
    }

    // Saves one manga setting: the server keeps the others.
    const saveManga = (manga: Partial<Settings["manga"]>) =>
        api.put<Settings>("/api/settings", { manga })
            .then(s => qc.setQueryData<Status>(["status"], old => (old ? { ...old, settings: s } : old)))
            .catch(() => {})
    const pickMode = (m: Mode) => {
        setPicked(m)
        saveManga({ readingMode: m })
    }
    const pickDirection = (d: "ltr" | "rtl") => {
        setPickedDir(d)
        saveManga({ direction: d })
    }

    const noteSize = (src: string, img: HTMLImageElement) => {
        if (img.naturalHeight > 0) setRatios(r => (r[src] ? r : { ...r, [src]: img.naturalWidth / img.naturalHeight }))
    }

    // The buttons show while the mouse moves and fade out while reading.
    const [controls, setControls] = useState(true)
    const [menuOpen, setMenuOpen] = useState(false)
    const overBar = useRef(false)
    const hideTimer = useRef(0)
    const poke = useCallback(() => {
        setControls(true)
        window.clearTimeout(hideTimer.current)
        hideTimer.current = window.setTimeout(() => !overBar.current && setControls(false), 2500)
    }, [])
    useEffect(() => {
        poke()
        return () => window.clearTimeout(hideTimer.current)
    }, [poke])
    const shown = controls || menuOpen || !pages

    // Saves where reading is, shortly after it changes, and right away when
    // the reader closes.
    const current = view === "long-strip" ? (stripPage ?? page) : (spread[0] ?? 0)
    const pending = useRef<(() => void) | null>(null)
    const lastSaved = useRef("")
    useEffect(() => {
        if (positionPending || !chapter || !total) return
        const key = `${provider}:${chapterId}:${current}`
        if (key === lastSaved.current) return
        const body = { provider, chapterId, chapter: chapter.chapter, page: current, pages: total }
        const save = () => {
            pending.current = null
            lastSaved.current = key
            qc.setQueryData<MangaPosition>(positionKey, { ...body, updatedAt: Math.floor(Date.now() / 1000) })
            api.put(`/api/manga/${id}/position`, body).catch(() => {})
        }
        pending.current = save
        const t = window.setTimeout(save, 600)
        return () => window.clearTimeout(t)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [positionPending, chapter, total, provider, chapterId, current, id])
    useEffect(() => () => pending.current?.(), [])

    // Bound again on every render: turning depends on the current spreads.
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return
            if (e.target instanceof Element && e.target.closest("input, textarea, select, [role=menu]")) return
            if (e.key === "f" || e.key === "F") {
                toggleFullscreen()
                return
            }
            if (e.key === "-" || e.key === "+" || e.key === "=" || e.key === "0") {
                if (e.key === "0") setZoom(1)
                else zoomBy(e.key === "-" ? -0.05 : 0.05)
                return
            }
            if (view === "long-strip") return
            const forward = rtl ? "ArrowLeft" : "ArrowRight"
            const back = rtl ? "ArrowRight" : "ArrowLeft"
            if (e.key === forward || e.key === "PageDown" || (e.key === " " && !e.shiftKey)) turn(true)
            else if (e.key === back || e.key === "PageUp" || e.key === " ") turn(false)
            else return
            // Space would also press the focused button (next chapter…).
            e.preventDefault()
        }
        window.addEventListener("keydown", onKey)
        return () => window.removeEventListener("keydown", onKey)
    })

    useEffect(() => {
        if (view !== "long-strip" && spreads.length > 0 && at === spreads.length - 1) markRead()
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [at, spreads.length, view, pages])

    const ready = !!pages && !positionPending
    const bar = "grid size-9 place-items-center rounded-full text-white/85 transition-colors hover:bg-white/15 hover:text-white disabled:opacity-30 [&_svg]:size-[18px]"

    return (
        <div className={cn("fixed inset-0 z-[70] bg-black text-white", !shown && view !== "long-strip" && "cursor-none")} onMouseMove={poke}>
            <div
                id="reader"
                className={cn("absolute inset-0", view === "long-strip" ? "overflow-y-auto" : "overflow-hidden")}
                onScroll={e => {
                    if (view !== "long-strip") return
                    const el = e.currentTarget
                    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 400) markRead()
                    const top = el.getBoundingClientRect().top + 80
                    for (const img of el.querySelectorAll<HTMLImageElement>("img[data-page]")) {
                        if (img.getBoundingClientRect().bottom > top) {
                            setStripPage(Number(img.dataset.page))
                            break
                        }
                    }
                }}
            >
                {(isLoading || (pages && positionPending)) && (
                    <div className="grid h-full place-items-center">
                        <Loader2 className="size-8 animate-spin text-white/70" />
                    </div>
                )}
                {error && <p className="p-10 pt-24 text-center text-rose-300">{(error as Error).message}</p>}
                {ready && view === "long-strip" && (
                    <div className="mx-auto flex flex-col pt-14" style={{ maxWidth: `${48 * zoom}rem` }}>
                        {pages.map((p, i) => (
                            <img
                                key={p.index}
                                data-page={i}
                                src={proxied(p)}
                                alt=""
                                loading="lazy"
                                className="w-full"
                                onLoad={e => {
                                    noteSize(srcs[i], e.currentTarget)
                                    // Back where reading stopped, once the page is there.
                                    if (i !== page || page === 0 || restored.current === chapterId) return
                                    restored.current = chapterId
                                    e.currentTarget.scrollIntoView({ block: "start" })
                                }}
                            />
                        ))}
                        {idx < list.length - 1 && (
                            <button onClick={() => go(idx + 1)} className="my-10 self-center rounded-lg bg-white px-6 py-3 font-medium text-neutral-950">
                                Next chapter
                            </button>
                        )}
                    </div>
                )}
                {ready && view !== "long-strip" && spread.length > 0 && (
                    // The pages fill the window, whole: each of a pair gets
                    // half of it, against the other one. Right to left, the
                    // first page of a pair is on the right. The sides turn
                    // pages; the middle shows or hides the buttons.
                    <div
                        // Room for the buttons above the pages, and some below.
                        className="flex h-full items-center justify-center px-4 pt-14 pb-6 select-none"
                        onClick={e => {
                            const r = e.currentTarget.getBoundingClientRect()
                            const x = (e.clientX - r.left) / r.width
                            if (x > 0.4 && x < 0.6) {
                                if (shown) setControls(false)
                                else poke()
                                return
                            }
                            turn(rtl ? x < 0.5 : x >= 0.5)
                        }}
                    >
                        <div className={cn("flex items-center justify-center", rtl && "flex-row-reverse")} style={{ width: `${zoom * 100}%`, height: `${zoom * 100}%` }}>
                            {mounted.map(i => {
                                const src = srcs[i]
                                const slot = spread.indexOf(i)
                                return (
                                    <img
                                        key={src}
                                        src={src}
                                        alt=""
                                        draggable={false}
                                        onLoad={e => noteSize(src, e.currentTarget)}
                                        className={cn(
                                            "object-contain",
                                            slot < 0 ? "hidden" : spread.length === 2 ? "h-full w-1/2" : "size-full",
                                            spread.length === 2 && slot >= 0 && ((slot === 0) !== rtl ? "object-right" : "object-left"),
                                        )}
                                    />
                                )
                            })}
                        </div>
                    </div>
                )}
            </div>

            <div
                className={cn(
                    "absolute inset-x-0 top-0 z-10 flex h-16 items-start gap-1 bg-gradient-to-b from-black/85 to-transparent px-3 pt-2.5 transition-opacity duration-300",
                    shown ? "opacity-100" : "pointer-events-none opacity-0",
                )}
                onMouseEnter={() => {
                    overBar.current = true
                    setControls(true)
                }}
                onMouseLeave={() => {
                    overBar.current = false
                    poke()
                }}
            >
                <Tooltip content="Back" side="bottom">
                    <button onClick={() => navigate(`/manga/entry?id=${id}`)} className={bar} aria-label="Back">
                        <ArrowLeft />
                    </button>
                </Tooltip>
                <div className="min-w-0 flex-1 px-2 pt-1.5">
                    <p className="truncate text-sm font-medium">{chapter ? chapter.title || `Chapter ${chapter.chapter}` : "Loading…"}</p>
                    {view !== mode && <p className="truncate text-xs text-white/50">This chapter comes in strips: they're joined into one long strip</p>}
                </div>
                {view !== "long-strip" && spread.length > 0 && (
                    <span className="px-2 pt-2 text-[13px] text-white/60 tabular-nums">
                        {spread.map(i => i + 1).join("–")} / {total}
                    </span>
                )}
                <div className="flex items-center">
                    <Tooltip content="Zoom out (−)" side="bottom">
                        <button disabled={zoom <= 0.5} onClick={() => zoomBy(-0.05)} className={bar} aria-label="Zoom out">
                            <ZoomOut />
                        </button>
                    </Tooltip>
                    <button onClick={() => setZoom(1)} title="Fit (0)" className="min-w-11 pt-0.5 text-center text-[13px] text-white/60 tabular-nums hover:text-white">
                        {Math.round(zoom * 100)}%
                    </button>
                    <Tooltip content="Zoom in (+)" side="bottom">
                        <button disabled={zoom >= 1} onClick={() => zoomBy(0.05)} className={bar} aria-label="Zoom in">
                            <ZoomIn />
                        </button>
                    </Tooltip>
                </div>
                <Dropdown onOpenChange={setMenuOpen}>
                    <Tooltip content="Reading mode" side="bottom">
                        <DropdownTrigger asChild>
                            <button className={cn(bar, "data-[state=open]:bg-white/15")} aria-label="Reading mode">
                                {mode === "double" ? <BookOpen /> : mode === "paged" ? <RectangleVertical /> : <Rows3 />}
                            </button>
                        </DropdownTrigger>
                    </Tooltip>
                    {/* Focus doesn't go back to the button: Space would open the menu again instead of turning the page. */}
                    <DropdownContent className="z-[80]" onCloseAutoFocus={e => e.preventDefault()}>
                        <DropdownLabel>Reading mode</DropdownLabel>
                        {MODES.map(([m, label]) => (
                            <DropdownItem key={m} onSelect={() => pickMode(m)}>
                                {mode === m ? "● " : ""}
                                {label}
                            </DropdownItem>
                        ))}
                        {view !== "long-strip" && (
                            <>
                                <DropdownSeparator />
                                <DropdownLabel>Reading direction</DropdownLabel>
                                {(
                                    [
                                        ["rtl", "Right to left (manga)"],
                                        ["ltr", "Left to right"],
                                    ] as const
                                ).map(([d, label]) => (
                                    <DropdownItem key={d} onSelect={() => pickDirection(d)}>
                                        {rtl === (d === "rtl") ? "● " : ""}
                                        {label}
                                    </DropdownItem>
                                ))}
                            </>
                        )}
                        {mode === "double" && <DropdownSeparator />}
                        {mode === "double" &&
                            (narrow ? (
                                <p className="max-w-52 px-2 py-1.5 text-xs text-subtle">One page at a time while the window is taller than it is wide</p>
                            ) : (
                                <DropdownItem onSelect={() => setShifted(s => (s === chapterId ? "" : chapterId))}>
                                    {shifted === chapterId ? "● " : ""}
                                    Shift pairs by one page
                                </DropdownItem>
                            ))}
                    </DropdownContent>
                </Dropdown>
                <Tooltip content={fullscreen ? "Exit full screen (F)" : "Full screen (F)"} side="bottom">
                    <button onClick={toggleFullscreen} className={bar} aria-label={fullscreen ? "Exit full screen" : "Full screen"}>
                        {fullscreen ? <Minimize /> : <Maximize />}
                    </button>
                </Tooltip>
                <Tooltip content="Previous chapter" side="bottom">
                    <button disabled={idx <= 0} onClick={() => go(idx - 1)} className={bar} aria-label="Previous chapter">
                        <ChevronsLeft />
                    </button>
                </Tooltip>
                <Tooltip content="Next chapter" side="bottom">
                    <button disabled={idx < 0 || idx >= list.length - 1} onClick={() => go(idx + 1)} className={bar} aria-label="Next chapter">
                        <ChevronsRight />
                    </button>
                </Tooltip>
            </div>

            {/* Page arrows on both sides; right to left, the left one goes forward. */}
            {ready && view !== "long-strip" &&
                ([
                    ["left-3", rtl, <ChevronLeft key="l" />],
                    ["right-3", !rtl, <ChevronRight key="r" />],
                ] as const).map(([side, forward, icon]) => (
                    <button
                        key={side}
                        onClick={() => turn(forward)}
                        disabled={forward ? !canForward : !canBack}
                        aria-label={forward ? "Next page" : "Previous page"}
                        className={cn(
                            "absolute top-1/2 z-10 grid size-12 -translate-y-1/2 place-items-center rounded-full bg-black/55 text-white transition-[opacity,background-color] duration-300 hover:bg-black/80 disabled:invisible [&_svg]:size-6",
                            side,
                            shown ? "opacity-100" : "pointer-events-none opacity-0",
                        )}
                    >
                        {icon}
                    </button>
                ))}

            {view !== "long-strip" && total > 0 && (
                <div className={cn("absolute inset-x-0 bottom-0 h-0.5 bg-white/10 transition-opacity duration-300", shown ? "opacity-100" : "opacity-0")}>
                    {/* Right to left, it fills from the right. */}
                    <div className="h-full bg-white/60 transition-[width] duration-300" style={{ width: `${((last + 1) / total) * 100}%`, marginLeft: rtl ? "auto" : undefined }} />
                </div>
            )}
        </div>
    )
}
