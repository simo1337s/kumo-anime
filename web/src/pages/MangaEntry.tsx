import { useQuery, useQueryClient } from "@tanstack/react-query"
import { BookOpen, RefreshCw, Search, Wand2 } from "lucide-react"
import { useState } from "react"
import { Link, useNavigate, useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import { ListStatusButton, ProgressEditor, ScoreEditor } from "@/components/entry/ListEditor"
import { PluginSlot } from "@/components/plugins/PluginSlot"
import { Badge, Button, Dialog, EmptyState, ErrorState, Input, Select, Skeleton } from "@/components/ui"
import { api, qs } from "@/lib/api"
import { useMangaChapters, useStatus } from "@/lib/queries"
import type { Media } from "@/lib/types"
import { banner, cleanDescription, cn, cover, formatLabel, statusLabel, title } from "@/lib/utils"

export default function MangaEntryPage() {
    const [params] = useSearchParams()
    const id = Number(params.get("id"))
    const { data: status } = useStatus()
    const { data: media, isLoading, error: mediaError, refetch: refetchMedia, isFetching: mediaFetching } = useQuery({ queryKey: ["manga", "media", id], queryFn: () => api.get<Media>(`/api/manga/${id}`), enabled: id > 0 })
    const { data: providers } = useQuery({ queryKey: ["manga-providers"], queryFn: () => api.get<{ id: string; name: string }[]>("/api/manga/providers") })
    const [provider, setProvider] = useState<string>(() => localStorage.getItem(`kumo-manga-provider-${id}`) || status?.settings.manga.defaultProvider || "")
    const effective = provider || providers?.[0]?.id || ""
    const { data: ch, isLoading: chLoading, error, refetch, isFetching } = useMangaChapters(id, effective)
    const [matchOpen, setMatchOpen] = useState(false)
    const navigate = useNavigate()
    const qc = useQueryClient()

    if (!(id > 0))
        return (
            <div className="p-10">
                <EmptyState
                    icon={<BookOpen className="size-6" />}
                    title="No manga selected"
                    action={
                        <Link to="/manga">
                            <Button>Go to Manga</Button>
                        </Link>
                    }
                >
                    This link doesn't point to a manga.
                </EmptyState>
            </div>
        )
    if (mediaError && !media)
        return (
            <div className="p-10">
                <ErrorState title="Couldn't load this manga" error={mediaError} retrying={mediaFetching} onRetry={() => refetchMedia()} />
            </div>
        )
    if (isLoading || !media) return <Skeleton className="m-10 h-96" />
    const progress = media.mediaListEntry?.progress ?? 0
    const chapters = ch?.chapters ?? []
    const next = chapters.find(c => parseFloat(c.chapter) > progress) ?? chapters[0]
    const read = (chapterId: string) => navigate(`/manga/read${qs({ id, provider: effective, chapter: chapterId })}`)

    return (
        <div className="min-h-full pb-24">
            <div className="relative">
                <div className="absolute inset-x-0 top-0 h-[440px] overflow-hidden">
                    {banner(media) && <img src={banner(media)} alt="" className="size-full object-cover opacity-60 fade-in" />}
                    <div className="absolute inset-0 bg-gradient-to-t from-bg via-bg/70 to-bg/10" />
                </div>
                <div className="relative z-10 flex flex-col gap-8 px-6 pt-[180px] pb-10 md:flex-row md:px-8 xl:px-10">
                    <img src={cover(media)} alt="" className="aspect-[2/3] w-40 shrink-0 rounded-lg object-cover shadow-2xl shadow-black/60 ring-1 ring-white/10 md:w-52" />
                    <div className="flex min-w-0 flex-1 flex-col justify-end gap-4">
                        <h1 className="text-3xl leading-[1.1] font-semibold tracking-tight md:text-[2.75rem]">{title(media)}</h1>
                        <div className="flex flex-wrap items-center gap-3 text-sm text-white/80">
                            <span>{formatLabel(media.format)}</span>
                            {media.chapters && <span>{media.chapters} chapters</span>}
                            <Badge>{statusLabel(media.status)}</Badge>
                            {media.genres?.slice(0, 4).map(g => (
                                <span key={g} className="text-white/60">
                                    {g}
                                </span>
                            ))}
                        </div>
                        <p className="line-clamp-3 max-w-4xl text-white/70">{cleanDescription(media.description)}</p>
                        <div className="flex flex-wrap gap-2.5">
                            {next && (
                                <Button variant="white" size="lg" icon={<BookOpen className="size-5" />} onClick={() => read(next.id)}>
                                    {progress ? "Continue" : "Start"} · Ch {next.chapter}
                                </Button>
                            )}
                            <ListStatusButton media={media} entry={media.mediaListEntry} />
                            {media.mediaListEntry && <ProgressEditor media={media} entry={media.mediaListEntry} />}
                            <ScoreEditor media={media} entry={media.mediaListEntry} />
                        </div>
                    </div>
                </div>
            </div>
            <div className="flex flex-col gap-6 px-6 md:px-8 xl:px-10">
                <div className="flex flex-wrap items-center gap-3">
                    <Select
                        className="w-60"
                        value={effective}
                        onChange={v => {
                            setProvider(v)
                            localStorage.setItem(`kumo-manga-provider-${id}`, v)
                        }}
                        options={(providers ?? []).map(p => ({ value: p.id, label: p.name }))}
                    />
                    <Button variant="subtle" icon={<RefreshCw className={cn("size-4", isFetching && "animate-spin")} />} onClick={() => api.get(`/api/manga/${id}/chapters${qs({ provider: effective, refresh: 1 })}`).then(() => qc.invalidateQueries({ queryKey: ["manga", "chapters", id] })).catch(e => toast.error(e.message))}>
                        Refresh
                    </Button>
                    <Button variant="subtle" icon={<Wand2 className="size-4" />} onClick={() => setMatchOpen(true)} disabled={!effective}>
                        Change match
                    </Button>
                    {ch?.mapping && (
                        <span className="text-sm text-muted">
                            Matched <span className="font-semibold text-fg">{ch.mapping.title}</span>
                        </span>
                    )}
                </div>
                {!effective && (
                    <EmptyState title="No manga provider installed" action={<Link to="/extensions?tab=marketplace"><Button variant="primary">Open marketplace</Button></Link>}>
                        Install a manga provider extension to read chapters.
                    </EmptyState>
                )}
                {chLoading && effective && <Skeleton className="h-64" />}
                {error && <EmptyState title="Couldn't load chapters" action={<Button onClick={() => refetch()}>Retry</Button>}>{(error as Error).message}</EmptyState>}
                {chapters.length > 0 && (
                    <div className="card divide-y divide-line">
                        {[...chapters].reverse().map(c => {
                            const done = parseFloat(c.chapter) <= progress
                            return (
                                <button key={c.id} onClick={() => read(c.id)} className={cn("flex w-full items-center justify-between gap-4 px-4 py-3 text-left transition hover:bg-white/[0.03]", done && "opacity-50")}>
                                    <div className="min-w-0">
                                        <p className="truncate font-medium">{c.title || `Chapter ${c.chapter}`}</p>
                                        <p className="text-xs text-subtle">{[c.scanlator, c.language?.toUpperCase(), c.updatedAt].filter(Boolean).join(" · ")}</p>
                                    </div>
                                    {done ? <Badge tone="green">Read</Badge> : <Badge>Ch {c.chapter}</Badge>}
                                </button>
                            )
                        })}
                    </div>
                )}
                <PluginSlot slot="manga-entry-screen-bottom" />
            </div>
            <MangaMatchDialog open={matchOpen} onOpenChange={setMatchOpen} provider={effective} mediaId={id} defaultQuery={media.title.english || media.title.romaji || ""} />
        </div>
    )
}

function MangaMatchDialog({ open, onOpenChange, provider, mediaId, defaultQuery }: { open: boolean; onOpenChange: (v: boolean) => void; provider: string; mediaId: number; defaultQuery: string }) {
    const [q, setQ] = useState(defaultQuery)
    const [res, setRes] = useState<{ id: string; title: string; image?: string; year?: number }[]>([])
    const [busy, setBusy] = useState(false)
    const qc = useQueryClient()
    const search = async () => {
        setBusy(true)
        try {
            setRes(await api.get(`/api/manga/search${qs({ provider, q })}`))
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <Dialog open={open} onOpenChange={onOpenChange} title="Pick the right manga">
            <div className="flex gap-2">
                <Input value={q} onChange={e => setQ(e.target.value)} onKeyDown={e => e.key === "Enter" && search()} icon={<Search className="size-4" />} />
                <Button variant="primary" loading={busy} onClick={search}>
                    Search
                </Button>
            </div>
            <div className="mt-4 flex flex-col gap-1">
                {res.map(r => (
                    <button
                        key={r.id}
                        onClick={async () => {
                            try {
                                await api.post("/api/manga/mapping", { provider, mediaId, id: r.id, title: r.title })
                                qc.invalidateQueries({ queryKey: ["manga", "chapters", mediaId] })
                                onOpenChange(false)
                            } catch (e: any) {
                                toast.error(e.message)
                            }
                        }}
                        className="flex items-center gap-3 rounded-xl p-2 text-left hover:bg-white/[0.06]"
                    >
                        {r.image && <img src={r.image} alt="" className="h-14 w-10 rounded object-cover" referrerPolicy="no-referrer" />}
                        <span className="font-medium">{r.title}</span>
                        {r.year ? <span className="text-sm text-subtle">{r.year}</span> : null}
                    </button>
                ))}
            </div>
        </Dialog>
    )
}
