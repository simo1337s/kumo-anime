import { useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, Download, MonitorPlay, Play, RefreshCw, Search, Terminal, Wand2 } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
import { api, qs } from "@/lib/api"
import { usePlay } from "@/lib/play"
import { installHint, installSource } from "@/lib/platform"
import { canInstallPrograms, useInstallPrograms } from "@/lib/programs"
import { useEpisodeMarker, useLanguageMode, useOnlineProviders, useStatus, useStreamEpisodes } from "@/lib/queries"
import type { EntryView } from "@/lib/types"
import { cn } from "@/lib/utils"
import { EpisodeCard, WatchedToggle } from "../EpisodeCard"
import { Badge, Button, Dialog, EmptyState, Field, IconButton, Input, Select, Skeleton, Tooltip } from "../ui"

export function StreamPanel({ entry }: { entry: EntryView }) {
    const { data: status } = useStatus()
    const settings = status?.settings
    const { data: providers } = useOnlineProviders()
    const media = entry.media
    const [provider, setProvider] = useState<string>(() => localStorage.getItem(`kumo-provider-${media.id}`) || settings?.onlineStream.defaultProvider || "ani-cli")
    // Sub/dub is remembered per anime on the server (also used by "continue
    // watching", the next-episode button and local files).
    const language = useLanguageMode(media.id)
    const dub = language.mode ? language.mode === "dub" : settings?.aniCli.defaultMode === "dub" || !!settings?.onlineStream.preferDub
    const setDub = (d: boolean) => language.set(d ? "dub" : "sub")
    const [matchOpen, setMatchOpen] = useState(false)
    const [dlOpen, setDlOpen] = useState(false)
    const { playStream, streamPlayer, remote } = usePlay()
    const marker = useEpisodeMarker(media, entry.listEntry)
    const qc = useQueryClient()

    useEffect(() => {
        try {
            localStorage.setItem(`kumo-provider-${media.id}`, provider)
        } catch {
            /* ignore */
        }
    }, [provider, media.id])

    const prov = providers?.find(p => p.id === provider)
    // Wait for the remembered choice, or the first request would list (and
    // remember) the default mode instead.
    const { data, isLoading, error, refetch, isFetching } = useStreamEpisodes(provider, media.id, dub, language.loaded)
    const progress = entry.listEntry?.progress ?? 0

    const episodes = useMemo(() => {
        const meta = new Map(entry.episodes.map(e => [e.number, e]))
        return (data?.episodes ?? []).map(e => {
            const m = meta.get(Math.floor(e.number))
            return {
                number: e.number,
                title: m?.title || e.title || "",
                image: m?.image || media.bannerImage || media.coverImage?.extraLarge,
                runtime: m?.runtime,
                watched: e.number <= progress,
                resumeAt: m?.resumeAt ?? 0,
                duration: m?.history?.duration ?? 0,
            }
        })
    }, [data, entry.episodes, media, progress])

    const refresh = async () => {
        await api.get(`/api/onlinestream/episodes${qs({ provider, mediaId: media.id, dub, refresh: 1 })}`).catch(e => toast.error(e.message))
        qc.invalidateQueries({ queryKey: ["os-episodes", provider, media.id, dub] })
    }

    const providerOptions = (providers ?? [{ id: "ani-cli", name: "ani-cli", builtin: true } as any]).map(p => ({ value: p.id, label: p.builtin ? "ani-cli (built-in)" : p.name }))
    const aniMissing = provider === "ani-cli" && status && !status.features.aniCli

    return (
        <div className="flex flex-col gap-6">
            <div className="flex flex-wrap items-center gap-3">
                <Select value={provider} onChange={setProvider} options={providerOptions} className="w-60" />
                {(prov?.supportsDub ?? true) && (
                    <div className="flex rounded-xl border border-line bg-surface-1 p-1">
                        {(["sub", "dub"] as const).map(m => (
                            <button
                                key={m}
                                onClick={() => setDub(m === "dub")}
                                className={cn("h-8 rounded-lg px-4 text-sm font-semibold uppercase transition", (m === "dub") === dub ? "bg-brand text-white" : "text-muted hover:text-fg")}
                            >
                                {m}
                            </button>
                        ))}
                    </div>
                )}
                <IconButton label="Refresh episode list" variant="subtle" onClick={refresh}>
                    <RefreshCw className={cn("size-4", isFetching && "animate-spin")} />
                </IconButton>
                <Button variant="subtle" icon={<Wand2 className="size-4" />} onClick={() => setMatchOpen(true)}>
                    {data?.mapping ? "Change match" : "Find manually"}
                </Button>
                <Button variant="subtle" icon={<Download className="size-4" />} disabled={!episodes.length} onClick={() => setDlOpen(true)}>
                    Download episodes
                </Button>
                {data?.mapping && (
                    <span className="text-sm text-muted">
                        Matched <span className="font-semibold text-fg">{data.mapping.title}</span>
                        {data.mapping.score < 0.8 && (
                            <Badge tone="amber" className="ml-2">
                                low confidence
                            </Badge>
                        )}
                    </span>
                )}
                {!remote && (
                    <span className="ml-auto flex items-center gap-1.5 text-xs text-subtle">
                        <MonitorPlay className="size-3.5" /> Plays in {streamPlayer() === "mpv" ? "mpv" : "the in-app player"}
                    </span>
                )}
            </div>

            {aniMissing && (
                <div className="flex items-start gap-4 rounded-2xl border border-amber-500/25 bg-amber-500/10 p-5">
                    <Terminal className="mt-0.5 size-5 text-amber-300" />
                    <div className="text-sm">
                        <p className="font-semibold text-amber-200">ani-cli isn’t installed</p>
                        {canInstallPrograms(status) ? (
                            <p className="mt-1 text-muted">Kumo can install it, with the programs it needs, or pick an extension provider from the marketplace.</p>
                        ) : (
                            <p className="mt-1 text-muted">
                                {installSource(status?.platform, "ani-cli")} <code className="rounded bg-black/40 px-1.5 py-0.5">{installHint(status?.platform, "ani-cli")}</code> (it also
                                needs <code className="rounded bg-black/40 px-1.5 py-0.5">mpv</code>, <code className="rounded bg-black/40 px-1.5 py-0.5">yt-dlp</code> or{" "}
                                <code className="rounded bg-black/40 px-1.5 py-0.5">ffmpeg</code>), or pick an extension provider from the marketplace.
                            </p>
                        )}
                    </div>
                    {canInstallPrograms(status) && <InstallAniCli running={!!status?.setupRunning} />}
                </div>
            )}

            {isLoading && (
                <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-6">
                    {Array.from({ length: 6 }).map((_, i) => (
                        <Skeleton key={i} className="aspect-video" />
                    ))}
                </div>
            )}
            {error && !aniMissing && (
                <EmptyState icon={<AlertTriangle className="size-6" />} title="Couldn't load episodes" action={<Button onClick={() => refetch()}>Try again</Button>}>
                    {(error as Error).message}
                </EmptyState>
            )}
            {!isLoading && !error && data && episodes.length === 0 && (
                <EmptyState icon={<Search className="size-6" />} title="No match found" action={<Button variant="primary" onClick={() => setMatchOpen(true)}>Search manually</Button>}>
                    {prov?.name ?? provider} didn’t return episodes for this anime{dub ? " in dub" : ""}.
                </EmptyState>
            )}

            {episodes.length > 0 && (
                <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-x-6 gap-y-8">
                    {episodes.map(ep => (
                        <EpisodeCard
                            key={ep.number}
                            image={ep.image}
                            number={ep.number}
                            title={ep.title || `Episode ${ep.number}`}
                            subtitle={`Episode ${ep.number}${dub ? " · Dub" : " · Sub"}`}
                            runtime={ep.runtime}
                            watched={ep.watched}
                            resumeAt={ep.resumeAt}
                            progress={ep.duration ? ep.resumeAt / ep.duration : 0}
                            blur={settings?.ui.blurUnwatched}
                            onClick={() => playStream(provider, media.id, ep.number, dub)}
                            actions={
                                <div className="flex gap-1">
                                    <WatchedToggle watched={ep.watched} disabled={marker.pending} onToggle={() => marker.mark(ep.number, !ep.watched)} />
                                    {!remote && (
                                        <Tooltip content={streamPlayer() === "mpv" ? "Play in the in-app player" : "Play in mpv"}>
                                            <button
                                                className="grid size-8 place-items-center rounded-lg bg-black/60 text-white backdrop-blur hover:bg-black/80"
                                                onClick={e => {
                                                    e.stopPropagation()
                                                    playStream(provider, media.id, ep.number, dub, { player: streamPlayer() === "mpv" ? "builtin" : "mpv" })
                                                }}
                                            >
                                                {streamPlayer() === "mpv" ? <Play className="size-4" /> : <MonitorPlay className="size-4" />}
                                            </button>
                                        </Tooltip>
                                    )}
                                    <Tooltip content="Download to library">
                                        <button
                                            className="grid size-8 place-items-center rounded-lg bg-black/60 text-white backdrop-blur hover:bg-black/80"
                                            onClick={e => {
                                                e.stopPropagation()
                                                api.post("/api/onlinestream/download", { provider, mediaId: media.id, episodes: [ep.number], dub }).catch(err => toast.error(err.message))
                                            }}
                                        >
                                            <Download className="size-4" />
                                        </button>
                                    </Tooltip>
                                </div>
                            }
                        />
                    ))}
                </div>
            )}

            <ManualMatchDialog open={matchOpen} onOpenChange={setMatchOpen} provider={provider} mediaId={media.id} dub={dub} defaultQuery={media.title.english || media.title.romaji || ""} />
            <DownloadDialog
                open={dlOpen}
                onOpenChange={setDlOpen}
                provider={provider}
                mediaId={media.id}
                dub={dub}
                episodes={episodes.map(e => e.number)}
                from={Math.max(progress + 1, episodes[0]?.number ?? 1)}
            />
        </div>
    )
}

