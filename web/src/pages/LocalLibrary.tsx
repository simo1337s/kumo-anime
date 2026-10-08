import { Check, CheckCheck, ChevronDown, FolderOpen, FolderSearch, FolderSync, HardDrive, Info, LayoutGrid, List as ListIcon, Play, Search, Settings2, Share2 } from "lucide-react"
import { useMemo, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { toast } from "@/lib/toast"
import { EpisodeCard } from "@/components/EpisodeCard"
import { Carousel, MediaCard, MediaCardSkeleton, MediaGrid } from "@/components/MediaCard"
import { useLibraryCardMenu } from "@/components/library/LibraryCardMenu"
import { Badge, Button, EmptyState, IconButton, Input, Progress, Select, Tabs, Tooltip } from "@/components/ui"
import { api } from "@/lib/api"
import { usePersisted } from "@/lib/hooks"
import { usePlay } from "@/lib/play"
import { useCollection, useEpisodeMarker, useLibraryFiles, useScan, useSharedLibraries, useStatus } from "@/lib/queries"
import { scanStore, useStore } from "@/lib/store"
import type { CollectionItem, LocalFile } from "@/lib/types"
import { banner, cn, cover, entryUrl, formatBytes, formatLabel, LIST_STATUS, mostCommon, relativeTime, title, totalEpisodes } from "@/lib/utils"

// "Local library": only anime that have files on this computer.

type Group = {
    item: CollectionItem
    files: LocalFile[] // main episodes first (by number), then specials / NC
    episodes: number[] // distinct downloaded main episode numbers
    size: number
    latest: number // newest file, unix seconds
    resolutions: string[]
    releaseGroups: string[]
    dir: string
    unwatched: number
    next: LocalFile | null // first downloaded episode not watched yet
}

type StatusFilter = "ALL" | "CURRENT" | "PLANNING" | "COMPLETED" | "PAUSED" | "DROPPED" | "NONE"
type SortKey = "added" | "title" | "unwatched" | "episodes" | "size"
type View = "grid" | "list"

const STATUS_TABS: { value: StatusFilter; label: string }[] = [
    { value: "ALL", label: "All" },
    { value: "CURRENT", label: "Watching" },
    { value: "PLANNING", label: "Planning" },
    { value: "COMPLETED", label: "Completed" },
    { value: "PAUSED", label: "Paused" },
    { value: "DROPPED", label: "Dropped" },
    { value: "NONE", label: "Not on my list" },
]

function statusOf(g: Group): StatusFilter {
    const s = g.item.listEntry?.status
    if (!s) return "NONE"
    return s === "REPEATING" ? "CURRENT" : (s as StatusFilter)
}

const uniq = (xs: (string | undefined)[]) => [...new Set(xs.filter((x): x is string => !!x))]

// [1, 2, 3, 5, 7, 8] -> "1–3, 5, 7–8"
function episodeRanges(nums: number[]) {
    const s = [...new Set(nums)].sort((a, b) => a - b)
    const out: string[] = []
    for (let i = 0; i < s.length; ) {
        let j = i
        while (j + 1 < s.length && s[j + 1] === s[j] + 1) j++
        out.push(i === j ? `${s[i]}` : `${s[i]}–${s[j]}`)
        i = j + 1
    }
    return out.join(", ")
}

function buildGroups(items: CollectionItem[], files: LocalFile[]): Group[] {
    const byMedia = new Map<number, LocalFile[]>()
    for (const f of files) {
        if (f.ignored || !f.mediaId) continue
        const list = byMedia.get(f.mediaId)
        if (list) list.push(f)
        else byMedia.set(f.mediaId, [f])
    }
    const rank = (k: string) => (k === "main" ? 0 : k === "special" ? 1 : 2)
    const out: Group[] = []
    for (const item of items) {
        const fs = byMedia.get(item.media.id)
        if (!fs?.length) continue
        fs.sort((a, b) => rank(a.kind) - rank(b.kind) || a.episode - b.episode || a.name.localeCompare(b.name))
        const main = fs.filter(f => f.kind === "main")
        const le = item.listEntry
        const watchedUpTo = le?.status === "COMPLETED" ? Infinity : (le?.progress ?? 0)
        const unwatched = main.filter(f => f.episode > watchedUpTo)
        out.push({
            item,
            files: fs,
            episodes: [...new Set(main.map(f => f.episode))],
            size: fs.reduce((n, f) => n + f.size, 0),
            latest: Math.max(...fs.map(f => f.modTime)),
            resolutions: uniq(fs.map(f => f.parsed?.resolution)),
            releaseGroups: uniq(fs.map(f => f.parsed?.releaseGroup)),
            dir: mostCommon(fs.map(f => f.dir)),
            unwatched: new Set(unwatched.map(f => f.episode)).size,
            next: unwatched[0] ?? null,
        })
    }
    return out
}

export default function LocalLibraryPage() {
    const { data: status } = useStatus()
    const { data: coll, isLoading: collLoading, error } = useCollection()
    const { data: localFiles, isLoading: filesLoading } = useLibraryFiles()
    // The libraries other Kumo apps on the network share with this one.
    const { data: shared } = useSharedLibraries()
    const [source, setSource] = usePersisted<string>("kumo-local-source", "local")
    const host = source === "local" ? undefined : shared?.find(l => l.host.id === source)
    const files = host ? host.files : localFiles
    const scan = useScan()
    const scanning = useStore(scanStore, s => s.running)
    const scanState = useStore(scanStore)
    const { playLocal } = usePlay()
    const navigate = useNavigate()
    const libraryMenu = useLibraryCardMenu()

    const [filter, setFilter] = usePersisted<StatusFilter>("kumo-local-filter", "ALL")
    const [sort, setSort] = usePersisted<SortKey>("kumo-local-sort", "added")
    const [view, setView] = usePersisted<View>("kumo-local-view", "grid")
    const [q, setQ] = useState("")

    const groups = useMemo(() => {
        const items = new Map<number, CollectionItem>()
        for (const l of coll?.lists ?? []) for (const it of l.items) items.set(it.media.id, it)
        for (const it of coll?.localOnly ?? []) items.set(it.media.id, it)
        return buildGroups([...items.values()], files ?? [])
    }, [coll, files])

    const counts = useMemo(() => {
        const c: Record<string, number> = { ALL: groups.length }
        for (const g of groups) c[statusOf(g)] = (c[statusOf(g)] ?? 0) + 1
        return c
    }, [groups])
    // Tabs without anime are hidden, so a remembered filter may have no tab
    // any more: fall back to All.
    const activeFilter: StatusFilter = filter !== "ALL" && STATUS_TABS.some(t => t.value === filter) && counts[filter] > 0 ? filter : "ALL"

    const shown = useMemo(() => {
        const needle = q.trim().toLowerCase()
        const out = groups.filter(g => {
            if (activeFilter !== "ALL" && statusOf(g) !== activeFilter) return false
            if (!needle) return true
            const m = g.item.media
            return [m.title.userPreferred, m.title.romaji, m.title.english, m.title.native, ...(m.synonyms ?? [])].some(t => t?.toLowerCase().includes(needle))
        })
        out.sort((a, b) => {
            switch (sort) {
                case "title":
                    return title(a.item.media).localeCompare(title(b.item.media))
                case "unwatched":
                    return b.unwatched - a.unwatched || b.latest - a.latest
                case "episodes":
                    return b.episodes.length - a.episodes.length
                case "size":
                    return b.size - a.size
                default:
                    return b.latest - a.latest
            }
        })
        return out
    }, [groups, activeFilter, sort, q])

    // Newest files on disk, one card per anime and day (a batch of 12
    // episodes shows up as "Episodes 1–12", not twelve cards).
    const recent = useMemo(() => {
        const media = new Map(groups.map(g => [g.item.media.id, g]))
        const batches = new Map<string, { group: Group; files: LocalFile[]; latest: number }>()
        const sorted = (files ?? []).filter(f => !f.ignored && f.mediaId && f.kind === "main" && media.has(f.mediaId)).sort((a, b) => b.modTime - a.modTime)
        for (const f of sorted) {
            const key = `${f.mediaId}:${new Date(f.modTime * 1000).toDateString()}`
            const b = batches.get(key)
            if (b) b.files.push(f)
            else if (batches.size < 16) batches.set(key, { group: media.get(f.mediaId)!, files: [f], latest: f.modTime })
        }
        return [...batches.values()].map(b => {
            b.files.sort((x, y) => x.episode - y.episode)
            const le = b.group.item.listEntry
            const watchedUpTo = le?.status === "COMPLETED" ? Infinity : (le?.progress ?? 0)
            return { ...b, play: b.files.find(f => f.episode > watchedUpTo) ?? b.files[0], allWatched: b.files.every(f => f.episode <= watchedUpTo) }
        })
    }, [files, groups])

    const totals = useMemo(
        () => ({
            episodes: groups.reduce((n, g) => n + g.episodes.length, 0),
            size: groups.reduce((n, g) => n + g.size, 0),
        }),
        [groups],
    )

    const loading = collLoading || filesLoading
    const libraryDir = status?.settings.library.dir
    const trusted = status?.client !== "lan"
    const openFolder = (path?: string) => path && api.post("/api/open", { path }).catch(e => toast.error(e.message))

    return (
        <div className="min-h-full pb-24">
            <header className="border-b border-line px-6 pt-8 pb-7 md:px-8 xl:px-10">
                <div className="flex flex-wrap items-end justify-between gap-6">
                    <div>
                        <h1 className="text-[1.75rem] font-semibold tracking-tight">Local library</h1>
                        <p className="mt-1 text-muted">
                            {host ? `Shared by ${host.host.name} over your network${host.host.online ? "" : " (not answering right now)"}.` : "Only anime you have downloaded on this computer."}
                        </p>
                    </div>
                    {!host && (
                        <div className="flex items-center gap-2">
                            {trusted && libraryDir && (
                                <IconButton label="Open library folder" variant="subtle" onClick={() => openFolder(libraryDir)}>
                                    <FolderOpen className="size-4" />
                                </IconButton>
                            )}
                            <IconButton label="Library tools (fix matches, ignored files)" variant="subtle" onClick={() => navigate("/library")}>
                                <Settings2 className="size-4" />
                            </IconButton>
                            <Button variant="primary" loading={scanning || scan.isPending} icon={<FolderSync className="size-4" />} onClick={() => scan.mutate(false)}>
                                {scanning ? "Scanning…" : "Scan for new files"}
                            </Button>
                        </div>
                    )}
                </div>
                {!!shared?.length && (
                    <Tabs
                        className="mt-6"
                        value={host ? source : "local"}
                        onChange={setSource}
                        tabs={[
                            { value: "local", label: "This computer", icon: <HardDrive className="size-4" /> },
                            ...shared.map(l => ({ value: l.host.id, label: l.host.name, icon: <Share2 className="size-4" /> })),
                        ]}
                    />
                )}
                <div className="mt-6 flex flex-wrap items-center gap-x-8 gap-y-3">
                    <Stat label="Anime" value={loading ? "…" : String(groups.length)} />
                    <Stat label="Episodes" value={loading ? "…" : String(totals.episodes)} />
                    <Stat label={host ? "Size" : "On disk"} value={loading ? "…" : formatBytes(totals.size)} />
                    {!host && !!coll?.unmatchedCount && (
                        <Link to="/library?tab=unmatched" className="flex items-center gap-2 rounded-lg bg-amber-400/10 px-3 py-2 text-sm text-amber-200 transition-colors hover:bg-amber-400/15">
                            <FolderSearch className="size-4" />
                            {coll.unmatchedCount} unmatched {coll.unmatchedCount === 1 ? "file" : "files"} — fix
                        </Link>
                    )}
                </div>
            </header>

            <div className="flex flex-col gap-10 px-6 pt-8 md:px-8 xl:px-10">
                {error && <EmptyState title="Couldn't load your library">{(error as Error).message}</EmptyState>}

                {scanning && groups.length === 0 && (
                    <div className="card mx-auto w-full max-w-xl p-6 rise-in">
                        <div className="flex items-center gap-3">
                            <FolderSync className="size-5 animate-pulse text-brand-strong" />
                            <div className="min-w-0 flex-1">
                                <p className="font-semibold">Scanning your library…</p>
                                <p className="truncate text-sm text-muted">{scanState.message || "Looking for video files…"}</p>
                            </div>
                        </div>
                        <Progress value={scanState.total > 0 ? scanState.done / scanState.total : 0} className={cn("mt-4 h-1.5", scanState.total === 0 && "animate-pulse")} />
                        <p className="mt-3 text-xs text-subtle">
                            Every show is looked up on AniList, then artwork and episode titles are downloaded. A big first scan can take a few minutes.
                        </p>
                    </div>
                )}

                {host && !loading && groups.length === 0 && (
                    <EmptyState icon={<Share2 className="size-6" />} title="Nothing to show yet">
                        {host.host.name} shares {host.files.length} {host.files.length === 1 ? "file" : "files"}; their anime show here once AniList answers.
                    </EmptyState>
                )}

                {!host && !loading && !scanning && groups.length === 0 && !error && (
                    <EmptyState
                        icon={<HardDrive className="size-6" />}
                        title={libraryDir ? "No downloaded anime yet" : "Choose your library folder"}
                        action={
                            libraryDir ? (
                                <Button loading={scanning} icon={<FolderSync className="size-4" />} onClick={() => scan.mutate(false)}>
                                    Scan now
                                </Button>
                            ) : (
                                <Button variant="primary" onClick={() => navigate("/settings?tab=library")}>
                                    Open settings
                                </Button>
                            )
                        }
                    >
                        {libraryDir ? (
                            <>
                                Kumo looks for video files in <span className="font-mono text-fg/80">{libraryDir}</span>. Download episodes with ani-cli or torrents, or copy files there.
                            </>
                        ) : (
                            "Pick the folder where your anime lives and Kumo will scan and match it automatically."
                        )}
                    </EmptyState>
                )}

                {recent.length > 0 && !q && (
                    <section className="rise-in">
                        <h2 className="mb-4 text-xl font-semibold tracking-tight">Recently added</h2>
                        <Carousel itemClassName="w-[300px]">
                            {recent.map(({ group, files: batch, latest, play, allWatched }) => {
                                const m = group.item.media
                                const eps = m.format === "MOVIE" ? "Movie" : batch.length > 1 ? `Episodes ${episodeRanges(batch.map(f => f.episode))}` : `Episode ${batch[0].episode}`
                                return (
                                    <EpisodeCard
                                        key={`${m.id}:${latest}`}
                                        image={banner(m) || cover(m)}
                                        number={play.episode}
                                        title={title(m)}
                                        subtitle={`${eps} · added ${relativeTime(latest)}`}
                                        hasFile
                                        watched={allWatched}
                                        onClick={() => playLocal(play.path, m.id, play.episode)}
                                        actions={
                                            <IconButton label="Open anime page" variant="ghost" size="xs" className="bg-black/70 text-white hover:bg-black/85 hover:text-white" onClick={e => (e.stopPropagation(), navigate(entryUrl(m)))}>
                                                <Info className="size-3.5" />
                                            </IconButton>
                                        }
                                    />
                                )
                            })}
                        </Carousel>
                    </section>
                )}

                {(loading || groups.length > 0) && (
                    <section>
                        <div className="mb-6 flex flex-wrap items-center gap-3">
                            <Tabs value={activeFilter} onChange={setFilter} tabs={STATUS_TABS.filter(t => t.value === "ALL" || counts[t.value]).map(t => ({ ...t, count: counts[t.value] ?? 0 }))} />
                            <div className="ml-auto flex flex-wrap items-center gap-3 lg:flex-nowrap">
                                <div className="w-60 shrink-0">
                                    <Input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter by title…" icon={<Search className="size-4" />} />
                                </div>
                                <Select
                                    className="w-48 shrink-0"
                                    value={sort}
                                    onChange={v => setSort(v as SortKey)}
                                    options={[
                                        { value: "added", label: "Recently added" },
                                        { value: "title", label: "Title" },
                                        { value: "unwatched", label: "Most unwatched" },
                                        { value: "episodes", label: "Most episodes" },
                                        { value: "size", label: "Largest on disk" },
                                    ]}
                                />
                                <Tabs
                                    value={view}
                                    onChange={setView}
                                    tabs={[
                                        { value: "grid", label: <LayoutGrid className="size-4" aria-label="Grid" /> },
                                        { value: "list", label: <ListIcon className="size-4" aria-label="List" /> },
                                    ]}
                                />
                            </div>
                        </div>

                        {loading ? (
                            <MediaGrid>
                                {Array.from({ length: 12 }).map((_, i) => (
                                    <MediaCardSkeleton key={i} />
                                ))}
                            </MediaGrid>
                        ) : shown.length === 0 ? (
                            <EmptyState icon={<Search className="size-6" />} title="Nothing matches">
                                Try another filter or search.
                            </EmptyState>
                        ) : view === "grid" ? (
                            <MediaGrid size={status?.settings.ui.cardSize}>
                                {shown.map(g => (
                                    <MediaCard
                                        key={g.item.media.id}
                                        media={g.item.media}
                                        listEntry={g.item.listEntry}
                                        localCount={g.files.length}
                                        downloaded={g.episodes}
                                        menu={libraryMenu.menu(g.item.media)}
                                    />
                                ))}
                            </MediaGrid>
                        ) : (
                            <div className="flex flex-col gap-3">
                                {shown.map(g => (
                                    <LibraryRow key={g.item.media.id} group={g} trusted={trusted && !host} onOpen={openFolder} onPlay={f => playLocal(f.path, f.mediaId, f.episode)} />
                                ))}
                            </div>
                        )}
                    </section>
                )}
            </div>
            {libraryMenu.dialog}
        </div>
    )
}

function Stat({ label, value }: { label: string; value: string }) {
    return (
        <div>
            <p className="text-xs text-subtle">{label}</p>
            <p className="text-lg font-semibold tabular-nums">{value}</p>
        </div>
    )
}

function LibraryRow({ group: g, trusted, onOpen, onPlay }: { group: Group; trusted: boolean; onOpen: (path: string) => void; onPlay: (f: LocalFile) => void }) {
    const [open, setOpen] = useState(false)
    const m = g.item.media
    const le = g.item.listEntry
    const total = totalEpisodes(m)
    const isMovie = m.format === "MOVIE"
    const extras = g.files.length - g.files.filter(f => f.kind === "main").length
    const marker = useEpisodeMarker(m, le)

    return (
        <div className="card overflow-hidden">
            <div className="flex items-center gap-4 p-3 pr-4">
                <Link to={entryUrl(m)} className="shrink-0">
                    <img src={cover(m)} alt="" loading="lazy" className="h-24 w-16 rounded-lg object-cover ring-1 ring-line" style={{ backgroundColor: m.coverImage?.color || undefined }} />
                </Link>
                <div className="min-w-0 flex-1">
                    <Link to={entryUrl(m)} className="line-clamp-1 text-[15px] font-semibold hover:underline">
                        {title(m)}
                    </Link>
                    <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] text-muted">
                        <span>{formatLabel(m.format)}</span>
                        {m.seasonYear && <span>· {m.seasonYear}</span>}
                        {le ? <span>· {LIST_STATUS[le.status] ?? le.status}</span> : <span>· Not on your list</span>}
                        {le && total > 0 && !isMovie && (
                            <span>
                                · watched {le.progress}/{total}
                            </span>
                        )}
                    </div>
                    <div className="mt-2 flex flex-wrap items-center gap-1.5">
                        <Badge tone="brand">
                            <HardDrive className="mr-1 size-3" />
                            {isMovie ? "Movie" : `Ep ${episodeRanges(g.episodes)}`}
                            {!isMovie && total > 0 && <span className="ml-1 opacity-70">({g.episodes.length}/{total})</span>}
                        </Badge>
                        {extras > 0 && <Badge>+{extras} extras</Badge>}
                        {g.unwatched > 0 && <Badge tone="green">{g.unwatched} unwatched</Badge>}
                        {g.resolutions.slice(0, 2).map(r => (
                            <Badge key={r}>{r}</Badge>
                        ))}
                        {g.releaseGroups.slice(0, 2).map(r => (
                            <Badge key={r} tone="blue">
                                {r}
                            </Badge>
                        ))}
                        <span className="text-xs text-subtle">
                            {formatBytes(g.size)} · added {relativeTime(g.latest)}
                        </span>
                    </div>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                    {g.next && (
                        <Button variant="primary" size="sm" icon={<Play className="size-4 fill-current" />} onClick={() => onPlay(g.next!)}>
                            {isMovie ? "Play" : `Ep ${g.next.episode}`}
                        </Button>
                    )}
                    {trusted && g.dir && (
                        <IconButton label="Open folder" variant="subtle" size="sm" onClick={() => onOpen(g.dir)}>
                            <FolderOpen className="size-4" />
                        </IconButton>
                    )}
                    <IconButton label={open ? "Hide files" : "Show files"} variant="subtle" size="sm" onClick={() => setOpen(!open)}>
                        <ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} />
                    </IconButton>
                </div>
            </div>
            {open && (
                <div className="border-t border-line bg-surface-1/50">
                    {g.files.map(f => {
                        const watched = le?.status === "COMPLETED" || (f.kind === "main" && (le?.progress ?? 0) >= f.episode)
                        return (
                            <div key={f.path} className="group/file flex w-full items-center gap-3 px-4 py-2 text-sm transition hover:bg-white/[0.04]">
                                <button onClick={() => onPlay(f)} title={f.path} className="flex min-w-0 flex-1 items-center gap-3 text-left">
                                    <span className="grid size-7 shrink-0 place-items-center rounded-full bg-white/[0.06] text-muted transition group-hover/file:bg-brand group-hover/file:text-white">
                                        <Play className="ml-0.5 size-3 fill-current" />
                                    </span>
                                    <span className={cn("w-16 shrink-0 font-semibold tabular-nums", watched && "text-subtle")}>
                                        {f.kind === "main" ? (isMovie ? "Movie" : `Ep ${f.episode}`) : f.kind === "nc" ? "NC" : "Special"}
                                    </span>
                                    <span className={cn("min-w-0 flex-1 truncate", watched ? "text-subtle" : "text-fg/85")}>{f.name}</span>
                                </button>
                                {f.parsed?.resolution && <span className="hidden shrink-0 text-xs text-subtle sm:inline">{f.parsed.resolution}</span>}
                                <span className="w-20 shrink-0 text-right text-xs text-subtle tabular-nums">{formatBytes(f.size)}</span>
                                {f.kind === "main" && (
                                    <Tooltip content={watched ? "Mark as unwatched" : "Mark as watched"}>
                                        <button
                                            aria-label={watched ? "Mark as unwatched" : "Mark as watched"}
                                            disabled={marker.pending}
                                            onClick={() => marker.mark(f.episode, !watched)}
                                            className={cn(
                                                "grid size-7 shrink-0 place-items-center rounded-full transition disabled:opacity-50",
                                                watched ? "bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25" : "text-subtle hover:bg-white/[0.08] hover:text-fg",
                                            )}
                                        >
                                            {watched ? <CheckCheck className="size-3.5" /> : <Check className="size-3.5" />}
                                        </button>
                                    </Tooltip>
                                )}
                            </div>
                        )
                    })}
                    {g.dir && <p className="truncate px-4 py-2 font-mono text-[11px] text-subtle">{g.dir}</p>}
                </div>
            )}
        </div>
    )
}
