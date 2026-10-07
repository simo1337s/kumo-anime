import { ChevronLeft, ChevronRight, Ellipsis, HardDrive, Star } from "lucide-react"
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
        <Link to={entryUrl(media)} className={cn("group/card focus-ring relative flex flex-col gap-2.5 rounded-lg outline-none", !menu && className)}>
            <div className="relative aspect-[2/3] overflow-hidden rounded-lg bg-surface-2">
                {cover(media) && (
                    <img
                        src={cover(media)}
                        alt=""
                        loading="lazy"
                        className="size-full object-cover transition-[filter] duration-300 group-hover/card:brightness-110"
                        style={{ backgroundColor: media.coverImage?.color || undefined }}
                    />
                )}
                {/* A hairline, so dark covers don't melt into the page. */}
                <div className="pointer-events-none absolute inset-0 rounded-lg ring-1 ring-white/[0.07] transition-[box-shadow] duration-300 ring-inset group-hover/card:ring-white/20" />

                <div className="absolute top-2 left-2 flex flex-col items-start gap-1">
                    {behind > 0 && <span className="rounded bg-brand px-1.5 py-0.5 text-[11px] font-semibold text-white">{behind} new</span>}
                    {!!localCount && localCount > 0 && (
                        <span className="flex items-center gap-1 rounded bg-black/70 px-1.5 py-0.5 text-[11px] font-medium text-white tabular-nums">
                            <HardDrive className="size-3" />
                            {downloaded?.length ?? localCount}
                        </span>
                    )}
                </div>
                {score ? (
                    <span
                        className={cn(
                            "absolute top-2 right-2 flex items-center gap-1 rounded bg-black/70 px-1.5 py-0.5 text-[11px] font-medium text-white tabular-nums opacity-0 transition-opacity group-hover/card:opacity-100",
                            menu && "right-10", // next to the menu button
                        )}
                    >
                        <Star className="size-3 fill-amber-300 text-amber-300" />
                        {score}%
                    </span>
                ) : null}
                {nextAir && (
                    <span className="absolute bottom-2.5 left-2 rounded bg-black/70 px-1.5 py-0.5 text-[11px] font-medium text-white">
                        Ep {nextAir.episode} in {timeUntil(nextAir.timeUntilAiring)}
                    </span>
                )}

                {showProgress && listEntry && total > 0 && progress > 0 && progress < total && (
                    <div className="absolute inset-x-0 bottom-0 h-[3px] bg-black/50">
                        <div className="h-full bg-brand" style={{ width: `${Math.min(100, (progress / total) * 100)}%` }} />
                    </div>
                )}
            </div>
            <div className="px-0.5">
                <p className="line-clamp-2 text-[13px] leading-snug font-medium text-fg/90 transition-colors group-hover/card:text-fg">{title(media)}</p>
                <div className="mt-1 flex items-center gap-1.5 text-xs text-subtle">
                    {listEntry && <span className={cn("size-1.5 shrink-0 rounded-full", statusDot[listEntry.status] ?? "bg-white/50")} />}
                    <span className="truncate">
                        {formatLabel(media.format)}
                        {media.seasonYear ? ` · ${media.seasonYear}` : ""}
                    </span>
                    {listEntry && total > 0 && (
                        <span className="ml-auto shrink-0 tabular-nums">
                            {progress}/{total}
                        </span>
                    )}
                </div>
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
                        className="absolute top-1.5 right-1.5 rounded-md bg-black/70 text-white opacity-0 group-hover/card:opacity-100 hover:bg-black/85 hover:text-white focus-visible:opacity-100 data-[state=open]:opacity-100 [@media(hover:none)]:opacity-100"
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
    return <div className={cn("grid gap-x-4 gap-y-6", cols, className)}>{children}</div>
}

export function MediaCardSkeleton() {
    return (
        <div className="flex flex-col gap-2.5">
            <div className="shimmer aspect-[2/3] rounded-lg" />
            <div className="shimmer h-3.5 w-3/4 rounded" />
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
            <div ref={ref} onScroll={update} className="no-scrollbar -mx-2 flex snap-x scroll-px-2 gap-4 overflow-x-auto scroll-smooth px-2 pt-1 pb-3">
                {children.map((c, i) => (
                    <div key={i} className={cn("w-[160px] shrink-0 snap-start", itemClassName)}>
                        {c}
                    </div>
                ))}
            </div>
            {edges.left && (
                <button
                    onClick={() => scroll(-1)}
                    className="absolute top-[38%] -left-3 grid size-9 -translate-y-1/2 place-items-center rounded-full bg-surface-3 text-fg opacity-0 shadow-lg shadow-black/50 transition group-hover/carousel:opacity-100 hover:bg-surface-4"
                >
                    <ChevronLeft className="size-5" />
                </button>
            )}
            {edges.right && children.length > 4 && (
                <button
                    onClick={() => scroll(1)}
                    className="absolute top-[38%] -right-3 grid size-9 -translate-y-1/2 place-items-center rounded-full bg-surface-3 text-fg opacity-0 shadow-lg shadow-black/50 transition group-hover/carousel:opacity-100 hover:bg-surface-4"
                >
                    <ChevronRight className="size-5" />
                </button>
            )}
        </div>
    )
}
