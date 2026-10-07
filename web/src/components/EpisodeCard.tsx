import { Check, CheckCheck, Clock, HardDrive, Minus, Play } from "lucide-react"
import { cn, formatDuration, img } from "@/lib/utils"
import { Tooltip } from "./ui"

type Props = {
    image?: string
    number: number
    title?: string
    subtitle?: React.ReactNode
    runtime?: number
    watched?: boolean
    hasFile?: boolean
    progress?: number // 0-1 resume progress
    resumeAt?: number
    aired?: boolean
    blur?: boolean
    large?: boolean
    onClick?: () => void
    actions?: React.ReactNode
    onRemove?: () => void // "Continue watching": removes the card's item
    className?: string
}

export function EpisodeCard({ image, number, title, subtitle, runtime, watched, hasFile, progress, resumeAt, aired = true, blur, large, onClick, actions, onRemove, className }: Props) {
    return (
        <div
            role="button"
            tabIndex={0}
            onClick={e => {
                // Menus open in portals outside the card, but React still
                // bubbles their clicks up to here: "Play in mpv" from the
                // episode menu used to also start the default player.
                if (!e.currentTarget.contains(e.target as Node)) return
                onClick?.()
            }}
            onKeyDown={e => {
                // Only keys on the card itself, not on its buttons.
                if (e.target !== e.currentTarget || (e.key !== "Enter" && e.key !== " ")) return
                e.preventDefault()
                onClick?.()
            }}
            className={cn("group/ep focus-ring flex cursor-pointer flex-col gap-2.5 rounded-lg outline-none", !aired && "cursor-default opacity-50", className)}
        >
            <div className="relative aspect-video overflow-hidden rounded-lg bg-surface-2">
                {image ? (
                    <img
                        src={img(image)}
                        alt=""
                        loading="lazy"
                        className={cn(
                            "size-full object-cover transition-[filter,opacity] duration-300 group-hover/ep:brightness-[0.8]",
                            blur && !watched && "scale-110 blur-xl",
                            watched && "opacity-75 group-hover/ep:opacity-100",
                        )}
                    />
                ) : (
                    <div className="grid size-full place-items-center bg-surface-3 text-3xl font-semibold text-white/15 tabular-nums">{number}</div>
                )}
                <div className="pointer-events-none absolute inset-0 rounded-lg ring-1 ring-white/[0.07] transition-[box-shadow] duration-300 ring-inset group-hover/ep:ring-white/20" />
                {aired && (
                    <div className="absolute inset-0 grid place-items-center opacity-0 transition-opacity duration-200 group-hover/ep:opacity-100">
                        <span className={cn("grid place-items-center rounded-full bg-white/95 text-neutral-950 shadow-lg shadow-black/40", large ? "size-14" : "size-11")}>
                            <Play className={cn("ml-0.5 fill-current", large ? "size-5" : "size-4")} />
                        </span>
                    </div>
                )}
                <div className="absolute top-2 left-2 flex gap-1">
                    {watched && (
                        <Tooltip content="Watched">
                            <span className="grid size-5 place-items-center rounded-full bg-emerald-500 text-white" aria-label="Watched">
                                <Check className="size-3 stroke-[3]" />
                            </span>
                        </Tooltip>
                    )}
                    {hasFile && (
                        <Tooltip content="Downloaded">
                            <span className="grid size-5 place-items-center rounded-full bg-black/70 text-white" aria-label="Downloaded">
                                <HardDrive className="size-3" />
                            </span>
                        </Tooltip>
                    )}
                </div>
                {actions && (
                    <div className="absolute top-2 right-2 opacity-0 transition-opacity group-hover/ep:opacity-100 focus-within:opacity-100" onClick={e => e.stopPropagation()} onKeyDown={e => e.stopPropagation()}>
                        {actions}
                    </div>
                )}
                {onRemove && (
                    <div
                        // On hover, and always on touch screens, which can't hover.
                        className="absolute top-2 right-2 opacity-0 transition-opacity group-hover/ep:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100"
                        onClick={e => e.stopPropagation()}
                        onKeyDown={e => e.stopPropagation()}
                    >
                        <Tooltip content="Remove from Continue watching">
                            <button
                                aria-label="Remove from Continue watching"
                                onClick={onRemove}
                                className="focus-ring grid size-7 place-items-center rounded-md bg-black/70 text-white transition-colors hover:bg-black/85"
                            >
                                <Minus className="size-4" />
                            </button>
                        </Tooltip>
                    </div>
                )}
                {resumeAt !== undefined && resumeAt > 0 && (
                    <span className="absolute right-2 bottom-2.5 flex items-center gap-1 rounded bg-black/70 px-1.5 py-0.5 text-[11px] font-medium text-white tabular-nums">
                        <Clock className="size-3" /> {formatDuration(resumeAt)}
                    </span>
                )}
                {!!progress && progress > 0 && (
                    <div className="absolute inset-x-0 bottom-0 h-[3px] bg-black/50">
                        <div className="h-full bg-brand" style={{ width: `${Math.min(100, progress * 100)}%` }} />
                    </div>
                )}
            </div>
            <div className="flex items-start justify-between gap-3 px-0.5">
                <div className="min-w-0">
                    {title && <p className={cn("truncate font-medium", large ? "text-[15px]" : "text-[13.5px]")}>{title}</p>}
                    <p className={cn("mt-0.5 truncate text-muted", large ? "text-[13.5px]" : "text-xs")}>{subtitle ?? `Episode ${number}`}</p>
                </div>
                {!!runtime && <span className="shrink-0 pt-0.5 text-xs text-subtle tabular-nums">{runtime}m</span>}
            </div>
        </div>
    )
}

// Small "mark as watched / unwatched" button for episode card actions.
export function WatchedToggle({ watched, onToggle, disabled }: { watched: boolean; onToggle: () => void; disabled?: boolean }) {
    const label = watched ? "Mark as unwatched" : "Mark as watched"
    return (
        <Tooltip content={label}>
            <button
                aria-label={label}
                disabled={disabled}
                onClick={e => {
                    e.stopPropagation()
                    onToggle()
                }}
                onKeyDown={e => e.stopPropagation()}
                className={cn(
                    "grid size-7 place-items-center rounded-md text-white transition-colors disabled:opacity-50",
                    watched ? "bg-emerald-500/90 hover:bg-emerald-500" : "bg-black/70 hover:bg-black/85",
                )}
            >
                {watched ? <CheckCheck className="size-4" /> : <Check className="size-4" />}
            </button>
        </Tooltip>
    )
}
