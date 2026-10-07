import { ChevronLeft, ChevronRight, Compass, Info, Play, RefreshCw, Star } from "lucide-react"
import { useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { ListCardMenu } from "@/components/ListCardMenu"
import { Carousel, MediaCard, MediaCardSkeleton } from "@/components/MediaCard"
import { PluginSlot } from "@/components/plugins/PluginSlot"
import { Button, EmptyState, ErrorState, SectionHeader, Skeleton } from "@/components/ui"
import { useDiscover } from "@/lib/queries"
import type { Media } from "@/lib/types"
import { banner, cleanDescription, cn, formatLabel, GENRES, SEASONS, seasonLabel, title } from "@/lib/utils"

// The season after the given one: Fall 2026 -> Winter 2027.
function seasonAfter(season: string, year: number) {
    const i = SEASONS.indexOf(season)
    return i === SEASONS.length - 1 ? { season: SEASONS[0], year: year + 1 } : { season: SEASONS[i + 1], year }
}

const seasonSearch = (season?: string, year?: number) => (season && year ? `/search?season=${season}&year=${year}&sort=POPULARITY_DESC` : "/search?sort=POPULARITY_DESC")

export default function DiscoverPage() {
    const { data, isLoading, error, refetch, isFetching } = useDiscover()
    const featured = (data?.trending ?? []).filter(m => m.bannerImage).slice(0, 6)
    const [i, setI] = useState(0)
    useEffect(() => {
        if (featured.length < 2) return
        const t = setInterval(() => setI(x => (x + 1) % featured.length), 9000)
        return () => clearInterval(t)
    }, [featured.length])

    if (error)
        return (
            <div className="p-10">
                <ErrorState title="Couldn't reach AniList" error={error} retrying={isFetching} onRetry={() => refetch()} />
            </div>
        )

    const next = data ? seasonAfter(data.season, data.year) : undefined
    const rows: { key: keyof NonNullable<typeof data>; title: string; subtitle?: string; more: string }[] = [
        { key: "trending", title: "Trending now", more: "/search?sort=TRENDING_DESC" },
        { key: "thisSeason", title: `Popular this season`, subtitle: data ? seasonLabel(data.season, data.year) : "", more: seasonSearch(data?.season, data?.year) },
        { key: "nextSeason", title: "Upcoming next season", subtitle: next ? seasonLabel(next.season, next.year) : "", more: seasonSearch(next?.season, next?.year) },
        { key: "popular", title: "All-time popular", more: "/search?sort=POPULARITY_DESC" },
        { key: "topRated", title: "Top rated", more: "/search?sort=SCORE_DESC" },
    ]
    // AniList can come back with nothing at all (it couldn't be reached and
    // nothing is cached): skip empty rows instead of showing bare headers.
    const shown = isLoading ? rows : rows.filter(r => ((data?.[r.key] as Media[] | undefined) ?? []).length > 0)
    const hero = isLoading || !!featured[i]

    return (
        <div className="min-h-full pb-24">
            {isLoading ? <Skeleton className="h-[440px] rounded-none" /> : featured[i] && <Featured media={featured[i]} index={i} count={featured.length} onIndex={setI} />}
            {/* Without a hero banner there is nothing to overlap. */}
            <div className={cn("relative z-10 flex flex-col gap-10 px-6 md:px-8 xl:px-10", hero ? "-mt-14" : "pt-8")}>
                <PluginSlot slot="after-discover-screen-header" />
                <div className="no-scrollbar flex gap-2 overflow-x-auto">
                    {GENRES.map(g => (
                        <Link key={g} to={`/search?genre=${encodeURIComponent(g)}`} className="flex h-8 shrink-0 items-center rounded-full bg-white/[0.06] px-3.5 text-[13px] font-medium text-fg/80 transition-colors hover:bg-white/[0.1] hover:text-fg">
                            {g}
                        </Link>
                    ))}
                </div>
                {shown.length === 0 && (
                    <EmptyState
                        icon={<Compass className="size-6" />}
                        title="Nothing to discover right now"
                        action={
                            <Button icon={<RefreshCw className="size-4" />} loading={isFetching} onClick={() => refetch()}>
                                Try again
                            </Button>
                        }
                    >
                        AniList didn't return any anime. Check your internet connection and try again.
                    </EmptyState>
                )}
                {shown.map(r => (
                    <section key={r.key}>
                        <SectionHeader
                            title={r.title}
                            subtitle={r.subtitle}
                            action={
                                <Link to={r.more} className="text-[13px] font-medium text-muted transition-colors hover:text-fg">
                                    See more
                                </Link>
                            }
                        />
                        {isLoading ? (
                            <div className="flex gap-4">
                                {Array.from({ length: 7 }).map((_, k) => (
                                    <div key={k} className="w-[160px] shrink-0">
                                        <MediaCardSkeleton />
                                    </div>
                                ))}
                            </div>
                        ) : (
                            <Carousel>{((data?.[r.key] as Media[]) ?? []).map(m => <MediaCard key={m.id} media={m} listEntry={m.mediaListEntry} menu={<ListCardMenu media={m} />} />)}</Carousel>
                        )}
                    </section>
                ))}
            </div>
        </div>
    )
}

function Featured({ media, index, count, onIndex }: { media: Media; index: number; count: number; onIndex: (i: number) => void }) {
    const desc = cleanDescription(media.description)
    return (
        <div className="relative h-[440px] overflow-hidden">
            <img key={media.id} src={banner(media)} alt="" className="absolute inset-0 size-full object-cover fade-in" />
            <div className="absolute inset-0 bg-gradient-to-t from-bg via-bg/50 to-bg/5" />
            <div className="absolute inset-0 bg-gradient-to-r from-bg/90 via-bg/40 to-transparent" />
            <div key={media.id + "t"} className="absolute bottom-24 left-6 max-w-2xl rise-in md:left-8 xl:left-10">
                <p className="text-sm font-medium text-white/65">Trending #{index + 1}</p>
                <h1 className="mt-1.5 text-4xl leading-[1.1] font-semibold tracking-tight text-white md:text-[2.75rem]">{title(media)}</h1>
                <div className="mt-3 flex flex-wrap items-center gap-x-2 text-sm text-white/75">
                    {media.meanScore && (
                        <>
                            <span className="flex items-center gap-1 font-medium text-white">
                                <Star className="size-3.5 fill-amber-300 text-amber-300" /> {media.meanScore}%
                            </span>
                            <span className="text-white/30">·</span>
                        </>
                    )}
                    <span>{formatLabel(media.format)}</span>
                    <span className="text-white/30">·</span>
                    <span>{seasonLabel(media.season, media.seasonYear)}</span>
                    {!!media.genres?.length && <span className="text-white/30">·</span>}
                    <span className="text-white/55">{media.genres?.slice(0, 3).join(", ")}</span>
                </div>
                {desc && <p className="mt-3 line-clamp-2 max-w-xl text-[15px] leading-relaxed text-white/65">{desc}</p>}
                <div className="mt-5 flex gap-2.5">
                    <Link to={`/entry?id=${media.id}`}>
                        <Button variant="white" size="lg" icon={<Play className="size-4 fill-current" />}>
                            Watch now
                        </Button>
                    </Link>
                    <Link to={`/entry?id=${media.id}&tab=details`}>
                        <Button variant="ghost" size="lg" className="glass text-white hover:bg-black/70 hover:text-white" icon={<Info className="size-4" />}>
                            Details
                        </Button>
                    </Link>
                </div>
            </div>
            {count > 1 && (
                <div className="absolute right-6 bottom-24 flex items-center gap-3 md:right-8 xl:right-10">
                    <div className="flex gap-1.5">
                        {Array.from({ length: count }).map((_, k) => (
                            <button
                                key={k}
                                aria-label={`Show ${k + 1}`}
                                onClick={() => onIndex(k)}
                                className={cn("h-1 rounded-full transition-all duration-300", k === index ? "w-5 bg-white" : "w-2.5 bg-white/30 hover:bg-white/50")}
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
