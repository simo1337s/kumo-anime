import { toast as sonner } from "sonner"
import { notificationsMutedStore } from "./store"

type Data = { id?: string | number } | undefined

let skipped = 0

// While notifications are muted (the bell in the sidebar) a toast doesn't
// show; errors still do. A toast updating one already on screen (by its id,
// like "Syncing…" then "Synced") takes it away instead of leaving it. The
// id returned matches no toast, so updating it shows nothing either.
function quiet<A extends [unknown, Data?], R>(show: (...args: A) => R) {
    return (...args: A): R | string => {
        if (!notificationsMutedStore.get()) return show(...args)
        const id = args[1]?.id
        if (id !== undefined) sonner.dismiss(id)
        return `muted-${++skipped}`
    }
}

// The app's toasts: sonner's, minus what muting silences.
export const toast = Object.assign(quiet(sonner), sonner, {
    success: quiet(sonner.success),
    info: quiet(sonner.info),
    warning: quiet(sonner.warning),
    message: quiet(sonner.message),
    loading: quiet(sonner.loading),
}) as typeof sonner
