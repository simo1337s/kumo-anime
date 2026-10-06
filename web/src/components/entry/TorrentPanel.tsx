import { useQuery } from "@tanstack/react-query"
import { ArrowDownToLine, Copy, ExternalLink, Magnet, Search, ShieldCheck, Users } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { useStatus, useTorrentSearch } from "@/lib/queries"
import type { EntryView, TorrentResult } from "@/lib/types"
import { cn, relativeTime } from "@/lib/utils"
import { Badge, Button, EmptyState, Input, Select, Switch } from "../ui"

export function TorrentPanel({ entry }: { entry: EntryView }) {
    const { data: status } = useStatus()
    const settings = status?.settings
    const media = entry.media
    const { data: providers } = useQuery({ queryKey: ["torrent-providers"], queryFn: () => api.get<{ id: string; name: string; extension: boolean }[]>("/api/torrents/providers") })
    const [provider, setProvider] = useState(settings?.torrent.defaultProvider || "nyaa")
    const [episode, setEpisode] = useState<number>(entry.nextEpisode?.number ?? 1)
    const [batch, setBatch] = useState(media.status === "FINISHED" && (entry.listEntry?.progress ?? 0) === 0 && media.format !== "MOVIE")
    const [resolution, setResolution] = useState(settings?.torrent.preferredResolution || "1080")
    const [query, setQuery] = useState("")
    const [selected, setSelected] = useState<Set<string>>(new Set())
    const [sending, setSending] = useState(false)
    const search = useTorrentSearch()

    const run = () => {
        setSelected(new Set())
        search.mutate({ provider, mediaId: media.id, query: query.trim() || undefined, episode: batch ? 0 : episode, batch, resolution: resolution === "any" ? "" : resolution })
    }
    useEffect(() => {
        run()
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [])

    const results = search.data ?? []
    const providerName = (id: string) => providers?.find(p => p.id === id)?.name ?? id
    const key = (r: TorrentResult) => r.infoHash || r.link || r.name
    const chosen = useMemo(() => results.filter(r => selected.has(key(r))), [results, selected])

    const download = async (items: TorrentResult[]) => {
        setSending(true)
        try {
            await api.post("/api/torrents/download", { provider, mediaId: media.id, results: items })
            setSelected(new Set())
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setSending(false)
        }
    }

    return (
        <div className="flex flex-col gap-5">
            <div className="card flex flex-wrap items-end gap-3 p-4">
                <label className="flex w-48 flex-col gap-1.5">
                    <span className="text-xs font-medium text-subtle">Provider</span>
                    <Select
                        value={provider}
                        onChange={setProvider}
                        options={[
                            { value: "all", label: `All providers${providers ? ` (${providers.length})` : ""}` },
                            ...(providers ?? []).map(p => ({ value: p.id, label: p.name + (p.extension ? " (extension)" : "") })),
                        ]}
                    />
                </label>
                <label className="flex w-28 flex-col gap-1.5">
                    <span className="text-xs font-medium text-subtle">Episode</span>
                    <Input type="number" min={1} value={episode} disabled={batch} onChange={e => setEpisode(Number(e.target.value))} />
                </label>
                <label className="flex w-32 flex-col gap-1.5">
                    <span className="text-xs font-medium text-subtle">Resolution</span>
                    <Select
                        value={resolution}
                        onChange={setResolution}
                        options={[
                            { value: "any", label: "Any" },
                            { value: "2160", label: "2160p" },
                            { value: "1080", label: "1080p" },
                            { value: "720", label: "720p" },
                            { value: "480", label: "480p" },
                        ]}
                    />
                </label>
                <label className="flex h-10 items-center gap-2.5 px-1">
                    <Switch checked={batch} onChange={setBatch} />
                    <span className="text-sm">Batch</span>
                </label>
                <div className="min-w-56 flex-1">
                    <span className="mb-1.5 block text-xs font-medium text-subtle">Custom query (optional)</span>
                    <Input value={query} onChange={e => setQuery(e.target.value)} onKeyDown={e => e.key === "Enter" && run()} placeholder="Leave empty for smart search" icon={<Search className="size-4" />} />
                </div>
                <Button variant="primary" loading={search.isPending} onClick={run} icon={<Search className="size-4" />}>
                    Search
                </Button>
            </div>

            {chosen.length > 0 && (
                <div className="glass sticky top-3 z-20 flex items-center justify-between rounded-2xl px-4 py-3 rise-in">
                    <span className="text-sm">{chosen.length} selected</span>
                    <Button variant="primary" size="sm" loading={sending} icon={<ArrowDownToLine className="size-4" />} onClick={() => download(chosen)}>
                        Send to {settings?.torrent.defaultClient === "transmission" ? "Transmission" : "qBittorrent"}
                    </Button>
                </div>
            )}

            {search.isPending && <div className="card h-40 shimmer" />}
            {!search.isPending && results.length === 0 && search.isSuccess && (
                <EmptyState icon={<Magnet className="size-6" />} title="No torrents found">
                    Try another provider, a different resolution, or a custom query.
                </EmptyState>
            )}

            {results.length > 0 && (
                <div className="card divide-y divide-line overflow-hidden">
                    {results.map(r => {
                        const k = key(r)
                        const isSel = selected.has(k)
                        return (
                            <div key={k} className={cn("flex items-center gap-4 px-4 py-3 transition hover:bg-white/[0.03]", isSel && "bg-brand-soft")}>
                                <input
                                    type="checkbox"
                                    checked={isSel}
                                    onChange={() =>
                                        setSelected(s => {
                                            const n = new Set(s)
                                            n.has(k) ? n.delete(k) : n.add(k)
                                            return n
                                        })
                                    }
                                    className="size-4 shrink-0 accent-[var(--brand)]"
                                />
                                <div className="min-w-0 flex-1">
                                    <p className="truncate text-sm font-medium" title={r.name}>
                                        {r.name}
                                    </p>
                                    <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
                                        {provider === "all" && r.provider && <Badge>{providerName(r.provider)}</Badge>}
                                        {r.releaseGroup && <Badge tone="brand">{r.releaseGroup}</Badge>}
                                        {r.resolution && <Badge>{r.resolution}</Badge>}
                                        {r.isBatch && <Badge tone="blue">Batch</Badge>}
                                        {r.episodeNumber > 0 && !r.isBatch && <Badge>Ep {r.episodeNumber}</Badge>}
                                        {r.dub && <Badge tone="amber">Dual audio</Badge>}
                                        {r.trusted && (
                                            <Badge tone="green">
                                                <ShieldCheck className="size-3" /> Trusted
                                            </Badge>
                                        )}
                                        {r.confirmed && <Badge tone="green">Confirmed</Badge>}
                                        {r.remake && <Badge tone="red">Remake</Badge>}
                                        <span className="text-xs text-subtle">
                                            {r.formattedSize} {r.date && `· ${relativeTime(new Date(r.date).getTime() / 1000)}`}
                                        </span>
                                    </div>
                                </div>
                                <div className="flex w-24 shrink-0 items-center justify-end gap-1 text-sm">
                                    <Users className="size-3.5 text-subtle" />
                                    <span className={cn("font-semibold", r.seeders > 20 ? "text-emerald-300" : r.seeders > 0 ? "text-amber-300" : "text-rose-300")}>{r.seeders}</span>
                                    <span className="text-subtle">/ {r.leechers}</span>
                                </div>
                                <div className="flex shrink-0 gap-1">
                                    {r.link && (
                                        <a href={r.link} target="_blank" rel="noopener noreferrer" className="grid size-9 place-items-center rounded-lg text-muted hover:bg-white/[0.06] hover:text-fg" title="Open page">
                                            <ExternalLink className="size-4" />
                                        </a>
                                    )}
                                    {r.magnetLink && (
                                        <button
                                            className="grid size-9 place-items-center rounded-lg text-muted hover:bg-white/[0.06] hover:text-fg"
                                            title="Copy magnet"
                                            onClick={() => {
                                                navigator.clipboard.writeText(r.magnetLink)
                                                toast.success("Magnet link copied")
                                            }}
                                        >
                                            <Copy className="size-4" />
                                        </button>
                                    )}
                                    <Button size="sm" variant="white" onClick={() => download([r])} loading={sending && chosen.length === 0}>
                                        Download
                                    </Button>
                                </div>
                            </div>
                        )
                    })}
                </div>
            )}
        </div>
    )
}