function ManualMatchDialog({ open, onOpenChange, provider, mediaId, dub, defaultQuery }: { open: boolean; onOpenChange: (v: boolean) => void; provider: string; mediaId: number; dub: boolean; defaultQuery: string }) {
    const [q, setQ] = useState(defaultQuery)
    const [results, setResults] = useState<{ id: string; title: string; subOrDub?: string; query?: string; index?: number; episodes?: number }[]>([])
    const [busy, setBusy] = useState(false)
    const qc = useQueryClient()
    useEffect(() => setQ(defaultQuery), [defaultQuery])
    const search = async () => {
        setBusy(true)
        try {
            setResults(await api.get(`/api/onlinestream/search${qs({ provider, q, dub })}`))
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }
    const pick = async (r: (typeof results)[number]) => {
        try {
            await api.post("/api/onlinestream/mapping", { provider, mediaId, dub, id: r.id, title: r.title, query: r.query, index: r.index })
            toast.success(`Now using “${r.title}”`)
            qc.invalidateQueries({ queryKey: ["os-episodes", provider, mediaId, dub] })
            onOpenChange(false)
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    return (
        <Dialog open={open} onOpenChange={onOpenChange} title="Pick the right anime" description="Search the provider and choose the matching entry. Kumo remembers your choice.">
            <div className="flex gap-2">
                <Input value={q} onChange={e => setQ(e.target.value)} onKeyDown={e => e.key === "Enter" && search()} placeholder="Search…" icon={<Search className="size-4" />} />
                <Button variant="primary" loading={busy} onClick={search}>
                    Search
                </Button>
            </div>
            <div className="mt-4 flex flex-col gap-1">
                {results.map(r => (
                    <button key={r.id} onClick={() => pick(r)} className="flex items-center justify-between gap-3 rounded-xl px-3 py-2.5 text-left hover:bg-white/[0.06]">
                        <span className="font-medium">{r.title}</span>
                        <span className="flex shrink-0 gap-1.5">
                            {r.episodes ? <Badge>{r.episodes} eps</Badge> : null}
                            {r.subOrDub && <Badge tone="brand">{r.subOrDub}</Badge>}
                        </span>
                    </button>
                ))}
                {!busy && results.length === 0 && <p className="py-6 text-center text-sm text-subtle">Search to see results</p>}
            </div>
        </Dialog>
    )
}

function DownloadDialog({ open, onOpenChange, provider, mediaId, dub, episodes, from }: { open: boolean; onOpenChange: (v: boolean) => void; provider: string; mediaId: number; dub: boolean; episodes: number[]; from: number }) {
    const last = episodes[episodes.length - 1] ?? 1
    const [start, setStart] = useState(from)
    const [end, setEnd] = useState(last)
    const [quality, setQuality] = useState("best")
    const [busy, setBusy] = useState(false)
    useEffect(() => {
        setStart(Math.min(from, last))
        setEnd(last)
    }, [from, last])
    const go = async () => {
        setBusy(true)
        try {
            const eps = episodes.filter(e => e >= start && e <= end)
            await api.post("/api/onlinestream/download", { provider, mediaId, episodes: eps, dub, quality })
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
            title="Download episodes"
            description={`Saved into your library folder (${dub ? "dub" : "sub"}) and matched automatically.`}
            footer={
                <>
                    <Button variant="ghost" onClick={() => onOpenChange(false)}>
                        Cancel
                    </Button>
                    <Button variant="primary" loading={busy} icon={<Download className="size-4" />} onClick={go}>
                        Download {episodes.filter(e => e >= start && e <= end).length} episodes
                    </Button>
                </>
            }
        >
            <div className="grid grid-cols-2 gap-4">
                <Field label="From episode">
                    <Input type="number" value={start} min={episodes[0]} max={last} onChange={e => setStart(Number(e.target.value))} />
                </Field>
                <Field label="To episode">
                    <Input type="number" value={end} min={start} max={last} onChange={e => setEnd(Number(e.target.value))} />
                </Field>
                <Field label="Quality" className="col-span-2">
                    <Select
                        value={quality}
                        onChange={setQuality}
                        options={[
                            { value: "best", label: "Best available" },
                            { value: "1080", label: "1080p" },
                            { value: "720", label: "720p" },
                            { value: "480", label: "480p" },
                            { value: "worst", label: "Smallest" },
                        ]}
                    />
                </Field>
            </div>
        </Dialog>
    )
}

// Windows: Kumo's setup installs ani-cli with Git (whose bash runs it).
function InstallAniCli({ running }: { running: boolean }) {
    const install = useInstallPrograms()
    return (
        <Button variant="primary" size="sm" className="ml-auto" icon={<Download className="size-4" />} loading={install.isPending} disabled={running} onClick={() => install.mutate()}>
            {running ? "Installing…" : "Install"}
        </Button>
    )
}
