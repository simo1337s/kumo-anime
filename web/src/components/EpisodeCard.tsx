import { Check, Clock, HardDrive, Play } from "lucide-react"
import { cn, formatDuration, img } from "@/lib/utils"

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
    className?: string
}

export function EpisodeCard({ image, number, title, subtitle, runtime, watched, hasFile, progress, resumeAt, aired = true, blur, large, onClick, actions, className }: Props) {
    return (
        <div
            role="button"
            tabIndex={0}
            onClick={onClick}
            onKeyDown={e => (e.key === "Enter" || e.key === " ") && onClick?.()}
            className={cn("group/ep focus-ring flex cursor-pointer flex-col gap-3 rounded-2xl outline-none", !aired && "cursor-default opacity-50", className)}
        >
            <div className="relative aspect-video overflow-hidden rounded-2xl bg-surface-2 ring-1 ring-line transition-all duration-300 group-hover/ep:ring-line-strong group-hover/ep:shadow-[0_18px_40px_-14px_rgb(0_0_0/0.9)]">
                {image ? (
                    <img
                        src={img(image)}
                        alt=""
                        loading="lazy"
                        className={cn("size-full object-cover transition duration-500 group-hover/ep:scale-[1.04]", blur && !watched && "blur-xl scale-110", watched && "opacity-70")}
                    />
                ) : (
                    <div className="grid size-full place-items-center bg-gradient-to-br from-surface-3 to-surface-1 text-4xl font-black text-white/10">{number}</div>
                )}
                <div className="absolute inset-0 bg-gradient-to-t from-black/70 via-transparent to-transparent" />
                <div className="absolute inset-0 grid place-items-center opacity-0 transition-opacity group-hover/ep:opacity-100">
                    {aired && (
                        <span className={cn("grid place-items-center rounded-full bg-white text-black shadow-2xl transition-transform group-hover/ep:scale-100", large ? "size-16 scale-90" : "size-12 scale-90")}>
                            <Play className={cn("ml-0.5 fill-black", large ? "size-6" : "size-5")} />
                        </span>
                    )}
                </div>
                <div className="absolute top-2.5 left-2.5 flex gap-1.5">
                    {watched && (
                        <span className="flex items-center gap-1 rounded-md bg-black/60 px-1.5 py-0.5 text-[11px] font-semibold text-emerald-300 backdrop-blur">
                            <Check className="size-3" /> Watched
                        </span>
                    )}
                    {hasFile && (
                        <span className="flex items-center gap-1 rounded-md bg-black/60 px-1.5 py-0.5 text-[11px] font-semibold text-white backdrop-blur">
                            <HardDrive className="size-3" />
                        </span>
                    )}
                </div>
                {actions && <div className="absolute top-2 right-2 opacity-0 transition-opacity group-hover/ep:opacity-100">{actions}</div>}
                {resumeAt !== undefined && resumeAt > 0 && (
                    <span className="absolute right-2.5 bottom-3 flex items-center gap-1 rounded-md bg-black/65 px-1.5 py-0.5 text-[11px] font-semibold text-white backdrop-blur">
                        <Clock className="size-3" /> {formatDuration(resumeAt)}
                    </span>
                )}
                {!!progress && progress > 0 && (
                    <div className="absolute inset-x-0 bottom-0 h-1 bg-white/15">
                        <div className="h-full bg-brand" style={{ width: `${Math.min(100, progress * 100)}%` }} />
                    </div>
                )}
            </div>
            <div className="flex items-start justify-between gap-3 px-0.5">
                <div className="min-w-0">
                    {title && <p className={cn("truncate font-semibold", large ? "text-lg" : "text-sm")}>{title}</p>}
                    <p className={cn("truncate text-muted", large ? "text-base" : "text-[13px]")}>{subtitle ?? `Episode ${number}`}</p>
                </div>
                {!!runtime && <span className="shrink-0 pt-0.5 text-sm text-subtle">{runtime}m</span>}
            </div>
        </div>
    )
}
