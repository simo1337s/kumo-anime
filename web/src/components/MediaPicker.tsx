import { Search } from "lucide-react"
import { useEffect, useState } from "react"
import { useCollection, useSearch } from "@/lib/queries"
import type { Media } from "@/lib/types"
import { cn, cover, formatLabel, title } from "@/lib/utils"
import { Dialog, Input, Spinner } from "./ui"

// Search AniList (and the user's list) to pick an anime.
export function MediaPicker({
    open,
    onOpenChange,
    onPick,
    initialQuery = "",
    heading = "Choose an anime",
    description,
}: {
    open: boolean
    onOpenChange: (v: boolean) => void
    onPick: (m: Media) => void
    initialQuery?: string
    heading?: string
    description?: React.ReactNode
}) {
    const [q, setQ] = useState(initialQuery)
    const [dq, setDq] = useState(initialQuery)
    useEffect(() => {
        if (open) {
            setQ(initialQuery)
            setDq(initialQuery)
        }
    }, [open, initialQuery])
    useEffect(() => {
        const t = setTimeout(() => setDq(q), 300)
        return () => clearTimeout(t)
    }, [q])
    const { data, isFetching } = useSearch({ search: dq, type: "ANIME", perPage: 20 }, open && dq.trim().length > 1)
    const { data: coll } = useCollection()
    const fromList = (coll?.lists ?? [])
        .flatMap(l => l.items)
        .filter(i => dq.trim().length > 1 && [title(i.media), i.media.title.english ?? "", i.media.title.romaji ?? ""].some(t => t.toLowerCase().includes(dq.toLowerCase())))
        .map(i => i.media)
        .slice(0, 6)
    const results = [...fromList, ...(data?.media ?? []).filter(m => !fromList.some(f => f.id === m.id))]

    return (
        <Dialog open={open} onOpenChange={onOpenChange} title={heading} description={description} className="w-[min(94vw,680px)]">
            <Input autoFocus value={q} onChange={e => setQ(e.target.value)} placeholder="Search AniList…" icon={<Search className="size-4" />} />
            <div className="mt-4 flex min-h-40 flex-col gap-1">
                {isFetching && results.length === 0 && (
                    <div className="grid h-40 place-items-center">
                        <Spinner />
                    </div>
                )}
                {results.map((m, i) => (
                    <button
                        key={m.id}
                        onClick={() => {
                            onPick(m)
                            onOpenChange(false)
                        }}
                        className="flex items-center gap-4 rounded-xl p-2 text-left transition hover:bg-white/[0.06]"
                    >
                        <img src={cover(m)} alt="" className="h-16 w-11 rounded-lg object-cover" />
                        <div className="min-w-0 flex-1">
                            <p className="truncate font-semibold">{title(m)}</p>
                            <p className="truncate text-sm text-muted">
                                {[formatLabel(m.format), m.seasonYear, m.episodes ? `${m.episodes} eps` : ""].filter(Boolean).join(" · ")}
                                {m.title.english && m.title.english !== title(m) ? ` · ${m.title.english}` : ""}
                            </p>
                        </div>
                        {i < fromList.length && <span className={cn("rounded-md bg-brand-soft px-2 py-0.5 text-[11px] font-semibold text-brand-strong")}>In your list</span>}
                    </button>
                ))}
                {!isFetching && dq.trim().length > 1 && results.length === 0 && <p className="py-10 text-center text-sm text-muted">No results</p>}
            </div>
        </Dialog>
    )
}
