import { toast } from "sonner"
import { api } from "./api"
import { useStatus } from "./queries"
import { playerStore } from "./store"

// Central place deciding between mpv and the in-app player.
export function usePlay() {
    const { data: status } = useStatus()
    const settings = status?.settings
    const remote = status?.client === "lan" // LAN devices can't use this PC's mpv

    const localPlayer = (override?: "mpv" | "builtin") => (remote ? "builtin" : override ?? settings?.playback.defaultPlayer ?? "mpv")
    const streamPlayer = (override?: "mpv" | "builtin") => (remote ? "builtin" : override ?? settings?.aniCli.player ?? "mpv")

    const playLocal = async (path: string, mediaId: number, episode: number, opts: { player?: "mpv" | "builtin"; start?: number } = {}) => {
        if (localPlayer(opts.player) === "builtin") {
            playerStore.set({ kind: "local", path, mediaId, episode, start: opts.start })
            return
        }
        const t = toast.loading("Opening mpv…")
        try {
            await api.post("/api/playback/local", { path, mediaId, episode, player: "mpv", start: opts.start })
            toast.success("Playing in mpv", { id: t })
        } catch (e: any) {
            toast.error(e.message, { id: t })
        }
    }

    const playStream = async (provider: string, mediaId: number, episode: number, dub: boolean, opts: { player?: "mpv" | "builtin"; server?: string } = {}) => {
        if (streamPlayer(opts.player) === "builtin") {
            playerStore.set({ kind: "stream", provider, mediaId, episode, dub, server: opts.server })
            return
        }
        const t = toast.loading(provider === "ani-cli" ? "Asking ani-cli for the stream…" : "Resolving stream…")
        try {
            await api.post("/api/onlinestream/play", { provider, mediaId, episode, dub, server: opts.server })
            toast.success("Playing in mpv", { id: t })
        } catch (e: any) {
            toast.error(e.message, { id: t })
        }
    }

    return { playLocal, playStream, localPlayer, streamPlayer, remote }
}
