import { useQuery } from "@tanstack/react-query"
import { ArrowLeft, BookOpen, ChevronLeft, ChevronRight, Loader2, RectangleVertical, Rows3 } from "lucide-react"
import { useEffect, useRef, useState, useSyncExternalStore } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import { Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger } from "@/components/ui"
import { api, qs } from "@/lib/api"
import { useMangaChapters, useStatus } from "@/lib/queries"
import type { Settings } from "@/lib/types"
import { cn } from "@/lib/utils"

type Page = { url: string; index: number; headers: Record<string, string> }
type Mode = Settings["manga"]["readingMode"]

const MODES: [Mode, string][] = [
    ["long-strip", "Long strip"],
    ["paged", "Single page"],
    ["double", "Two pages"],
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

export default function MangaReaderPage() {
    const [params, setParams] = useSearchParams()
    const id = Number(params.get("id"))
    const provider = params.get("provider") ?? ""
    const chapterId = params.get("chapter") ?? ""
    const { data: status } = useStatus()
    // The mode picked in the reader, else the default from Settings.
    const [picked, setPicked] = useState<Mode>()
    const saved = status?.settings.manga.readingMode
    const mode = picked ?? (saved === "paged" || saved === "double" ? saved : "long-strip")
    const rtl = status?.settings.manga.direction === "rtl"
    const narrow = useSyncExternalStore(onPortraitChange, () => portrait.matches)
    const double = mode === "double" && !narrow
    // The page turned to; another chapter starts at its first page.
    const [pos, setPos] = useState({ chapter: chapterId, page: 0 })
    const page = pos.chapter === chapterId ? pos.page : 0
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

    // The pages on screen together: one, or two side by side.
    const srcs = pages?.map(proxied) ?? []
    const spreads = double ? pairUp(srcs.length, i => !!wide[srcs[i]], shifted !== chapterId) : srcs.map((_, i) => [i])
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

    const go = (i: number) => {
        const c = list[i]
        if (!c) return
        setPos({ chapter: c.id, page: 0 })
        setParams({ id: String(id), provider, chapter: c.id })
        document.getElementById("reader")?.scrollTo({ top: 0 })
    }

    const turn = (forward: boolean) => {
        if (forward) {
            if (at < spreads.length - 1) setPos({ chapter: chapterId, page: spreads[at + 1][0] })
            else if (spreads.length && idx < list.length - 1) go(idx + 1)
        } else if (at > 0) setPos({ chapter: chapterId, page: spreads[at - 1][0] })
    }

    const markRead = () => {
        if (!chapter || marked.current === chapter.id) return
        marked.current = chapter.id
        api.post(`/api/manga/${id}/progress`, { chapter: chapter.chapter })
            .then(() => toast.success(`Chapter ${chapter.chapter} marked as read`))
            .catch(() => {})
    }

    const noteSize = (src: string, img: HTMLImageElement) => {
        if (img.naturalWidth > img.naturalHeight) setWide(w => (w[src] ? w : { ...w, [src]: true }))
    }

    // Bound again on every render: turning depends on the current spreads.
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (mode === "long-strip" || e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return
            if (e.target instanceof Element && e.target.closest("input, textarea, select, [role=menu]")) return
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
        if (mode !== "long-strip" && last >= 0 && last === srcs.length - 1) markRead()
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [last, mode, pages])

    return (
        <div className="fixed inset-0 z-[70] flex flex-col bg-black">
            <div className="flex h-14 shrink-0 items-center gap-3 border-b border-white/10 bg-black/80 px-4 backdrop-blur">
                <button onClick={() => navigate(`/manga/entry?id=${id}`)} className="grid size-9 place-items-center rounded-full hover:bg-white/10">
                    <ArrowLeft className="size-5" />
                </button>
                <p className="min-w-0 flex-1 truncate text-sm font-semibold">{chapter ? chapter.title || `Chapter ${chapter.chapter}` : "Loading…"}</p>
                {mode !== "long-strip" && spread.length > 0 && (
                    <span className="text-sm text-white/60 tabular-nums">
                        {spread.map(i => i + 1).join("–")} / {srcs.length}
                    </span>
                )}
                <Dropdown>
                    <DropdownTrigger asChild>
                        <button className="grid size-9 place-items-center rounded-full hover:bg-white/10 data-[state=open]:bg-white/10" title="Reading mode">
                            {mode === "double" ? <BookOpen className="size-4" /> : mode === "paged" ? <RectangleVertical className="size-4" /> : <Rows3 className="size-4" />}
                        </button>
                    </DropdownTrigger>
                    {/* Focus doesn't go back to the button: Space would open the menu again instead of turning the page. */}
                    <DropdownContent className="z-[80]" onCloseAutoFocus={e => e.preventDefault()}>
                        <DropdownLabel>Reading mode</DropdownLabel>
                        {MODES.map(([m, label]) => (
                            <DropdownItem key={m} onSelect={() => setPicked(m)}>
                                {mode === m ? "● " : ""}
                                {label}
                            </DropdownItem>
                        ))}
                        {mode === "double" && <DropdownSeparator />}
                        {mode === "double" &&
                            (narrow ? (
                                <p className="max-w-52 px-2.5 py-1.5 text-xs text-subtle">One page at a time while the window is taller than it is wide</p>
                            ) : (
                                <DropdownItem onSelect={() => setShifted(s => (s === chapterId ? "" : chapterId))}>
                                    {shifted === chapterId ? "● " : ""}
                                    Shift pairs by one page
                                </DropdownItem>
                            ))}
                    </DropdownContent>
                </Dropdown>
                <button disabled={idx <= 0} onClick={() => go(idx - 1)} className="grid size-9 place-items-center rounded-full hover:bg-white/10 disabled:opacity-30" title="Previous chapter">
                    <ChevronLeft className="size-5" />
                </button>
                <button disabled={idx < 0 || idx >= list.length - 1} onClick={() => go(idx + 1)} className="grid size-9 place-items-center rounded-full hover:bg-white/10 disabled:opacity-30" title="Next chapter">
                    <ChevronRight className="size-5" />
                </button>
            </div>
            <div
                id="reader"
                className="flex-1 overflow-y-auto"
                onScroll={e => {
                    const el = e.currentTarget
                    if (mode === "long-strip" && el.scrollTop + el.clientHeight >= el.scrollHeight - 400) markRead()
                }}
            >
                {isLoading && (
                    <div className="grid h-full place-items-center">
                        <Loader2 className="size-8 animate-spin text-white/70" />
                    </div>
                )}
                {error && <p className="p-10 text-center text-rose-300">{(error as Error).message}</p>}
                {pages && mode === "long-strip" && (
                    <div className="mx-auto flex max-w-3xl flex-col">
                        {pages.map(p => (
                            <img key={p.index} src={proxied(p)} alt="" loading="lazy" className="w-full" />
                        ))}
                        {idx < list.length - 1 && (
                            <button onClick={() => go(idx + 1)} className="my-10 self-center rounded-xl bg-white px-6 py-3 font-semibold text-black">
                                Next chapter
                            </button>
                        )}
                    </div>
                )}
                {mode !== "long-strip" && spread.length > 0 && (
                    // Right to left, the first page of a pair is on the right.
                    <div
                        className={cn("flex h-full items-center justify-center select-none", rtl && "flex-row-reverse")}
                        onClick={e => {
                            const r = e.currentTarget.getBoundingClientRect()
                            const left = e.clientX - r.left < r.width / 2
                            turn(rtl ? left : !left)
                        }}
                    >
                        {near.map((src, k) => {
                            const slot = spread.indexOf(from + k)
                            return (
                                <img
                                    key={src}
                                    src={src}
                                    alt=""
                                    onLoad={e => noteSize(src, e.currentTarget)}
                                    className={cn(
                                        "object-contain",
                                        slot < 0 ? "hidden" : !double ? "max-h-full max-w-full" : spread.length === 1 ? "size-full" : "h-full w-1/2",
                                        // Each page of a pair gets half the width and sits against the other one.
                                        spread.length === 2 && slot >= 0 && ((slot === 0) !== rtl ? "object-right" : "object-left"),
                                    )}
                                />
                            )
                        })}
                    </div>
                )}
            </div>
        </div>
    )
}
