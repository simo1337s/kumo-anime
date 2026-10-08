import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
    AppWindow,
    BookOpen,
    Check,
    ChevronDown,
    Clapperboard,
    Copy,
    Cpu,
    Download,
    Eye,
    EyeOff,
    FileClock,
    Folder,
    FolderOpen,
    Gamepad2,
    HardDrive,
    Info,
    LibraryBig,
    Magnet,
    MonitorPlay,
    Network,
    Palette,
    Plus,
    RefreshCw,
    Save,
    Terminal,
    Trash2,
    Tv,
    X,
} from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { useSearchParams } from "react-router-dom"
import { toast } from "@/lib/toast"
import { LoginDialog } from "@/components/LoginDialog"
import { Badge, Button, Dialog, Input, Select, Switch, Textarea } from "@/components/ui"
import { versionName } from "@/components/UpdateBanner"
import { api, desktop, type DesktopPrefs } from "@/lib/api"
import { useCheckUpdate, useOnlineProviders, useSaveSettings, useSharing, useStatus, useUpdateStatus } from "@/lib/queries"
import { exampleMpvPath, installHint, installSource, platformName } from "@/lib/platform"
import type { Tool } from "@/lib/platform"
import { canInstallPrograms, useInstallPrograms, useWatchSetup } from "@/lib/programs"
import { accentPreviewStore } from "@/lib/store"
import type { Settings, SharingPeer, Status } from "@/lib/types"
import { cn, copyText, formatBytes, img, relativeTime } from "@/lib/utils"

type SectionId =
    | "app"
    | "ui"
    | "library"
    | "playback"
    | "mpv"
    | "transcode"
    | "torrent-provider"
    | "torrent-client"
    | "streaming"
    | "manga"
    | "discord"
    | "logs"
    | "about"

const NAV: { items: { id: SectionId; label: string; icon: React.ReactNode }[] }[] = [
    {
        items: [
            { id: "app", label: "App", icon: <AppWindow /> },
            { id: "ui", label: "User Interface", icon: <Palette /> },
            { id: "library", label: "Local Anime Library", icon: <LibraryBig /> },
        ],
    },
    {
        items: [
            { id: "playback", label: "Video Playback", icon: <Clapperboard /> },
            { id: "mpv", label: "Desktop Media Player", icon: <MonitorPlay /> },
            { id: "transcode", label: "Transcoding / Direct Play", icon: <Cpu /> },
        ],
    },
    {
        items: [
            { id: "torrent-provider", label: "Torrent Provider", icon: <Magnet /> },
            { id: "torrent-client", label: "Torrent Client", icon: <Download /> },
        ],
    },
    {
        items: [
            { id: "streaming", label: "Online Streaming", icon: <Tv /> },
            { id: "manga", label: "Manga", icon: <BookOpen /> },
            { id: "discord", label: "Discord", icon: <Gamepad2 /> },
        ],
    },
    {
        items: [
            { id: "logs", label: "Logs & Cache", icon: <FileClock /> },
            { id: "about", label: "About & updates", icon: <Info /> },
        ],
    },
]

const ALIASES: Record<string, SectionId> = { "anicli": "streaming", "online-streaming": "streaming" }
const SECTION_IDS: string[] = NAV.flatMap(g => g.items.map(i => i.id))

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

// Puts the fields edited in draft (compared with base, the server copy the
// draft was made from) on top of a newer server copy.
function rebase(draft: Settings, base: Settings, server: Settings): Settings {
    const out: Record<string, any> = structuredClone(server)
    const old: Record<string, any> = base
    for (const [key, value] of Object.entries(draft) as [string, any][]) {
        if (same(value, old[key])) continue
        if (value && typeof value === "object" && !Array.isArray(value) && old[key] && typeof old[key] === "object") {
            for (const field of Object.keys(value)) if (!same(value[field], old[key][field])) out[key] = { ...out[key], [field]: value[field] }
        } else out[key] = value
    }
    return out as Settings
}

