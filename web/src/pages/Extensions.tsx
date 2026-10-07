import { useQueryClient } from "@tanstack/react-query"
import {
    AlertTriangle,
    ArrowUpCircle,
    BookOpen,
    CheckCircle2,
    Download,
    ExternalLink,
    FileText,
    Globe,
    Link2,
    Magnet,
    Puzzle,
    RefreshCw,
    Search,
    Settings2,
    ShieldAlert,
    ShieldCheck,
    Star,
    Trash2,
    Tv,
} from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { useSearchParams } from "react-router-dom"
import { toast } from "sonner"
import { Badge, Button, Dialog, EmptyState, Field, IconButton, Input, Select, Switch, Tabs } from "@/components/ui"
import { api } from "@/lib/api"
import { useExtensions, useMarketplace, useStatus } from "@/lib/queries"
import type { ExtensionInfo, LogLineT, MarketEntry } from "./extensionTypes"
import { cn } from "@/lib/utils"

const TYPES: Record<string, { label: string; icon: React.ReactNode }> = {
    plugin: { label: "Plugin", icon: <Puzzle className="size-3.5" /> },
    "onlinestream-provider": { label: "Online streaming", icon: <Tv className="size-3.5" /> },
    "anime-torrent-provider": { label: "Torrent", icon: <Magnet className="size-3.5" /> },
    "manga-provider": { label: "Manga", icon: <BookOpen className="size-3.5" /> },
    "custom-source": { label: "Custom source", icon: <Globe className="size-3.5" /> },
}

export default function ExtensionsPage() {
    const [params, setParams] = useSearchParams()
    const tab = params.get("tab") === "marketplace" ? "marketplace" : "installed"
    const { data: installed } = useExtensions()
    const [manualOpen, setManualOpen] = useState(false)
    return (
        <div className="min-h-full px-6 pt-8 pb-24 md:px-8 xl:px-10">
            <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
                <div>
                    <h1 className="text-[1.75rem] font-semibold tracking-tight">Extensions</h1>
                    <p className="mt-1 max-w-2xl text-muted">Streaming, torrent and manga providers plus plugins, from the extension marketplace.</p>
                </div>
                <Button icon={<Link2 className="size-4" />} onClick={() => setManualOpen(true)}>
                    Install from URL
                </Button>
            </div>
            <Tabs
                className="mb-8"
                value={tab}
                onChange={t => setParams({ tab: t }, { replace: true })}
                tabs={[
                    { value: "installed", label: "Installed", count: installed?.length ?? 0 },
                    { value: "marketplace", label: "Marketplace" },
                ]}
            />
            {tab === "installed" ? <Installed /> : <Marketplace />}
            <InstallUrlDialog open={manualOpen} onOpenChange={setManualOpen} />
        </div>
    )
}

function ExtIcon({ src, className }: { src?: string; className?: string }) {
    const [err, setErr] = useState(false)
    if (!src || err)
        return (
            <span className={cn("grid shrink-0 place-items-center rounded-lg bg-white/[0.06] text-muted", className)}>
                <Puzzle className="size-5" />
            </span>
        )
    return <img src={src} alt="" onError={() => setErr(true)} className={cn("shrink-0 rounded-lg bg-white/5 object-cover", className)} />
}

// ---------------------------------------------------------------------------

function Installed() {
    const { data, isLoading } = useExtensions()
    const qc = useQueryClient()
    const [checking, setChecking] = useState(false)
    const [updates, setUpdates] = useState<Record<string, string>>({})
    const check = async () => {
        setChecking(true)
        try {
            const u = await api.get<{ id: string; latestVersion: string }[]>("/api/extensions/updates")
            setUpdates(Object.fromEntries(u.map(x => [x.id, x.latestVersion])))
            toast.success(u.length ? `${u.length} update(s) available` : "Everything is up to date")
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setChecking(false)
        }
    }
    if (isLoading) return <div className="card h-40 shimmer" />
    if (!data?.length)
        return (
            <EmptyState icon={<Puzzle className="size-6" />} title="No extensions installed">
                Browse the marketplace to add providers and plugins.
            </EmptyState>
        )
    const groups = Object.keys(TYPES)
        .map(t => ({ type: t, items: data.filter(e => e.manifest.type === t) }))
        .filter(g => g.items.length)
    return (
        <div className="flex flex-col gap-10">
            <div>
                <Button size="sm" icon={<RefreshCw className="size-4" />} loading={checking} onClick={check}>
                    Check for updates
                </Button>
            </div>
            {groups.map(g => (
                <section key={g.type}>
                    <h2 className="mb-4 flex items-center gap-2 text-lg font-semibold tracking-tight">
                        {TYPES[g.type].icon} {TYPES[g.type].label}
                    </h2>
                    <div className="grid gap-3 lg:grid-cols-2 2xl:grid-cols-3">
                        {g.items.map(e => (
                            <InstalledCard key={e.manifest.id} ext={e} update={updates[e.manifest.id]} onChange={() => qc.invalidateQueries({ queryKey: ["extensions"] })} />
                        ))}
                    </div>
                </section>
            ))}
        </div>
    )
}

