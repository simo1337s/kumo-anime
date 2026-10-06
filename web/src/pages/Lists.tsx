import { ListChecks, Search } from "lucide-react"
import { useMemo, useState } from "react"
import { MediaCard, MediaCardSkeleton, MediaGrid } from "@/components/MediaCard"
import { EmptyState, Input, Select, Tabs } from "@/components/ui"
import { useRawList } from "@/lib/queries"
import { LIST_STATUS, LIST_STATUS_MANGA, title } from "@/lib/utils"

export default function ListsPage() {
    const [type, setType] = useState<"ANIME" | "MANGA">("ANIME")
    const { data, isLoading } = useRawList(type)
    const [status, setStatus] = useState("CURRENT")
    const [q, setQ] = useState("")
    const [sort, setSort] = useState("updated")
    const labels = type === "MANGA" ? LIST_STATUS_MANGA : LIST_STATUS

    const byStatus = useMemo(() => {
        const out: Record<string, any[]> = {}
        for (const l of data?.lists ?? []) {
            if (l.isCustomList) continue
            out[l.status] = [...(out[l.status] ?? []), ...l.entries]
        }
        return out
    }, [data])

    const entries = useMemo(() => {
        let list = (byStatus[status] ?? []).slice()
        if (q.trim()) list = list.filter(e => title(e.media).toLowerCase().includes(q.toLowerCase()) || (e.media.title.english ?? "").toLowerCase().includes(q.toLowerCase()))
        list.sort((a, b) => {
            switch (sort) {
                case "title":
                    return title(a.media).localeCompare(title(b.media))
                case "score":
                    return (b.score || 0) - (a.score || 0)
                case "progress":
                    return (b.progress || 0) - (a.progress || 0)
                default:
                    return (b.updatedAt || 0) - (a.updatedAt || 0)
            }
        })
        return list
    }, [byStatus, status, q, sort])

    return (
        <div className="min-h-full px-6 pt-10 pb-24 md:px-10 xl:px-14">
            <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
                <div>
                    <h1 className="text-4xl font-extrabold tracking-tight">My lists</h1>
                    <p className="mt-1 text-muted">Everything on your {type === "ANIME" ? "anime" : "manga"} list.</p>
                </div>
                <Tabs value={type} onChange={setType} tabs={[{ value: "ANIME", label: "Anime" }, { value: "MANGA", label: "Manga" }]} />
            </div>
            <div className="mb-8 flex flex-wrap items-center gap-3">
                <Tabs value={status} onChange={setStatus} tabs={Object.entries(labels).map(([k, v]) => ({ value: k, label: v, count: byStatus[k]?.length ?? 0 }))} />
                <div className="ml-auto flex gap-3">
                    <Input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter…" icon={<Search className="size-4" />} className="w-56" />
                    <Select
                        className="w-44"
                        value={sort}
                        onChange={setSort}
                        options={[
                            { value: "updated", label: "Last updated" },
                            { value: "title", label: "Title" },
                            { value: "score", label: "Score" },
                            { value: "progress", label: "Progress" },
                        ]}
                    />
                </div>
            </div>
            {isLoading ? (
                <MediaGrid>
                    {Array.from({ length: 12 }).map((_, i) => (
                        <MediaCardSkeleton key={i} />
                    ))}
                </MediaGrid>
            ) : entries.length === 0 ? (
                <EmptyState icon={<ListChecks className="size-6" />} title="Nothing here yet">
                    Add {type === "ANIME" ? "anime" : "manga"} to your list from their page.
                </EmptyState>
            ) : (
                <MediaGrid>
                    {entries.map(e => (
                        <MediaCard key={e.mediaId} media={e.media} listEntry={e} />
                    ))}
                </MediaGrid>
            )}
        </div>
    )
}
