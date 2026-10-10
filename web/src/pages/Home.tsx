import { ArrowDownUp, ChevronLeft, ChevronRight, Download, FolderOpen, FolderSearch, FolderSync, HardDrive, LibraryBig, ListFilter, LogIn, MoreVertical, Puzzle, RefreshCw, Settings2, Sparkles, X } from "lucide-react"
import { useMemo, useRef, useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { EpisodeCard } from "@/components/EpisodeCard"
import { LoginDialog } from "@/components/LoginDialog"
import { MediaCard, MediaCardSkeleton, MediaGrid } from "@/components/MediaCard"
import { useLibraryCardMenu } from "@/components/library/LibraryCardMenu"
import { PluginSlot } from "@/components/plugins/PluginSlot"
import { ProgramsNotice } from "@/components/ProgramsNotice"
import { Button, Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger, EmptyState, IconButton, Skeleton } from "@/components/ui"
import { UpdateBanner } from "@/components/UpdateBanner"
import { api } from "@/lib/api"
import { usePersisted } from "@/lib/hooks"
import { usePlay } from "@/lib/play"
import { fetchLanguageMode, useCollection, useScan, useStatus } from "@/lib/queries"
import { scanStore, useStore } from "@/lib/store"
import type { CollectionItem, CollectionView, ContinueItem } from "@/lib/types"
import { banner, cn, cover, img as imgUrl, title } from "@/lib/utils"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "@/lib/toast"

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
    const libraryMenu = useLibraryCardMenu()

    const continueItems = data?.continueWatching ?? []
    const [heroIdx, setHeroIdx] = useState(0)
    // Removing items can leave the index past the end.
    const heroAt = Math.min(heroIdx, Math.max(0, continueItems.length - 1))
    const hero = continueItems[heroAt] ?? null

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
        // Logged out, the list is the local one: there is nothing to sync.
        if (!status?.loggedIn) {
            toast.error("Log in to AniList to sync your lists", { action: { label: "Log in", onClick: () => setLoginOpen(true) } })
            return
        }
        const id = toast.loading("Syncing with AniList…")
        try {
            await api.get("/api/anime/collection?refresh=1")
            qc.invalidateQueries({ queryKey: ["collection"] })
            toast.success("Synced with AniList", { id })
        } catch (e: any) {
            toast.error(e.message, { id })
        }
    }

    const isEmpty = !isLoading && !error && (data?.lists?.length ?? 0) === 0 && (data?.localOnly?.length ?? 0) === 0
    const filtered = !!genre || onlyLocal
    const clearFilters = () => {
        setGenre(null)
        setOnlyLocal(false)
    }

    return (
        <div className="relative min-h-full pb-24">
            <UpdateBanner />
            <Hero
                item={hero}
                count={continueItems.length}
                index={heroAt}
                onIndex={setHeroIdx}
                loading={isLoading}
                toolbar={
                    <div className="glass flex items-center gap-0.5 rounded-lg p-1 [&_button]:text-white/80 [&_button:hover]:bg-white/10 [&_button:hover]:text-white [&_button[data-state=open]]:bg-white/10">
                        <Button size="sm" variant="ghost" icon={<HardDrive className="size-4" />} onClick={() => navigate("/local")}>
                            Local library
                        </Button>
                        <IconButton label="Library tools" size="sm" onClick={() => navigate("/library")}>
                            <LibraryBig className="size-4" />
                        </IconButton>
                        <IconButton label={`Unmatched files (${data?.unmatchedCount ?? 0})`} size="sm" className="relative" onClick={() => navigate("/library?tab=unmatched")}>
                            <FolderSearch className="size-4" />
                            {!!data?.unmatchedCount && <span className="absolute top-1 right-1 size-2 rounded-full bg-amber-400" />}
                        </IconButton>
                        <IconButton label="Downloads" size="sm" onClick={() => navigate("/downloads")}>
                            <Download className="size-4" />
                        </IconButton>
                        <Button size="sm" variant="ghost" loading={scanning || scan.isPending} icon={<FolderSync className="size-4" />} onClick={() => scan.mutate(false)}>
                            Refresh
                        </Button>
                        <Dropdown>
                            <DropdownTrigger asChild>
                                <IconButton label="Display" size="sm">
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
                                <IconButton label="More" size="sm">
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

            <div className="relative z-10 -mt-20 flex flex-col gap-10 px-6 md:px-8 xl:px-10">
                <PluginSlot slot="after-home-screen-toolbar" />
                <ProgramsNotice />

                {continueItems.length > 0 && <ContinueRow items={continueItems} onFocus={setHeroIdx} />}
                {isLoading && (
                    <div className="flex gap-5">
                        {Array.from({ length: 3 }).map((_, i) => (
                            <Skeleton key={i} className="aspect-video w-[400px]" />
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

                {!isLoading && !error && !isEmpty && filtered && lists.length === 0 && (
                    <EmptyState
                        icon={<ListFilter className="size-6" />}
                        title="Nothing matches these filters"
                        action={
                            <Button icon={<X className="size-4" />} onClick={clearFilters}>
                                Clear filters
                            </Button>
                        }
                    >
                        {genre ? `${onlyLocal ? "None of your downloaded anime" : "Nothing in your lists"} is tagged ${genre}.` : "None of the anime in your lists are downloaded yet."}
                    </EmptyState>
                )}

                {lists.map(l => (
                    <section key={l.key} className="fade-in">
                        <div className="mb-4 flex items-baseline gap-2.5">
                            <h2 className="text-xl font-semibold tracking-tight">{l.name}</h2>
                            <span className="text-sm text-subtle tabular-nums">{l.items.length}</span>
                        </div>
                        <MediaGrid size={status?.settings.ui.cardSize}>
                            {l.items.map(it => (
                                <MediaCard
                                    key={it.media.id}
                                    media={it.media}
                                    listEntry={it.listEntry}
                                    localCount={it.localFiles}
                                    downloaded={it.downloaded}
                                    // Here only because of local files, which the menu acts on.
                                    menu={l.key === "LOCAL" ? libraryMenu.menu(it.media) : undefined}
                                />
                            ))}
                        </MediaGrid>
                    </section>
                ))}

                <PluginSlot slot="home-screen-bottom" />
            </div>
            <LoginDialog open={loginOpen} onOpenChange={setLoginOpen} />
            {libraryMenu.dialog}
        </div>
    )
}

function Hero({ item, count, index, onIndex, loading, toolbar }: { item: ContinueItem | null; count: number; index: number; onIndex: (i: number) => void; loading: boolean; toolbar: React.ReactNode }) {
    // The sharp AniList banner, like the anime page: episode thumbnails are
    // often small, blurry screenshots.
    const img = !item ? "" : item.media.bannerImage ? banner(item.media) : imgUrl(item.image) || cover(item.media)
    return (
        <div className="relative h-[clamp(300px,44vh,480px)] w-full overflow-hidden">
            {img ? (
                <img key={img} src={img} alt="" className="absolute inset-0 size-full object-cover fade-in" />
            ) : (
                <div className="absolute inset-0 bg-surface-1" />
            )}
            <div className="absolute inset-0 bg-gradient-to-t from-bg via-bg/60 to-bg/5" />
            <div className="absolute inset-0 bg-gradient-to-r from-bg/80 via-bg/25 to-transparent" />
            <div className="absolute top-4 right-4 z-20">{toolbar}</div>
            <div className="absolute inset-x-0 bottom-28 px-6 md:px-8 xl:px-10">
                {loading ? (
                    <Skeleton className="h-11 w-96" />
                ) : item ? (
                    <div key={item.media.id} className="max-w-3xl rise-in">
                        <p className="text-sm font-medium text-white/65">Continue watching</p>
                        {/* On a TV the cards below are what's picked (the banner follows them). */}
                        <Link to={`/entry?id=${item.media.id}`} data-tv-skip-nav className="mt-1.5 block text-4xl leading-[1.1] font-semibold tracking-tight text-white transition-colors hover:text-white/85 md:text-[2.75rem]">
                            {title(item.media)}
                        </Link>
                    </div>
                ) : (
                    <div className="rise-in">
                        <p className="text-sm font-medium text-white/65">Welcome to</p>
                        <h1 className="mt-1 text-4xl font-semibold tracking-tight">Kumo</h1>
                    </div>
                )}
            </div>
            {count > 1 && (
                <div data-tv-skip-nav className="absolute right-4 bottom-28 z-20 flex items-center gap-3 md:right-8 xl:right-10">
                    <div className="flex gap-1.5">
                        {Array.from({ length: Math.min(count, 8) }).map((_, i) => (
                            <button
                                key={i}
                                aria-label={`Show ${i + 1}`}
                                onClick={() => onIndex(i)}
                                className={cn("h-1 rounded-full transition-all duration-300", i === index ? "w-5 bg-white" : "w-2.5 bg-white/30 hover:bg-white/50")}
                            />
                        ))}
                    </div>
                    <div className="flex gap-1">
                        <button aria-label="Previous" className="glass grid size-8 place-items-center rounded-full transition-colors hover:bg-black/70" onClick={() => onIndex((index - 1 + count) % count)}>
                            <ChevronLeft className="size-4" />
                        </button>
                        <button aria-label="Next" className="glass grid size-8 place-items-center rounded-full transition-colors hover:bg-black/70" onClick={() => onIndex((index + 1) % count)}>
                            <ChevronRight className="size-4" />
                        </button>
                    </div>
                </div>
            )}
        </div>
    )
}

function ContinueRow({ items, onFocus }: { items: ContinueItem[]; onFocus: (i: number) => void }) {
    const { playLocal, playStream } = usePlay()
    const { data: status } = useStatus()
    const navigate = useNavigate()
    const qc = useQueryClient()
    const ref = useRef<HTMLDivElement>(null)
    const play = async (it: ContinueItem) => {
        if (it.hasFile && it.filePath) playLocal(it.filePath, it.media.id, it.episode)
        else if (status?.settings.onlineStream.enabled) {
            // Same provider and sub/dub as last time for this anime.
            const provider = localStorage.getItem(`kumo-provider-${it.media.id}`) || status.settings.onlineStream.defaultProvider || "ani-cli"
            const dub = await fetchLanguageMode(it.media.id, status.settings.aniCli.defaultMode === "dub")
            playStream(provider, it.media.id, it.episode, dub)
        } else navigate(`/entry?id=${it.media.id}`)
    }
    // The server hides it until it offers another episode or the anime is
    // watched again. This row and the hero both show the collection's list,
    // so it is edited right away (a refetch in flight would undo that).
    const remove = async (it: ContinueItem, at: number) => {
        const edit = (fn: (list: ContinueItem[]) => ContinueItem[]) =>
            qc.setQueryData<CollectionView>(["collection"], old => (old ? { ...old, continueWatching: fn(old.continueWatching ?? []) } : old))
        const refresh = () => qc.invalidateQueries({ queryKey: ["collection"] })
        await qc.cancelQueries({ queryKey: ["collection"] })
        edit(list => list.filter(x => x.media.id !== it.media.id))
        const hidden = api.post("/api/continue/hide", { mediaId: it.media.id, episode: it.episode })
        hidden.catch(e => toast.error(e.message)).finally(refresh)
        toast.success("Removed from Continue watching", {
            action: {
                label: "Undo",
                onClick: async () => {
                    await qc.cancelQueries({ queryKey: ["collection"] })
                    edit(list => (list.some(x => x.media.id === it.media.id) ? list : [...list.slice(0, at), it, ...list.slice(at)]))
                    await hidden.catch(() => {}) // unhide after the hide, never before
                    api.post("/api/continue/unhide", { mediaId: it.media.id })
                        .catch(e => toast.error(e.message))
                        .finally(refresh)
                },
            },
        })
    }
    return (
        <div ref={ref} className="no-scrollbar -mx-2 flex snap-x scroll-px-2 gap-5 overflow-x-auto px-2 pb-2">
            {items.map((it, i) => (
                <div key={it.media.id} className="w-[min(82vw,400px)] shrink-0 snap-start" onMouseEnter={() => onFocus(i)} onFocus={() => onFocus(i)}>
                    <EpisodeCard
                        large
                        image={it.image}
                        number={it.episode}
                        title={it.title || (it.media.format === "MOVIE" ? title(it.media) : `Episode ${it.episode}`)}
                        subtitle={
                            <span>
                                <span className="text-fg/80">Episode {it.episode}</span>
                                {it.total > 0 && <span className="text-subtle"> of {it.total}</span>}
                                <span className="text-subtle"> · {title(it.media)}</span>
                            </span>
                        }
                        runtime={it.runtime}
                        hasFile={it.hasFile}
                        progress={it.duration > 0 ? it.resumeAt / it.duration : 0}
                        resumeAt={it.resumeAt}
                        onClick={() => play(it)}
                        onRemove={() => remove(it, i)}
                        onInfo={() => navigate(`/entry?id=${it.media.id}`)}
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
                    className={cn("h-8 shrink-0 rounded-full px-3.5 text-[13px] font-medium transition-colors", !value ? "bg-white text-neutral-950" : "bg-white/[0.05] text-muted hover:bg-white/[0.09] hover:text-fg")}
                >
                    All
                </button>
                {genres.map(g => (
                    <button
                        key={g}
                        onClick={() => onChange(value === g ? null : g)}
                        className={cn("h-8 shrink-0 rounded-full px-3.5 text-[13px] font-medium transition-colors", value === g ? "bg-white text-neutral-950" : "bg-white/[0.05] text-muted hover:bg-white/[0.09] hover:text-fg")}
                    >
                        {g}
                    </button>
                ))}
            </div>
            <button onClick={() => ref.current?.scrollBy({ left: 400, behavior: "smooth" })} data-tv-skip-nav aria-label="More genres" className="ml-2 grid size-8 shrink-0 place-items-center rounded-full text-muted transition-colors hover:bg-white/[0.06] hover:text-fg">
                <ChevronRight className="size-4" />
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
            <div className="p-7">
                <h2 className="text-xl font-semibold tracking-tight">Let’s set things up</h2>
                <p className="mt-1 text-sm text-muted">Three quick steps and your anime library is ready.</p>
                <div className="mt-6 grid gap-3 md:grid-cols-3">
                    {steps.map((s, i) => (
                        <div key={i} className={cn("flex flex-col gap-3 rounded-lg bg-white/[0.03] p-5", s.done && "opacity-60")}>
                            <div className="flex items-center gap-3">
                                <span className={cn("grid size-9 place-items-center rounded-lg [&_svg]:size-[18px]", s.done ? "bg-emerald-500/15 text-emerald-300" : "bg-white/[0.06] text-fg/80")}>{s.icon}</span>
                                <span className="text-xs text-subtle">Step {i + 1}{s.done ? " · done" : ""}</span>
                            </div>
                            <p className="font-medium">{s.title}</p>
                            <p className="flex-1 text-sm text-muted">{s.text}</p>
                            {!s.done && <div>{s.action}</div>}
                        </div>
                    ))}
                </div>
            </div>
        </div>
    )
}
