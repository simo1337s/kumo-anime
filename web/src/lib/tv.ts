// TV mode: Kumo on a TV (the Android app on a Fire TV), driven by the remote.
//
// The remote's D-pad sends arrow keys: they move the focus to the nearest
// thing that can be pressed in that direction (spatial navigation), and OK
// (Enter) presses it. Inside a dialog or the player, the focus stays there.
// Menus move it themselves, text fields keep Left/Right for the text, and
// the player keeps the keys it uses (see Player). Back, Menu (☰) and the
// media keys come from the app (window.kumoBack, window.kumoKey).

import { createStore } from "./store"

// The Android app's bridge (MainActivity.java).
type AndroidBridge = {
    keepAwake?: (on: boolean) => void
    installApk?: (path: string) => void
}

declare global {
    interface Window {
        KumoAndroid?: AndroidBridge
        kumoBack?: () => boolean
        kumoKey?: (key: string) => void
    }
}

export const android: AndroidBridge | undefined = typeof window === "undefined" ? undefined : window.KumoAndroid

// Kumo's Android app (a TV's, or a phone's): no mpv there.
export const androidApp = typeof navigator !== "undefined" && /\bKumoAndroid\b/.test(navigator.userAgent)

// The Android app says it in its user agent; ?tv=1 tries TV mode anywhere.
export const tv =
    typeof window !== "undefined" &&
    (/\bKumoTV\b/.test(navigator.userAgent) ||
        (() => {
            try {
                const q = new URLSearchParams(location.search).get("tv")
                if (q !== null) sessionStorage.setItem("kumo-tv", q === "1" ? "1" : "")
                return sessionStorage.getItem("kumo-tv") === "1"
            } catch {
                return false
            }
        })())

// How wide the page is laid out on the TV, in CSS pixels: a 1080p TV is
// 960 wide at the WebView's own scale (2x), which makes everything big.
// Settings › User Interface changes it (stored on the TV).
export const TV_WIDTHS = [
    { value: 1600, label: "Smallest" },
    { value: 1440, label: "Smaller" },
    { value: 1280, label: "Default" },
    { value: 1100, label: "Larger" },
    { value: 960, label: "Largest" },
]
const TV_WIDTH_KEY = "kumo-tv-width"

export function tvWidth(): number {
    try {
        const w = Number(localStorage.getItem(TV_WIDTH_KEY))
        if (TV_WIDTHS.some(o => o.value === w)) return w
    } catch {
        /* ignore */
    }
    return 1280
}

export function setTvWidth(w: number) {
    try {
        localStorage.setItem(TV_WIDTH_KEY, String(w))
    } catch {
        /* ignore */
    }
    applyTvWidth()
}

// The app lays the page out at the viewport's width (it uses the page's
// viewport, see MainActivity), scaled to fit the screen: the width the page
// had at first (device-width) over the new one.
let deviceWidth = 0

function applyTvWidth() {
    if (!androidApp) return
    const meta = document.querySelector<HTMLMetaElement>('meta[name="viewport"]')
    deviceWidth ||= window.innerWidth || window.screen?.width || 0
    if (!meta || !deviceWidth) return
    const w = tvWidth()
    const scale = Math.round((deviceWidth / w) * 10000) / 10000
    meta.content = `width=${w}, initial-scale=${scale}, minimum-scale=${scale}, maximum-scale=${scale}, user-scalable=no`
}

type Dir = "up" | "down" | "left" | "right"
const DIRS: Record<string, Dir> = { ArrowUp: "up", ArrowDown: "down", ArrowLeft: "left", ArrowRight: "right" }

const FOCUSABLE = [
    "a[href]",
    "button:not([disabled])",
    "input:not([disabled]):not([type=hidden])",
    "select:not([disabled])",
    "textarea:not([disabled])",
    "summary",
    '[tabindex]:not([tabindex="-1"])',
    '[role="button"]:not([aria-disabled="true"])',
].join(",")

// Where the focus may go: the open dialog, else the player (data-tv-scope),
// else the page.
function scope(): HTMLElement {
    const dialogs = document.querySelectorAll<HTMLElement>('[role="dialog"], [role="alertdialog"]')
    if (dialogs.length) return dialogs[dialogs.length - 1]
    const scopes = document.querySelectorAll<HTMLElement>("[data-tv-scope]")
    if (scopes.length) return scopes[scopes.length - 1]
    return document.body
}

