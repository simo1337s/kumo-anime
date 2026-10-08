import type { QueryClient } from "@tanstack/react-query"
import { toast } from "@/lib/toast"
import { playbackStore, pluginStore, scanStore, torrentCountStore, trayOpenStore } from "./store"
import type { DownloadItem, PluginState, Status, UpdateStatus } from "./types"

type ServerEvent = { type: string; payload: any }

let navigateFn: ((to: string) => void) | null = null
export function setNavigate(fn: (to: string) => void) {
    navigateFn = fn
}

// Connects to /api/events and keeps caches & stores in sync.
export function connectEvents(qc: QueryClient) {
    let es: EventSource | null = null
    let closed = false
    let retry: ReturnType<typeof setTimeout> | null = null

    const handle = (ev: ServerEvent) => {
        const p = ev.payload
        switch (ev.type) {
            case "toast": {
                const fn = { success: toast.success, error: toast.error, warning: toast.warning, info: toast.info }[p.level as string] ?? toast
                fn(p.message)
                break
            }
            case "scan-progress":
                scanStore.set({ running: true, stage: p.stage, done: p.done, total: p.total, message: p.message })
                break
            case "scan-done":
                scanStore.set({ running: false, stage: "", done: 0, total: 0, message: "" })
                if (p?.error) toast.error(`Library scan failed: ${p.error}`)
                qc.invalidateQueries({ queryKey: ["collection"] })
                qc.invalidateQueries({ queryKey: ["library"] })
                qc.invalidateQueries({ queryKey: ["entry"] })
                break
            case "library-updated":
                qc.invalidateQueries({ queryKey: ["collection"] })
                qc.invalidateQueries({ queryKey: ["library"] })
                qc.invalidateQueries({ queryKey: ["entry"] })
                qc.invalidateQueries({ queryKey: ["sharing", "libraries"] })
                break
            case "sharing-updated":
                qc.invalidateQueries({ queryKey: ["sharing"] })
                break
            case "history-updated":
                // Watched on another Kumo of the same account: Continue
                // watching and the episodes' positions.
                qc.invalidateQueries({ queryKey: ["collection"] })
                qc.invalidateQueries({ queryKey: ["entry"] })
                break
            case "collection-updated":
                qc.invalidateQueries({ queryKey: ["collection"] })
                qc.invalidateQueries({ queryKey: ["entry"] })
                qc.invalidateQueries({ queryKey: ["list"] })
                // Manga lists and the list entry on a manga's page; not the
                // chapters or pages, which providers would send again.
                qc.invalidateQueries({ queryKey: ["manga", "collection"] })
                qc.invalidateQueries({ queryKey: ["manga", "media"] })
                break
            case "settings-updated":
                qc.invalidateQueries({ queryKey: ["status"] })
                break
            case "playback-status":
                playbackStore.set(p)
                break
            case "playback-ended":
                playbackStore.set(null)
                qc.invalidateQueries({ queryKey: ["collection"] })
                qc.invalidateQueries({ queryKey: ["entry", p?.mediaId] })
                break
            case "download-progress": {
                qc.setQueryData<DownloadItem[]>(["downloads"], old => {
                    if (!old) return old
                    if (p.removed) return old.filter(d => d.id !== p.id)
                    if (p.cleared) return old.filter(d => !["completed", "failed", "canceled"].includes(d.status))
                    const idx = old.findIndex(d => d.id === p.id)
                    if (idx === -1) return [p, ...old]
                    const next = old.slice()
                    next[idx] = p
                    return next
                })
                break
            }
            case "torrent-count":
                torrentCountStore.set(p)
                break
            case "extensions-updated":
                qc.invalidateQueries({ queryKey: ["extensions"] })
                qc.invalidateQueries({ queryKey: ["marketplace"] })
                qc.invalidateQueries({ queryKey: ["plugins-ui"] })
                qc.invalidateQueries({ queryKey: ["os-providers"] })
                break
            case "plugin-ui":
                handlePluginEvent(p)
                break
            case "update-status": {
                // Only this computer installs updates (GET /api/update says
                // so to devices on the network too).
                const lan = qc.getQueryData<Status>(["status"])?.client === "lan"
                qc.setQueryData<UpdateStatus>(["update"], lan ? { ...p, canApply: false, applyNote: "", manualCommand: "" } : p)
                break
            }
        }
    }

    // The server's version when this page loaded: back after a restart as
    // another version (an update installed while it ran), the page loads
    // again, as that version's.
    let version: string | null = null

    const connect = () => {
        if (closed) return
        es = new EventSource("/api/events")
        // Events sent while disconnected are lost: re-sync the scan state so
        // a missed "scan-done" can't leave the scan indicator spinning, and
        // the update's.
        es.onopen = () => {
            qc.invalidateQueries({ queryKey: ["update"] })
            fetch("/api/status", { credentials: "same-origin" })
                .then(r => (r.ok ? r.json() : null))
                .then(st => {
                    if (st?.version && version && st.version !== version) window.location.reload()
                    if (st?.version) version = st.version
                    if (st && !st.scanning) scanStore.set({ running: false, stage: "", done: 0, total: 0, message: "" })
                })
                .catch(() => {})
        }
        es.onmessage = msg => {
            try {
                handle(JSON.parse(msg.data))
            } catch {
                /* ignore */
            }
        }
        es.onerror = () => {
            es?.close()
            if (!closed) retry = setTimeout(connect, 3000)
        }
    }
    connect()
    return () => {
        closed = true
        if (retry) clearTimeout(retry)
        es?.close()
    }
}

function handlePluginEvent(p: { pluginId: string; type: string; payload: any }) {
    switch (p.type) {
        case "state":
            pluginStore.set(prev => ({
                ...prev,
                [p.pluginId]: { ...(prev[p.pluginId] ?? { id: p.pluginId, name: p.pluginId, icon: "" }), state: p.payload } as PluginState,
            }))
            break
        case "removed":
            pluginStore.set(prev => {
                const next = { ...prev }
                delete next[p.pluginId]
                return next
            })
            break
        case "navigate": {
            const path = String(p.payload?.path || "/")
            const params = new URLSearchParams(p.payload?.searchParams || {}).toString()
            navigateFn?.(params ? `${path}?${params}` : path)
            break
        }
        case "reload":
            window.location.reload()
            break
        case "tray-open":
            trayOpenStore.set({ pluginId: p.pluginId, trayId: p.payload?.trayId })
            break
        case "tray-close":
            trayOpenStore.set(null)
            break
        case "open-url":
            if (p.payload?.url) window.open(p.payload.url, "_blank", "noopener")
            break
        case "clipboard":
            navigator.clipboard?.writeText(p.payload?.text ?? "").catch(() => {})
            break
        case "webview-message":
            window.dispatchEvent(new CustomEvent("kumo-webview-message", { detail: { pluginId: p.pluginId, ...p.payload } }))
            break
    }
}
