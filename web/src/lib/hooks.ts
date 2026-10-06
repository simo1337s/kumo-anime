import { useEffect, useState } from "react"

// useState that survives reloads (per browser). Storage can be unavailable
// (private windows, blocked site data), so every access is guarded.
export function usePersisted<T>(key: string, initial: T) {
    const [v, setV] = useState<T>(() => {
        try {
            const raw = localStorage.getItem(key)
            return raw ? (JSON.parse(raw) as T) : initial
        } catch {
            return initial
        }
    })
    useEffect(() => {
        try {
            localStorage.setItem(key, JSON.stringify(v))
        } catch {
            /* ignore */
        }
    }, [key, v])
    return [v, setV] as const
}