function candidates(root: HTMLElement): HTMLElement[] {
    return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(el => {
        if (el.closest('[aria-hidden="true"], [inert], [data-tv-skip-nav]')) return false
        const r = el.getBoundingClientRect()
        if (r.width < 2 || r.height < 2) return false
        const cs = getComputedStyle(el)
        if (cs.visibility === "hidden" || cs.display === "none") return false
        // Shown only on mouse hover (a card's extra buttons): not on a TV.
        for (let n: HTMLElement | null = el; n && n !== root; n = n.parentElement) {
            if (getComputedStyle(n).opacity === "0") return false
        }
        return true
    })
}

// The area something is in: a menu at the side (the app's, a page's), or
// the page. Up and Down stay in it while they can.
const AREAS = "aside, nav, [role=dialog], [data-tv-scope], main"
const areaOf = (el: Element | null) => el?.closest(AREAS) ?? null

// The nearest candidate in a direction: the closest along it, much
// preferring those in line with the focused one (the same row or column).
function nearest(from: DOMRect, dir: Dir, cands: HTMLElement[], current: Element | null): HTMLElement | null {
    const fx = from.left + from.width / 2
    const fy = from.top + from.height / 2
    const area = areaOf(current)
    let best: HTMLElement | null = null
    let bestScore = Infinity
    for (const el of cands) {
        if (el === current || el.contains(current) || (current && current.contains(el))) continue
        const r = el.getBoundingClientRect()
        const cx = r.left + r.width / 2
        const cy = r.top + r.height / 2
        let along: number, across: number, offset: number
        switch (dir) {
            case "right":
                if (cx <= fx + 1 || r.right <= from.right) continue
                along = Math.max(0, r.left - from.right)
                across = gap(from.top, from.bottom, r.top, r.bottom)
                offset = Math.abs(cy - fy)
                break
            case "left":
                if (cx >= fx - 1 || r.left >= from.left) continue
                along = Math.max(0, from.left - r.right)
                across = gap(from.top, from.bottom, r.top, r.bottom)
                offset = Math.abs(cy - fy)
                break
            case "down":
                if (cy <= fy + 1 || r.bottom <= from.bottom) continue
                along = Math.max(0, r.top - from.bottom)
                across = gap(from.left, from.right, r.left, r.right)
                offset = Math.abs(cx - fx)
                break
            default:
                if (cy >= fy - 1 || r.top >= from.top) continue
                along = Math.max(0, from.top - r.bottom)
                across = gap(from.left, from.right, r.left, r.right)
                offset = Math.abs(cx - fx)
        }
        let score = along + across * 0.6 + offset * 0.1
        if ((dir === "up" || dir === "down") && areaOf(el) !== area) score += 100000
        if (score < bestScore) {
            bestScore = score
            best = el
        }
    }
    return best
}

// The distance between two ranges (0 when they overlap).
function gap(a1: number, a2: number, b1: number, b2: number) {
    return Math.max(0, b1 - a2, a1 - b2)
}

// Where the focus starts: what's on screen nearest the top left of the page
// (not the sidebar), when nothing is focused yet.
function first(cands: HTMLElement[]): HTMLElement | null {
    const onScreen = cands.filter(el => {
        const r = el.getBoundingClientRect()
        return r.bottom > 0 && r.top < window.innerHeight && r.right > 0 && r.left < window.innerWidth
    })
    const pool = onScreen.length ? onScreen : cands
    const main = document.getElementById("main-scroll")
    const inMain = main ? pool.filter(el => main.contains(el)) : []
    let best: HTMLElement | null = null
    let bestScore = Infinity
    for (const el of inMain.length ? inMain : pool) {
        const r = el.getBoundingClientRect()
        const score = r.top * 2 + r.left
        if (score < bestScore) {
            bestScore = score
            best = el
        }
    }
    return best
}

export function focusEl(el: HTMLElement) {
    el.focus({ preventScroll: true })
    const r = el.getBoundingClientRect()
    const margin = 48
    if (r.top < margin || r.bottom > window.innerHeight - margin || r.left < 0 || r.right > window.innerWidth) {
        el.scrollIntoView({ block: r.height > window.innerHeight / 2 ? "start" : "center", inline: "nearest", behavior: "smooth" })
    }
}

// move moves the focus in a direction; false when there's nowhere to go.
export function move(dir: Dir, root: HTMLElement = scope()): boolean {
    const cands = candidates(root)
    const current = document.activeElement as HTMLElement | null
    const inside = current && current !== document.body && root.contains(current) && cands.includes(current)
    const next = inside ? nearest(current!.getBoundingClientRect(), dir, cands, current) : first(cands)
    if (!next) return false
    focusEl(next)
    return true
}

