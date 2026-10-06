import { createContext, useContext, useEffect, useRef, useState } from "react"
import { api } from "@/lib/api"
import type { UINode } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Badge, Button, Dialog, Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger, Popover, Select, Switch, Tooltip } from "../ui"

// Renders the component trees produced by plugin trays (Seanime's tray
// component API) and routes events back to the plugin.

const PluginCtx = createContext<string>("")

export function sendPluginEvent(pluginId: string, evt: Record<string, unknown>) {
    return api.post(`/api/plugins/${encodeURIComponent(pluginId)}/event`, evt).catch(() => {})
}

function useSend() {
    const pluginId = useContext(PluginCtx)
    return (evt: Record<string, unknown>) => sendPluginEvent(pluginId, evt)
}

export function PluginTree({ pluginId, node }: { pluginId: string; node: UINode | null }) {
    if (!node) return null
    return (
        <PluginCtx.Provider value={pluginId}>
            <div className="kumo-plugin text-sm">
                <Node node={node} />
            </div>
        </PluginCtx.Provider>
    )
}

function Items({ items }: { items?: any[] }) {
    if (!Array.isArray(items)) return null
    return (
        <>
            {items.map((it, i) => (typeof it === "string" ? <span key={i}>{it}</span> : it && typeof it === "object" && it.type ? <Node key={it.id ?? i} node={it} /> : null))}
        </>
    )
}

function gap(n?: number) {
    return n === undefined ? "0.5rem" : `${n * 0.25}rem`
}

const intentVariant = (intent?: string) => {
    if (!intent) return "subtle" as const
    if (intent.startsWith("primary")) return intent.includes("subtle") || intent.includes("outline") ? ("outline" as const) : ("primary" as const)
    if (intent.startsWith("alert") || intent.startsWith("danger")) return "danger" as const
    if (intent.startsWith("success")) return "success" as const
    if (intent.startsWith("white")) return "white" as const
    if (intent.includes("link") || intent.includes("basic")) return "ghost" as const
    return "subtle" as const
}

const badgeTone = (intent?: string) =>
    (({ primary: "brand", success: "green", warning: "amber", alert: "red", info: "blue", blue: "blue" } as const)[intent?.split("-")[0] as "primary"] ?? "gray") as
        | "brand"
        | "green"
        | "amber"
        | "red"
        | "blue"
        | "gray"

