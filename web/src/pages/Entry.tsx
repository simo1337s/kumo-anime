import { useQueryClient } from "@tanstack/react-query"
import {
    CalendarClock,
    CheckCheck,
    ExternalLink,
    Film,
    FolderOpen,
    HardDrive,
    Info,
    Magnet,
    MonitorPlay,
    MoreHorizontal,
    Play,
    RefreshCw,
    Tv,
    Users,
} from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { Link, useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import { EpisodeCard, WatchedToggle } from "@/components/EpisodeCard"
import { ListStatusButton, ProgressEditor, ScoreEditor } from "@/components/entry/ListEditor"
import { StreamPanel } from "@/components/entry/StreamPanel"
import { TorrentPanel } from "@/components/entry/TorrentPanel"
import { Carousel, MediaCard } from "@/components/MediaCard"
import { PluginActions, PluginSlot, usePluginDropdownActions } from "@/components/plugins/PluginSlot"
import { sendPluginEvent } from "@/components/plugins/PluginRenderer"
import { Badge, Button, Dropdown, DropdownContent, DropdownItem, DropdownSeparator, DropdownTrigger, EmptyState, Skeleton, Tabs } from "@/components/ui"
import { api } from "@/lib/api"
import { usePlay } from "@/lib/play"
import { useEntry, useEpisodeMarker, useStatus } from "@/lib/queries"
import type { EntryView } from "@/lib/types"
import { banner, cleanDescription, cn, cover, formatLabel, img, scoreColor, seasonLabel, statusLabel, timeUntil, title, totalEpisodes } from "@/lib/utils"

type Tab = "episodes" | "stream" | "torrents" | "details"

export default function EntryPage() {
    const [params, setParams] = useSearchParams()
    const id = Number(params.get("id"))
    const { data: entry, isLoading, error } = useEntry(id)
    const { data: status } = useStatus()

    useEffect(() => {
        document.getElementById("main-scroll")?.scrollTo({ top: 0 })
    }, [id])

    const defaultTab: Tab = entry ? (entry.localCount > 0 ? "episodes" : status?.settings.onlineStream.enabled !== false ? "stream" : "torrents") : "episodes"
    const tab = (params.get("tab") as Tab) || defaultTab
    const setTab = (t: Tab) =>
        setParams(
            p => {
                p.set("tab", t)
                return p
            },
            { replace: true },
        )

    if (isLoading) return <EntrySkeleton />
    if (error || !entry) {
        return (
            <div className="p-10">
                <EmptyState icon={<Info className="size-6" />} title="Couldn't load this anime">
                    {(error as Error)?.message ?? "Not found"}
                </EmptyState>
            </div>
        )
    }

    const tabs: { value: Tab; label: string; icon: React.ReactNode; count?: number }[] = [
        { value: "episodes", label: "Library", icon: <HardDrive className="size-4" />, count: entry.localCount || undefined },
        ...(status?.settings.onlineStream.enabled !== false ? [{ value: "stream" as Tab, label: "Watch online", icon: <Tv className="size-4" /> }] : []),
        { value: "torrents", label: "Torrents", icon: <Magnet className="size-4" /> },
        { value: "details", label: "Details", icon: <Info className="size-4" /> },
    ]

    return (
        <div className="relative min-h-full pb-24">
            <EntryHero entry={entry} onTab={setTab} />
            <div className="relative z-10 flex flex-col gap-8 px-6 md:px-10 xl:px-14">
                <PluginSlot slot="before-anime-entry-episode-list" />
                <Tabs value={tab} onChange={setTab} tabs={tabs} className="self-start" />
                {/* Keyed by anime too: panels keep per-anime state (dub, search results). */}
                <div key={`${entry.media.id}-${tab}`} className="fade-in">
                    {tab === "episodes" && <EpisodesTab entry={entry} onStream={() => setTab("stream")} onTorrents={() => setTab("torrents")} />}
                    {tab === "stream" && <StreamPanel entry={entry} />}
                    {tab === "torrents" && <TorrentPanel entry={entry} />}
                    {tab === "details" && <DetailsTab entry={entry} />}
                </div>
                <PluginSlot slot="after-anime-entry-episode-list" />
                <PluginSlot slot="after-anime-episode-list" />
                <PluginSlot slot="anime-screen-bottom" />
            </div>
        </div>
    )
}

function EntryHero({ entry, onTab }: { entry: EntryView; onTab: (t: Tab) => void }) {
    const media = entry.media
    const { data: status } = useStatus()
    const { playLocal, playStream } = usePlay()
    const [expanded, setExpanded] = useState(false)
    const qc = useQueryClient()
    const dropdownActions = usePluginDropdownActions("anime-page-dropdown")
    const next = entry.nextEpisode
    const desc = cleanDescription(media.description)
    const total = totalEpisodes(media)
    const score = media.meanScore ?? media.averageScore
    const bannerImg = entry.images?.fanart ? img(entry.images.fanart) : banner(media)

    const watch = () => {
        if (!next) return
        if (next.file) playLocal(next.file.path, media.id, next.number)
        else if (status?.settings.onlineStream.enabled !== false) {
            const provider = localStorage.getItem(`kumo-provider-${media.id}`) || status?.settings.onlineStream.defaultProvider || "ani-cli"
            const savedDub = localStorage.getItem(`kumo-dub-${media.id}`)
            const dub = savedDub !== null ? savedDub === "1" : status?.settings.aniCli.defaultMode === "dub"
            playStream(provider, media.id, next.number, dub)
        } else onTab("torrents")
    }

    const refresh = async () => {
        await api.get(`/api/anime/${media.id}?refresh=1`)
        qc.invalidateQueries({ queryKey: ["entry", media.id] })
        toast.success("Metadata refreshed")
    }

    return (
        <div className="relative">
            <div className="absolute inset-x-0 top-0 h-[520px] overflow-hidden">
                {bannerImg && <img src={bannerImg} alt="" className="size-full object-cover opacity-70 fade-in" />}
                <div className="absolute inset-0 bg-gradient-to-t from-bg via-bg/70 to-bg/10" />
                <div className="absolute inset-0 bg-gradient-to-r from-bg/90 via-bg/30 to-transparent" />
            </div>
            <div className="relative z-10 flex flex-col gap-8 px-6 pt-[220px] pb-10 md:flex-row md:px-10 xl:px-14">
                <div className="w-44 shrink-0 md:w-56">
                    <img src={cover(media)} alt="" className="aspect-[2/3] w-full rounded-2xl object-cover shadow-[0_30px_60px_-20px_rgb(0_0_0/0.9)] ring-1 ring-white/10 rise-in" />
                </div>
                <div className="flex min-w-0 flex-1 flex-col justify-end gap-4 rise-in">
                    <div>
                        <h1 className="text-3xl leading-tight font-extrabold text-white drop-shadow-xl md:text-5xl">{title(media)}</h1>
                        {media.title.english && media.title.english !== title(media) && <p className="mt-1.5 text-lg text-white/70">{media.title.english}</p>}
                    </div>
                    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm text-white/80">
                        {score ? <span className={cn("text-base font-bold", scoreColor(score))}>{score}%</span> : null}
                        <span>{formatLabel(media.format)}</span>
                        {media.season && <span>{seasonLabel(media.season, media.seasonYear)}</span>}
                        {total > 0 && <span>{total} episodes</span>}
                        {media.duration && <span>{media.duration} min</span>}
                        <Badge tone={media.status === "RELEASING" ? "green" : "gray"}>{statusLabel(media.status)}</Badge>
                        {media.studios?.nodes?.[0] && <span className="text-white/60">{media.studios.nodes.map(s => s.name).join(", ")}</span>}
                    </div>
                    {media.nextAiringEpisode && (
                        <div className="flex items-center gap-2 text-sm font-medium text-brand-strong">
                            <CalendarClock className="size-4" /> Episode {media.nextAiringEpisode.episode} airs in {timeUntil(media.nextAiringEpisode.timeUntilAiring)}
                        </div>
                    )}
                    <div className="flex flex-wrap gap-2">
                        {media.genres?.map(g => (
                            <Link key={g} to={`/search?genre=${encodeURIComponent(g)}`} className="rounded-full border border-white/10 bg-white/[0.06] px-3 py-1 text-xs font-medium text-white/85 backdrop-blur hover:bg-white/[0.12]">
                                {g}
                            </Link>
                        ))}
                    </div>
                    {desc && (
                        <div className="max-w-4xl">
                            <p className={cn("text-[15px] leading-relaxed whitespace-pre-line text-white/75", !expanded && "line-clamp-3")}>{desc}</p>
                            {desc.length > 260 && (
                                <button onClick={() => setExpanded(e => !e)} className="mt-1 text-sm font-medium text-brand-strong hover:underline">
                                    {expanded ? "Show less" : "Read more"}
                                </button>
                            )}
                        </div>
                    )}
                    <div className="mt-1 flex flex-wrap items-center gap-2.5">
                        {next && (
                            <Button variant="white" size="lg" icon={<Play className="size-5 fill-black" />} onClick={watch}>
                                {next.resumeAt > 0 ? "Resume" : entry.listEntry?.progress ? "Continue" : "Watch"} · Ep {next.number}
                            </Button>
                        )}
                        <ListStatusButton media={media} entry={entry.listEntry} />
                        {entry.listEntry && <ProgressEditor media={media} entry={entry.listEntry} />}
                        <ScoreEditor media={media} entry={entry.listEntry} />
                        <PluginActions kind="anime-page-button" mediaId={media.id} />
                        <Dropdown>
                            <DropdownTrigger asChild>
                                <Button variant="subtle" size="lg" className="glass w-12 px-0" aria-label="More">
                                    <MoreHorizontal className="size-5" />
                                </Button>
                            </DropdownTrigger>
                            <DropdownContent align="start">
                                {entry.localCount > 0 && status?.client !== "lan" && (
                                    <DropdownItem icon={<FolderOpen />} onSelect={() => api.post("/api/library/open-folder", { mediaId: media.id }).catch(e => toast.error(e.message))}>
                                        Open folder
                                    </DropdownItem>
                                )}
                                <DropdownItem icon={<ExternalLink />} onSelect={() => window.open(`https://anilist.co/anime/${media.id}`, "_blank", "noopener")}>
                                    View on AniList
                                </DropdownItem>
                                {media.idMal && (
                                    <DropdownItem icon={<ExternalLink />} onSelect={() => window.open(`https://myanimelist.net/anime/${media.idMal}`, "_blank", "noopener")}>
                                        View on MyAnimeList
                                    </DropdownItem>
                                )}
                                <DropdownItem icon={<RefreshCw />} onSelect={refresh}>
                                    Refresh metadata
                                </DropdownItem>
                                {dropdownActions.length > 0 && <DropdownSeparator />}
                                {dropdownActions.map(a => (
                                    <DropdownItem key={a.pluginId + a.id} onSelect={() => sendPluginEvent(a.pluginId, { kind: "action", actionId: a.id, mediaId: media.id })}>
                                        {a.props.label}
                                    </DropdownItem>
                                ))}
                            </DropdownContent>
                        </Dropdown>
                    </div>
                </div>
            </div>
        </div>
    )
}

function EpisodesTab({ entry, onStream, onTorrents }: { entry: EntryView; onStream: () => void; onTorrents: () => void }) {
    const { playLocal, localPlayer, remote } = usePlay()
    const { data: status } = useStatus()
    const marker = useEpisodeMarker(entry.media, entry.listEntry)
    const [showAll, setShowAll] = useState(false)
    const media = entry.media

    const withFiles = entry.episodes.filter(e => e.hasFile)
    const list = showAll ? entry.episodes : withFiles
    const specials = [...(entry.specials ?? []), ...(entry.others ?? [])]

    if (entry.localCount === 0) {
        return (
            <EmptyState
                icon={<HardDrive className="size-6" />}
                title="No local files yet"
                action={
                    <div className="flex gap-2">
                        {status?.settings.onlineStream.enabled !== false && (
                            <Button variant="primary" icon={<Tv className="size-4" />} onClick={onStream}>
                                Watch online
                            </Button>
                        )}
                        <Button icon={<Magnet className="size-4" />} onClick={onTorrents}>
                            Find torrents
                        </Button>
                    </div>
                }
            >
                Download episodes with ani-cli or a torrent and they will show up here automatically.
            </EmptyState>
        )
    }

    return (
        <div className="flex flex-col gap-8">
            <div className="flex items-center justify-between">
                <p className="text-sm text-muted">
                    {withFiles.length} of {entry.episodes.length || "?"} episodes downloaded
                </p>
                {entry.episodes.length > withFiles.length && (
                    <button onClick={() => setShowAll(s => !s)} className="text-sm font-medium text-brand-strong hover:underline">
                        {showAll ? "Show downloaded only" : "Show all episodes"}
                    </button>
                )}
            </div>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-x-6 gap-y-8">
                {list.map(ep => (
                    <EpisodeCard
                        key={ep.number}
                        image={ep.image}
                        number={ep.number}
                        title={media.format === "MOVIE" ? title(media) : ep.title || `Episode ${ep.number}`}
                        subtitle={ep.file ? ep.file.name : `Episode ${ep.number}${ep.airDate ? ` · ${ep.airDate}` : ""}`}
                        runtime={ep.runtime}
                        watched={ep.watched}
                        hasFile={ep.hasFile}
                        aired={ep.hasFile || ep.aired}
                        resumeAt={ep.resumeAt}
                        progress={ep.history?.duration ? ep.resumeAt / ep.history.duration : 0}
                        blur={status?.settings.ui.blurUnwatched}
                        onClick={() => (ep.file ? playLocal(ep.file.path, media.id, ep.number) : onStream())}
                        actions={
                            <div className="flex gap-1">
                                {(ep.aired || ep.hasFile) && <WatchedToggle watched={ep.watched} disabled={marker.pending} onToggle={() => marker.mark(ep.number, !ep.watched)} />}
                                <Dropdown>
                                    <DropdownTrigger asChild>
                                        <button onClick={e => e.stopPropagation()} className="grid size-8 place-items-center rounded-lg bg-black/60 text-white backdrop-blur hover:bg-black/80">
                                            <MoreHorizontal className="size-4" />
                                        </button>
                                    </DropdownTrigger>
                                    <DropdownContent>
                                        {ep.file && !remote && (
                                            <DropdownItem icon={<MonitorPlay />} onSelect={() => playLocal(ep.file!.path, media.id, ep.number, { player: localPlayer() === "mpv" ? "builtin" : "mpv" })}>
                                                Play in {localPlayer() === "mpv" ? "in-app player" : "mpv"}
                                            </DropdownItem>
                                        )}
                                        {ep.file && ep.resumeAt > 0 && (
                                            <DropdownItem icon={<Play />} onSelect={() => playLocal(ep.file!.path, media.id, ep.number, { start: 0 })}>
                                                Play from the beginning
                                            </DropdownItem>
                                        )}
                                        <DropdownItem icon={<CheckCheck />} onSelect={() => marker.mark(ep.number, !ep.watched)}>
                                            {ep.watched ? "Mark as unwatched" : "Mark as watched (and everything before)"}
                                        </DropdownItem>
                                    </DropdownContent>
                                </Dropdown>
                            </div>
                        }
                    />
                ))}
            </div>
            {specials.length > 0 && (
                <div>
                    <h3 className="mb-4 text-xl font-bold">Specials & extras</h3>
                    <div className="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-x-5 gap-y-6">
                        {specials.map(sp => (
                            <EpisodeCard
                                key={sp.file?.path}
                                image={media.bannerImage || cover(media)}
                                number={sp.number}
                                title={sp.title}
                                subtitle={sp.kind === "nc" ? "Opening / ending" : "Special"}
                                hasFile
                                onClick={() => sp.file && playLocal(sp.file.path, media.id, 0)}
                            />
                        ))}
                    </div>
                </div>
            )}
            {entry.metadataNote && <p className="text-xs text-subtle">{entry.metadataNote}</p>}
        </div>
    )
}

function DetailsTab({ entry }: { entry: EntryView }) {
    const media = entry.media
    const relations = (media.relations?.edges ?? []).filter(e => e.node && ["ANIME", "MANGA"].includes(e.node.type))
    const recs = (media.recommendations?.nodes ?? []).map(n => n.mediaRecommendation).filter(Boolean)
    const chars = media.characters?.edges ?? []
    const tags = (media.tags ?? []).filter(t => !t.isMediaSpoiler).slice(0, 18)
    const rankings = useMemo(() => (media.rankings ?? []).slice(0, 4), [media.rankings])
    return (
        <div className="flex flex-col gap-10">
            <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
                <InfoCard label="Format" value={formatLabel(media.format)} icon={<Film className="size-4" />} />
                <InfoCard label="Source" value={(media.source ?? "").replace(/_/g, " ").toLowerCase()} icon={<Info className="size-4" />} />
                <InfoCard
                    label="Aired"
                    value={[media.startDate, media.endDate]
                        .map(d => (d?.year ? `${d.year}-${String(d.month ?? 1).padStart(2, "0")}-${String(d.day ?? 1).padStart(2, "0")}` : ""))
                        .filter(Boolean)
                        .join(" → ")}
                    icon={<CalendarClock className="size-4" />}
                />
                <InfoCard label="Popularity" value={media.popularity ? media.popularity.toLocaleString() : "—"} icon={<Users className="size-4" />} />
            </div>
            {rankings.length > 0 && (
                <div className="flex flex-wrap gap-2">
                    {rankings.map((r, i) => (
                        <Badge key={i} tone="brand">
                            #{r.rank} {r.context} {r.season ? seasonLabel(r.season, r.year) : r.year ?? ""}
                        </Badge>
                    ))}
                </div>
            )}
            {tags.length > 0 && (
                <div>
                    <h3 className="mb-3 text-lg font-bold">Tags</h3>
                    <div className="flex flex-wrap gap-2">
                        {tags.map(t => (
                            <span key={t.name} className="rounded-lg border border-line bg-surface-1 px-3 py-1.5 text-sm">
                                {t.name} <span className="text-subtle">{t.rank}%</span>
                            </span>
                        ))}
                    </div>
                </div>
            )}
            {chars.length > 0 && (
                <div>
                    <h3 className="mb-4 text-lg font-bold">Characters</h3>
                    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                        {chars.map(c => (
                            <div key={c.node.id} className="card flex items-center justify-between gap-3 overflow-hidden p-0">
                                <div className="flex items-center gap-3">
                                    <img src={img(c.node.image.large)} alt="" loading="lazy" className="h-20 w-14 object-cover" />
                                    <div>
                                        <p className="text-sm font-semibold">{c.node.name.full}</p>
                                        <p className="text-xs text-subtle capitalize">{c.role.toLowerCase()}</p>
                                    </div>
                                </div>
                                {c.voiceActors[0] && (
                                    <div className="flex items-center gap-3 text-right">
                                        <div>
                                            <p className="text-sm font-medium">{c.voiceActors[0].name.full}</p>
                                            <p className="text-xs text-subtle">Japanese</p>
                                        </div>
                                        <img src={img(c.voiceActors[0].image.large)} alt="" loading="lazy" className="h-20 w-14 object-cover" />
                                    </div>
                                )}
                            </div>
                        ))}
                    </div>
                </div>
            )}
            {relations.length > 0 && (
                <div>
                    <h3 className="mb-4 text-lg font-bold">Related</h3>
                    <Carousel>
                        {relations.map(r => (
                            <div key={r.node.id}>
                                <p className="mb-2 text-xs font-semibold tracking-wider text-brand-strong uppercase">{r.relationType.replace(/_/g, " ")}</p>
                                <MediaCard media={r.node} />
                            </div>
                        ))}
                    </Carousel>
                </div>
            )}
            {recs.length > 0 && (
                <div>
                    <h3 className="mb-4 text-lg font-bold">Recommendations</h3>
                    <Carousel>{recs.map(m => <MediaCard key={m!.id} media={m!} />)}</Carousel>
                </div>
            )}
            {media.trailer?.site === "youtube" && (
                <a href={`https://www.youtube.com/watch?v=${media.trailer.id}`} target="_blank" rel="noopener noreferrer" className="group relative block w-full max-w-xl overflow-hidden rounded-2xl">
                    <img src={img(media.trailer.thumbnail)} alt="" className="aspect-video w-full object-cover transition group-hover:scale-105" />
                    <span className="absolute inset-0 grid place-items-center bg-black/40">
                        <span className="flex items-center gap-2 rounded-full bg-white px-5 py-2.5 font-semibold text-black">
                            <Play className="size-4 fill-black" /> Watch trailer
                        </span>
                    </span>
                </a>
            )}
        </div>
    )
}

function InfoCard({ label, value, icon }: { label: string; value: string; icon: React.ReactNode }) {
    return (
        <div className="card flex items-center gap-4 p-4">
            <span className="grid size-10 place-items-center rounded-xl bg-white/[0.05] text-muted">{icon}</span>
            <div>
                <p className="text-xs font-medium text-subtle">{label}</p>
                <p className="font-semibold capitalize">{value || "—"}</p>
            </div>
        </div>
    )
}

function EntrySkeleton() {
    return (
        <div className="px-6 pt-[220px] md:px-14">
            <div className="flex gap-8">
                <Skeleton className="aspect-[2/3] w-56" />
                <div className="flex flex-1 flex-col justify-end gap-4">
                    <Skeleton className="h-12 w-2/3" />
                    <Skeleton className="h-5 w-1/3" />
                    <Skeleton className="h-20 w-full max-w-3xl" />
                    <Skeleton className="h-12 w-72" />
                </div>
            </div>
        </div>
    )
}