// Keys the focused element keeps: text fields their Left/Right, sliders
// theirs, menus all arrows.
function keeps(el: Element | null, dir: Dir): boolean {
    if (!el) return false
    if (el.closest('[role="menu"], [role="listbox"]')) return true
    if (el instanceof HTMLTextAreaElement) return true
    if (el instanceof HTMLInputElement) {
        if (el.type === "range") return dir === "left" || dir === "right"
        if (["checkbox", "radio", "button", "submit", "color"].includes(el.type)) return false
        return dir === "left" || dir === "right"
    }
    return false
}

function onKey(e: KeyboardEvent) {
    const dir = DIRS[e.key]
    if (!dir || e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) return
    // The player has its keys (it moves the focus itself, see Player).
    if (document.querySelector("[data-tv-scope]") && !document.querySelector('[role="dialog"], [role="alertdialog"]')) return
    if (keeps(document.activeElement, dir)) return
    // Before the page's own handlers (capture): a menu button doesn't open
    // on Down, the focus moves on.
    e.preventDefault()
    e.stopPropagation()
    move(dir)
}

// A key the page gets as if pressed (Back, the remote's media keys).
export function press(key: string, target: EventTarget = document.activeElement ?? document.body) {
    const code = key === " " ? "Space" : key.length === 1 ? "Key" + key.toUpperCase() : key
    const ev = new KeyboardEvent("keydown", { key, code, bubbles: true, cancelable: true })
    target.dispatchEvent(ev)
    return ev.defaultPrevented
}

// The remote's Menu key (☰) does what a right-click does: it opens the
// focused card's menu. Where there's none, it lists what a mouse would find
// on it (the buttons shown on hover, out of the remote's reach); in the
// player, its menus (subtitles, audio, settings).
export type TvMenu = { items: { label: string; el: HTMLElement }[]; rect: DOMRect; from: HTMLElement | null }
export const tvMenuStore = createStore<TvMenu | null>(null)

function labelOf(el: HTMLElement) {
    return (el.getAttribute("aria-label") || el.title || el.textContent || "").replace(/\s*\([^)]*\)\s*$/, "").trim()
}

export function openMenu() {
    const player = !document.querySelector('[role="dialog"], [role="alertdialog"]') ? document.querySelector<HTMLElement>("[data-tv-scope]") : null
    const from = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : null
    let items: HTMLElement[]
    let rect: DOMRect
    if (player) {
        items = [...player.querySelectorAll<HTMLElement>('[aria-haspopup="menu"]')]
        rect = (player.querySelector("[data-tv-controls]") ?? player).getBoundingClientRect()
    } else {
        if (!from) return
        rect = from.getBoundingClientRect()
        const ev = new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, clientX: rect.left + rect.width / 2, clientY: rect.top + rect.height / 3 })
        // A menu opened (MediaCard's).
        if (!from.dispatchEvent(ev)) return
        items = [...from.querySelectorAll<HTMLElement>('button, a[href], [role="button"]')].filter(b => !b.matches(":disabled") && !b.closest('[aria-hidden="true"]'))
    }
    const list = items.map(el => ({ label: labelOf(el), el })).filter(i => i.label)
    if (list.length) tvMenuStore.set({ items: list, rect, from })
}

// runMenuItem does what pressing it would: a menu button opens its menu.
export function runMenuItem(el: HTMLElement) {
    if (el.getAttribute("aria-haspopup") === "menu") {
        el.focus({ preventScroll: true })
        el.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", code: "Enter", bubbles: true, cancelable: true }))
    } else el.click()
}

// Android updates: the server downloads the APK, the app asks Android to
// install it, once each time Update is pressed.
let installWanted = false

export function wantInstall() {
    installWanted = true
}

export function installIfWanted(st: { state?: string; installFile?: string }) {
    if (!installWanted || st.state !== "installing" || !st.installFile || !android?.installApk) return
    installWanted = false
    android.installApk(st.installFile)
}

let started = false

// initTV turns TV mode on (once).
export function initTV(goBack: () => boolean) {
    if (!tv || started) return
    started = true
    document.documentElement.classList.add("tv")
    applyTvWidth()
    window.addEventListener("keydown", onKey, true)
    // Back: a menu or dialog closes, then the player, then the page goes
    // back. false: nothing left, the app goes to the TV's home.
    window.kumoBack = () => {
        if (document.querySelector('[role="menu"], [role="listbox"], [role="dialog"], [role="alertdialog"], [data-tv-scope]')) {
            press("Escape")
            return true
        }
        return goBack()
    }
    // The remote's media keys, as the player's keys.
    window.kumoKey = (key: string) => {
        if (key === "menu") {
            if (!tvMenuStore.get() && !document.querySelector('[role="menu"]')) openMenu()
            return
        }
        const k = { playpause: " ", rewind: "j", forward: "l" }[key]
        if (k && document.querySelector("[data-tv-scope]")) press(k)
    }
}
