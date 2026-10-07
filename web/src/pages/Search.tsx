import { Search as SearchIcon, SlidersHorizontal, X } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { useSearchParams } from "react-router-dom"
import { ListCardMenu } from "@/components/ListCardMenu"
import { MediaCard, MediaCardSkeleton, MediaGrid } from "@/components/MediaCard"
import { Button, EmptyState, ErrorState, Input, Select } from "@/components/ui"
import { useSearch } from "@/lib/queries"
import type { Media } from "@/lib/types"
import { cn, GENRES, SEASONS } from "@/lib/utils"

const SORTS = [
    { value: "", label: "Best match / trending" },
    { value: "POPULARITY_DESC", label: "Popularity" },
    { value: "SCORE_DESC", label: "Score" },
    { value: "TRENDING_DESC", label: "Trending" },
    { value: "START_DATE_DESC", label: "Newest" },
    { value: "FAVOURITES_DESC", label: "Favourites" },
    { value: "TITLE_ROMAJI", label: "Title" },
]
const FORMATS = ["TV", "MOVIE", "OVA", "ONA", "SPECIAL", "TV_SHORT"]
const MANGA_FORMATS = ["MANGA", "NOVEL", "ONE_SHOT"]
const FILTERS = ["genre", "season", "year", "format", "status"]
const formatsFor = (type: string) => (type === "MANGA" ? MANGA_FORMATS : FORMATS)

