import { ChevronLeft, ChevronRight, Info, Play, Star } from "lucide-react"
import { useEffect, useState } from "react"
import { Link } from "react-router-dom"
import { Carousel, MediaCard, MediaCardSkeleton } from "@/components/MediaCard"
import { PluginSlot } from "@/components/plugins/PluginSlot"
import { Button, EmptyState, SectionHeader, Skeleton } from "@/components/ui"
import { useDiscover } from "@/lib/queries"
import type { Media } from "@/lib/types"
import { banner, cleanDescription, cn, formatLabel, GENRES, seasonLabel, title } from "@/lib/utils"

export default function DiscoverPage() {
    const { data, isLoading, error } = useDiscover()
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
                <EmptyState icon={<Info className="size-6" />} title="Couldn't reach AniList">
                    {(error as Error).message}
                </EmptyState>
            </div>
        )

    const rows: { key: keyof NonNullable<typeof data>; title: string; subtitle?: string }[] = [
        { key: "trending", title: "Trending now" },
        { key: "thisSeason", title: `Popular this season`, subtitle: data ? seasonLabel(data.season, data.year) : "" },
        { key: "nextSeason", title: "Upcoming next season" },
        { key: "popular", title: "All-time popular" },
        { key: "topRated", title: "Top rated" },
    ]

    return (
        <div className="min-h-full pb-24">
            {isLoading ? <Skeleton className="h-[460px] rounded-none" /> : featured[i] && <Featured media={featured[i]} index={i} count={featured.length} onIndex={setI} />}
            <div className="relative z-10 -mt-16 flex flex-col gap-12 px-6 md:px-10 xl:px-14">
                <PluginSlot slot="after-discover-screen-header" />
                <div className="no-scrollbar flex gap-2 overflow-x-auto">
                    {GENRES.map(g => (
                        <Link key={g} to={`/search?genre=${encodeURIComponent(g)}`} className="glass shrink-0 rounded-full px-4 py-2 text-sm font-medium text-fg/85 transition hover:text-white">
                            {g}
                        </Link>
                    ))}
                </div>
                {rows.map(r => (
                    <section key={r.key}>
                        <SectionHeader title={r.title} subtitle={r.subtitle} action={<Link to={`/search?sort=${r.key === "topRated" ? "SCORE_DESC" : "POPULARITY_DESC"}`} className="text-sm font-medium text-muted hover:text-fg">See more</Link>} />
                        {isLoading ? (
                            <div className="flex gap-5">
                                {Array.from({ length: 7 }).map((_, k) => (
                                    <div key={k} className="w-[160px] shrink-0">
                                        <MediaCardSkeleton />
                                    </div>
                                ))}
                            </div>
                        ) : (
                            <Carousel>{((data?.[r.key] as Media[]) ?? []).map(m => <MediaCard key={m.id} media={m} listEntry={m.mediaListEntry} />)}</Carousel>
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
        <div className="relative h-[480px] overflow-hidden">
            <img key={media.id} src={banner(media)} alt="" className="absolute inset-0 size-full object-cover fade-in animate-slow-zoom" />
            <div className="absolute inset-0 bg-gradient-to-t from-bg via-bg/50 to-transparent" />
            <div className="absolute inset-0 bg-gradient-to-r from-bg/90 via-bg/40 to-transparent" />
            <div key={media.id + "t"} className="absolute bottom-24 left-6 max-w-2xl rise-in md:left-10 xl:left-14">
                <p className="text-sm font-semibold tracking-wider text-brand-strong uppercase">Trending #{index + 1}</p>
                <h1 className="mt-2 text-4xl leading-tight font-extrabold drop-shadow-xl md:text-5xl">{title(media)}</h1>
                <div className="mt-3 flex items-center gap-3 text-sm text-white/80">
                    {media.meanScore && (
                        <span className="flex items-center gap-1 font-semibold">
                            <Star className="size-4 fill-amber-300 text-amber-300" /> {media.meanScore}%
                        </span>
                    )}
                    <span>{formatLabel(media.format)}</span>
                    <span>{seasonLabel(media.season, media.seasonYear)}</span>
                    {media.genres?.slice(0, 3).map(g => (
                        <span key={g} className="text-white/60">
                            {g}
                        </span>
                    ))}
                </div>
                {desc && <p className="mt-3 line-clamp-3 text-white/70">{desc}</p>}
                <div className="mt-5 flex gap-3">
                    <Link to={`/entry?id=${media.id}`}>
                        <Button variant="white" size="lg" icon={<Play className="size-5 fill-black" />}>
                            Watch now
                        </Button>
                    </Link>
                    <Link to={`/entry?id=${media.id}&tab=details`}>
                        <Button variant="subtle" size="lg" className="glass" icon={<Info className="size-5" />}>
                            Details
                        </Button>
                    </Link>
                </div>
            </div>
            {count > 1 && (
                <div className="absolute right-6 bottom-24 flex items-center gap-2 md:right-14">
                    <button className="glass grid size-9 place-items-center rounded-full" onClick={() => onIndex((index - 1 + count) % count)}>
                        <ChevronLeft className="size-4" />
                    </button>
                    {Array.from({ length: count }).map((_, k) => (
                        <button key={k} onClick={() => onIndex(k)} className={cn("h-1.5 rounded-full transition-all", k === index ? "w-6 bg-white" : "w-1.5 bg-white/40")} />
                    ))}
                    <button className="glass grid size-9 place-items-center rounded-full" onClick={() => onIndex((index + 1) % count)}>
                        <ChevronRight className="size-4" />
                    </button>
                </div>
            )}
        </div>
    )
}
