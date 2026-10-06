import { CheckCircle2, Download, Loader2, RotateCcw, Trash2, X, XCircle } from "lucide-react"
import { Link } from "react-router-dom"
import { toast } from "sonner"
import { Badge, Button, EmptyState, IconButton, Progress } from "@/components/ui"
import { api } from "@/lib/api"
import { useDownloads, useStatus } from "@/lib/queries"
import type { DownloadItem } from "@/lib/types"
import { cn, img } from "@/lib/utils"

export default function DownloadsPage() {
    const { data, isLoading } = useDownloads()
    const { data: status } = useStatus()
    const items = data ?? []
    const active = items.filter(i => ["queued", "resolving", "downloading"].includes(i.status))
    const done = items.filter(i => !["queued", "resolving", "downloading"].includes(i.status))
    const call = (p: Promise<unknown>) => p.catch((e: Error) => toast.error(e.message))

    return (
        <div className="min-h-full px-6 pt-10 pb-24 md:px-10 xl:px-14">
            <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
                <div>
                    <h1 className="text-4xl font-extrabold tracking-tight">Downloads</h1>
                    <p className="mt-1 text-muted">
                        Episodes downloaded with ani-cli or streaming extensions, saved to{" "}
                        <span className="font-medium text-fg">{status?.settings.aniCli.downloadDir || status?.settings.library.dir || "your library"}</span>.
                    </p>
                </div>
                {done.length > 0 && (
                    <Button icon={<Trash2 className="size-4" />} onClick={() => call(api.post("/api/downloads/clear"))}>
                        Clear finished
                    </Button>
                )}
            </div>
            {!isLoading && items.length === 0 && (
                <EmptyState icon={<Download className="size-6" />} title="No downloads yet">
                    Open an anime, go to <b>Watch online</b> and hit the download button on any episode.
                </EmptyState>
            )}
            {active.length > 0 && (
                <section className="mb-10">
                    <h2 className="mb-4 text-lg font-bold">In progress</h2>
                    <div className="flex flex-col gap-3">
                        {active.map(i => (
                            <Row key={i.id} item={i} onCancel={() => call(api.post(`/api/downloads/${i.id}/cancel`))} />
                        ))}
                    </div>
                </section>
            )}
            {done.length > 0 && (
                <section>
                    <h2 className="mb-4 text-lg font-bold">History</h2>
                    <div className="flex flex-col gap-3">
                        {done.map(i => (
                            <Row key={i.id} item={i} onRetry={() => call(api.post(`/api/downloads/${i.id}/retry`))} onRemove={() => call(api.del(`/api/downloads/${i.id}`))} />
                        ))}
                    </div>
                </section>
            )}
        </div>
    )
}

function Row({ item, onCancel, onRetry, onRemove }: { item: DownloadItem; onCancel?: () => void; onRetry?: () => void; onRemove?: () => void }) {
    const statusLabel = { queued: "Queued", resolving: "Resolving stream", downloading: "Downloading", completed: "Completed", failed: "Failed", canceled: "Canceled" }[item.status]
    const tone = { queued: "gray", resolving: "blue", downloading: "brand", completed: "green", failed: "red", canceled: "gray" }[item.status] as "gray"
    return (
        <div className="card flex items-center gap-4 p-3 pr-4">
            <Link to={`/entry?id=${item.mediaId}`} className="shrink-0">
                {item.image ? <img src={img(item.image)} alt="" className="h-20 w-14 rounded-lg object-cover" /> : <div className="h-20 w-14 rounded-lg bg-surface-3" />}
            </Link>
            <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                    <p className="truncate font-semibold">{item.animeTitle}</p>
                    <Badge tone="brand">Ep {item.episode}</Badge>
                    <Badge className="uppercase">{item.mode}</Badge>
                    <Badge>{item.source === "anicli" ? "ani-cli" : item.provider}</Badge>
                </div>
                <div className="mt-2 flex items-center gap-3">
                    {item.status === "downloading" || item.status === "resolving" ? <Loader2 className="size-4 animate-spin text-brand" /> : item.status === "completed" ? <CheckCircle2 className="size-4 text-emerald-400" /> : item.status === "failed" ? <XCircle className="size-4 text-rose-400" /> : null}
                    <Badge tone={tone}>{statusLabel}</Badge>
                    {item.status === "downloading" && (
                        <span className="text-xs text-muted tabular-nums">
                            {Math.floor(item.progress * 100)}% {item.speed && `· ${item.speed}`} {item.eta && `· ETA ${item.eta}`}
                        </span>
                    )}
                    {item.error && <span className="truncate text-xs text-rose-300" title={item.error}>{item.error}</span>}
                    {item.status === "completed" && item.output && <span className="truncate text-xs text-subtle" title={item.output}>{item.output}</span>}
                </div>
                {(item.status === "downloading" || item.status === "resolving") && <Progress value={item.progress} className={cn("mt-2.5", item.status === "resolving" && "opacity-50")} />}
            </div>
            <div className="flex shrink-0 gap-1">
                {onCancel && (
                    <IconButton label="Cancel" onClick={onCancel}>
                        <X className="size-4" />
                    </IconButton>
                )}
                {onRetry && (item.status === "failed" || item.status === "canceled") && (
                    <IconButton label="Retry" onClick={onRetry}>
                        <RotateCcw className="size-4" />
                    </IconButton>
                )}
                {onRemove && (
                    <IconButton label="Remove from list" onClick={onRemove}>
                        <Trash2 className="size-4" />
                    </IconButton>
                )}
            </div>
        </div>
    )
}
