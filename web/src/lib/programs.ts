// The programs Kumo uses and, on Windows, the setup that installs them
// (server/internal/winsetup: Scoop, Git, ani-cli, ffmpeg, mpv, yt-dlp… in a
// PowerShell window).
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useEffect } from "react"
import { toast } from "@/lib/toast"
import { api } from "./api"
import type { Status } from "./types"

// The programs that aren't installed, the ones most of Kumo needs first.
export function missingPrograms(status?: Status) {
    const f = status?.features
    if (!f) return []
    const all: [string, boolean][] = [
        ["ffmpeg", f.ffmpeg],
        ["ani-cli", f.aniCli],
        ["mpv", f.mpv],
        ["yt-dlp", f.ytDlp],
    ]
    return all.filter(([, ok]) => !ok).map(([name]) => name)
}

// Whether Kumo can install its programs itself: on Windows, from this
// computer (not from another device on the network).
export function canInstallPrograms(status?: Status) {
    return status?.platform === "windows" && status.client !== "lan"
}

export function useInstallPrograms() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: () => api.post("/api/setup/programs"),
        onSuccess: () => {
            toast.success("Kumo's setup opened in a PowerShell window")
            qc.invalidateQueries({ queryKey: ["status"] })
        },
        onError: (e: any) => toast.error(e.message),
    })
}

// While the setup window is open, checks every few seconds which programs
// are there, so they show up as soon as they're installed.
export function useWatchSetup(status?: Status) {
    const qc = useQueryClient()
    const running = !!status?.setupRunning
    useEffect(() => {
        if (!running) return
        const t = setInterval(() => qc.invalidateQueries({ queryKey: ["status"] }), 4000)
        return () => clearInterval(t)
    }, [running, qc])
}
