import { useRef } from "react"
import { useStore } from "@/lib/store"
import { runMenuItem, tvMenuStore, type TvMenu as Menu } from "@/lib/tv"
import { Dropdown, DropdownContent, DropdownItem, DropdownTrigger } from "./ui"

// What the remote's Menu key (☰) lists, where a right-click opens nothing
// (see openMenu in lib/tv.ts): picking one presses it.
export function TvMenu() {
    const menu = useStore(tvMenuStore)
    const last = useRef<Menu | null>(null)
    const picked = useRef<HTMLElement | null>(null)
    if (menu) last.current = menu
    const at = menu?.rect
    return (
        <Dropdown open={!!menu} onOpenChange={v => !v && tvMenuStore.set(null)} modal={false}>
            <DropdownTrigger asChild>
                <span aria-hidden className="pointer-events-none fixed" style={at ? { left: at.left + at.width / 2, top: at.top + at.height / 3 } : undefined} />
            </DropdownTrigger>
            <DropdownContent
                align="center"
                sideOffset={2}
                onCloseAutoFocus={e => {
                    e.preventDefault()
                    const el = picked.current
                    picked.current = null
                    if (el) runMenuItem(el)
                    else if (document.activeElement === document.body) last.current?.from?.focus({ preventScroll: true })
                }}
            >
                {menu?.items.map((it, i) => (
                    <DropdownItem key={i} onSelect={() => (picked.current = it.el)}>
                        {it.label}
                    </DropdownItem>
                ))}
            </DropdownContent>
        </Dropdown>
    )
}
