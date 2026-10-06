import { ChevronLeft, ChevronRight, Ellipsis, HardDrive, Play, Star } from "lucide-react"
import { useRef, useState } from "react"
import { Link } from "react-router-dom"
import type { ListEntry, Media } from "@/lib/types"
import { airedEpisodes, cn, cover, entryUrl, formatLabel, timeUntil, title, totalEpisodes } from "@/lib/utils"
import { Dropdown, DropdownContent, DropdownTrigger, IconButton } from "./ui"

type Props = {
    media: Media
    listEntry?: Pick<ListEntry, "progress" | "status" | "score"> | null
    localCount?: number
    downloaded?: number[] | null
    className?: string
    showProgress?: boolean
    size?: "sm" | "md" | "lg"
    menu?: React.ReactNode // items of a menu opened with a "⋯" button or a right-click
}

const statusDot: Record<string, string> = {
    CURRENT: "bg-sky-400",
    REPEATING: "bg-sky-400",
    PLANNING: "bg-violet-400",
    COMPLETED: "bg-emerald-400",
    PAUSED: "bg-amber-400",
    DROPPED: "bg-rose-400",
}

export function MediaCard({ media, listEntry, localCount, downloaded, className, showProgress = true, menu }: Props) {
    const total = media.type === "MANGA" ? media.chapters ?? 0 : totalEpisodes(media)
    const progress = listEntry?.progress ?? 0
    const aired = airedEpisodes(media)
    // "new" = aired episodes of an airing show the user hasn't watched yet
    const behind =
        media.type !== "MANGA" && media.status === "RELEASING" && listEntry && (listEntry.status === "CURRENT" || listEntry.status === "REPEATING")
            ? Math.max(0, aired - progress)
            : 0
    const nextAir = media.nextAiringEpisode
    const score = media.meanScore ?? media.averageScore

    const card = (
        <Link to={entryUrl(media)} className={cn("group/card focus-ring relative flex flex-col gap-2.5 rounded-2xl outline-none", !menu && className)}>
            <div className="relative aspect-[2/3] overflow-hidden rounded-2xl bg-surface-2 ring-1 ring-line transition-all duration-300 group-hover/card:-translate-y-1 group-hover/card:shadow-[0_18px_40px_-12px_rgb(0_0_0/0.8)] group-hover/card:ring-line-strong">
                {cover(media) && (
                    <img
                        src={cover(media)}
                        alt=""
                        loading="lazy"
                        className="size-full object-cover transition-transform duration-500 group-hover/card:scale-[1.06]"
                        style={{ backgroundColor: media.coverImage?.color || undefined }}
                    />
                )}
                <div className="absolute inset-0 bg-gradient-to-t from-black/85 via-black/10 to-transparent opacity-80 transition-opacity group-hover/card:opacity-100" />

                {showProgress && listEntry && total > 0 && progress > 0 && (
                    <div className="absolute inset-x-0 top-0 h-1 bg-black/40">
                        <div className="h-full bg-brand" style={{ width: `${Math.min(100, (progress / total) * 100)}%` }} />
                    </div>
                )}

                <div className="absolute top-2.5 left-2.5 flex flex-col items-start gap-1.5">
                    {behind > 0 && <span className="rounded-md bg-brand px-1.5 py-0.5 text-[11px] font-bold text-white shadow-lg">{behind} new</span>}
                    {!!localCount && localCount > 0 && (
                        <span className="flex items-center gap-1 rounded-md bg-black/60 px-1.5 py-0.5 text-[11px] font-semibold text-white backdrop-blur">
                            <HardDrive className="size-3" />
                            {downloaded?.length ?? localCount}
                        </span>
                    )}
                </div>
                {score ? (
                    <span
                        className={cn(
                            "absolute top-2.5 right-2.5 flex items-center gap-1 rounded-md bg-black/60 px-1.5 py-0.5 text-[11px] font-semibold text-white opacity-0 backdrop-blur transition-opacity group-hover/card:opacity-100",
                            menu && "right-11", // next to the menu button
                        )}
                    >
                        <Star className="size-3 fill-amber-300 text-amber-300" />
                        {score}%
                    </span>
                ) : null}

                <div className="absolute inset-x-0 bottom-0 p-3">
                    {nextAir && (
                        <p className="mb-1 text-[11px] font-semibold text-white/90">
                            Ep {nextAir.episode} in {timeUntil(nextAir.timeUntilAiring)}
                        </p>
                    )}
                    <div className="flex items-center gap-1.5 text-[11px] font-medium text-white/70">
                        {listEntry && <span className={cn("size-1.5 rounded-full", statusDot[listEntry.status] ?? "bg-white/50")} />}
                        <span>{formatLabel(media.format)}</span>
                        {media.seasonYear && <span>· {media.seasonYear}</span>}
                    </div>
                </div>

                <div className="absolute inset-0 grid place-items-center opacity-0 transition-opacity duration-300 group-hover/card:opacity-100">
                    <span className="grid size-12 scale-90 place-items-center rounded-full bg-white/15 text-white backdrop-blur-md transition-transform duration-300 group-hover/card:scale-100">
                        <Play className="ml-0.5 size-5 fill-white" />
                    </span>
                </div>
            </div>
            <div className="px-0.5">
                <p className="line-clamp-2 text-[13.5px] leading-snug font-semibold text-fg/95 transition-colors group-hover/card:text-white">{title(media)}</p>
                {listEntry && total > 0 && (
                    <p className="mt-0.5 text-xs text-subtle">
                        {progress} / {total} {media.type === "MANGA" ? "ch" : "ep"}
                    </p>
                )}
            </div>
        </Link>
    )
    return menu ? (
        <CardMenu menu={menu} className={className}>
            {card}
        </CardMenu>
    ) : (
        card
    )
}