function Node({ node }: { node: UINode }) {
    const send = useSend()
    const p = node.props ?? {}
    const style = p.style as React.CSSProperties | undefined
    const cls = p.className as string | undefined
    const click = (e?: React.MouseEvent) => {
        e?.stopPropagation()
        if (p.onClick) send({ kind: "handler", name: p.onClick, payload: {} })
    }

    switch (node.type) {
        case "div":
            return (
                <div className={cls} style={style}>
                    <Items items={p.items} />
                </div>
            )
        case "flex":
            return (
                <div className={cn("flex", p.direction === "column" ? "flex-col" : "flex-row flex-wrap items-center", cls)} style={{ gap: gap(p.gap), ...style }}>
                    <Items items={p.items} />
                </div>
            )
        case "stack":
            return (
                <div className={cn("flex flex-col", cls)} style={{ gap: gap(p.gap), ...style }}>
                    <Items items={p.items} />
                </div>
            )
        case "text":
            return (
                <p className={cn("leading-relaxed whitespace-pre-wrap", cls)} style={style}>
                    {String(p.text ?? "")}
                </p>
            )
        case "span":
            return (
                <span className={cls} style={style}>
                    {String(p.text ?? "")}
                    <Items items={p.items} />
                </span>
            )
        case "p":
            return (
                <p className={cls} style={style}>
                    <Items items={p.items} />
                </p>
            )
        case "button":
            return (
                <Button
                    size={p.size === "xs" ? "xs" : p.size === "lg" ? "md" : "sm"}
                    variant={intentVariant(p.intent)}
                    disabled={!!p.disabled}
                    loading={!!p.loading}
                    className={cls}
                    style={style}
                    onClick={click}
                >
                    {String(p.label ?? "")}
                </Button>
            )
        case "anchor":
            return (
                <a href={p.href} target={p.target ?? "_blank"} rel="noopener noreferrer" onClick={p.onClick ? click : undefined} className={cn("text-brand hover:underline", cls)} style={style}>
                    {String(p.text ?? "")}
                </a>
            )
        case "a":
            return (
                <a href={p.href} target={p.target ?? "_blank"} rel="noopener noreferrer" onClick={p.onClick ? click : undefined} className={cls} style={style}>
                    <Items items={p.items} />
                </a>
            )
        case "img":
            return <img src={p.src} alt={p.alt ?? ""} width={p.width} height={p.height} className={cn("rounded-lg", cls)} style={style} />
        case "badge":
            return (
                <Badge tone={badgeTone(p.intent)} className={cls}>
                    {String(p.text ?? "")}
                </Badge>
            )
        case "alert": {
            const tone = { success: "border-emerald-500/30 bg-emerald-500/10", warning: "border-amber-500/30 bg-amber-500/10", alert: "border-rose-500/30 bg-rose-500/10" }[
                p.intent as string
            ]
            return (
                <div className={cn("rounded-xl border px-4 py-3", tone ?? "border-sky-500/30 bg-sky-500/10", cls)} style={style}>
                    {p.title && <p className="font-semibold">{p.title}</p>}
                    {p.description && <p className="mt-0.5 text-muted">{p.description}</p>}
                </div>
            )
        }
        case "css":
            return <style>{String(p.css ?? "")}</style>
        case "tooltip":
            return (
                <Tooltip content={String(p.text ?? "")} side={p.side}>
                    <span>{p.item && <Node node={p.item} />}</span>
                </Tooltip>
            )
        case "input":
        case "select":
        case "checkbox":
        case "switch":
        case "radioGroup":
            return <FieldNode node={node} />
        case "modal":
            return <ModalNode node={node} />
        case "dropdownMenu":
            return (
                <Dropdown>
                    <DropdownTrigger asChild>
                        <span className="inline-flex">{p.trigger && <Node node={p.trigger} />}</span>
                    </DropdownTrigger>
                    <DropdownContent>
                        <Items items={p.items} />
                    </DropdownContent>
                </Dropdown>
            )
        case "dropdownMenuItem":
            return (
                <DropdownItem disabled={!!p.disabled} onSelect={() => p.onClick && send({ kind: "handler", name: p.onClick, payload: {} })}>
                    {typeof p.item === "string" ? p.item : p.item && <Node node={p.item} />}
                </DropdownItem>
            )
        case "dropdownMenuSeparator":
            return <DropdownSeparator />
        case "dropdownMenuLabel":
            return <DropdownLabel>{String(p.label ?? "")}</DropdownLabel>
        case "popover":
            return (
                <Popover side="bottom" align="start" trigger={<span className="inline-flex">{p.trigger && <Node node={p.trigger} />}</span>}>
                    <div className="flex flex-col gap-2">
                        <Items items={p.items} />
                    </div>
                </Popover>
            )
        case "tabs":
            return <TabsNode node={node} />
        case "tabsList":
            return (
                <div className={cn("flex gap-1 rounded-xl border border-line bg-surface-2 p-1", cls)}>
                    <Items items={p.items} />
                </div>
            )
        case "tabsTrigger":
            return <TabsTrigger value={p.value} item={p.item} />
        case "tabsContent":
            return <TabsContent value={p.value} items={p.items} />
        case "html":
            return <HtmlFrame html={String(p.html ?? "")} />
        default:
            return p.items ? <Items items={p.items} /> : null
    }
}

