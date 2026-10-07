import { useQuery, useQueryClient } from "@tanstack/react-query"
import { ArrowDown, ArrowUp, FolderOpen, Magnet, Pause, Play, Plus, Power, Settings2, Trash2 } from "lucide-react"
import { useState } from "react"
import { Link, useNavigate } from "react-router-dom"
import { toast } from "sonner"
import { Badge, Button, Dialog, Dropdown, DropdownContent, DropdownItem, DropdownTrigger, EmptyState, IconButton, Input, Progress, Tabs } from "@/components/ui"
import { api } from "@/lib/api"
import { useStatus, useTorrentList } from "@/lib/queries"
import type { Torrent } from "@/lib/types"
import { cn, formatBytes, formatSpeed, timeUntil } from "@/lib/utils"

const stateTone: Record<string, "green" | "blue" | "amber" | "red" | "gray" | "brand"> = {
    downloading: "brand",
    seeding: "green",
    completed: "green",
    paused: "gray",
    stalled: "amber",
    checking: "blue",
    queued: "gray",
    metadata: "blue",
    error: "red",
}

export default function TorrentsPage() {
    const { data: status } = useStatus()
    const navigate = useNavigate()
    const clientName = status?.settings.torrent.defaultClient
    const { data: clientStatus, refetch: refetchStatus } = useQuery({
        queryKey: ["torrent-client-status"],
        queryFn: () => api.get<{ client: string; connected: boolean; version: string; error?: string; needsAuth?: boolean }>("/api/torrent-client/status"),
        refetchInterval: 15000,
    })
    const { data: list, isLoading } = useTorrentList(!!clientStatus?.connected)
    const [filter, setFilter] = useState<"all" | "active" | "done">("all")
    const [addOpen, setAddOpen] = useState(false)
    const [starting, setStarting] = useState(false)
    const qc = useQueryClient()

    const act = async (hashes: string[], action: string) => {
        try {
            await api.post("/api/torrent-client/action", { hashes, action })
            qc.invalidateQueries({ queryKey: ["torrents"] })
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const start = async () => {
        setStarting(true)
        try {
            await api.post("/api/torrent-client/start")
            await refetchStatus()
            toast.success("Torrent client started")
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setStarting(false)
        }
    }

    const torrents = (list ?? []).filter(t => (filter === "all" ? true : filter === "active" ? ["downloading", "stalled", "metadata", "queued", "checking"].includes(t.state) : t.progress >= 1))
    const down = (list ?? []).reduce((a, t) => a + t.downSpeed, 0)
    const up = (list ?? []).reduce((a, t) => a + t.upSpeed, 0)

    return (
        <div className="min-h-full px-6 pt-8 pb-24 md:px-8 xl:px-10">
            <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
                <div>
                    <h1 className="text-[1.75rem] font-semibold tracking-tight">Torrents</h1>
                    <p className="mt-1 flex items-center gap-2 text-muted">
                        <span className={cn("size-2 rounded-full", clientStatus?.connected ? "bg-emerald-400" : "bg-rose-400")} />
                        {clientName === "none" || !clientName
                            ? "No torrent client configured"
                            : clientStatus?.connected
                              ? `${clientStatus.client === "qbittorrent" ? "qBittorrent" : "Transmission"} ${clientStatus.version}`
                              : clientStatus?.needsAuth
                                ? `${clientName === "qbittorrent" ? "qBittorrent" : "Transmission"} is running but needs your login`
                                : `${clientName === "qbittorrent" ? "qBittorrent" : "Transmission"} is not reachable`}
                    </p>
                </div>
                <div className="flex items-center gap-3">
                    {clientStatus?.connected && (
                        <div className="flex items-center gap-4 rounded-xl border border-line bg-surface-1 px-4 py-2 text-sm tabular-nums">
                            <span className="flex items-center gap-1.5 text-brand-strong">
                                <ArrowDown className="size-4" /> {formatSpeed(down)}
                            </span>
                            <span className="flex items-center gap-1.5 text-emerald-300">
                                <ArrowUp className="size-4" /> {formatSpeed(up)}
                            </span>
                        </div>
                    )}
                    {!clientStatus?.connected && clientName && clientName !== "none" && clientStatus?.needsAuth && (
                        <Button variant="primary" icon={<Settings2 className="size-4" />} onClick={() => navigate("/settings?tab=torrent-client")}>
                            Fix login in settings
                        </Button>
                    )}
                    {!clientStatus?.connected && clientName && clientName !== "none" && !clientStatus?.needsAuth && (
                        <Button variant="primary" icon={<Power className="size-4" />} loading={starting} onClick={start}>
                            Start client
                        </Button>
                    )}
                    <Button icon={<Plus className="size-4" />} onClick={() => setAddOpen(true)} disabled={!clientName || clientName === "none"}>
                        Add magnet
                    </Button>
                </div>
            </div>

            {(!clientName || clientName === "none") && (
                <EmptyState icon={<Magnet className="size-6" />} title="Connect a torrent client" action={<Link to="/settings?tab=torrent-client"><Button variant="primary">Open settings</Button></Link>}>
                    Kumo works with qBittorrent (Web UI) and Transmission to download anime straight into your library.
                </EmptyState>
            )}
            {clientName && clientName !== "none" && !clientStatus?.connected && clientStatus?.error && (
                <div className="mb-6 rounded-xl border border-rose-500/25 bg-rose-500/10 p-4 text-sm text-rose-200">{clientStatus.error}</div>
            )}

            {clientStatus?.connected && (
                <>
                    <Tabs
                        className="mb-6"
                        value={filter}
                        onChange={setFilter}
                        tabs={[
                            { value: "all", label: "All", count: list?.length ?? 0 },
                            { value: "active", label: "Active" },
                            { value: "done", label: "Completed" },
                        ]}
                    />
                    {isLoading && <div className="card h-40 shimmer" />}
                    {!isLoading && torrents.length === 0 && <EmptyState icon={<Magnet className="size-6" />} title="No torrents" />}
                    <div className="flex flex-col gap-3">
                        {torrents.map(t => (
                            <TorrentRow key={t.hash} t={t} onAction={a => act([t.hash], a)} canOpen={status?.client !== "lan"} />
                        ))}
                    </div>
                </>
            )}
            <AddMagnetDialog open={addOpen} onOpenChange={setAddOpen} />
        </div>
    )
}

function TorrentRow({ t, onAction, canOpen }: { t: Torrent; onAction: (a: string) => void; canOpen: boolean }) {
    const paused = t.state === "paused" || t.state === "completed"
    return (
        <div className="card flex items-center gap-5 p-4 transition hover:border-line-strong">
            <div className="min-w-0 flex-1">
                <div className="flex items-center gap-3">
                    <p className="truncate font-medium" title={t.name}>
                        {t.name}
                    </p>
                    <Badge tone={stateTone[t.state] ?? "gray"} className="capitalize">
                        {t.state}
                    </Badge>
                </div>
                <div className="mt-3 flex items-center gap-4">
                    <Progress value={t.progress} className="h-1.5" barClassName={t.progress >= 1 ? "bg-emerald-400" : undefined} />
                    <span className="w-12 shrink-0 text-right text-sm font-semibold tabular-nums">{Math.floor(t.progress * 100)}%</span>
                </div>
                <div className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-xs text-subtle tabular-nums">
                    <span>{formatBytes(t.size)}</span>
                    <span className="flex items-center gap-1">
                        <ArrowDown className="size-3" /> {formatSpeed(t.downSpeed)}
                    </span>
                    <span className="flex items-center gap-1">
                        <ArrowUp className="size-3" /> {formatSpeed(t.upSpeed)}
                    </span>
                    <span>
                        {t.seeds} seeds · {t.peers} peers
                    </span>
                    {t.eta > 0 && t.progress < 1 && <span>ETA {timeUntil(t.eta)}</span>}
                    <span>Ratio {t.ratio.toFixed(2)}</span>
                </div>
            </div>
            <div className="flex shrink-0 items-center gap-1">
                <IconButton label={paused ? "Resume" : "Pause"} onClick={() => onAction(paused ? "resume" : "pause")}>
                    {paused ? <Play className="size-4" /> : <Pause className="size-4" />}
                </IconButton>
                {canOpen && (
                    <IconButton label="Open folder" onClick={() => onAction("open")}>
                        <FolderOpen className="size-4" />
                    </IconButton>
                )}
                <Dropdown>
                    <DropdownTrigger asChild>
                        <button className="grid size-10 place-items-center rounded-xl text-muted hover:bg-white/[0.06] hover:text-rose-300" aria-label="Remove">
                            <Trash2 className="size-4" />
                        </button>
                    </DropdownTrigger>
                    <DropdownContent>
                        <DropdownItem onSelect={() => onAction("remove")}>Remove torrent (keep files)</DropdownItem>
                        <DropdownItem danger onSelect={() => onAction("removeData")}>
                            Remove torrent and delete files
                        </DropdownItem>
                    </DropdownContent>
                </Dropdown>
            </div>
        </div>
    )
}

function AddMagnetDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
    const [magnet, setMagnet] = useState("")
    const [busy, setBusy] = useState(false)
    const add = async () => {
        setBusy(true)
        try {
            await api.post("/api/torrent-client/add", { magnet })
            toast.success("Added to the torrent client")
            setMagnet("")
            onOpenChange(false)
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title="Add a torrent"
            description="Paste a magnet link or a .torrent URL. It is saved to your library folder."
            footer={
                <Button variant="primary" loading={busy} disabled={!magnet.trim()} onClick={add}>
                    Add
                </Button>
            }
        >
            <Input autoFocus value={magnet} onChange={e => setMagnet(e.target.value)} placeholder="magnet:?xt=urn:btih:…" icon={<Magnet className="size-4" />} />
        </Dialog>
    )
}
