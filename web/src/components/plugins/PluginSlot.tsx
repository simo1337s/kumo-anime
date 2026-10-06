import { useEffect, useMemo, useRef } from "react"
import { pluginStore, useStore } from "@/lib/store"
import { cn } from "@/lib/utils"
import { Button } from "../ui"
import { sendPluginEvent } from "./PluginRenderer"

// Bridge injected into plugin webviews: exposes window.webview.channel.
const BRIDGE = `<script>(function(){var h={};window.webview={channel:{on:function(n,cb){(h[n]=h[n]||[]).push(cb)},send:function(n,p){parent.postMessage({__kumoWebview:true,name:n,payload:p},'*')}}};window.addEventListener('message',function(e){var d=e.data;if(d&&d.__kumoToWebview){(h[d.name]||[]).forEach(function(cb){try{cb(d.payload)}catch(err){console.error(err)}})}});})();</script>`

function withBridge(html: string) {
    if (/<head[^>]*>/i.test(html)) return html.replace(/<head([^>]*)>/i, `<head$1>${BRIDGE}`)
    return BRIDGE + html
}

export function WebviewFrame({ pluginId, id, html, options, className }: { pluginId: string; id: string; html: string; options: Record<string, any>; className?: string }) {
    const ref = useRef<HTMLIFrameElement>(null)
    useEffect(() => {
        const onMsg = (e: MessageEvent) => {
            if (e.source !== ref.current?.contentWindow) return
            const d = e.data
            if (d && d.__kumoWebview) sendPluginEvent(pluginId, { kind: "webview-message", webviewId: id, name: d.name, payload: d.payload })
        }
        const onServer = (e: Event) => {
            const d = (e as CustomEvent).detail
            if (d?.pluginId === pluginId && d.webviewId === id) ref.current?.contentWindow?.postMessage({ __kumoToWebview: true, name: d.name, payload: d.payload }, "*")
        }
        window.addEventListener("message", onMsg)
        window.addEventListener("kumo-webview-message", onServer)
        return () => {
            window.removeEventListener("message", onMsg)
            window.removeEventListener("kumo-webview-message", onServer)
        }
    }, [pluginId, id])
    const height = options.height || (options.autoHeight ? "560px" : "420px")
    return (
        <iframe
            ref={ref}
            title={`${pluginId} webview`}
            // No allow-same-origin: plugin pages can't touch the app or its API.
            sandbox="allow-scripts allow-popups allow-forms"
            srcDoc={withBridge(html)}
            className={cn("w-full rounded-2xl border border-line bg-transparent", className)}
            style={{ height, maxHeight: options.maxHeight, maxWidth: options.maxWidth }}
        />
    )
}

// Renders plugin webviews registered for a given screen slot.
export function PluginSlot({ slot, className }: { slot: string; className?: string }) {
    const plugins = useStore(pluginStore)
    const views = useMemo(
        () =>
            Object.values(plugins).flatMap(p =>
                (p.state?.webviews ?? []).filter(w => !w.hidden && w.options?.slot === slot && w.html).map(w => ({ pluginId: p.id, name: p.name, ...w })),
            ),
        [plugins, slot],
    )
    if (views.length === 0) return null
    return (
        <div className={cn("flex flex-col gap-4", className)}>
            {views.map(v => (
                <WebviewFrame key={v.pluginId + v.id} pluginId={v.pluginId} id={v.id} html={v.html} options={v.options} />
            ))}
        </div>
    )
}

// Buttons plugins mounted on media pages (ctx.action.newAnimePageButton).
export function PluginActions({ kind, mediaId }: { kind: string; mediaId: number }) {
    const plugins = useStore(pluginStore)
    const actions = Object.values(plugins).flatMap(p => (p.state?.actions ?? []).filter(a => a.kind === kind).map(a => ({ pluginId: p.id, ...a })))
    return (
        <>
            {actions.map(a => (
                <Button
                    key={a.pluginId + a.id}
                    size="md"
                    variant="subtle"
                    loading={!!a.props.loading}
                    disabled={!!a.props.disabled}
                    style={a.props.style}
                    title={a.props.tooltipText}
                    onClick={() => sendPluginEvent(a.pluginId, { kind: "action", actionId: a.id, mediaId })}
                >
                    {a.props.label}
                </Button>
            ))}
        </>
    )
}

export function usePluginDropdownActions(kind: string) {
    const plugins = useStore(pluginStore)
    return Object.values(plugins).flatMap(p => (p.state?.actions ?? []).filter(a => a.kind === kind).map(a => ({ pluginId: p.id, ...a })))
}
