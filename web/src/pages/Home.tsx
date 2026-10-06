import { ArrowDownUp, ChevronLeft, ChevronRight, Download, FolderOpen, FolderSearch, FolderSync, HardDrive, LibraryBig, ListFilter, LogIn, MoreVertical, Puzzle, RefreshCw, Settings2, Sparkles } from "lucide-react"
import { useMemo, useRef, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { EpisodeCard } from "@/components/EpisodeCard"
import { LoginDialog } from "@/components/LoginDialog"
import { MediaCard, MediaCardSkeleton, MediaGrid } from "@/components/MediaCard"
import { PluginSlot } from "@/components/plugins/PluginSlot"
import { Button, Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger, EmptyState, IconButton, Skeleton } from "@/components/ui"
import { api } from "@/lib/api"
import { usePersisted } from "@/lib/hooks"
import { usePlay } from "@/lib/play"
import { useCollection, useScan, useStatus } from "@/lib/queries"
import { scanStore, useStore } from "@/lib/store"
import type { CollectionItem, ContinueItem } from "@/lib/types"
import { banner, cn, img as imgUrl, title } from "@/lib/utils"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

type SortKey = "activity" | "title" | "score" | "progress" | "airing"

export default function HomePage() {
    const { data: status } = useStatus()
    const { data, isLoading, error } = useCollection()
    const scan = useScan()
    const scanning = useStore(scanStore, s => s.running)
    const [genre, setGenre] = useState<string | null>(null)
    const [onlyLocal, setOnlyLocal] = usePersisted("kumo-home-only-local", false)
    const [sort, setSort] = usePersisted<SortKey>("kumo-home-sort", "activity")
    const [loginOpen, setLoginOpen] = useState(false)
    const navigate = useNavigate()
    const qc = useQueryClient()

    const continueItems = data?.continueWatching ?? []
    const [heroIdx, setHeroIdx] = useState(0)
    const hero = continueItems[heroIdx] ?? null

    const filterItems = (items: CollectionItem[]) => {
        let out = items
        if (genre) out = out.filter(i => i.media.genres?.includes(genre))
        if (onlyLocal) out = out.filter(i => i.localFiles > 0)
        const sorted = [...out]
        switch (sort) {
            case "title":
                sorted.sort((a, b) => title(a.media).localeCompare(title(b.media)))
                break
            case "score":
                sorted.sort((a, b) => (b.listEntry?.score || b.media.meanScore || 0) - (a.listEntry?.score || a.media.meanScore || 0))
                break
            case "progress":
                sorted.sort((a, b) => (b.listEntry?.progress ?? 0) - (a.listEntry?.progress ?? 0))
                break
            case "airing":
                sorted.sort((a, b) => (a.media.nextAiringEpisode?.airingAt ?? Infinity) - (b.media.nextAiringEpisode?.airingAt ?? Infinity))
                break
        }
        return sorted
    }

    const lists = useMemo(() => {
        const ls = data?.lists ?? []
        const watching = ls.filter(l => l.status === "CURRENT" || l.status === "REPEATING").flatMap(l => l.items)
        const rest = ls.filter(l => l.status !== "CURRENT" && l.status !== "REPEATING")
        const out: { key: string; name: string; items: CollectionItem[] }[] = []
        if (watching.length) out.push({ key: "CURRENT", name: "Currently watching", items: watching })
        for (const l of rest) out.push({ key: l.status, name: l.name, items: l.items })
        if (data?.localOnly?.length) out.push({ key: "LOCAL", name: "In your library", items: data.localOnly })
        return out.map(l => ({ ...l, items: filterItems(l.items) })).filter(l => l.items.length > 0)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [data, genre, onlyLocal, sort])

    const refreshAnilist = async () => {
        await api.get("/api/anime/collection?refresh=1")
        qc.invalidateQueries({ queryKey: ["collection"] })
        toast.success("Synced with AniList")
    }

    const isEmpty = !isLoading && !error && (data?.lists?.length ?? 0) === 0 && (data?.localOnly?.length ?? 0) === 0

    return (
        <div className="relative min-h-full pb-24">
            <Hero
                item={hero}
                count={continueItems.length}
                index={heroIdx}
                onIndex={setHeroIdx}
                loading={isLoading}
                toolbar={
                    <div className="flex items-center gap-1.5">
                        <Button size="sm" variant="subtle" className="glass" icon={<HardDrive className="size-4" />} onClick={() => navigate("/local")}>
                            Local library
                        </Button>
                        <IconButton label="Library tools" variant="subtle" size="sm" className="glass" onClick={() => navigate("/library")}>
                            <LibraryBig className="size-4" />
                        </IconButton>
                        <IconButton label={`Unmatched files (${data?.unmatchedCount ?? 0})`} variant="subtle" size="sm" className="glass relative" onClick={() => navigate("/library?tab=unmatched")}>
                            <FolderSearch className="size-4" />
                            {!!data?.unmatchedCount && <span className="absolute -top-1 -right-1 size-2.5 rounded-full bg-amber-400 ring-2 ring-black" />}
                        </IconButton>
                        <IconButton label="Downloads" variant="subtle" size="sm" className="glass" onClick={() => navigate("/downloads")}>
                            <Download className="size-4" />
                        </IconButton>
                        <Button size="sm" variant="subtle" className="glass" loading={scanning || scan.isPending} icon={<FolderSync className="size-4" />} onClick={() => scan.mutate(false)}>
                            Refresh
                        </Button>
                        <Dropdown>
                            <DropdownTrigger asChild>
                                <IconButton label="Display" variant="subtle" size="sm" className="glass">
                                    <Settings2 className="size-4" />
                                </IconButton>
                            </DropdownTrigger>
                            <DropdownContent>
                                <DropdownLabel>Show</DropdownLabel>
                                <DropdownItem icon={<HardDrive />} onSelect={() => setOnlyLocal(!onlyLocal)}>
                                    {onlyLocal ? "● " : ""}Only downloaded anime
                                </DropdownItem>
                                <DropdownItem icon={<ListFilter />} onSelect={() => setOnlyLocal(false)}>
                                    {!onlyLocal ? "● " : ""}Everything in my lists
                                </DropdownItem>
                                <DropdownSeparator />
                                <DropdownLabel>Sort by</DropdownLabel>
                                {(
                                    [
                                        ["activity", "Recent activity"],
                                        ["title", "Title"],
                                        ["score", "Score"],
                                        ["progress", "Progress"],
                                        ["airing", "Next airing"],
                                    ] as [SortKey, string][]
                                ).map(([k, l]) => (
                                    <DropdownItem key={k} icon={<ArrowDownUp />} onSelect={() => setSort(k)}>
                                        {sort === k ? "● " : ""}
                                        {l}
                                    </DropdownItem>
                                ))}
                            </DropdownContent>
                        </Dropdown>
                        <Dropdown>
                            <DropdownTrigger asChild>
                                <IconButton label="More" variant="subtle" size="sm" className="glass">
                                    <MoreVertical className="size-4" />
                                </IconButton>
                            </DropdownTrigger>
                            <DropdownContent>
                                <DropdownItem icon={<RefreshCw />} onSelect={() => scan.mutate(true)}>
                                    Full rescan (re-match everything)
                                </DropdownItem>
                                <DropdownItem icon={<Sparkles />} onSelect={refreshAnilist}>
                                    Sync with AniList
                                </DropdownItem>
                                {status?.client !== "lan" && (
                                    <DropdownItem icon={<FolderOpen />} onSelect={() => api.post("/api/open", { path: status?.settings.library.dir }).catch(e => toast.error(e.message))}>
                                        Open library folder
                                    </DropdownItem>
                                )}
                            </DropdownContent>
                        </Dropdown>
                    </div>
                }
            />

            <div className="relative z-10 -mt-24 flex flex-col gap-12 px-6 md:px-10 xl:px-14">
                <PluginSlot slot="after-home-screen-toolbar" />

                {continueItems.length > 0 && <ContinueRow items={continueItems} onFocus={setHeroIdx} />}
                {isLoading && (
                    <div className="flex gap-5">
                        {Array.from({ length: 3 }).map((_, i) => (
                            <Skeleton key={i} className="aspect-video w-[440px]" />
                        ))}
                    </div>
                )}

                {(data?.genres?.length ?? 0) > 0 && <GenreBar genres={data!.genres!} value={genre} onChange={setGenre} />}

                {isEmpty && (
                    <Welcome
                        loggedIn={!!status?.loggedIn}
                        hasDir={!!status?.settings.library.dir}
                        onLogin={() => setLoginOpen(true)}
                    />
                )}
                {error && (
                    <EmptyState icon={<RefreshCw className="size-6" />} title="Couldn't load your library">
                        {(error as Error).message}
                    </EmptyState>
                )}

                {isLoading && (
                    <MediaGrid>
                        {Array.from({ length: 12 }).map((_, i) => (
                            <MediaCardSkeleton key={i} />
                        ))}
                    </MediaGrid>
                )}

                {lists.map(l => (
                    <section key={l.key} className="rise-in">
                        <div className="mb-5 flex items-center justify-between">
                            <h2 className="text-[1.65rem] font-bold tracking-tight">
                                {l.name} <span className="ml-1 text-base font-medium text-subtle">{l.items.length}</span>
                            </h2>
                        </div>
                        <MediaGrid size={status?.settings.ui.cardSize}>
                            {l.items.map(it => (
                                <MediaCard key={it.media.id} media={it.media} listEntry={it.listEntry} localCount={it.localFiles} downloaded={it.downloaded} />
                            ))}
                        </MediaGrid>
                    </section>
                ))}

                <PluginSlot slot="home-screen-bottom" />
            </div>
            <LoginDialog open={loginOpen} onOpenChange={setLoginOpen} />
        </div>
    )
}

function Hero({ item, count, index, onIndex, loading, toolbar }: { item: ContinueItem | null; count: number; index: number; onIndex: (i: number) => void; loading: boolean; toolbar: React.ReactNode }) {
    const img = item?.image ? imgUrl(item.image) : item ? banner(item.media) : ""
    return (
        <div className="relative h-[clamp(320px,46vh,520px)] w-full overflow-hidden">
            {img ? (
                <img key={img} src={img} alt="" className="absolute inset-0 size-full object-cover fade-in animate-slow-zoom" />
            ) : (
                <div className="absolute inset-0 bg-[radial-gradient(ellipse_at_20%_0%,color-mix(in_oklab,var(--brand)_35%,transparent),transparent_60%),radial-gradient(ellipse_at_90%_30%,color-mix(in_oklab,var(--brand)_18%,transparent),transparent_55%)]" />
            )}
            <div className="absolute inset-0 bg-gradient-to-t from-bg via-bg/55 to-transparent" />
            <div className="absolute inset-0 bg-gradient-to-r from-bg/70 via-transparent to-transparent" />
            <div className="absolute top-5 right-6 z-20">{toolbar}</div>
            <div className="absolute inset-x-0 bottom-32 px-6 md:px-10 xl:px-14">
                {loading ? (
                    <Skeleton className="h-12 w-96" />
                ) : item ? (
                    <div key={item.media.id} className="rise-in">
                        <p className="text-2xl font-semibold text-white/85 drop-shadow md:text-3xl">Continue watching</p>
                        <Link to={`/entry?id=${item.media.id}`} className="mt-1 block max-w-4xl text-4xl leading-tight font-extrabold text-white drop-shadow-xl hover:underline md:text-5xl">
                            {title(item.media)}
                        </Link>
                    </div>
                ) : (
                    <div className="rise-in">
                        <p className="text-2xl font-semibold text-white/80">Welcome to</p>
                        <h1 className="text-gradient text-5xl font-extrabold">Kumo</h1>
                    </div>
                )}
            </div>
            {count > 1 && (
                <div className="absolute right-6 bottom-32 z-20 flex items-center gap-2">
                    <button className="glass grid size-8 place-items-center rounded-full" onClick={() => onIndex((index - 1 + count) % count)}>
                        <ChevronLeft className="size-4" />
                    </button>
                    <div className="flex gap-1">
                        {Array.from({ length: Math.min(count, 8) }).map((_, i) => (
                            <span key={i} className={cn("h-1.5 rounded-full transition-all", i === index ? "w-5 bg-white" : "w-1.5 bg-white/35")} />
                        ))}
                    </div>
                    <button className="glass grid size-8 place-items-center rounded-full" onClick={() => onIndex((index + 1) % count)}>
                        <ChevronRight className="size-4" />
                    </button>
                </div>
            )}
        </div>
    )
}

function ContinueRow({ items, onFocus }: { items: ContinueItem[]; onFocus: (i: number) => void }) {
    const { playLocal, playStream } = usePlay()
    const { data: status } = useStatus()
    const navigate = useNavigate()
    const ref = useRef<HTMLDivElement>(null)
    const play = (it: ContinueItem) => {
        if (it.hasFile && it.filePath) playLocal(it.filePath, it.media.id, it.episode)
        else if (status?.settings.onlineStream.enabled) {
            const dub = status.settings.aniCli.defaultMode === "dub"
            playStream(status.settings.onlineStream.defaultProvider || "ani-cli", it.media.id, it.episode, dub)
        } else navigate(`/entry?id=${it.media.id}`)
    }
    return (
        <div ref={ref} className="no-scrollbar -mx-2 flex snap-x gap-6 overflow-x-auto px-2 pb-2">
            {items.map((it, i) => (
                <div key={it.media.id} className="w-[min(82vw,440px)] shrink-0 snap-start" onMouseEnter={() => onFocus(i)}>
                    <EpisodeCard
                        large
                        image={it.image}
                        number={it.episode}
                        title={it.title || (it.media.format === "MOVIE" ? title(it.media) : `Episode ${it.episode}`)}
                        subtitle={
                            <span>
                                <span className="font-semibold text-fg/90">Episode {it.episode}</span>
                                {it.total > 0 && <span className="text-subtle"> / {it.total}</span>}
                                <span className="text-subtle"> - {title(it.media)}</span>
                            </span>
                        }
                        runtime={it.runtime}
                        hasFile={it.hasFile}
                        progress={it.duration > 0 ? it.resumeAt / it.duration : 0}
                        resumeAt={it.resumeAt}
                        onClick={() => play(it)}
                    />
                </div>
            ))}
        </div>
    )
}

function GenreBar({ genres, value, onChange }: { genres: string[]; value: string | null; onChange: (g: string | null) => void }) {
    const ref = useRef<HTMLDivElement>(null)
    return (
        <div className="relative flex items-center">
            <div ref={ref} className="no-scrollbar flex flex-1 gap-2 overflow-x-auto scroll-smooth py-1">
                <button
                    onClick={() => onChange(null)}
                    className={cn("h-9 shrink-0 rounded-full px-4 text-[15px] font-medium transition", !value ? "bg-white text-black" : "text-muted hover:bg-white/[0.06] hover:text-fg")}
                >
                    All
                </button>
                {genres.map(g => (
                    <button
                        key={g}
                        onClick={() => onChange(value === g ? null : g)}
                        className={cn("h-9 shrink-0 rounded-full px-4 text-[15px] font-medium transition", value === g ? "bg-white text-black" : "text-muted hover:bg-white/[0.06] hover:text-fg")}
                    >
                        {g}
                    </button>
                ))}
            </div>
            <button onClick={() => ref.current?.scrollBy({ left: 400, behavior: "smooth" })} className="ml-2 grid size-9 shrink-0 place-items-center rounded-full text-muted hover:bg-white/[0.06] hover:text-fg">
                <ChevronRight className="size-5" />
            </button>
        </div>
    )
}

function Welcome({ loggedIn, hasDir, onLogin }: { loggedIn: boolean; hasDir: boolean; onLogin: () => void }) {
    const steps = [
        { done: loggedIn, icon: <LogIn className="size-5" />, title: "Connect AniList", text: "Sync your lists, progress and scores (optional — Kumo works offline too).", action: <Button size="sm" variant="primary" onClick={onLogin}>Log in</Button> },
        { done: hasDir, icon: <FolderOpen className="size-5" />, title: "Choose your library folder", text: "Kumo scans it, matches every file to AniList and grabs artwork, descriptions and episode titles.", action: <Link to="/settings?tab=library"><Button size="sm">Open settings</Button></Link> },
        { done: false, icon: <Puzzle className="size-5" />, title: "Add extensions", text: "Install streaming, torrent and manga providers or plugins from the marketplace.", action: <Link to="/extensions?tab=marketplace"><Button size="sm">Browse marketplace</Button></Link> },
    ]
    return (
        <div className="card overflow-hidden rise-in">
            <div className="bg-[radial-gradient(ellipse_at_top_left,color-mix(in_oklab,var(--brand)_22%,transparent),transparent_60%)] p-8">
                <h2 className="text-3xl font-extrabold">Let’s set things up</h2>
                <p className="mt-1 text-muted">Three quick steps and your anime library is ready.</p>
                <div className="mt-6 grid gap-4 md:grid-cols-3">
                    {steps.map((s, i) => (
                        <div key={i} className={cn("flex flex-col gap-3 rounded-2xl border border-line bg-surface-2/70 p-5", s.done && "opacity-60")}>
                            <div className="flex items-center gap-3">
                                <span className={cn("grid size-10 place-items-center rounded-xl", s.done ? "bg-emerald-500/15 text-emerald-300" : "bg-brand-soft text-brand-strong")}>{s.icon}</span>
                                <span className="text-xs font-semibold tracking-wider text-subtle uppercase">Step {i + 1}{s.done ? " · done" : ""}</span>
                            </div>
                            <p className="font-semibold">{s.title}</p>
                            <p className="flex-1 text-sm text-muted">{s.text}</p>
                            {!s.done && <div>{s.action}</div>}
                        </div>
                    ))}
                </div>
            </div>
        </div>
    )
}