export default function SearchPage() {
    const [params, setParams] = useSearchParams()
    const q = params.get("q") ?? ""
    const [text, setText] = useState(q)
    const type = (params.get("type") as "ANIME" | "MANGA") || "ANIME"
    const genres = params.getAll("genre")
    const season = params.get("season") ?? ""
    const year = params.get("year") ?? ""
    // A format of the other type (e.g. from an old link) isn't sent, as the select shows.
    const format = formatsFor(type).find(f => f === params.get("format")) ?? ""
    const status = params.get("status") ?? ""
    const sort = params.get("sort") ?? ""
    const [page, setPage] = useState(1)
    const [acc, setAcc] = useState<Media[]>([])

    // setParams hands its updater the params of the render it came from, which
    // are out of date in the debounced update below (or after two quick
    // clicks): always build on the URL as it is now.
    const update = (change: (p: URLSearchParams) => void) =>
        setParams(
            () => {
                const p = new URLSearchParams(window.location.search)
                change(p)
                return p
            },
            { replace: true },
        )
    const set = (k: string, v: string | string[] | null) =>
        update(p => {
            p.delete(k)
            if (Array.isArray(v)) v.forEach(x => p.append(k, x))
            else if (v) p.set(k, v)
        })

    // The box writes q to the URL after a pause in typing and follows q when
    // it changes from outside (Back/Forward, quick search). synced is the
    // last q written or seen, so our own writes don't reset what is typed.
    const synced = useRef(q)
    useEffect(() => {
        if (q === synced.current) return
        synced.current = q
        setText(q)
    }, [q])
    useEffect(() => {
        const t = setTimeout(() => {
            const next = text.trim()
            if (next === synced.current) return
            synced.current = next
            set("q", next || null)
        }, 350)
        return () => clearTimeout(t)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [text])

    const query = useMemo(
        () => ({
            search: params.get("q") ?? undefined,
            type,
            genres: genres.length ? genres : undefined,
            season: season || undefined,
            year: year ? Number(year) : undefined,
            formats: format ? [format] : undefined,
            status: status || undefined,
            sort: sort ? [sort] : undefined,
            perPage: 36,
        }),
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [params.toString()],
    )
    useEffect(() => {
        setPage(1)
        setAcc([])
    }, [query])
    const { data, isFetching, error, refetch } = useSearch({ ...query, page })
    useEffect(() => {
        if (!data?.media) return
        setAcc(prev => (page === 1 ? data.media! : [...prev, ...data.media!.filter(m => !prev.some(p => p.id === m.id))]))
    }, [data, page])

    const years = Array.from({ length: new Date().getFullYear() - 1959 }, (_, i) => String(new Date().getFullYear() + 1 - i))
    const active = genres.length + (season ? 1 : 0) + (year ? 1 : 0) + (format ? 1 : 0) + (status ? 1 : 0)

    return (
        <div className="min-h-full px-6 pt-10 pb-24 md:px-10 xl:px-14">
            <div className="mb-8 flex flex-col gap-6">
                <div className="flex items-center justify-between gap-4">
                    <h1 className="text-4xl font-extrabold tracking-tight">Search</h1>
                    <div className="flex rounded-xl border border-line bg-surface-1 p-1">
                        {(["ANIME", "MANGA"] as const).map(t => (
                            <button
                                key={t}
                                onClick={() =>
                                    update(p => {
                                        if (t === "ANIME") p.delete("type")
                                        else p.set("type", t)
                                        // Anime and manga have different formats.
                                        if (!formatsFor(t).includes(p.get("format") ?? "")) p.delete("format")
                                    })
                                }
                                className={cn("h-8 rounded-lg px-4 text-sm font-semibold transition", type === t ? "bg-white/10 text-fg" : "text-muted hover:text-fg")}
                            >
                                {t === "ANIME" ? "Anime" : "Manga"}
                            </button>
                        ))}
                    </div>
                </div>
                <Input autoFocus value={text} onChange={e => setText(e.target.value)} placeholder="Search by title…" icon={<SearchIcon className="size-4" />} className="h-12 text-base" />
                <div className="flex flex-wrap items-center gap-3">
                    <SlidersHorizontal className="size-4 text-subtle" />
                    <Select className="w-44" value={season} onChange={v => set("season", v)} options={[{ value: "", label: "Any season" }, ...SEASONS.map(s => ({ value: s, label: s.charAt(0) + s.slice(1).toLowerCase() }))]} />
                    <Select className="w-32" value={year} onChange={v => set("year", v)} options={[{ value: "", label: "Any year" }, ...years.map(y => ({ value: y, label: y }))]} />
                    <Select className="w-40" value={format} onChange={v => set("format", v)} options={[{ value: "", label: "Any format" }, ...formatsFor(type).map(f => ({ value: f, label: f.replace("_", " ") }))]} />
                    <Select
                        className="w-40"
                        value={status}
                        onChange={v => set("status", v)}
                        options={[
                            { value: "", label: "Any status" },
                            { value: "RELEASING", label: "Airing" },
                            { value: "FINISHED", label: "Finished" },
                            { value: "NOT_YET_RELEASED", label: "Upcoming" },
                        ]}
                    />
                    <Select className="w-52" value={sort} onChange={v => set("sort", v)} options={SORTS} />
                    {active > 0 && (
                        <Button variant="ghost" size="sm" icon={<X className="size-4" />} onClick={() => update(p => FILTERS.forEach(k => p.delete(k)))}>
                            Clear filters
                        </Button>
                    )}
                </div>
                <div className="flex flex-wrap gap-2">
                    {GENRES.map(g => {
                        const on = genres.includes(g)
                        return (
                            <button
                                key={g}
                                onClick={() =>
                                    update(p => {
                                        const cur = p.getAll("genre")
                                        p.delete("genre")
                                        for (const v of cur.includes(g) ? cur.filter(c => c !== g) : [...cur, g]) p.append("genre", v)
                                    })
                                }
                                className={cn("rounded-full border px-3.5 py-1.5 text-sm font-medium transition", on ? "border-brand bg-brand text-white" : "border-line text-muted hover:border-line-strong hover:text-fg")}
                            >
                                {g}
                            </button>
                        )
                    })}
                </div>
            </div>

            {acc.length === 0 && isFetching ? (
                <MediaGrid>
                    {Array.from({ length: 18 }).map((_, i) => (
                        <MediaCardSkeleton key={i} />
                    ))}
                </MediaGrid>
            ) : acc.length === 0 && error ? (
                <ErrorState title="Couldn't search AniList" error={error} onRetry={() => refetch()} />
            ) : acc.length === 0 ? (
                <EmptyState icon={<SearchIcon className="size-6" />} title="Nothing found">
                    Try a different title or fewer filters.
                </EmptyState>
            ) : (
                <>
                    <MediaGrid>
                        {acc.map(m => (
                            <MediaCard key={m.id} media={m} listEntry={m.mediaListEntry} menu={<ListCardMenu media={m} />} />
                        ))}
                    </MediaGrid>
                    {error && page > 1 ? (
                        <ErrorState compact className="mt-6" title="Couldn't load more results" error={error} retrying={isFetching} onRetry={() => refetch()} />
                    ) : (
                        data?.pageInfo?.hasNextPage && (
                            <div className="mt-10 flex justify-center">
                                <Button loading={isFetching} onClick={() => setPage(p => p + 1)}>
                                    Load more
                                </Button>
                            </div>
                        )
                    )}
                </>
            )}
        </div>
    )
}
