import { useEffect, useMemo } from "react"
import { usePluginStates } from "@/lib/queries"
import { pluginStore, trayOpenStore, useStore } from "@/lib/store"
import type { PluginState } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Popover, Tooltip } from "../ui"
import { PluginTree, sendPluginEvent } from "./PluginRenderer"
import { Puzzle } from "lucide-react"
import { NavLink } from "react-router-dom"

// Keeps the plugin store seeded from the API (live updates come via SSE).
export function usePlugins() {
    const { data } = usePluginStates()
    useEffect(() => {
        if (!data) return
        const next: Record<string, PluginState> = {}
        data.forEach(p => {
            let state = p.state as any
            if (typeof state === "string") {
                try {
                    state = JSON.parse(state)
                } catch {
                    state = {}
                }
            }
            next[p.id] = { ...p, state: state ?? {} }
        })
        pluginStore.set(prev => {
            // keep names/icons from the API, state from whichever is newer (SSE wins)
            const merged: Record<string, PluginState> = {}
            for (const [id, p] of Object.entries(next)) merged[id] = { ...p, state: prev[id]?.state && Object.keys(prev[id].state).length ? prev[id].state : p.state }
            return merged
        })
    }, [data])
    return useStore(pluginStore)
}

export function PluginTrays() {
    const plugins = usePlugins()
    const open = useStore(trayOpenStore)
    const trays = useMemo(
        () => Object.values(plugins).flatMap(p => (p.state?.trays ?? []).map(t => ({ plugin: p, tray: t }))),
        [plugins],
    )
    const pages = useMemo(
        () => Object.values(plugins).flatMap(p => (p.state?.webviews ?? []).filter(w => w.options?.sidebar && !w.hidden).map(w => ({ plugin: p, view: w }))),
        [plugins],
    )
    if (trays.length === 0 && pages.length === 0) return null
    return (
        <div className="mb-1 flex flex-wrap items-center justify-center gap-1 border-b border-line pb-2 lg:justify-start">
            {pages.map(({ plugin, view }) => (
                <Tooltip key={plugin.id + view.id} content={view.options.sidebar?.label || plugin.name} side="right">
                    <NavLink
                        to={`/webview?plugin=${encodeURIComponent(plugin.id)}&id=${encodeURIComponent(view.id)}`}
                        className={({ isActive }) => cn("grid size-9 place-items-center rounded-lg transition-colors hover:bg-white/[0.06]", isActive && "bg-white/[0.08]")}
                    >
                        {plugin.icon ? <img src={plugin.icon} alt="" className="size-5 rounded object-contain" /> : <Puzzle className="size-[18px] stroke-[1.75] text-muted" />}
                    </NavLink>
                </Tooltip>
            ))}
            {trays.map(({ plugin, tray }) => {
                const isOpen = open?.pluginId === plugin.id && open?.trayId === tray.id
                const icon = (
                    <button
                        className={cn(
                            "focus-ring relative grid size-9 place-items-center rounded-lg transition-colors hover:bg-white/[0.06]",
                            isOpen && "bg-white/[0.08]",
                        )}
                        onClick={() => {
                            sendPluginEvent(plugin.id, { kind: "tray-click", trayId: tray.id })
                            if (!tray.withContent) return
                        }}
                    >
                        {tray.iconUrl ? (
                            <img src={tray.iconUrl} alt="" className="size-5 rounded object-contain" />
                        ) : (
                            <Puzzle className="size-[18px] stroke-[1.75] text-muted" />
                        )}
                        {!!tray.badge?.number && (
                            <span className="absolute -top-0.5 -right-0.5 grid h-4 min-w-4 place-items-center rounded-full bg-brand px-1 text-[9px] font-bold text-white">
                                {tray.badge.number}
                            </span>
                        )}
                    </button>
                )
                if (!tray.withContent) {
                    return (
                        <Tooltip key={plugin.id + tray.id} content={tray.tooltipText || plugin.name} side="right">
                            {icon}
                        </Tooltip>
                    )
                }
                return (
                    <Popover
                        key={plugin.id + tray.id}
                        open={isOpen}
                        onOpenChange={v => {
                            trayOpenStore.set(v ? { pluginId: plugin.id, trayId: tray.id } : null)
                            sendPluginEvent(plugin.id, { kind: v ? "tray-open" : "tray-close", trayId: tray.id })
                        }}
                        side="right"
                        align="end"
                        className="w-[min(92vw,var(--tray-w))]"
                        trigger={icon}
                    >
                        <div style={{ ["--tray-w" as any]: tray.width || "26rem", minHeight: tray.minHeight || undefined }} className="w-[min(88vw,var(--tray-w))]">
                            <div className="mb-3 flex items-center gap-2 border-b border-line pb-3">
                                {tray.iconUrl && <img src={tray.iconUrl} alt="" className="size-5 rounded" />}
                                <span className="text-sm font-semibold">{plugin.name}</span>
                            </div>
                            <PluginTree pluginId={plugin.id} node={tray.tree} />
                        </div>
                    </Popover>
                )
            })}
        </div>
    )
}
