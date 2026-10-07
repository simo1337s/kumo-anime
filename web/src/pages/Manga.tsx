import { useQuery } from "@tanstack/react-query"
import { BookOpen, Puzzle } from "lucide-react"
import { useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { MediaCard, MediaCardSkeleton, MediaGrid } from "@/components/MediaCard"
import { Button, EmptyState, ErrorState, Tabs } from "@/components/ui"
import { api } from "@/lib/api"
import type { ListEntry } from "@/lib/types"
import { LIST_STATUS_MANGA } from "@/lib/utils"

export default function MangaPage() {
    const { data, isLoading, error, refetch, isFetching } = useQuery({
        queryKey: ["manga", "collection"],
        queryFn: () => api.get<{ lists: { status: string; isCustomList: boolean; entries: ListEntry[] }[] | null }>("/api/manga/collection"),
    })
    const { data: providers } = useQuery({ queryKey: ["manga-providers"], queryFn: () => api.get<{ id: string; name: string }[]>("/api/manga/providers") })
    const [status, setStatus] = useState("CURRENT")
    const byStatus = useMemo(() => {
        const out: Record<string, ListEntry[]> = {}
        for (const l of data?.lists ?? []) if (!l.isCustomList) out[l.status] = [...(out[l.status] ?? []), ...l.entries]
        return out
    }, [data])
    const entries = byStatus[status] ?? []

    return (
        <div className="min-h-full px-6 pt-8 pb-24 md:px-8 xl:px-10">
            <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
                <div>
                    <h1 className="text-[1.75rem] font-semibold tracking-tight">Manga</h1>
                    <p className="mt-1 text-muted">Read with manga provider extensions. Progress syncs to your list.</p>
                </div>
                <Link to="/search?type=MANGA">
                    <Button>Find manga</Button>
                </Link>
            </div>
            {providers && providers.length === 0 && (
                <div className="mb-8 flex items-center justify-between gap-4 rounded-xl bg-white/[0.03] p-5">
                    <div className="flex items-center gap-3">
                        <Puzzle className="size-5 text-muted" />
                        <p className="text-sm">Install a manga provider extension to start reading.</p>
                    </div>
                    <Link to="/extensions?tab=marketplace">
                        <Button size="sm" variant="primary">
                            Open marketplace
                        </Button>
                    </Link>
                </div>
            )}
            <Tabs className="mb-8" value={status} onChange={setStatus} tabs={Object.entries(LIST_STATUS_MANGA).map(([k, v]) => ({ value: k, label: v, count: byStatus[k]?.length ?? 0 }))} />
            {isLoading ? (
                <MediaGrid>
                    {Array.from({ length: 12 }).map((_, i) => (
                        <MediaCardSkeleton key={i} />
                    ))}
                </MediaGrid>
            ) : error && !data ? (
                <ErrorState title="Couldn't load your manga list" error={error} retrying={isFetching} onRetry={() => refetch()} />
            ) : entries.length === 0 ? (
                <EmptyState icon={<BookOpen className="size-6" />} title="Nothing here">
                    Add manga to your list from search.
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