function FieldNode({ node }: { node: UINode }) {
    const send = useSend()
    const p = node.props
    const [value, setValue] = useState<any>(p.value ?? (node.type === "checkbox" || node.type === "switch" ? false : ""))
    const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
    useEffect(() => {
        setValue(p.value ?? (node.type === "checkbox" || node.type === "switch" ? false : ""))
    }, [p.value, node.type])

    const commit = (v: any, immediate = false) => {
        setValue(v)
        const fire = () => send({ kind: "field", ref: p.fieldRef, value: v, handler: p.onChange })
        if (timer.current) clearTimeout(timer.current)
        if (immediate) fire()
        else timer.current = setTimeout(fire, 250)
    }

    const label = p.label ? <span className="text-sm font-medium text-fg/90">{p.label}</span> : null
    switch (node.type) {
        case "input":
            return (
                <label className={cn("flex flex-col gap-1.5", p.className)} style={p.style}>
                    {label}
                    {p.textarea ? (
                        <textarea
                            value={value}
                            placeholder={p.placeholder}
                            disabled={p.disabled}
                            onChange={e => commit(e.target.value)}
                            className="min-h-20 rounded-xl border border-line bg-surface-2 px-3 py-2 outline-none focus:border-brand/60"
                        />
                    ) : (
                        <input
                            value={value}
                            placeholder={p.placeholder}
                            disabled={p.disabled}
                            onChange={e => commit(e.target.value)}
                            onKeyDown={e => e.key === "Enter" && p.onSelect && send({ kind: "handler", name: p.onSelect, payload: { value } })}
                            className="h-9 rounded-xl border border-line bg-surface-2 px-3 outline-none focus:border-brand/60"
                        />
                    )}
                </label>
            )
        case "select":
            return (
                <label className={cn("flex flex-col gap-1.5", p.className)}>
                    {label}
                    <Select
                        value={String(value ?? "")}
                        disabled={p.disabled}
                        onChange={v => commit(v, true)}
                        options={[...(p.placeholder && !value ? [{ value: "", label: p.placeholder }] : []), ...((p.options as any[]) ?? []).map(o => ({ value: String(o.value), label: String(o.label) }))]}
                    />
                </label>
            )
        case "checkbox":
            return (
                <label className={cn("flex cursor-pointer items-center gap-2.5", p.className)}>
                    <input type="checkbox" checked={!!value} disabled={p.disabled} onChange={e => commit(e.target.checked, true)} className="size-4 accent-[var(--brand)]" />
                    {label}
                </label>
            )
        case "switch":
            return (
                <label className={cn("flex cursor-pointer items-center justify-between gap-3", p.side === "left" && "flex-row-reverse justify-end", p.className)}>
                    {label}
                    <Switch checked={!!value} disabled={p.disabled} onChange={v => commit(v, true)} />
                </label>
            )
        case "radioGroup":
            return (
                <div className={cn("flex flex-col gap-1.5", p.className)}>
                    {label}
                    {((p.options as any[]) ?? []).map(o => (
                        <label key={o.value} className="flex cursor-pointer items-center gap-2">
                            <input type="radio" checked={value === o.value} onChange={() => commit(o.value, true)} className="accent-[var(--brand)]" />
                            {o.label}
                        </label>
                    ))}
                </div>
            )
    }
    return null
}

function ModalNode({ node }: { node: UINode }) {
    const send = useSend()
    const p = node.props
    const [open, setOpen] = useState(!!p.open)
    useEffect(() => setOpen(!!p.open), [p.open])
    const change = (v: boolean) => {
        setOpen(v)
        if (p.onOpenChange) send({ kind: "handler", name: p.onOpenChange, payload: { open: v } })
    }
    return (
        <>
            <span className="inline-flex" onClick={() => change(true)}>
                {p.trigger && <Node node={p.trigger} />}
            </span>
            <Dialog
                open={open}
                onOpenChange={change}
                title={p.title}
                description={p.description}
                footer={
                    p.footer ? (
                        <div className="flex gap-2">
                            <Items items={p.footer} />
                        </div>
                    ) : undefined
                }
            >
                <div className="flex flex-col gap-3">
                    <Items items={p.items} />
                </div>
            </Dialog>
        </>
    )
}

const TabsCtx = createContext<{ value: string; set: (v: string) => void }>({ value: "", set: () => {} })

function TabsNode({ node }: { node: UINode }) {
    const [value, set] = useState<string>(node.props.defaultValue ?? "")
    return (
        <TabsCtx.Provider value={{ value, set }}>
            <div className={cn("flex flex-col gap-3", node.props.className)}>
                <Items items={node.props.items} />
            </div>
        </TabsCtx.Provider>
    )
}

function TabsTrigger({ value, item }: { value: string; item: any }) {
    const t = useContext(TabsCtx)
    return (
        <button
            onClick={() => t.set(value)}
            className={cn("h-8 rounded-lg px-3 text-sm font-medium transition", t.value === value ? "bg-white/[0.09] text-fg" : "text-muted hover:text-fg")}
        >
            {typeof item === "string" ? item : item && <Node node={item} />}
        </button>
    )
}

function TabsContent({ value, items }: { value: string; items: any[] }) {
    const t = useContext(TabsCtx)
    if (t.value !== value) return null
    return (
        <div className="flex flex-col gap-2">
            <Items items={items} />
        </div>
    )
}

function HtmlFrame({ html }: { html: string }) {
    return <iframe title="plugin" sandbox="allow-popups" srcDoc={html} className="h-80 w-full rounded-xl border border-line bg-white" />
}
