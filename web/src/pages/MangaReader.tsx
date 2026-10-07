import { useQuery, useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, BookOpen, ChevronLeft, ChevronRight, Loader2, Maximize, Minimize, RectangleVertical, Rows3 } from "lucide-react"
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger, Tooltip } from "@/components/ui"
import { api, qs } from "@/lib/api"
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

// Pairs pages like a printed book: the first one (the cover) alone, then
// 2–3, 4–5… or, shifted by one, 1–2, 3–4… A page wider than it is tall is a
// double spread already and stays alone; pairing goes on after it.
function pairUp(n: number, wide: (i: number) => boolean, cover: boolean) {
    const out: number[][] = []
    for (let i = 0; i < n; i += out[out.length - 1].length) {
        out.push((i === 0 && cover) || i === n - 1 || wide(i) || wide(i + 1) ? [i] : [i, i + 1])
    }
    return out
}

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
    const rtl = status?.settings.manga.direction === "rtl"
    const narrow = useSyncExternalStore(onPortraitChange, () => portrait.matches)
    const fullscreen = useSyncExternalStore(onFullscreenChange, () => !!document.fullscreenElement)
    const double = mode === "double" && !narrow
    // The chapter whose pairs are shifted by one.
    const [shifted, setShifted] = useState("")
    // Pages found to be wider than tall once loaded, by image URL.
    const [wide, setWide] = useState<Record<string, boolean>>({})
    const navigate = useNavigate()
    const marked = useRef<string>("")

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

    // The pages on screen together: one, or two side by side.
    const spreads = double ? pairUp(total, i => !!wide[srcs[i]], shifted !== chapterId) : srcs.map((_, i) => [i])
    const at = Math.max(0, spreads.findIndex(s => s.includes(page)))
    const spread = spreads[at] ?? []
    const last = spread[spread.length - 1] ?? -1
    // Pages near the ones on screen stay mounted, hidden: the next ones load
    // before they are turned to (telling which are double spreads before they
    // get paired) and turning back is instant. Page images aren't cacheable
    // (the proxy sends no-store): only the element that loaded one shows it
    // without downloading it again.
    const from = Math.max(0, (spread[0] ?? 0) - 2)
    const near = srcs.slice(from, last + 5)

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

    const pickMode = (m: Mode) => {
        setPicked(m)
        // Only the reading mode: the server keeps the other settings.
        api.put<Settings>("/api/settings", { manga: { readingMode: m } })
            .then(s => qc.setQueryData<Status>(["status"], old => (old ? { ...old, settings: s } : old)))
            .catch(() => {})
    }

    const noteSize = (src: string, img: HTMLImageElement) => {
        if (img.naturalWidth > img.naturalHeight) setWide(w => (w[src] ? w : { ...w, [src]: true }))
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
    const current = mode === "long-strip" ? (stripPage ?? page) : (spread[0] ?? 0)
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
            if (mode === "long-strip") return
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
        if (mode !== "long-strip" && last >= 0 && last === total - 1) markRead()
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [last, mode, pages])

    const ready = !!pages && !positionPending
    const bar = "grid size-9 place-items-center rounded-full text-white/85 transition-colors hover:bg-white/15 hover:text-white disabled:opacity-30 [&_svg]:size-[18px]"

    return (
        <div className={cn("fixed inset-0 z-[70] bg-black text-white", !shown && mode !== "long-strip" && "cursor-none")} onMouseMove={poke}>
            <div
                id="reader"
                className={cn("absolute inset-0", mode === "long-strip" ? "overflow-y-auto" : "overflow-hidden")}
                onScroll={e => {
                    if (mode !== "long-strip") return
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
                {ready && mode === "long-strip" && (
                    <div className="mx-auto flex max-w-3xl flex-col">
                        {pages.map((p, i) => (
                            <img
                                key={p.index}
                                data-page={i}
                                src={proxied(p)}
                                alt=""
                                loading="lazy"
                                className="w-full"
                                onLoad={e => {
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
                {ready && mode !== "long-strip" && spread.length > 0 && (
                    // The pages fill the window, whole: each of a pair gets
                    // half of it, against the other one. Right to left, the
                    // first page of a pair is on the right. The sides turn
                    // pages; the middle shows or hides the buttons.
                    <div
                        className={cn("flex h-full items-center justify-center select-none", rtl && "flex-row-reverse")}
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
                        {near.map((src, k) => {
                            const slot = spread.indexOf(from + k)
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
                </div>
                {mode !== "long-strip" && spread.length > 0 && (
                    <span className="px-2 pt-2 text-[13px] text-white/60 tabular-nums">
                        {spread.map(i => i + 1).join("–")} / {total}
                    </span>
                )}
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
                        <ChevronLeft />
                    </button>
                </Tooltip>
                <Tooltip content="Next chapter" side="bottom">
                    <button disabled={idx < 0 || idx >= list.length - 1} onClick={() => go(idx + 1)} className={bar} aria-label="Next chapter">
                        <ChevronRight />
                    </button>
                </Tooltip>
            </div>

            {mode !== "long-strip" && total > 0 && (
                <div className={cn("absolute inset-x-0 bottom-0 h-0.5 bg-white/10 transition-opacity duration-300", shown ? "opacity-100" : "opacity-0")}>
                    <div className="h-full bg-white/60 transition-[width] duration-300" style={{ width: `${((last + 1) / total) * 100}%` }} />
                </div>
            )}
        </div>
    )
}
