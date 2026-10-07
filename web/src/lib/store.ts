import { useSyncExternalStore } from "react"
import type { PlaybackSession, PluginState } from "./types"

export type Store<T> = {
    get: () => T
    set: (next: T | ((prev: T) => T)) => void
    subscribe: (fn: () => void) => () => void
}

export function createStore<T>(initial: T): Store<T> {
    let state = initial
    const listeners = new Set<() => void>()
    return {
        get: () => state,
        set: next => {
            state = typeof next === "function" ? (next as (p: T) => T)(state) : next
            listeners.forEach(l => l())
        },
        subscribe: fn => {
            listeners.add(fn)
            return () => listeners.delete(fn)
        },
    }
}

export function useStore<T, S = T>(store: Store<T>, selector: (s: T) => S = s => s as unknown as S): S {
    return useSyncExternalStore(store.subscribe, () => selector(store.get()))
}

// ---------------------------------------------------------------------------
// App-wide stores

export const playbackStore = createStore<PlaybackSession | null>(null)

export type ScanState = { running: boolean; stage: string; done: number; total: number; message: string }
export const scanStore = createStore<ScanState>({ running: false, stage: "", done: 0, total: 0, message: "" })

export const torrentCountStore = createStore<number>(0)

export const pluginStore = createStore<Record<string, PluginState>>({})

// Requests from plugins to open a tray (tray.open()).
export const trayOpenStore = createStore<{ pluginId: string; trayId: string } | null>(null)

// In-app player
export type PlayerRequest =
    | { kind: "local"; path: string; mediaId: number; episode: number; start?: number }
    | { kind: "stream"; provider: string; mediaId: number; episode: number; dub: boolean; server?: string }

export const playerStore = createStore<PlayerRequest | null>(null)

export const passwordStore = createStore<boolean>(false)

export const searchOpenStore = createStore<boolean>(false)

// Accent color picked in Settings but not saved yet (null: use the saved one).
export const accentPreviewStore = createStore<string | null>(null)

// Notifications muted with the bell in the sidebar (lib/toast.ts): only
// errors show. Remembered by this browser.
const MUTED_KEY = "kumo-notifications-muted"
export const notificationsMutedStore = createStore<boolean>(
    (() => {
        try {
            return localStorage.getItem(MUTED_KEY) === "1"
        } catch {
            return false
        }
    })(),
)
notificationsMutedStore.subscribe(() => {
    try {
        localStorage.setItem(MUTED_KEY, notificationsMutedStore.get() ? "1" : "0")
    } catch {
        /* storage unavailable: muted until reload */
    }
})
