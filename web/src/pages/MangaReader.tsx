import { useQuery } from "@tanstack/react-query"
import { ArrowLeft, ChevronLeft, ChevronRight, Columns2, Loader2, Rows3 } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import { api, qs } from "@/lib/api"
import { useMangaChapters, useStatus } from "@/lib/queries"
import { cn } from "@/lib/utils"

type Page = { url: string; index: number; headers: Record<string, string> }

function proxied(p: Page) {
    const u = btoa(unescape(encodeURIComponent(p.url))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
    const h = Object.keys(p.headers ?? {}).length ? btoa(JSON.stringify(p.headers)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "") : ""
    return `/api/proxy?u=${u}${h ? `&h=${h}` : ""}`
}

export default function MangaReaderPage() {
    const [params, setParams] = useSearchParams()
    const id = Number(params.get("id"))
    const provider = params.get("provider") ?? ""
    const chapterId = params.get("chapter") ?? ""
    const { data: status } = useStatus()
    const [mode, setMode] = useState<"long-strip" | "paged">(() => (status?.settings.manga.readingMode as "paged") || "long-strip")
    const rtl = status?.settings.manga.direction === "rtl"
    const [page, setPage] = useState(0)
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

    const go = (i: number) => {
        const c = list[i]
        if (!c) return
        setPage(0)
        setParams({ id: String(id), provider, chapter: c.id })
        document.getElementById("reader")?.scrollTo({ top: 0 })
    }

    const markRead = () => {
        if (!chapter || marked.current === chapter.id) return
        marked.current = chapter.id
        api.post(`/api/manga/${id}/progress`, { chapter: chapter.chapter })
            .then(() => toast.success(`Chapter ${chapter.chapter} marked as read`))
            .catch(() => {})
    }

    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (mode !== "paged") return
            const forward = rtl ? "ArrowLeft" : "ArrowRight"
            const back = rtl ? "ArrowRight" : "ArrowLeft"
            if (e.key === forward) setPage(p => Math.min((pages?.length ?? 1) - 1, p + 1))
            if (e.key === back) setPage(p => Math.max(0, p - 1))
        }
        window.addEventListener("keydown", onKey)
        return () => window.removeEventListener("keydown", onKey)
    }, [mode, rtl, pages])

    useEffect(() => {
        if (mode === "paged" && pages && page === pages.length - 1) markRead()
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [page, mode, pages])

    return (
        <div className="fixed inset-0 z-[70] flex flex-col bg-black">
            <div className="flex h-14 shrink-0 items-center gap-3 border-b border-white/10 bg-black/80 px-4 backdrop-blur">
                <button onClick={() => navigate(`/manga/entry?id=${id}`)} className="grid size-9 place-items-center rounded-full hover:bg-white/10">
                    <ArrowLeft className="size-5" />
                </button>
                <p className="min-w-0 flex-1 truncate text-sm font-semibold">{chapter ? chapter.title || `Chapter ${chapter.chapter}` : "Loading…"}</p>
                {mode === "paged" && pages && (
                    <span className="text-sm text-white/60 tabular-nums">
                        {page + 1} / {pages.length}
                    </span>
                )}
                <button onClick={() => setMode(m => (m === "paged" ? "long-strip" : "paged"))} className="grid size-9 place-items-center rounded-full hover:bg-white/10" title="Reading mode">
                    {mode === "paged" ? <Rows3 className="size-4" /> : <Columns2 className="size-4" />}
                </button>
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
                {pages && mode === "paged" && pages[page] && (
                    <div
                        className={cn("flex h-full items-center justify-center")}
                        onClick={e => {
                            const r = e.currentTarget.getBoundingClientRect()
                            const left = e.clientX - r.left < r.width / 2
                            const forward = rtl ? left : !left
                            if (forward) {
                                if (page < pages.length - 1) setPage(page + 1)
                                else if (idx < list.length - 1) go(idx + 1)
                            } else if (page > 0) setPage(page - 1)
                        }}
                    >
                        <img src={proxied(pages[page])} alt="" className="max-h-full max-w-full object-contain" />
                    </div>
                )}
            </div>
        </div>
    )
}
