import * as DialogPrimitive from "@radix-ui/react-dialog"
import { ArrowRight, Search } from "lucide-react"
import { useEffect, useState } from "react"
import { useNavigate } from "react-router-dom"
import { useSearch } from "@/lib/queries"
import { searchOpenStore, useStore } from "@/lib/store"
import { cn, cover, entryUrl, formatLabel, title } from "@/lib/utils"
import { Spinner } from "./ui"

function useDebounced<T>(v: T, ms = 300) {
    const [d, setD] = useState(v)
    useEffect(() => {
        const t = setTimeout(() => setD(v), ms)
        return () => clearTimeout(t)
    }, [v, ms])
    return d
}

export function QuickSearch() {
    const open = useStore(searchOpenStore)
    const [q, setQ] = useState("")
    const [type, setType] = useState<"ANIME" | "MANGA">("ANIME")
    const [sel, setSel] = useState(0)
    const dq = useDebounced(q)
    const navigate = useNavigate()
    const { data, isFetching } = useSearch({ search: dq, type, perPage: 8 }, open && dq.trim().length > 1)
    const results = dq.trim().length > 1 ? data?.media ?? [] : []

    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
                e.preventDefault()
                searchOpenStore.set(!searchOpenStore.get())
            }
        }
        window.addEventListener("keydown", onKey)
        return () => window.removeEventListener("keydown", onKey)
    }, [])
    useEffect(() => setSel(0), [dq, type])

    const go = (to: string) => {
        searchOpenStore.set(false)
        setQ("")
        navigate(to)
    }

    return (
        <DialogPrimitive.Root open={open} onOpenChange={v => searchOpenStore.set(v)}>
            <DialogPrimitive.Portal>
                <DialogPrimitive.Overlay className="fixed inset-0 z-[70] bg-black/60 backdrop-blur-sm fade-in" />
                <DialogPrimitive.Content
                    aria-describedby={undefined}
                    className="fixed top-[12vh] left-1/2 z-[70] w-[min(94vw,640px)] -translate-x-1/2 overflow-hidden rounded-2xl border border-line-strong bg-surface-1 shadow-2xl rise-in outline-none"
                >
                    <DialogPrimitive.Title className="sr-only">Search</DialogPrimitive.Title>
                    <div className="flex items-center gap-3 border-b border-line px-5">
                        <Search className="size-5 text-subtle" />
                        <input
                            autoFocus
                            value={q}
                            onChange={e => setQ(e.target.value)}
                            onKeyDown={e => {
                                if (e.key === "ArrowDown") {
                                    e.preventDefault()
                                    setSel(s => Math.min(results.length - 1, s + 1))
                                } else if (e.key === "ArrowUp") {
                                    e.preventDefault()
                                    setSel(s => Math.max(0, s - 1))
                                } else if (e.key === "Enter") {
                                    if (results[sel]) go(entryUrl(results[sel]))
                                    else if (q.trim()) go(`/search?q=${encodeURIComponent(q)}`)
                                }
                            }}
                            placeholder={`Search ${type === "ANIME" ? "anime" : "manga"}…`}
                            className="h-16 flex-1 bg-transparent text-lg outline-none placeholder:text-subtle"
                        />
                        {isFetching && <Spinner className="size-4" />}
                        <div className="flex rounded-lg border border-line p-0.5 text-xs">
                            {(["ANIME", "MANGA"] as const).map(t => (
                                <button key={t} onClick={() => setType(t)} className={cn("rounded-md px-2.5 py-1 font-semibold", type === t ? "bg-white/10 text-fg" : "text-muted")}>
                                    {t === "ANIME" ? "Anime" : "Manga"}
                                </button>
                            ))}
                        </div>
                    </div>
                    <div className="max-h-[60vh] overflow-y-auto p-2">
                        {results.map((m, i) => (
                            <button
                                key={m.id}
                                onMouseEnter={() => setSel(i)}
                                onClick={() => go(entryUrl(m))}
                                className={cn("flex w-full items-center gap-4 rounded-xl p-2 text-left transition", sel === i ? "bg-white/[0.07]" : "")}
                            >
                                <img src={cover(m)} alt="" className="h-16 w-11 shrink-0 rounded-lg object-cover" />
                                <div className="min-w-0 flex-1">
                                    <p className="truncate font-semibold">{title(m)}</p>
                                    <p className="truncate text-sm text-muted">
                                        {[formatLabel(m.format), m.seasonYear, m.episodes ? `${m.episodes} eps` : m.chapters ? `${m.chapters} ch` : ""].filter(Boolean).join(" · ")}
                                    </p>
                                </div>
                                {sel === i && <ArrowRight className="size-4 text-muted" />}
                            </button>
                        ))}
                        {dq.trim().length > 1 && !isFetching && results.length === 0 && <p className="p-6 text-center text-sm text-muted">No results</p>}
                        {dq.trim().length <= 1 && (
                            <p className="p-6 text-center text-sm text-subtle">
                                Type to search AniList · <kbd className="rounded bg-white/10 px-1.5 py-0.5 text-xs">Ctrl K</kbd> to toggle
                            </p>
                        )}
                    </div>
                    {q.trim() && (
                        <button onClick={() => go(`/search?q=${encodeURIComponent(q)}&type=${type}`)} className="flex w-full items-center justify-between border-t border-line px-5 py-3 text-sm text-muted hover:bg-white/[0.04] hover:text-fg">
                            Advanced search for “{q}” <ArrowRight className="size-4" />
                        </button>
                    )}
                </DialogPrimitive.Content>
            </DialogPrimitive.Portal>
        </DialogPrimitive.Root>
    )
}
