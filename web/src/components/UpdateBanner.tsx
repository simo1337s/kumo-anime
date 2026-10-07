import { AlertTriangle, ArrowUpCircle, Check, ChevronDown, Copy, Download, ExternalLink, Loader2, RefreshCw, RotateCcw } from "lucide-react"
import { useEffect, useState } from "react"
import { toast } from "@/lib/toast"
import { Button, Progress } from "@/components/ui"
import { api, ApiError } from "@/lib/api"
import { usePersisted } from "@/lib/hooks"
import { useApplyUpdate, useStatus, useUpdateStatus } from "@/lib/queries"
import { createStore, useStore } from "@/lib/store"
import type { UpdateStatus, UpdateVersion } from "@/lib/types"
import { cn, copyText, plural } from "@/lib/utils"

const BUSY = ["downloading", "building", "installing"]

// A version's name: its number, or else its commit.
export function versionName(v?: UpdateVersion | null) {
    return v?.version || v?.commit?.slice(0, 7) || ""
}

// Waiting for Kumo to come back after it restarted into a new version. It
// outlives the banner, so leaving the Home page doesn't stop it.
const restartStore = createStore<"" | "waiting" | "timeout">("")

// Polls the server until it answers with another version, or answers at all
// after it was gone, then reloads the page: the new version's UI. Gives up
// after 2 minutes.
function waitForRestart(version: string) {
    if (restartStore.get() === "waiting") return
    restartStore.set("waiting")
    const started = Date.now()
    let gone = false
    const poll = async () => {
        try {
            const res = await fetch("/api/status", { credentials: "same-origin", cache: "no-store" })
            if (res.ok) {
                const st = await res.json()
                if (gone || st.version !== version) {
                    location.reload()
                    return
                }
            } else gone = true
        } catch {
            gone = true
        }
        if (Date.now() - started < 120_000) setTimeout(poll, 1000)
        else restartStore.set("timeout")
    }
    setTimeout(poll, 1000)
}

// Home page: a newer Kumo is available, and installing it. Only this
// computer installs updates, so devices on the network don't see it.
export function UpdateBanner() {
    const { data: status } = useStatus()
    const { data: u } = useUpdateStatus()
    const apply = useApplyUpdate()
    const restart = useStore(restartStore)
    const [later, setLater] = usePersisted("kumo-update-later", "")
    const [open, setOpen] = useState(false)
    const windows = status?.platform === "windows"

    // On Windows Kumo quits for the installer, which starts the new version.
    const toInstaller = windows && u?.state === "installing"
    const version = status?.version
    useEffect(() => {
        if (toInstaller && version) waitForRestart(version)
    }, [toInstaller, version])

    if (!status || !u || status.client === "lan") return null
    const latest = versionName(u.latest)
    const key = latest || "unknown"
    const busy = BUSY.includes(u.state)
    const shown = u.available || u.state === "ready" || u.state === "failed"
    if (!restart && !busy && (!shown || later === key)) return null

    const restartNow = () =>
        api.post("/api/update/restart")
            .then(() => waitForRestart(status.version))
            // No answer at all: it's restarting already.
            .catch(e => (e instanceof ApiError ? toast.error(e.message) : waitForRestart(status.version)))
    const laterButton = (
        <Button size="sm" variant="ghost" onClick={() => setLater(key)}>
            Later
        </Button>
    )

    if (restart)
        return restart === "waiting" ? (
            <Panel
                icon={<Spin />}
                title="Restarting Kumo…"
                text={windows ? "The installer is updating Kumo, which then opens again. This page reloads when Kumo is back." : "This page reloads when Kumo is back."}
            />
        ) : (
            <Panel
                icon={<Icon tone="amber"><AlertTriangle /></Icon>}
                title="Kumo hasn't come back yet"
                text="Start Kumo again from your app menu, then reload this page."
                actions={
                    <Button size="sm" icon={<RotateCcw className="size-4" />} onClick={() => location.reload()}>
                        Reload
                    </Button>
                }
            />
        )

    if (busy) {
        const last = u.log?.at(-1)
        return (
            <Panel icon={<Spin />} title={`Updating to Kumo ${latest}`} text={u.message}>
                <div className="border-t border-line px-5 py-4">
                    <Progress value={u.progress >= 0 ? u.progress / 100 : 1} barClassName={cn(u.progress < 0 && "animate-pulse opacity-50")} />
                    {last && <p className="mt-2 truncate font-mono text-xs text-subtle">{last}</p>}
                </div>
            </Panel>
        )
    }

    if (u.state === "ready") {
        const restartButton = (
            <Button size="sm" variant="primary" icon={<RefreshCw className="size-4" />} onClick={restartNow}>
                Restart Kumo
            </Button>
        )
        // pkexec couldn't ask for the password: the user installs it.
        if (u.manualCommand)
            return (
                <Panel icon={<Icon><Download /></Icon>} title={`Kumo ${latest} is ready to install`} text={u.message} actions={laterButton}>
                    <div className="flex flex-col items-start gap-3 border-t border-line px-5 py-4">
                        <Command command={u.manualCommand} />
                        {restartButton}
                    </div>
                </Panel>
            )
        return (
            <Panel
                icon={<Icon tone="green"><Check /></Icon>}
                title={`Kumo ${latest} is installed`}
                text={u.message}
                actions={
                    <>
                        {restartButton}
                        {laterButton}
                    </>
                }
            />
        )
    }

    if (u.state === "failed") {
        const log = u.log ?? []
        return (
            <Panel
                icon={<Icon tone="red"><AlertTriangle /></Icon>}
                title="Kumo couldn't be updated"
                text={u.message}
                actions={
                    <>
                        {u.available && u.canApply && (
                            <Button size="sm" variant="primary" icon={<RotateCcw className="size-4" />} loading={apply.isPending} onClick={() => apply.mutate()}>
                                Retry
                            </Button>
                        )}
                        {laterButton}
                    </>
                }
            >
                {(!!u.manualCommand || log.length > 0) && (
                    <div className="flex flex-col gap-3 border-t border-line px-5 py-4">
                        {u.manualCommand && <Command command={u.manualCommand} />}
                        {log.length > 0 && (
                            <details className="text-xs">
                                <summary className="cursor-pointer text-subtle hover:text-fg">Output</summary>
                                <pre className="mt-2 max-h-56 overflow-auto rounded-xl bg-black/30 p-3 leading-relaxed whitespace-pre-wrap text-fg/80">{log.join("\n")}</pre>
                            </details>
                        )}
                    </div>
                )}
            </Panel>
        )
    }

    // A newer version is available.
    const hasNews = (u.changes?.length ?? 0) > 0 || !!u.note
    return (
        <Panel
            icon={<Icon><ArrowUpCircle /></Icon>}
            title={`Kumo ${latest} is available`}
            text={
                <>
                    {u.changesTotal > 0 && <span className="block">{plural(u.changesTotal, "change")} since your version</span>}
                    {!u.canApply && u.applyNote && <span className="block">{u.applyNote}</span>}
                </>
            }
            actions={
                <>
                    {hasNews && (
                        <Button size="sm" variant="subtle" onClick={() => setOpen(o => !o)}>
                            What's new
                            <ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} />
                        </Button>
                    )}
                    {u.canApply ? (
                        <Button size="sm" variant="primary" icon={<Download className="size-4" />} loading={apply.isPending} onClick={() => apply.mutate()}>
                            Update
                        </Button>
                    ) : (
                        windows &&
                        u.latest?.url && (
                            <a href={u.latest.url} target="_blank" rel="noopener noreferrer">
                                <Button size="sm" variant="primary" icon={<ExternalLink className="size-4" />}>
                                    Download
                                </Button>
                            </a>
                        )
                    )}
                    {laterButton}
                </>
            }
        >
            {open && hasNews && <Changes u={u} />}
        </Panel>
    )
}

