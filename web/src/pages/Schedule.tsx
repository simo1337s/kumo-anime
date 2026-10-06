import { CalendarDays, Clock } from "lucide-react"
import { useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { PluginSlot } from "@/components/plugins/PluginSlot"
import { Badge, EmptyState, Skeleton, Tabs } from "@/components/ui"
import { useSchedule } from "@/lib/queries"
import type { ScheduleItem } from "@/lib/types"
import { cn, cover, formatLabel, LIST_STATUS, timeUntil, title } from "@/lib/utils"

export default function SchedulePage() {
    const { data, isLoading, error } = useSchedule(8)
    const [filter, setFilter] = useState<"mine" | "all">(() => (localStorage.getItem("kumo-schedule-filter") as "mine" | "all") || "mine")

    const days = useMemo(() => {
        const items = (data ?? []).filter(i => filter === "all" || i.listStatus)
        const map = new Map<string, ScheduleItem[]>()
        for (const it of items) {
            const d = new Date(it.airingAt * 1000)
            const key = d.toDateString()
            if (!map.has(key)) map.set(key, [])
            map.get(key)!.push(it)
        }
        return [...map.entries()].map(([k, v]) => ({ date: new Date(k), items: v }))
    }, [data, filter])

    const today = new Date().toDateString()
    const now = Date.now() / 1000

    return (
        <div className="min-h-full px-6 pt-10 pb-24 md:px-10 xl:px-14">
            <PluginSlot slot="schedule-screen-top" className="mb-8" />
            <div className="mb-8 flex flex-wrap items-center justify-between gap-4">
                <div>
                    <h1 className="text-4xl font-extrabold tracking-tight">Schedule</h1>
                    <p className="mt-1 text-muted">Airing times in your local timezone.</p>
                </div>
                <Tabs
                    value={filter}
                    onChange={v => {
                        setFilter(v)
                        localStorage.setItem("kumo-schedule-filter", v)
                    }}
                    tabs={[
                        { value: "mine", label: "My list" },
                        { value: "all", label: "Everything airing" },
                    ]}
                />
            </div>
            {isLoading && (
                <div className="flex flex-col gap-4">
                    {Array.from({ length: 4 }).map((_, i) => (
                        <Skeleton key={i} className="h-32" />
                    ))}
                </div>
            )}
            {error && <EmptyState title="Couldn't load the schedule">{(error as Error).message}</EmptyState>}
            {!isLoading && days.length === 0 && (
                <EmptyState icon={<CalendarDays className="size-6" />} title="Nothing scheduled">
                    {filter === "mine" ? "None of the anime in your list air this week." : "No episodes found."}
                </EmptyState>
            )}
            <div className="flex flex-col gap-10">
                {days.map(d => {
                    const isToday = d.date.toDateString() === today
                    return (
                        <section key={d.date.toISOString()}>
                            <h2 className={cn("mb-4 flex items-center gap-3 text-xl font-bold", isToday && "text-brand-strong")}>
                                {d.date.toLocaleDateString(undefined, { weekday: "long", month: "long", day: "numeric" })}
                                {isToday && <Badge tone="brand">Today</Badge>}
                            </h2>
                            <div className="grid gap-3 md:grid-cols-2 2xl:grid-cols-3">
                                {d.items.map(it => {
                                    const aired = it.airingAt < now
                                    return (
                                        <Link key={it.id} to={`/entry?id=${it.media.id}`} className={cn("card group flex items-center gap-4 overflow-hidden p-0 transition hover:border-line-strong", aired && "opacity-60")}>
                                            <img src={cover(it.media)} alt="" loading="lazy" className="h-24 w-16 shrink-0 object-cover" />
                                            <div className="min-w-0 flex-1 py-3">
                                                <p className="truncate font-semibold group-hover:text-white">{title(it.media)}</p>
                                                <p className="mt-0.5 text-sm text-muted">
                                                    Episode {it.episode}
                                                    {it.media.episodes ? ` / ${it.media.episodes}` : ""} · {formatLabel(it.media.format)}
                                                </p>
                                                {it.listStatus && <p className="mt-1 text-xs text-brand-strong">{LIST_STATUS[it.listStatus]}</p>}
                                            </div>
                                            <div className="pr-4 text-right">
                                                <p className="text-lg font-bold tabular-nums">{new Date(it.airingAt * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</p>
                                                <p className="flex items-center justify-end gap-1 text-xs text-subtle">
                                                    <Clock className="size-3" /> {aired ? "aired" : `in ${timeUntil(it.airingAt - now)}`}
                                                </p>
                                            </div>
                                        </Link>
                                    )
                                })}
                            </div>
                        </section>
                    )
                })}
            </div>
            <PluginSlot slot="schedule-screen-bottom" className="mt-8" />
        </div>
    )
}