export default function SettingsPage() {
    const { data: status } = useStatus()
    const [params, setParams] = useSearchParams()
    const raw = params.get("tab") ?? "app"
    const tab = ALIASES[raw] ?? raw
    // Unknown tabs (old links, typos) open the first one instead of nothing.
    const section = (SECTION_IDS.includes(tab) ? tab : "app") as SectionId
    const save = useSaveSettings()
    const server = status?.settings
    // draft is what the form edits, base the server copy it was made from.
    const [draft, setDraft] = useState<Settings | null>(null)
    const [base, setBase] = useState<Settings | null>(null)
    const reset = (s: Settings) => {
        setDraft(structuredClone(s))
        setBase(s)
    }
    useEffect(() => {
        if (!server) return
        if (!draft || !base) {
            reset(server)
            return
        }
        if (same(server, base)) return
        // Saved somewhere else meanwhile (another tab or device, the auto
        // downloader switch…): follow it, keeping what was edited here so a
        // save doesn't put the old values back.
        const next = rebase(draft, base, server)
        if (!same(draft, base) && !same(next, draft)) toast.info("Settings were changed somewhere else. Your unsaved changes here were kept.")
        setDraft(next)
        setBase(server)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [server])
    const dirty = useMemo(() => !!draft && !!server && !same(draft, server), [draft, server])

    // Preview the accent color being edited; App shows the saved one again
    // once the preview ends (Discard, leaving the page).
    const accent = draft?.ui.accentColor
    useEffect(() => {
        accentPreviewStore.set(accent || null)
    }, [accent])
    useEffect(() => () => accentPreviewStore.set(null), [])

    if (!draft || !status) return null
    const set = <K extends keyof Settings>(k: K, v: Partial<Settings[K]>) => setDraft(d => (d ? { ...d, [k]: { ...d[k], ...v } } : d))
    const current = NAV.flatMap(g => g.items).find(i => i.id === section) ?? NAV[0].items[0]

    const onSave = () => {
        const oldPort = status.settings.server.port
        save.mutate(draft, {
            onSuccess: s => {
                reset(s)
                // The server moves to the new port; follow it.
                if (s.server.port !== oldPort && ["127.0.0.1", "localhost"].includes(location.hostname)) {
                    setTimeout(() => {
                        location.href = `${location.protocol}//${location.hostname}:${s.server.port}${location.pathname}${location.search}`
                    }, 1500)
                }
            },
        })
    }

    return (
        <div className="flex min-h-full flex-col gap-8 px-6 pt-8 pb-24 md:px-8 lg:flex-row lg:gap-12 xl:px-10">
            <aside className="w-full shrink-0 lg:sticky lg:top-8 lg:w-56 lg:self-start">
                <h1 className="mb-5 px-2.5 text-[1.75rem] font-semibold tracking-tight">Settings</h1>
                <div className="flex flex-col gap-4">
                    {NAV.map((g, i) => (
                        <div key={i} className="flex flex-col gap-0.5">
                            {g.items.map(it => (
                                <button
                                    key={it.id}
                                    onClick={() => setParams({ tab: it.id }, { replace: true })}
                                    className={cn(
                                        "focus-ring flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13.5px] font-medium transition-colors [&>svg]:size-4 [&>svg]:stroke-[1.75]",
                                        section === it.id ? "bg-white/[0.08] text-fg" : "text-muted hover:bg-white/[0.04] hover:text-fg",
                                    )}
                                >
                                    {it.icon}
                                    {it.label}
                                </button>
                            ))}
                        </div>
                    ))}
                </div>
                <p className="mt-6 px-2.5 text-xs text-subtle">
                    Kumo {status.version} · {platformName(status.platform)} · {status.client === "desktop" ? "Desktop" : status.client === "lan" ? "LAN" : "Web UI"}
                </p>
            </aside>

            <section className="min-w-0 flex-1 lg:max-w-4xl">
                <div className="mb-7 border-b border-line pb-5 lg:mt-[3.25rem]">
                    <h2 className="text-xl font-semibold tracking-tight">{current.label}</h2>
                    <p className="mt-0.5 text-sm text-muted">{SUBTITLES[current.id]}</p>
                </div>
                <div key={section} className="flex flex-col gap-8 fade-in">
                    {section === "app" && <AppSection draft={draft} set={set} />}
                    {section === "ui" && <UISection draft={draft} set={set} />}
                    {section === "library" && <LibrarySection draft={draft} set={set} />}
                    {section === "playback" && <PlaybackSection draft={draft} set={set} />}
                    {section === "mpv" && <MpvSection draft={draft} set={set} />}
                    {section === "transcode" && <TranscodeSection draft={draft} set={set} />}
                    {section === "torrent-provider" && <TorrentProviderSection draft={draft} set={set} />}
                    {section === "torrent-client" && <TorrentClientSection draft={draft} set={set} />}
                    {section === "streaming" && <StreamingSection draft={draft} set={set} />}
                    {section === "manga" && <MangaSection draft={draft} set={set} />}
                    {section === "discord" && <DiscordSection draft={draft} set={set} />}
                    {section === "logs" && <LogsSection />}
                    {section === "about" && <AboutSection />}
                </div>
                {section !== "logs" && section !== "about" && (
                    <div className={cn("mt-8 flex items-center gap-3", dirty && "sticky bottom-6 z-20")}>
                        <Button variant="white" icon={<Save className="size-4" />} loading={save.isPending} onClick={onSave} disabled={!dirty}>
                            Save
                        </Button>
                        {dirty && (
                            <>
                                <span className="rounded-md border border-line-strong bg-surface-2 px-2.5 py-1.5 text-[13px] text-muted shadow-lg shadow-black/30">Unsaved changes</span>
                                <Button variant="ghost" size="sm" onClick={() => reset(status.settings)}>
                                    Discard
                                </Button>
                            </>
                        )}
                    </div>
                )}
            </section>
        </div>
    )
}

const SUBTITLES: Record<SectionId, string> = {
    app: "Account, server and network access",
    ui: "Make Kumo look the way you like",
    library: "Manage your local anime library",
    playback: "How episodes are played and tracked",
    mpv: "Configure mpv, the desktop media player",
    transcode: "Play every video format in the in-app player",
    "torrent-provider": "Where torrents are searched",
    "torrent-client": "Configure the torrent client",
    streaming: "ani-cli and streaming extensions",
    manga: "Read manga with provider extensions",
    discord: "Show what you're watching on Discord",
    logs: "Server logs and cached data",
    about: "Kumo's version and updates",
}

type SectionProps = { draft: Settings; set: <K extends keyof Settings>(k: K, v: Partial<Settings[K]>) => void }

// ---------------------------------------------------------------------------
// Layout helpers

function Group({ title, children, description }: { title?: string; description?: string; children: React.ReactNode }) {
    return (
        <div>
            {title && <h3 className="mb-1 text-[15px] font-semibold">{title}</h3>}
            {description && <p className="mb-3 text-[13px] text-muted">{description}</p>}
            {!description && title && <div className="mb-3" />}
            <div className="card divide-y divide-line">{children}</div>
        </div>
    )
}

// wide: the value (e.g. a long path) takes the remaining space and wraps
// instead of pushing out of the card.
function Row({ label, help, children, wide }: { label: React.ReactNode; help?: React.ReactNode; children: React.ReactNode; wide?: boolean }) {
    return (
        <div className="flex items-center justify-between gap-6 px-4 py-3.5">
            <div className="min-w-0">
                <p className="text-sm font-medium">{label}</p>
                {help && <p className="mt-0.5 text-xs text-subtle">{help}</p>}
            </div>
            <div className={wide ? "flex min-w-0 flex-1 justify-end" : "shrink-0"}>{children}</div>
        </div>
    )
}

function Stack({ label, help, children }: { label: React.ReactNode; help?: React.ReactNode; children: React.ReactNode }) {
    return (
        <div className="flex flex-col gap-2 px-4 py-3.5">
            <p className="text-sm font-medium">{label}</p>
            {children}
            {help && <p className="text-xs text-subtle">{help}</p>}
        </div>
    )
}

function Password({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder?: string }) {
    const [show, setShow] = useState(false)
    return (
        <div className="relative">
            <Input type={show ? "text" : "password"} value={value} placeholder={placeholder} onChange={e => onChange(e.target.value)} className="pr-10" />
            <button type="button" onClick={() => setShow(s => !s)} className="absolute top-1/2 right-3 -translate-y-1/2 text-subtle hover:text-fg">
                {show ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
            </button>
        </div>
    )
}

function Collapsible({ title, children }: { title: string; children: React.ReactNode }) {
    const [open, setOpen] = useState(false)
    return (
        <div className="card">
            <button onClick={() => setOpen(o => !o)} className="flex w-full items-center justify-between px-4 py-3.5 text-[15px] font-medium">
                {title}
                <ChevronDown className={cn("size-4 text-muted transition-transform duration-200", open && "rotate-180")} />
            </button>
            {open && <div className="divide-y divide-line border-t border-line">{children}</div>}
        </div>
    )
}

function DirPicker({ open, onOpenChange, onPick, start }: { open: boolean; onOpenChange: (v: boolean) => void; onPick: (p: string) => void; start?: string }) {
    const [path, setPath] = useState(start ?? "")
    const { data, error } = useQuery({
        queryKey: ["dirs", path],
        // roots: the drives on Windows.
        queryFn: () => api.get<{ path: string; parent: string; dirs: string[]; sep?: string; roots?: string[] | null }>(`/api/fs/dirs?path=${encodeURIComponent(path)}`),
        enabled: open,
    })
    useEffect(() => {
        if (open) setPath(start ?? "")
    }, [open, start])
    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title="Choose a folder"
            className="w-[min(94vw,620px)]"
            footer={
                <Button
                    variant="primary"
                    onClick={() => {
                        if (data) onPick(data.path)
                        onOpenChange(false)
                    }}
                >
                    Use this folder
                </Button>
            }
        >
            <Input value={data?.path ?? path} onChange={e => setPath(e.target.value)} onKeyDown={e => e.key === "Enter" && setPath((e.target as HTMLInputElement).value)} icon={<Folder className="size-4" />} />
            {error && <p className="mt-2 text-sm text-rose-300">{(error as Error).message}</p>}
            {!!data?.roots?.length && (
                <div className="mt-3 flex flex-wrap gap-1.5">
                    {data.roots.map(r => (
                        <Button key={r} size="xs" variant={data.path.toLowerCase().startsWith(r.toLowerCase()) ? "primary" : "subtle"} icon={<HardDrive className="size-3.5" />} onClick={() => setPath(r)}>
                            {r.replace(/\\$/, "")}
                        </Button>
                    ))}
                </div>
            )}
            <div className="mt-3 flex max-h-80 flex-col overflow-y-auto">
                {data && data.parent !== data.path && (
                    <button onClick={() => setPath(data.parent)} className="flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-muted hover:bg-white/[0.05]">
                        <FolderOpen className="size-4" /> ..
                    </button>
                )}
                {data?.dirs.map(d => (
                    <button
                        key={d}
                        onClick={() => setPath(`${data.path.replace(/[\\/]$/, "")}${data.sep ?? "/"}${d}`)}
                        className="flex items-center gap-3 rounded-lg px-3 py-2 text-left text-sm hover:bg-white/[0.05]"
                    >
                        <Folder className="size-4 text-brand-strong" /> {d}
                    </button>
                ))}
            </div>
        </Dialog>
    )
}

function PathInput({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder?: string }) {
    const [open, setOpen] = useState(false)
    const { data: status } = useStatus()
    return (
        <div className="flex items-center gap-2">
            <div className="relative flex-1">
                <Input value={value} onChange={e => onChange(e.target.value)} placeholder={placeholder} icon={<Folder className="size-4 text-amber-300" />} />
                {value && <Check className="absolute top-1/2 right-3 size-4 -translate-y-1/2 text-emerald-400" />}
            </div>
            {status?.client !== "lan" && (
                <button onClick={() => setOpen(true)} className="grid size-10 place-items-center rounded-xl text-muted hover:bg-white/[0.06] hover:text-fg" aria-label="Browse">
                    <FolderOpen className="size-5" />
                </button>
            )}
            <DirPicker open={open} onOpenChange={setOpen} onPick={onChange} start={value} />
        </div>
    )
}

const toLines = (text: string) => text.split("\n").map(s => s.trim()).filter(Boolean)

// One entry per line. The box keeps the text as typed (blank lines, spaces)
// and only the cleaned-up list goes into the draft, so typing is never
// rewritten under the cursor; leaving the box tidies it.
function LinesInput({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
    const joined = value.join("\n")
    const [text, setText] = useState(joined)
    // The list was replaced from outside (Discard, a save, a change made elsewhere).
    useEffect(() => setText(t => (toLines(t).join("\n") === joined ? t : joined)), [joined])
    return (
        <Textarea
            value={text}
            onChange={e => {
                setText(e.target.value)
                onChange(toLines(e.target.value))
            }}
            onBlur={() => setText(joined)}
        />
    )
}

function Detect({ ok, label }: { ok?: boolean; label: string }) {
    return ok ? <Badge tone="green">{label} found</Badge> : <Badge tone="red">{label} not found</Badge>
}

// Where other devices open Kumo, once LAN access is saved.
function LanAddresses({ status }: { status?: Status }) {
    const urls = status?.lanUrls ?? []
    if (!status?.settings.server.allowLan) return <p className="px-4 py-3.5 text-sm text-muted">Save, and the address to open on your other devices shows up here.</p>
    if (!urls.length)
        return (
            <p className="px-4 py-3.5 text-sm text-amber-200/80">
                Kumo can't find this computer's address on your network. Is it connected to your router (by cable or Wi-Fi)?
            </p>
        )
    return (
        <div className="px-4 py-3.5">
            <p className="text-sm font-medium">On your other devices, open</p>
            <div className="mt-2 flex flex-wrap gap-2">
                {urls.map(u => (
                    <button
                        key={u}
                        onClick={() => copyText(u).then(ok => (ok ? toast.success("Address copied") : toast.error("Couldn't copy it")))}
                        className="flex items-center gap-2 rounded-lg bg-black/30 px-3 py-1.5 font-mono text-sm transition hover:bg-black/45"
                        title="Copy"
                    >
                        {u}
                        <Copy className="size-3.5 text-subtle" />
                    </button>
                ))}
            </div>
            <p className="mt-2 text-xs text-subtle">
                Type it with http://, not https://. On a Mac, Chrome, Brave and Firefox first need Local Network access (System Settings › Privacy & Security ›
                Local Network); Safari works right away. A VPN on this computer can block other devices too: in Mullvad, turn on Local network sharing.
            </p>
        </div>
    )
}

// ---------------------------------------------------------------------------
// Sections

function AppSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const [loginOpen, setLoginOpen] = useState(false)
    const qc = useQueryClient()
    const isDesktop = status?.client === "desktop"
    const lan = status?.client === "lan"
    return (
        <>
            <Group title="AniList">
                <div className="flex items-center justify-between gap-4 px-4 py-4">
                    {status?.user ? (
                        <div className="flex items-center gap-3">
                            <img src={img(status.user.avatar.large)} alt="" className="size-11 rounded-full" />
                            <div>
                                <p className="font-semibold">{status.user.name}</p>
                                <p className="text-xs text-subtle">Lists, progress and scores sync with AniList</p>
                            </div>
                        </div>
                    ) : (
                        <div>
                            <p className="font-medium">Not connected</p>
                            <p className="text-xs text-subtle">Your list is stored locally until you log in.</p>
                        </div>
                    )}
                    {status?.user ? (
                        <Button
                            variant="danger"
                            size="sm"
                            onClick={async () => {
                                try {
                                    await api.post("/api/auth/logout")
                                    qc.invalidateQueries()
                                } catch (e: any) {
                                    toast.error(e.message)
                                }
                            }}
                        >
                            Log out
                        </Button>
                    ) : (
                        <Button variant="primary" size="sm" onClick={() => setLoginOpen(true)}>
                            Log in with AniList
                        </Button>
                    )}
                </div>
                <Stack label="AniList client ID" help="Kumo opens anilist.co/api/v2/oauth/authorize?client_id=…&response_type=token">
                    <Input value={draft.anilist.clientId} onChange={e => set("anilist", { clientId: e.target.value })} />
                </Stack>
            </Group>

            <Group title="Web UI & network" description="Kumo only ever talks to this computer and — if you allow it — your home network. Connections from the internet are always refused.">
                <Row
                    label="Web UI"
                    help={
                        status?.webUiForced
                            ? "Running without the desktop app, so the browser UI is always on."
                            : `Use Kumo from any browser at http://127.0.0.1:${draft.server.port}`
                    }
                >
                    <Switch checked={draft.server.webUi || !!status?.webUiForced} disabled={!isDesktop || status?.webUiForced} onChange={v => set("server", { webUi: v })} />
                </Row>
                <Row label="Allow devices on my network" help="Phones, tablets, TVs and other computers on your home network can open Kumo in a browser.">
                    <Switch checked={draft.server.allowLan} disabled={lan} onChange={v => set("server", { allowLan: v, webUi: v ? true : draft.server.webUi })} />
                </Row>
                {draft.server.allowLan && <LanAddresses status={status} />}
                {draft.server.allowLan && (
                    <Stack label="Network password" help="Asked once per device on your network. This computer never needs it.">
                        <Password value={draft.server.password} onChange={v => set("server", { password: v })} placeholder="Leave empty for no password" />
                    </Stack>
                )}
                <Row label="Port" help="Restart-free: Kumo moves to the new port when you save.">
                    <Input type="number" className="w-28" disabled={lan} value={draft.server.port} onChange={e => set("server", { port: Number(e.target.value) })} />
                </Row>
                {!isDesktop && !lan && <p className="px-4 py-3 text-xs text-amber-200/80">Turning the Web UI off is only possible from the desktop app, so you can't lock yourself out.</p>}
            </Group>

            <DesktopGroup />

            <ProgramsGroup />

            <Group title="Extensions">
                <Stack label="Marketplace URL" help="Any compatible extension index works.">
                    <Input value={draft.extensions.marketplaceUrl} onChange={e => set("extensions", { marketplaceUrl: e.target.value })} />
                </Stack>
            </Group>

            <Group title="Data">
                <Row label="Data folder" help="Database, cache and extension storage" wide>
                    <code title={status?.dataDir} className="min-w-0 rounded-lg bg-black/30 px-2 py-1 text-xs break-all">
                        {status?.dataDir}
                    </code>
                </Row>
            </Group>
            <LoginDialog open={loginOpen} onOpenChange={setLoginOpen} />
        </>
    )
}

// The desktop app's own settings, saved by the app itself (they take effect
// at once, no Save).
function DesktopGroup() {
    const bridge = desktop()
    const [prefs, setPrefs] = useState<DesktopPrefs | null>(null)
    useEffect(() => {
        bridge?.getPrefs?.().then(setPrefs, () => {})
    }, [bridge])
    if (!bridge?.setPref || !prefs) return null
    const mac = bridge.platform === "darwin"
    const change = async (name: keyof DesktopPrefs, value: boolean) => {
        setPrefs({ ...prefs, [name]: value })
        if (!(await bridge.setPref!(name, value).catch(() => false))) {
            setPrefs(prefs)
            toast.error("Couldn't change that setting")
        }
    }
    return (
        <Group title="Desktop app">
            <Row
                label="Keep running when the window is closed"
                help={
                    mac
                        ? "Streaming to your other devices, downloads and the auto downloader keep going, without the window's memory. Click Kumo in the Dock to open it again; quit with Cmd+Q."
                        : "Streaming to your other devices, downloads and the auto downloader keep going, without the window's memory. Open or quit Kumo from its tray icon, or open it again from the app menu."
                }
            >
                <Switch checked={prefs.keepRunning} onChange={v => change("keepRunning", v)} />
            </Row>
            <Row
                label="Free memory while the computer is locked"
                help="After 5 minutes locked, the window lets go of the page and loads it again when you unlock. Never while a video, the manga reader or Settings is open."
            >
                <Switch checked={prefs.freeWhenLocked} onChange={v => change("freeWhenLocked", v)} />
            </Row>
        </Group>
    )
}

function InstallProgramsButton() {
    const { data: status } = useStatus()
    const install = useInstallPrograms(status)
    return (
        <Button variant="primary" size="sm" loading={install.isPending} disabled={status?.setupRunning} onClick={() => install.mutate()}>
            {status?.setupRunning ? "Installing…" : "Install"}
        </Button>
    )
}

// What Kumo found of the programs it uses and, on Windows and macOS, the
// setup that installs them.
function ProgramsGroup() {
    const { data: status } = useStatus()
    useWatchSetup(status)
    const f = status?.features
    const windows = status?.platform === "windows"
    const programs: { tool: Tool; ok?: boolean; use: string }[] = [
        { tool: "ffmpeg", ok: f?.ffmpeg, use: "The in-app player (most files) and episode downloads" },
        { tool: "ani-cli", ok: f?.aniCli, use: windows ? "Sub/dub streaming and downloads (it runs in Git's bash)" : "Sub/dub streaming and downloads" },
        { tool: "mpv", ok: f?.mpv, use: "The external player" },
        { tool: "yt-dlp", ok: f?.ytDlp, use: "Faster, more reliable episode downloads" },
    ]
    return (
        <Group title="Programs" description="Kumo uses these programs for some of what it does.">
            {programs.map(p => (
                <Row
                    key={p.tool}
                    label={p.tool}
                    help={
                        p.ok || canInstallPrograms(status) ? (
                            p.use
                        ) : (
                            <>
                                {p.use}. {installSource(status?.platform, p.tool)} <code className="rounded bg-black/30 px-1.5 py-0.5">{installHint(status?.platform, p.tool)}</code>
                            </>
                        )
                    }
                >
                    <Badge tone={p.ok ? "green" : "red"}>{p.ok ? "Found" : "Not found"}</Badge>
                </Row>
            ))}
            {canInstallPrograms(status) && (
                <Row
                    label="Install missing programs"
                    help={
                        status?.platform === "darwin"
                            ? "Downloads ffmpeg, yt-dlp and ani-cli for this Mac (Apple silicon or Intel) into Kumo's folder, in a Terminal window: no Homebrew or password needed. mpv, the external player, comes from Homebrew on Apple silicon Macs that have it. Programs you already have are skipped."
                            : "Installs Git, ani-cli, ffmpeg, mpv, yt-dlp and the rest of what Kumo uses with Scoop, for your Windows user (no administrator rights), in a PowerShell window. Programs you already have are skipped. It also offers to sign you in to GitHub, for Kumo's updates."
                    }
                >
                    <InstallProgramsButton />
                </Row>
            )}
        </Group>
    )
}

const ACCENTS = ["#7c6cf2", "#5b8cff", "#22c3a6", "#f2557a", "#f59e0b", "#e879f9", "#38bdf8", "#a3e635"]

function UISection({ draft, set }: SectionProps) {
    return (
        <>
            <Group title="Theme">
                <Stack label="Accent color">
                    <div className="flex flex-wrap items-center gap-2.5">
                        {ACCENTS.map(c => (
                            <button
                                key={c}
                                onClick={() => set("ui", { accentColor: c })}
                                className={cn("size-9 rounded-full ring-2 ring-offset-2 ring-offset-[var(--surface-1)] transition", draft.ui.accentColor === c ? "ring-white" : "ring-transparent hover:ring-white/30")}
                                style={{ background: c }}
                                aria-label={c}
                            />
                        ))}
                        <input
                            type="color"
                            value={draft.ui.accentColor}
                            onChange={e => set("ui", { accentColor: e.target.value })}
                            className="size-9 cursor-pointer rounded-full border border-line bg-transparent"
                        />
                    </div>
                </Stack>
                <Row label="Card size">
                    <Select
                        className="w-36"
                        value={draft.ui.cardSize}
                        onChange={v => set("ui", { cardSize: v })}
                        options={[
                            { value: "sm", label: "Compact" },
                            { value: "md", label: "Comfortable" },
                            { value: "lg", label: "Large" },
                        ]}
                    />
                </Row>
                <Row label="Reduce motion" help="Turn off animations">
                    <Switch checked={draft.ui.reducedMotion} onChange={v => set("ui", { reducedMotion: v })} />
                </Row>
            </Group>
            <Group title="Content">
                <Row label="Hide spoilers" help="Blur thumbnails of episodes you haven't watched yet">
                    <Switch checked={draft.ui.blurUnwatched} onChange={v => set("ui", { blurUnwatched: v })} />
                </Row>
                <Row label="Show adult content" help="Include 18+ entries in search and discover">
                    <Switch checked={draft.ui.showAdult} onChange={v => set("ui", { showAdult: v })} />
                </Row>
            </Group>
        </>
    )
}

// Library sharing with the Kumo apps on the other computers at home. Turning
// it on or off and the name are saved with the rest; who gets this
// computer's library changes at once.
function SharingGroup({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const saved = status?.settings.sharing
    const trusted = status?.client !== "lan"
    const { data: sharing } = useSharing(!!saved?.enabled)
    const qc = useQueryClient()
    const [address, setAddress] = useState("")
    const [adding, setAdding] = useState(false)
    const call = async (fn: () => Promise<unknown>) => {
        try {
            await fn()
            qc.invalidateQueries({ queryKey: ["sharing"] })
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const add = async () => {
        if (!address.trim()) return
        setAdding(true)
        await call(async () => {
            const p = await api.post<SharingPeer>("/api/sharing/connect", { address })
            toast.success(`Found ${p.name}`)
            setAddress("")
        })
        setAdding(false)
    }
    const peers = sharing?.peers ?? []
    return (
        <Group
            title="Library sharing"
            description="Watch the libraries of Kumo on your other computers at home, and share yours with them. Each computer decides who gets its library. Watching updates the AniList account (or local list) of the computer you watch on, never the other's."
        >
            <Row label="Library sharing" help="Find the Kumo apps on this network, and let them find this one. Turn it on on both computers.">
                <Switch checked={draft.sharing.enabled} disabled={!trusted} onChange={v => set("sharing", { enabled: v })} />
            </Row>
            {draft.sharing.enabled && (
                <Stack label="Name on the network" help="What the other Kumo apps call this one.">
                    <Input value={draft.sharing.name} disabled={!trusted} placeholder={sharing?.name || "This computer's name"} onChange={e => set("sharing", { name: e.target.value })} />
                </Stack>
            )}
            {draft.sharing.enabled !== !!saved?.enabled && <p className="px-4 py-3 text-xs text-amber-200/80">Save to turn library sharing {draft.sharing.enabled ? "on" : "off"}.</p>}
            {saved?.enabled && sharing && (
                <>
                    <div className="px-4 pt-3.5 pb-1">
                        <p className="text-sm font-medium">Kumo apps on this network</p>
                        <p className="mt-0.5 text-xs text-subtle">
                            Turn on <b>Share my library</b> for those that may watch this computer's anime.
                            {sharing.addresses.length > 0 && <> This computer is at {sharing.addresses.join(", ")}.</>}
                        </p>
                    </div>
                    {peers.length === 0 && (
                        <p className="px-4 py-3 text-sm text-muted">
                            {sharing.listening ? "Looking for Kumo apps… Turn on library sharing on the other computer too." : "This computer can't listen for the others (another program uses the network port): add them by address below."}
                        </p>
                    )}
                    {peers.map(p => (
                        <div key={p.id} className="flex items-center justify-between gap-4 px-4 py-3.5">
                            <div className="min-w-0">
                                <p className="flex items-center gap-2 text-sm font-medium">
                                    <span className={cn("size-2 shrink-0 rounded-full", p.online ? "bg-emerald-400" : "bg-white/20")} />
                                    <span className="truncate">{p.name}</span>
                                </p>
                                <p className="mt-0.5 text-xs text-subtle">
                                    {[
                                        p.address,
                                        !p.online && p.lastSeen > 0 && `seen ${relativeTime(p.lastSeen)}`,
                                        p.shares && `shares its library with you${p.files ? ` (${p.files} ${p.files === 1 ? "file" : "files"})` : ""}`,
                                        p.error,
                                    ]
                                        .filter(Boolean)
                                        .join(" · ")}
                                </p>
                            </div>
                            <div className="flex shrink-0 items-center gap-3">
                                <span className="text-xs text-muted">Share my library</span>
                                <Switch checked={p.allowed} disabled={!trusted} onChange={v => call(() => api.post(`/api/sharing/peers/${p.id}`, { allowed: v }))} />
                                {!p.online && trusted && (
                                    <button
                                        onClick={() => call(() => api.del(`/api/sharing/peers/${p.id}`))}
                                        className="grid size-8 place-items-center rounded-lg text-muted hover:bg-white/[0.06] hover:text-rose-300"
                                        aria-label={`Remove ${p.name}`}
                                        title="Remove"
                                    >
                                        <X className="size-4" />
                                    </button>
                                )}
                            </div>
                        </div>
                    ))}
                    {trusted && (
                        <Stack label="Add by address" help="When the computers don't find each other by themselves (some routers block it): the other computer's address, shown in its settings here.">
                            <div className="flex gap-2">
                                <Input value={address} onChange={e => setAddress(e.target.value)} onKeyDown={e => e.key === "Enter" && add()} placeholder="192.168.1.20:43211" />
                                <Button onClick={add} loading={adding} disabled={!address.trim()}>
                                    Add
                                </Button>
                            </div>
                        </Stack>
                    )}
                </>
            )}
        </Group>
    )
}

function LibrarySection({ draft, set }: SectionProps) {
    const extra = draft.library.extraDirs ?? []
    return (
        <>
            <Group title="Local library">
                <Stack label="Library directory" help="Path of the directory where your media files are located.">
                    <PathInput value={draft.library.dir} onChange={v => set("library", { dir: v })} placeholder="/mnt/big/Media/Anime" />
                </Stack>
                <Stack label="Additional library directories" help="Include additional directory paths if your library is spread across multiple locations.">
                    <div className="flex flex-col gap-2">
                        {extra.map((d, i) => (
                            <div key={i} className="flex items-center gap-2">
                                <div className="flex-1">
                                    <PathInput value={d} onChange={v => set("library", { extraDirs: extra.map((x, j) => (j === i ? v : x)) })} />
                                </div>
                                <button onClick={() => set("library", { extraDirs: extra.filter((_, j) => j !== i) })} className="grid size-10 place-items-center rounded-xl text-muted hover:bg-white/[0.06] hover:text-rose-300">
                                    <X className="size-4" />
                                </button>
                            </div>
                        ))}
                        <button onClick={() => set("library", { extraDirs: [...extra, ""] })} className="grid size-9 place-items-center rounded-full bg-white/[0.07] text-fg hover:bg-white/[0.12]" aria-label="Add directory">
                            <Plus className="size-4" />
                        </button>
                    </div>
                </Stack>
            </Group>
            <Group title="Scanning">
                <Row label="Automatically refresh library" help="Watch the folders and pick up new or removed files on their own">
                    <Switch checked={draft.library.autoRefresh} onChange={v => set("library", { autoRefresh: v })} />
                </Row>
                <Row label="Refresh library on startup">
                    <Switch checked={draft.library.refreshOnStartup} onChange={v => set("library", { refreshOnStartup: v })} />
                </Row>
            </Group>
            <SharingGroup draft={draft} set={set} />
            <Collapsible title="Advanced">
                <Stack label={`Matching threshold — ${Math.round(draft.library.matchThreshold * 100)}%`} help="How similar a file name must be to an AniList title to match automatically.">
                    <input type="range" min={0.3} max={0.95} step={0.05} value={draft.library.matchThreshold} onChange={e => set("library", { matchThreshold: Number(e.target.value) })} className="accent-[var(--brand)]" />
                </Stack>
                <Row label="Match anime that aren't in my list" help="Search all of AniList when a title isn't on your list">
                    <Switch checked={draft.library.matchOutsideList} onChange={v => set("library", { matchOutsideList: v })} />
                </Row>
                <Stack label="Ignore patterns" help="One glob per line, matched against file and folder names (e.g. *sample*).">
                    <LinesInput value={draft.library.ignorePatterns ?? []} onChange={v => set("library", { ignorePatterns: v })} />
                </Stack>
            </Collapsible>
        </>
    )
}

const LANGS = [
    { value: "jpn,ja", label: "Japanese" },
    { value: "eng,en", label: "English" },
    { value: "spa,es", label: "Spanish" },
    { value: "por,pt", label: "Portuguese" },
    { value: "fre,fra,fr", label: "French" },
    { value: "ger,deu,de", label: "German" },
    { value: "ita,it", label: "Italian" },
    { value: "ara,ar", label: "Arabic" },
    { value: "rus,ru", label: "Russian" },
    { value: "", label: "File default" },
]

function PlaybackSection({ draft, set }: SectionProps) {
    const p = draft.playback
    return (
        <>
            <Group title="Player">
                <Row label="Default player for local files" help="mpv opens in its own window; the in-app player works everywhere (and on other devices).">
                    <Select
                        className="w-48"
                        value={p.defaultPlayer}
                        onChange={v => set("playback", { defaultPlayer: v as "mpv" })}
                        options={[
                            { value: "builtin", label: "In-app player" },
                            { value: "mpv", label: "mpv" },
                        ]}
                    />
                </Row>
                <Row label="Resume where I left off" help="Episodes continue from the saved position in mpv and the in-app player">
                    <Switch checked={p.resumePlayback} onChange={v => set("playback", { resumePlayback: v })} />
                </Row>
                <Row label="Auto play next episode">
                    <Switch checked={p.autoPlayNext} onChange={v => set("playback", { autoPlayNext: v })} />
                </Row>
                <Row label="Skip openings automatically" help="Uses AniSkip timestamps (the same source as ani-cli --skip)">
                    <Switch checked={p.skipIntroAniSkip} onChange={v => set("playback", { skipIntroAniSkip: v })} />
                </Row>
                <Row label="Skip endings automatically" help="Jumps past the ending song, also with AniSkip timestamps">
                    <Switch checked={p.skipOutroAniSkip} onChange={v => set("playback", { skipOutroAniSkip: v })} />
                </Row>
            </Group>
            <Group title="Progress">
                <Row label="Update progress automatically" help="Mark the episode as watched on AniList after you finish it">
                    <Switch checked={p.autoUpdateProgress} onChange={v => set("playback", { autoUpdateProgress: v })} />
                </Row>
                <Stack label={`Count as watched at ${Math.round(p.completionThreshold * 100)}%`}>
                    <input type="range" min={0.5} max={1} step={0.01} value={p.completionThreshold} onChange={e => set("playback", { completionThreshold: Number(e.target.value) })} className="accent-[var(--brand)]" />
                </Stack>
            </Group>
            <Group title="Audio & subtitles" description="Kumo remembers the audio and subtitle track you pick for each anime and uses it for the next episodes.">
                <Row label="Remember my track choice per anime">
                    <Switch checked={p.rememberTracks} onChange={v => set("playback", { rememberTracks: v })} />
                </Row>
                <Row label="Preferred audio language">
                    <Select className="w-44" value={p.preferredAudioLang} onChange={v => set("playback", { preferredAudioLang: v })} options={LANGS} />
                </Row>
                <Row label="Preferred subtitle language">
                    <Select className="w-44" value={p.preferredSubLang} onChange={v => set("playback", { preferredSubLang: v })} options={LANGS} />
                </Row>
            </Group>
        </>
    )
}

function MpvSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    return (
        <Group title="mpv">
            <Row label="Status">
                <Detect ok={status?.features.mpv} label="mpv" />
            </Row>
            <Stack
                label="Executable"
                help={`Name or full path, e.g. mpv or ${exampleMpvPath(status?.platform)} (install with: ${installHint(status?.platform, "mpv")})`}
            >
                <Input value={draft.mpv.path} onChange={e => set("mpv", { path: e.target.value })} icon={<Terminal className="size-4" />} />
            </Stack>
            <Stack label="Extra arguments" help="Passed to every mpv launch, e.g. --profile=gpu-hq --hwdec=auto">
                <Input value={draft.mpv.extraArgs} onChange={e => set("mpv", { extraArgs: e.target.value })} />
            </Stack>
            <Row label="Start in fullscreen">
                <Switch checked={draft.mpv.fullscreen} onChange={v => set("mpv", { fullscreen: v })} />
            </Row>
        </Group>
    )
}

function TranscodeSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const t = draft.transcode
    return (
        <>
            <Group title="In-app player" description="Browsers can't decode every format (HEVC, AC3/DTS audio, MKV with multiple tracks…). Kumo repackages or converts those with ffmpeg on the fly so every file plays.">
                <Row label="Status">
                    <div className="flex gap-2">
                        <Detect ok={status?.features.ffmpeg} label="ffmpeg" />
                        <Detect ok={status?.features.ffprobe} label="ffprobe" />
                    </div>
                </Row>
                <Row label="Mode">
                    <Select
                        className="w-56"
                        value={t.mode}
                        onChange={v => set("transcode", { mode: v })}
                        options={[
                            { value: "auto", label: "Automatic (recommended)" },
                            { value: "direct", label: "Always direct play" },
                            { value: "transcode", label: "Always transcode" },
                        ]}
                    />
                </Row>
                <Row label="Hardware acceleration">
                    <Select
                        className="w-56"
                        value={t.hwAccel}
                        onChange={v => set("transcode", { hwAccel: v })}
                        options={[
                            { value: "", label: "None (CPU, libx264)" },
                            { value: "vaapi", label: "VA-API (AMD / Intel)" },
                            { value: "nvenc", label: "NVENC (NVIDIA)" },
                            { value: "qsv", label: "Quick Sync (Intel)" },
                        ]}
                    />
                </Row>
                {t.hwAccel === "vaapi" && (
                    <Stack label="VA-API device">
                        <Input value={t.vaapiNode} onChange={e => set("transcode", { vaapiNode: e.target.value })} />
                    </Stack>
                )}
                <Row label="x264 preset" help="Faster presets use less CPU">
                    <Select className="w-40" value={t.preset} onChange={v => set("transcode", { preset: v })} options={["ultrafast", "superfast", "veryfast", "faster", "fast", "medium"].map(p => ({ value: p, label: p }))} />
                </Row>
            </Group>
            <Group title="Paths">
                <Stack label="ffmpeg">
                    <Input value={t.ffmpegPath} onChange={e => set("transcode", { ffmpegPath: e.target.value })} />
                </Stack>
                <Stack label="ffprobe">
                    <Input value={t.ffprobePath} onChange={e => set("transcode", { ffprobePath: e.target.value })} />
                </Stack>
            </Group>
        </>
    )
}

function TorrentProviderSection({ draft, set }: SectionProps) {
    const { data: providers } = useQuery({ queryKey: ["torrent-providers"], queryFn: () => api.get<{ id: string; name: string; extension: boolean }[]>("/api/torrents/providers") })
    return (
        <Group title="Search">
            <Row label="Default torrent provider" help="Install more from Extensions › Marketplace">
                <Select className="w-56" value={draft.torrent.defaultProvider} onChange={v => set("torrent", { defaultProvider: v })} options={[{ value: "all", label: "All providers" }, ...(providers ?? []).map(p => ({ value: p.id, label: p.name + (p.extension ? " (extension)" : "") }))]} />
            </Row>
            <Row label="Preferred resolution">
                <Select
                    className="w-40"
                    value={draft.torrent.preferredResolution}
                    onChange={v => set("torrent", { preferredResolution: v })}
                    options={[
                        { value: "", label: "Any" },
                        { value: "2160", label: "2160p" },
                        { value: "1080", label: "1080p" },
                        { value: "720", label: "720p" },
                        { value: "480", label: "480p" },
                    ]}
                />
            </Row>
        </Group>
    )
}

function ClientFields({ value, onChange, showTags }: { value: Settings["qbittorrent"]; onChange: (v: Partial<Settings["qbittorrent"]>) => void; showTags?: boolean }) {
    return (
        <div className="flex flex-col gap-4 p-4">
            <label className="flex flex-col gap-1.5">
                <span className="text-sm font-medium">Host</span>
                <Input value={value.host} onChange={e => onChange({ host: e.target.value })} />
            </label>
            <div className="grid gap-4 md:grid-cols-3">
                <label className="flex flex-col gap-1.5">
                    <span className="text-sm font-medium">Username</span>
                    <Input value={value.username} onChange={e => onChange({ username: e.target.value })} />
                </label>
                <label className="flex flex-col gap-1.5">
                    <span className="text-sm font-medium">Password</span>
                    <Password value={value.password} onChange={v => onChange({ password: v })} />
                </label>
                <label className="flex flex-col gap-1.5">
                    <span className="text-sm font-medium">Port</span>
                    <Input type="number" value={value.port} onChange={e => onChange({ port: Number(e.target.value) })} />
                </label>
            </div>
            <label className="flex flex-col gap-1.5">
                <span className="text-sm font-medium">Executable</span>
                <Input value={value.executable} onChange={e => onChange({ executable: e.target.value })} placeholder="/usr/bin/qbittorrent" />
            </label>
            <label className="flex flex-col gap-1.5">
                <span className="text-sm font-medium">Tags</span>
                <Input value={value.tags} onChange={e => onChange({ tags: e.target.value })} />
                <span className="text-xs text-subtle">Comma separated tags to apply to downloaded torrents. e.g. kumo,anime</span>
            </label>
            {showTags && (
                <label className="flex flex-col gap-1.5">
                    <span className="text-sm font-medium">Category</span>
                    <Input value={value.category} onChange={e => onChange({ category: e.target.value })} />
                    <span className="text-xs text-subtle">Category to apply to downloaded torrents.</span>
                </label>
            )}
            <label className="flex items-center gap-3 text-sm">
                <Switch checked={value.useHttps} onChange={v => onChange({ useHttps: v })} /> Use HTTPS
            </label>
        </div>
    )
}

function TorrentClientSection({ draft, set }: SectionProps) {
    const [open, setOpen] = useState<string>(draft.torrent.defaultClient === "transmission" ? "transmission" : "qbittorrent")
    const [testing, setTesting] = useState(false)
    const save = useSaveSettings()
    const test = async () => {
        setTesting(true)
        try {
            await save.mutateAsync(draft)
            const s = await api.get<{ connected: boolean; version: string; error?: string }>("/api/torrent-client/status")
            s.connected ? toast.success(`Connected — version ${s.version}`) : toast.error(s.error ?? "Not reachable")
        } finally {
            setTesting(false)
        }
    }
    return (
        <>
            <div className="card p-4">
                <p className="mb-2 text-sm font-medium">Default Torrent Client</p>
                <Select
                    value={draft.torrent.defaultClient}
                    onChange={v => set("torrent", { defaultClient: v })}
                    options={[
                        { value: "qbittorrent", label: "qBittorrent" },
                        { value: "transmission", label: "Transmission" },
                        { value: "none", label: "None" },
                    ]}
                />
            </div>
            <div className="card divide-y divide-line">
                {(["qbittorrent", "transmission"] as const).map(c => (
                    <div key={c}>
                        <button onClick={() => setOpen(o => (o === c ? "" : c))} className="flex w-full items-center justify-between px-4 py-4">
                            <span className="flex items-center gap-2.5 text-lg font-semibold">
                                <HardDrive className="size-5 text-brand-strong" />
                                {c === "qbittorrent" ? "qBittorrent" : "Transmission"}
                            </span>
                            <ChevronDown className={cn("size-5 text-muted transition-transform", open === c && "rotate-180")} />
                        </button>
                        {open === c && <ClientFields value={draft[c]} onChange={v => set(c, v)} showTags={c === "qbittorrent"} />}
                    </div>
                ))}
            </div>
            <div>
                <Button icon={<Network className="size-4" />} loading={testing} onClick={test} disabled={draft.torrent.defaultClient === "none"}>
                    Save & test connection
                </Button>
            </div>
            <Group title="Integration">
                <Row label="Show active torrent count" help="Show the number of active torrents in the sidebar.">
                    <Switch checked={draft.torrent.showActiveCount} onChange={v => set("torrent", { showActiveCount: v })} />
                </Row>
                <Row label="Create a folder per anime" help="Save each anime into its own folder inside the library">
                    <Switch checked={draft.torrent.createSubfolder} onChange={v => set("torrent", { createSubfolder: v })} />
                </Row>
                <Row label="Auto downloader" help="Check RSS rules periodically">
                    <Switch checked={draft.torrent.autoDownloader} onChange={v => set("torrent", { autoDownloader: v })} />
                </Row>
                <Row label="Check every (minutes)">
                    <Input type="number" min={5} className="w-24" value={draft.torrent.autoDownloadMinutes} onChange={e => set("torrent", { autoDownloadMinutes: Number(e.target.value) })} />
                </Row>
            </Group>
        </>
    )
}

function StreamingSection({ draft, set }: SectionProps) {
    const { data: status } = useStatus()
    const { data: providers } = useOnlineProviders()
    const { data: ani } = useQuery({ queryKey: ["anicli-status"], queryFn: () => api.get<{ installed: boolean; path: string; version: string; ytDlp: boolean; ffmpeg: boolean }>("/api/anicli/status") })
    const a = draft.aniCli
    return (
        <>
            <Group title="Online streaming">
                <Row label="Enable online streaming">
                    <Switch checked={draft.onlineStream.enabled} onChange={v => set("onlineStream", { enabled: v })} />
                </Row>
                <Row label="Default provider" help="ani-cli is built in; extensions come from the marketplace">
                    <Select className="w-56" value={draft.onlineStream.defaultProvider} onChange={v => set("onlineStream", { defaultProvider: v })} options={(providers ?? [{ id: "ani-cli", name: "ani-cli", builtin: true } as any]).map(p => ({ value: p.id, label: p.builtin ? "ani-cli" : p.name }))} />
                </Row>
                <Row label="Play streams in" help="Other devices on your network always use the in-app player">
                    <Select
                        className="w-44"
                        value={a.player}
                        onChange={v => set("aniCli", { player: v as "mpv" })}
                        options={[
                            { value: "builtin", label: "In-app player" },
                            { value: "mpv", label: "mpv" },
                        ]}
                    />
                </Row>
            </Group>
            <Group title="ani-cli" description="Kumo drives your installed ani-cli to find sub and dub streams and downloads, so updating ani-cli keeps everything working.">
                <Row label="Status">
                    <div className="flex flex-wrap justify-end gap-2">
                        {ani?.installed ? <Badge tone="green">ani-cli {ani.version}</Badge> : <Badge tone="red">ani-cli not found</Badge>}
                        <Badge tone={ani?.ytDlp ? "green" : "gray"}>yt-dlp {ani?.ytDlp ? "✓" : "✗"}</Badge>
                        <Badge tone={ani?.ffmpeg ? "green" : "gray"}>ffmpeg {ani?.ffmpeg ? "✓" : "✗"}</Badge>
                    </div>
                </Row>
                {!ani?.installed && !canInstallPrograms(status) && (
                    <p className="px-4 py-3 text-sm text-muted">
                        {installSource(status?.platform, "ani-cli")}: <code className="rounded bg-black/30 px-1.5 py-0.5">{installHint(status?.platform, "ani-cli")}</code>
                    </p>
                )}
                {!ani?.installed && canInstallPrograms(status) && (
                    <Row
                        label="Install ani-cli"
                        help={
                            status?.platform === "darwin"
                                ? "With the other programs Kumo uses, downloaded into Kumo's folder: Settings › App › Programs."
                                : "With Git, whose bash runs it, and the other programs Kumo uses: Settings › App › Programs."
                        }
                    >
                        <InstallProgramsButton />
                    </Row>
                )}
                <Stack label="Executable">
                    <Input value={a.path} onChange={e => set("aniCli", { path: e.target.value })} icon={<Terminal className="size-4" />} />
                </Stack>
                <Row label="Default audio">
                    <div className="flex rounded-lg bg-white/[0.04] p-0.5">
                        {(["sub", "dub"] as const).map(m => (
                            <button key={m} onClick={() => set("aniCli", { defaultMode: m })} className={cn("h-7 rounded-md px-3 text-xs font-medium uppercase transition-colors", a.defaultMode === m ? "bg-white/[0.1] text-fg" : "text-muted hover:text-fg")}>
                                {m}
                            </button>
                        ))}
                    </div>
                </Row>
                <Row label="Quality">
                    <Select
                        className="w-40"
                        value={a.quality}
                        onChange={v => set("aniCli", { quality: v })}
                        options={[
                            { value: "best", label: "Best" },
                            { value: "1080", label: "1080p" },
                            { value: "720", label: "720p" },
                            { value: "480", label: "480p" },
                            { value: "worst", label: "Worst" },
                        ]}
                    />
                </Row>
                <Row label="Downloader">
                    <Select
                        className="w-40"
                        value={a.downloader}
                        onChange={v => set("aniCli", { downloader: v })}
                        options={[
                            { value: "auto", label: "Automatic" },
                            { value: "yt-dlp", label: "yt-dlp" },
                            { value: "ffmpeg", label: "ffmpeg" },
                        ]}
                    />
                </Row>
                <Stack label="Download folder" help={`Empty = your library folder (${status?.settings.library.dir || "not set"}). Downloads are matched automatically.`}>
                    <PathInput value={a.downloadDir} onChange={v => set("aniCli", { downloadDir: v })} placeholder="Library folder" />
                </Stack>
            </Group>
        </>
    )
}

function MangaSection({ draft, set }: SectionProps) {
    const { data } = useQuery({ queryKey: ["manga-providers"], queryFn: () => api.get<{ id: string; name: string }[]>("/api/manga/providers") })
    return (
        <Group title="Manga">
            <Row label="Enable manga">
                <Switch checked={draft.manga.enabled} onChange={v => set("manga", { enabled: v })} />
            </Row>
            <Row label="Default provider" help="Install manga providers from the marketplace">
                <Select className="w-56" value={draft.manga.defaultProvider} onChange={v => set("manga", { defaultProvider: v })} options={[{ value: "", label: "First installed" }, ...(data ?? []).map(p => ({ value: p.id, label: p.name }))]} />
            </Row>
            <Row label="Reading mode">
                <Select
                    className="w-44"
                    value={draft.manga.readingMode}
                    onChange={v => set("manga", { readingMode: v as "double" })}
                    options={[
                        { value: "double", label: "Two pages" },
                        { value: "paged", label: "Single page" },
                        { value: "long-strip", label: "Long strip" },
                    ]}
                />
            </Row>
            <Row label="Reading direction">
                <Select
                    className="w-44"
                    value={draft.manga.direction}
                    onChange={v => set("manga", { direction: v })}
                    options={[
                        { value: "rtl", label: "Right to left (manga)" },
                        { value: "ltr", label: "Left to right" },
                    ]}
                />
            </Row>
        </Group>
    )
}

function DiscordSection({ draft, set }: SectionProps) {
    return (
        <Group title="Rich presence" description="Talks to the Discord app on this computer — nothing is sent anywhere else.">
            <Row label="Show what I'm watching">
                <Switch checked={draft.discord.richPresence} onChange={v => set("discord", { richPresence: v })} />
            </Row>
            <Stack label="Discord application ID" help="Create an application at discord.com/developers and paste its Application ID.">
                <Input value={draft.discord.clientId} onChange={e => set("discord", { clientId: e.target.value })} />
            </Stack>
        </Group>
    )
}

function LogsSection() {
    const { data: logs, refetch } = useQuery({ queryKey: ["logs"], queryFn: () => api.get<string[]>("/api/logs"), refetchInterval: 4000 })
    const { data: cache, refetch: refetchCache } = useQuery({ queryKey: ["cache-size"], queryFn: () => api.get<{ entries: number }>("/api/cache/size") })
    const { data: imgs, refetch: refetchImgs } = useQuery({ queryKey: ["image-cache"], queryFn: () => api.get<{ files: number; bytes: number }>("/api/images/stats") })
    const clear = async () => {
        try {
            await api.post("/api/cache/clear")
            toast.success("Cache cleared")
            refetchCache()
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const clearImages = async () => {
        try {
            await api.post("/api/images/clear")
            toast.success("Artwork cache cleared — covers will be downloaded again")
            refetchImgs()
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    return (
        <>
            <Group title="Cache" description="Cached AniList data, episode metadata, search results and probes.">
                <Row label={`${cache?.entries ?? 0} cached entries`}>
                    <Button variant="danger" size="sm" icon={<Trash2 className="size-4" />} onClick={clear}>
                        Clear cache
                    </Button>
                </Row>
                <Row label={`Artwork: ${imgs?.files ?? 0} images (${formatBytes(imgs?.bytes ?? 0)})`} help="Covers, banners and episode thumbnails downloaded for offline use">
                    <Button variant="danger" size="sm" icon={<Trash2 className="size-4" />} onClick={clearImages}>
                        Clear artwork
                    </Button>
                </Row>
            </Group>
            <div>
                <div className="mb-3 flex items-center justify-between">
                    <h3 className="font-semibold">Server logs</h3>
                    <Button size="sm" variant="ghost" onClick={() => refetch()}>
                        Refresh
                    </Button>
                </div>
                <pre className="card max-h-[60vh] overflow-auto p-4 text-xs leading-relaxed text-fg/80">{(logs ?? []).slice(-400).join("\n") || "No logs yet."}</pre>
            </div>
        </>
    )
}

// The version, and Kumo's own updates: the Home page offers and installs
// them, this says where things stand.
function AboutSection() {
    const { data: status } = useStatus()
    const { data: u } = useUpdateStatus()
    const check = useCheckUpdate()
    const lan = status?.client === "lan"
    const commit = u?.current.commit
    const latest = versionName(u?.latest)
    const busy = ["downloading", "building", "installing"].includes(u?.state ?? "")
    let state: { tone: "green" | "brand" | "amber" | "gray"; label: string; help?: string }
    if (!u || u.checking) state = { tone: "gray", label: "Checking…" }
    else if (busy) state = { tone: "brand", label: `Updating to ${latest}`, help: u.message }
    else if (u.state === "ready") state = { tone: "green", label: `${latest} installed`, help: u.message }
    else if (u.available) state = { tone: "brand", label: `${latest} available`, help: lan ? undefined : u.canApply ? "Update from the Home page." : u.applyNote }
    else if (u.latest) state = { tone: "green", label: "Up to date", help: u.note }
    else if (u.error) state = { tone: "amber", label: "Couldn't check" }
    else state = { tone: "gray", label: "Not checked yet", help: "Kumo checks shortly after it starts, then every 6 hours." }
    if (u?.state === "failed" && !busy) state.help = [u.message, state.help].filter(Boolean).join(" ")
    return (
        <>
            <Group title="Kumo">
                <Row label="Version" help={`${platformName(status?.platform)} · ${status?.client === "desktop" ? "Desktop" : status?.client === "lan" ? "LAN" : "Web UI"}`}>
                    <span className="flex items-center gap-2 text-sm">
                        {status?.version}
                        {commit && (
                            <code className="rounded bg-black/30 px-1.5 py-0.5 text-xs" title={commit}>
                                {commit.slice(0, 7)}
                            </code>
                        )}
                    </span>
                </Row>
            </Group>
            <Group title="Updates" description="Kumo looks for a newer version on GitHub and shows it on the Home page, where you can install it.">
                <Row label="Status" help={state.help}>
                    <Badge tone={state.tone}>{state.label}</Badge>
                </Row>
                {u?.latest && (
                    <Row label="Newest version">
                        <a href={u.latest.url} target="_blank" rel="noopener noreferrer" className="flex items-center gap-2 text-sm hover:underline">
                            {latest}
                            {u.latest.commit && u.latest.version && <code className="rounded bg-black/30 px-1.5 py-0.5 text-xs">{u.latest.commit.slice(0, 7)}</code>}
                        </a>
                    </Row>
                )}
                <Row label="Last check" help={u?.checking ? "Checking now…" : u?.checkedAt ? `${relativeTime(u.checkedAt)}. Kumo checks every 6 hours.` : "Never"}>
                    {!lan && (
                        <Button size="sm" icon={<RefreshCw className="size-4" />} loading={check.isPending || u?.checking} onClick={() => check.mutate()}>
                            Check for updates
                        </Button>
                    )}
                </Row>
                {u?.error && (
                    <div className="px-4 py-3.5 text-sm">
                        <p className="text-rose-300">{u.error}</p>
                        {u.hint && <p className="mt-1 text-muted">{u.hint}</p>}
                    </div>
                )}
            </Group>
        </>
    )
}