function Panel({ icon, title, text, actions, children }: { icon: React.ReactNode; title: React.ReactNode; text?: React.ReactNode; actions?: React.ReactNode; children?: React.ReactNode }) {
    return (
        <div className="px-6 pt-6 md:px-8 xl:px-10">
            <div className="card overflow-hidden rise-in">
                <div className="flex flex-wrap items-center gap-x-4 gap-y-3 px-5 py-4">
                    {icon}
                    <div className="min-w-0 flex-1">
                        <p className="font-medium">{title}</p>
                        {text && <div className="mt-0.5 text-sm text-muted">{text}</div>}
                    </div>
                    {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
                </div>
                {children}
            </div>
        </div>
    )
}

function Icon({ children, tone = "brand" }: { children: React.ReactNode; tone?: "brand" | "green" | "amber" | "red" }) {
    const tones = {
        brand: "bg-brand-soft text-brand-strong",
        green: "bg-emerald-500/15 text-emerald-300",
        amber: "bg-amber-500/15 text-amber-300",
        red: "bg-rose-500/15 text-rose-300",
    }
    return <span className={cn("grid size-9 shrink-0 place-items-center rounded-lg [&>svg]:size-[18px]", tones[tone])}>{children}</span>
}

function Spin() {
    return (
        <Icon>
            <Loader2 className="animate-spin" />
        </Icon>
    )
}

// A command to run in a terminal, with a copy button.
function Command({ command }: { command: string }) {
    const copy = () => copyText(command).then(ok => (ok ? toast.success("Command copied") : toast.error("Couldn't copy it")))
    return (
        <div className="flex w-full items-center gap-2 rounded-lg bg-black/30 py-1.5 pr-1.5 pl-3">
            <code className="min-w-0 flex-1 overflow-x-auto font-mono text-xs whitespace-nowrap">{command}</code>
            <Button size="xs" variant="ghost" icon={<Copy className="size-3.5" />} onClick={copy}>
                Copy
            </Button>
        </div>
    )
}

function Changes({ u }: { u: UpdateStatus }) {
    const changes = u.changes ?? []
    const more = u.changesTotal - changes.length
    return (
        <div className="border-t border-line px-5 py-4">
            {u.note && <p className="mb-2 text-sm text-muted">{u.note}</p>}
            {changes.length > 0 && (
                <ul className="flex flex-col gap-1.5 text-sm">
                    {changes.map((c, i) => (
                        <li key={i} className="flex gap-2">
                            <span className="text-subtle">•</span>
                            <span className="min-w-0">{c}</span>
                        </li>
                    ))}
                </ul>
            )}
            {more > 0 && <p className="mt-2 text-xs text-subtle">and {plural(more, "more change")}</p>}
        </div>
    )
}