// A card with a menu, opened with a "⋯" button (on hover, always shown on
// touch screens, which can't hover) or by right-clicking the card, at the
// pointer. Both are next to the card's link, not in it, so using them never
// opens the anime.
function CardMenu({ menu, className, children }: { menu: React.ReactNode; className?: string; children: React.ReactNode }) {
    const [at, setAt] = useState<{ x: number; y: number } | null>(null) // in the card
    const focused = useRef<Element | null>(null)
    return (
        <div
            className={cn("group/card relative", className)}
            onContextMenu={e => {
                e.preventDefault()
                // React also brings right-clicks in the open menu (a portal) here.
                if (!e.currentTarget.contains(e.target as Node)) return
                const r = e.currentTarget.getBoundingClientRect()
                const x = e.clientX - r.left
                const y = e.clientY - r.top
                focused.current = document.activeElement
                // Opened with the menu key there may be no pointer in the card.
                setAt(x >= 0 && y >= 0 && x <= r.width && y <= r.height ? { x, y } : { x: r.width / 2, y: r.height / 3 })
            }}
        >
            {children}
            <Dropdown open={!!at} onOpenChange={v => !v && setAt(null)} modal={false}>
                <DropdownTrigger asChild>
                    <span aria-hidden className="pointer-events-none absolute" style={{ left: at?.x, top: at?.y }} />
                </DropdownTrigger>
                <DropdownContent
                    align="start"
                    sideOffset={2}
                    // Focus goes back where it was (the card, after the menu
                    // key), unless something else took it (a dialog, a click).
                    onCloseAutoFocus={e => {
                        e.preventDefault()
                        if (document.activeElement === document.body && focused.current instanceof HTMLElement) focused.current.focus()
                    }}
                >
                    {menu}
                </DropdownContent>
            </Dropdown>
            <Dropdown>
                <DropdownTrigger asChild>
                    <IconButton
                        label="More options"
                        size="xs"
                        // Lifts with the cover on hover.
                        className="absolute top-2 right-2 rounded-full bg-black/60 text-white opacity-0 backdrop-blur duration-300 group-hover/card:-translate-y-1 group-hover/card:opacity-100 hover:bg-black/80 hover:text-white focus-visible:opacity-100 data-[state=open]:opacity-100 [@media(hover:none)]:opacity-100"
                    >
                        <Ellipsis className="size-4" />
                    </IconButton>
                </DropdownTrigger>
                <DropdownContent>{menu}</DropdownContent>
            </Dropdown>
        </div>
    )
}

export function MediaGrid({ children, className, size = "md" }: { children: React.ReactNode; className?: string; size?: string }) {
    const cols =
        size === "sm"
            ? "grid-cols-[repeat(auto-fill,minmax(130px,1fr))]"
            : size === "lg"
              ? "grid-cols-[repeat(auto-fill,minmax(200px,1fr))]"
              : "grid-cols-[repeat(auto-fill,minmax(160px,1fr))]"
    return <div className={cn("grid gap-x-5 gap-y-7", cols, className)}>{children}</div>
}

export function MediaCardSkeleton() {
    return (
        <div className="flex flex-col gap-2.5">
            <div className="shimmer aspect-[2/3] rounded-2xl" />
            <div className="shimmer h-4 w-3/4 rounded-md" />
        </div>
    )
}

export function Carousel({ children, className, itemClassName }: { children: React.ReactNode[]; className?: string; itemClassName?: string }) {
    const ref = useRef<HTMLDivElement>(null)
    const [edges, setEdges] = useState({ left: false, right: true })
    const update = () => {
        const el = ref.current
        if (!el) return
        setEdges({ left: el.scrollLeft > 8, right: el.scrollLeft + el.clientWidth < el.scrollWidth - 8 })
    }
    const scroll = (dir: number) => ref.current?.scrollBy({ left: dir * ref.current.clientWidth * 0.85, behavior: "smooth" })
    return (
        <div className={cn("group/carousel relative", className)}>
            <div ref={ref} onScroll={update} className="no-scrollbar -mx-2 flex snap-x gap-5 overflow-x-auto scroll-smooth px-2 pt-1 pb-3">
                {children.map((c, i) => (
                    <div key={i} className={cn("w-[160px] shrink-0 snap-start", itemClassName)}>
                        {c}
                    </div>
                ))}
            </div>
            {edges.left && (
                <button
                    onClick={() => scroll(-1)}
                    className="absolute top-[38%] -left-4 grid size-10 -translate-y-1/2 place-items-center rounded-full border border-line-strong bg-surface-2/90 text-fg opacity-0 shadow-xl backdrop-blur transition group-hover/carousel:opacity-100 hover:bg-surface-3"
                >
                    <ChevronLeft className="size-5" />
                </button>
            )}
            {edges.right && children.length > 4 && (
                <button
                    onClick={() => scroll(1)}
                    className="absolute top-[38%] -right-4 grid size-10 -translate-y-1/2 place-items-center rounded-full border border-line-strong bg-surface-2/90 text-fg opacity-0 shadow-xl backdrop-blur transition group-hover/carousel:opacity-100 hover:bg-surface-3"
                >
                    <ChevronRight className="size-5" />
                </button>
            )}
        </div>
    )
}