function InstalledCard({ ext, update, onChange }: { ext: ExtensionInfo; update?: string; onChange: () => void }) {
    const m = ext.manifest
    const [cfgOpen, setCfgOpen] = useState(false)
    const [logsOpen, setLogsOpen] = useState(false)
    const [grantOpen, setGrantOpen] = useState(false)
    const [busy, setBusy] = useState(false)
    const call = async (p: Promise<unknown>, ok?: string) => {
        setBusy(true)
        try {
            await p
            if (ok) toast.success(ok)
            onChange()
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }
    const isPlugin = m.type === "plugin"
    const needsGrant = isPlugin && !ext.granted
    return (
        <div className="card flex flex-col gap-4 p-4">
            <div className="flex items-start gap-3">
                <ExtIcon src={m.icon} className="size-12" />
                <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                        <p className="truncate font-semibold">{m.name}</p>
                        <span className="text-xs text-subtle">v{m.version}</span>
                    </div>
                    <p className="text-xs text-subtle">by {m.author}</p>
                </div>
                <Switch
                    checked={ext.enabled && !needsGrant}
                    disabled={busy}
                    onChange={v => (v && needsGrant ? setGrantOpen(true) : call(api.post(`/api/extensions/${m.id}/enable`, { enabled: v })))}
                />
            </div>
            {m.description && <p className="line-clamp-2 text-sm text-muted">{m.description}</p>}
            <div className="flex flex-wrap gap-1.5">
                <Badge>{m.language === "typescript" ? "TS" : "JS"}</Badge>
                {m.lang && <Badge className="uppercase">{m.lang}</Badge>}
                {!ext.supported && <Badge tone="amber">Not supported by Kumo yet</Badge>}
                {ext.error && <Badge tone="red">Error</Badge>}
                {ext.configError && <Badge tone="amber">Needs configuration</Badge>}
                {needsGrant && <Badge tone="amber">Permissions needed</Badge>}
                {ext.running && <Badge tone="green">Running</Badge>}
            </div>
            {ext.error && <p className="rounded-lg bg-rose-500/10 px-3 py-2 text-xs text-rose-200">{ext.error}</p>}
            <div className="mt-auto flex flex-wrap items-center gap-1.5">
                {update && (
                    <Button size="sm" variant="primary" icon={<ArrowUpCircle className="size-4" />} loading={busy} onClick={() => call(api.post(`/api/extensions/${m.id}/update`), `Updated to ${update}`)}>
                        Update to {update}
                    </Button>
                )}
                {needsGrant && (
                    <Button size="sm" variant="primary" icon={<ShieldCheck className="size-4" />} onClick={() => setGrantOpen(true)}>
                        Review permissions
                    </Button>
                )}
                {(m.userConfig?.fields?.length ?? 0) > 0 && (
                    <Button size="sm" icon={<Settings2 className="size-4" />} onClick={() => setCfgOpen(true)}>
                        Configure
                    </Button>
                )}
                <Button size="sm" variant="ghost" icon={<FileText className="size-4" />} onClick={() => setLogsOpen(true)}>
                    Logs
                </Button>
                {m.website && (
                    <a href={m.website} target="_blank" rel="noopener noreferrer">
                        <IconButton size="sm" label="Website">
                            <ExternalLink className="size-4" />
                        </IconButton>
                    </a>
                )}
                <IconButton size="sm" label="Uninstall" className="ml-auto hover:text-rose-300" onClick={() => confirm(`Uninstall ${m.name}?`) && call(api.del(`/api/extensions/${m.id}`), "Uninstalled")}>
                    <Trash2 className="size-4" />
                </IconButton>
            </div>
            <ConfigDialog open={cfgOpen} onOpenChange={setCfgOpen} ext={ext} onSaved={onChange} />
            <LogsDialog open={logsOpen} onOpenChange={setLogsOpen} id={m.id} />
            <GrantDialog open={grantOpen} onOpenChange={setGrantOpen} ext={ext} onDone={onChange} />
        </div>
    )
}

function ConfigDialog({ open, onOpenChange, ext, onSaved }: { open: boolean; onOpenChange: (v: boolean) => void; ext: ExtensionInfo; onSaved: () => void }) {
    const fields = ext.manifest.userConfig?.fields ?? []
    const [values, setValues] = useState<Record<string, string>>({})
    useEffect(() => {
        if (open) setValues(Object.fromEntries(fields.map(f => [f.name, ext.userConfig.values?.[f.name] ?? f.default ?? ""])))
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open])
    const save = async () => {
        try {
            await api.post(`/api/extensions/${ext.manifest.id}/config`, { values })
            toast.success("Saved")
            onSaved()
            onOpenChange(false)
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    return (
        <Dialog open={open} onOpenChange={onOpenChange} title={`Configure ${ext.manifest.name}`} footer={<Button variant="primary" onClick={save}>Save</Button>}>
            <div className="flex flex-col gap-4">
                {fields.map(f => (
                    <Field key={f.name} label={f.label || f.name} help={f.description}>
                        {f.type === "switch" ? (
                            <Switch checked={values[f.name] === "true"} onChange={v => setValues(s => ({ ...s, [f.name]: v ? "true" : "false" }))} />
                        ) : f.type === "select" ? (
                            <Select value={values[f.name] ?? ""} onChange={v => setValues(s => ({ ...s, [f.name]: v }))} options={(f.options ?? []).map(o => ({ value: o.value, label: o.label }))} />
                        ) : (
                            <Input value={values[f.name] ?? ""} onChange={e => setValues(s => ({ ...s, [f.name]: e.target.value }))} />
                        )}
                    </Field>
                ))}
            </div>
        </Dialog>
    )
}

function LogsDialog({ open, onOpenChange, id }: { open: boolean; onOpenChange: (v: boolean) => void; id: string }) {
    const [lines, setLines] = useState<LogLineT[]>([])
    useEffect(() => {
        if (open) api.get<LogLineT[] | null>(`/api/extensions/${id}/logs`).then(l => setLines(l ?? [])).catch(e => toast.error(e.message))
    }, [open, id])
    return (
        <Dialog open={open} onOpenChange={onOpenChange} title="Extension logs" className="w-[min(94vw,820px)]">
            {lines.length === 0 ? (
                <p className="py-8 text-center text-sm text-subtle">No output yet.</p>
            ) : (
                <pre className="max-h-[60vh] overflow-auto rounded-xl bg-black/40 p-4 text-xs leading-relaxed">
                    {lines.map((l, i) => (
                        <div key={i} className={cn(l.level === "error" ? "text-rose-300" : l.level === "warn" ? "text-amber-300" : "text-fg/80")}>
                            <span className="text-subtle">{new Date(l.time * 1000).toLocaleTimeString()} </span>
                            {l.message}
                        </div>
                    ))}
                </pre>
            )}
        </Dialog>
    )
}

function GrantDialog({ open, onOpenChange, ext, onDone }: { open: boolean; onOpenChange: (v: boolean) => void; ext: ExtensionInfo; onDone: () => void }) {
    const perms = ext.manifest.plugin?.permissions
    const grant = async () => {
        try {
            await api.post(`/api/extensions/${ext.manifest.id}/grant`)
            toast.success(`${ext.manifest.name} enabled`)
            onDone()
            onOpenChange(false)
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const scopes = perms?.scopes ?? []
    const domains = perms?.allow?.networkAccess?.allowedDomains ?? []
    const unsafe = perms?.allow?.unsafeFlags ?? []
    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title={`Allow ${ext.manifest.name}?`}
            description="Plugins run sandboxed. Review what this one asks for before enabling it."
            footer={
                <>
                    <Button variant="ghost" onClick={() => onOpenChange(false)}>
                        Cancel
                    </Button>
                    <Button variant="primary" icon={<ShieldCheck className="size-4" />} onClick={grant}>
                        Grant & enable
                    </Button>
                </>
            }
        >
            <div className="flex flex-col gap-5 text-sm">
                <div>
                    <p className="mb-2 font-semibold">Requested access</p>
                    <div className="flex flex-wrap gap-1.5">
                        {scopes.length ? scopes.map(s => <Badge key={s} tone={s.includes("token") || s === "system" ? "amber" : "gray"}>{s}</Badge>) : <span className="text-muted">Nothing special</span>}
                    </div>
                </div>
                {domains.length > 0 && (
                    <div>
                        <p className="mb-2 font-semibold">Network</p>
                        <p className="text-muted">{domains.join(", ")}</p>
                        {perms?.allow.networkAccess.reasoning && <p className="mt-1 text-xs text-subtle">“{perms.allow.networkAccess.reasoning}”</p>}
                    </div>
                )}
                {unsafe.length > 0 && (
                    <div className="rounded-xl border border-amber-500/25 bg-amber-500/10 p-3 text-amber-200">
                        <p className="flex items-center gap-2 font-semibold">
                            <ShieldAlert className="size-4" /> Page modification
                        </p>
                        <p className="mt-1 text-xs">This plugin wants to modify the app’s pages. Kumo doesn’t allow that, so those features won’t work.</p>
                    </div>
                )}
                <p className="text-xs text-subtle">
                    Kumo never gives plugins access to your files, terminal or local network. {scopes.includes("anilist-token") && "This plugin can read your AniList token to make requests on your behalf."}
                </p>
            </div>
        </Dialog>
    )
}

// ---------------------------------------------------------------------------

function Marketplace() {
    const { data: status } = useStatus()
    const [url, setUrl] = useState("")
    const effectiveUrl = url || status?.settings.extensions.marketplaceUrl || ""
    const { data, isLoading, error, refetch, isFetching } = useMarketplace(effectiveUrl)
    const [type, setType] = useState("all")
    const [q, setQ] = useState("")
    const [sort, setSort] = useState("stars")
    const [hideBroken, setHideBroken] = useState(true)
    const [reloading, setReloading] = useState(false)
    const qc = useQueryClient()
    const reload = async () => {
        setReloading(true)
        try {
            await api.get(`/api/extensions/marketplace?refresh=1&url=${encodeURIComponent(effectiveUrl)}`)
            qc.invalidateQueries({ queryKey: ["marketplace"] })
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setReloading(false)
        }
    }

    const entries = useMemo(() => {
        let list = (data ?? []).filter(e => (type === "all" || e.type === type) && (!hideBroken || (!e.brokenTag && !e.deprecatedTag)))
        if (q.trim()) {
            const s = q.toLowerCase()
            list = list.filter(e => e.name.toLowerCase().includes(s) || e.description?.toLowerCase().includes(s) || e.author?.toLowerCase().includes(s))
        }
        return [...list].sort((a, b) => (sort === "stars" ? (b.stars ?? 0) - (a.stars ?? 0) : sort === "new" ? (b.updatedAt ?? "").localeCompare(a.updatedAt ?? "") : a.name.localeCompare(b.name)))
    }, [data, type, q, sort, hideBroken])

    const counts = useMemo(() => {
        const c: Record<string, number> = {}
        for (const e of data ?? []) c[e.type] = (c[e.type] ?? 0) + 1
        return c
    }, [data])

    return (
        <div className="flex flex-col gap-6">
            <div className="card flex flex-col gap-3 p-4">
                <div className="flex gap-2">
                    <Input defaultValue={effectiveUrl} onKeyDown={e => e.key === "Enter" && setUrl((e.target as HTMLInputElement).value)} onBlur={e => setUrl(e.target.value)} icon={<Globe className="size-4" />} />
                    <Button icon={<RefreshCw className={cn("size-4", (reloading || isFetching) && "animate-spin")} />} disabled={reloading} onClick={reload}>
                        Reload
                    </Button>
                </div>
                <div className="flex flex-wrap items-center gap-3">
                    <Input value={q} onChange={e => setQ(e.target.value)} placeholder="Search extensions…" icon={<Search className="size-4" />} className="w-72" />
                    <Select
                        className="w-48"
                        value={type}
                        onChange={setType}
                        options={[{ value: "all", label: `All types (${data?.length ?? 0})` }, ...Object.entries(TYPES).map(([k, v]) => ({ value: k, label: `${v.label} (${counts[k] ?? 0})` }))]}
                    />
                    <Select
                        className="w-40"
                        value={sort}
                        onChange={setSort}
                        options={[
                            { value: "stars", label: "Most starred" },
                            { value: "new", label: "Recently updated" },
                            { value: "name", label: "Name" },
                        ]}
                    />
                    <label className="flex items-center gap-2 text-sm text-muted">
                        <Switch checked={hideBroken} onChange={setHideBroken} /> Hide broken
                    </label>
                </div>
            </div>
            {isLoading && (
                <div className="grid gap-3 lg:grid-cols-2 2xl:grid-cols-3">
                    {Array.from({ length: 9 }).map((_, i) => (
                        <div key={i} className="card h-44 shimmer" />
                    ))}
                </div>
            )}
            {error && (
                <EmptyState icon={<AlertTriangle className="size-6" />} title="Couldn't load the marketplace" action={<Button onClick={() => refetch()}>Retry</Button>}>
                    {(error as Error).message}
                </EmptyState>
            )}
            <div className="grid gap-3 lg:grid-cols-2 2xl:grid-cols-3">
                {entries.map(e => (
                    <MarketCard key={e.id} e={e} />
                ))}
            </div>
        </div>
    )
}

function MarketCard({ e }: { e: MarketEntry }) {
    const [busy, setBusy] = useState(false)
    const [grant, setGrant] = useState<ExtensionInfo | null>(null)
    const qc = useQueryClient()
    const install = async () => {
        setBusy(true)
        try {
            const info = await api.post<ExtensionInfo>("/api/extensions/install", { manifestURI: e.manifestURI })
            qc.invalidateQueries({ queryKey: ["marketplace"] })
            qc.invalidateQueries({ queryKey: ["extensions"] })
            if (info.manifest.type === "plugin") setGrant(info)
        } catch (err: any) {
            toast.error(err.message)
        } finally {
            setBusy(false)
        }
    }
    const t = TYPES[e.type]
    return (
        <div className={cn("card flex flex-col gap-3 p-4 transition hover:border-line-strong", e.brokenTag && "opacity-60")}>
            <div className="flex items-start gap-3">
                <ExtIcon src={e.icon} className="size-12" />
                <div className="min-w-0 flex-1">
                    <p className="truncate font-semibold">{e.name}</p>
                    <p className="truncate text-xs text-subtle">
                        by {e.author} · v{e.version}
                    </p>
                </div>
                {e.stars > 0 && (
                    <span className="flex items-center gap-1 text-xs font-semibold text-amber-300">
                        <Star className="size-3.5 fill-amber-300" /> {e.stars}
                    </span>
                )}
            </div>
            <p className="line-clamp-2 min-h-10 text-sm text-muted">{e.description}</p>
            <div className="flex flex-wrap gap-1.5">
                {t && (
                    <Badge tone="brand">
                        {t.icon}
                        {t.label}
                    </Badge>
                )}
                {e.lang && <Badge className="uppercase">{e.lang}</Badge>}
                {e.workingTag && (
                    <Badge tone="green">
                        <CheckCircle2 className="size-3" /> Working
                    </Badge>
                )}
                {e.brokenTag && <Badge tone="red">Broken</Badge>}
                {e.deprecatedTag && <Badge tone="amber">Deprecated</Badge>}
                {e.official && <Badge tone="blue">Official</Badge>}
                {!e.supported && <Badge tone="amber">Not supported</Badge>}
            </div>
            <div className="mt-auto flex items-center gap-2 pt-1">
                {e.installed ? (
                    e.hasUpdate ? (
                        <Button size="sm" variant="primary" icon={<ArrowUpCircle className="size-4" />} loading={busy} onClick={install}>
                            Update to {e.version}
                        </Button>
                    ) : (
                        <Badge tone="green">
                            <CheckCircle2 className="size-3" /> Installed
                        </Badge>
                    )
                ) : (
                    <Button size="sm" variant="white" icon={<Download className="size-4" />} loading={busy} onClick={install} disabled={!e.manifestURI}>
                        Install
                    </Button>
                )}
                {e.permalink && (
                    <a href={e.permalink} target="_blank" rel="noopener noreferrer" className="ml-auto text-xs text-subtle hover:text-fg" title="VirusTotal scan">
                        VT {e.flags}
                    </a>
                )}
            </div>
            {grant && <GrantDialog open onOpenChange={v => !v && setGrant(null)} ext={grant} onDone={() => qc.invalidateQueries({ queryKey: ["extensions"] })} />}
        </div>
    )
}

function InstallUrlDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
    const [url, setUrl] = useState("")
    const [busy, setBusy] = useState(false)
    const qc = useQueryClient()
    const go = async () => {
        setBusy(true)
        try {
            await api.post("/api/extensions/install", { manifestURI: url })
            qc.invalidateQueries({ queryKey: ["extensions"] })
            setUrl("")
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
            title="Install from URL"
            description="Paste the manifest URL of a marketplace extension."
            footer={
                <Button variant="primary" loading={busy} disabled={!url.trim()} onClick={go}>
                    Install
                </Button>
            }
        >
            <Input autoFocus value={url} onChange={e => setUrl(e.target.value)} placeholder="https://raw.githubusercontent.com/…/manifest.json" icon={<Link2 className="size-4" />} />
        </Dialog>
    )
}
