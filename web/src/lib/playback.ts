// How the in-app player gets local files that need converting.
//
// Chromium (the desktop app, Chrome, Brave, Edge) and Firefox play the
// converted file as one continuous stream. Safari, and every browser on
// iPhone and iPad (they're all Safari underneath), only play video they can
// fetch in byte ranges, so they get it as HLS: a playlist of short segments.

const ua = typeof navigator === "undefined" ? "" : navigator.userAgent

// iPadOS asks for desktop sites, so it says it's a Mac; touch gives it away.
const appleMobile = /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && typeof navigator !== "undefined" && navigator.maxTouchPoints > 1)
const chromium = /Chrome\/|Chromium\/|Edg\/|OPR\/|Electron\//.test(ua) && !appleMobile
const safari = appleMobile || (/AppleWebKit\//.test(ua) && /Safari\//.test(ua) && !chromium)

// localStorage["kumo-local-stream"] = "hls" or "progressive" forces one way,
// for trying the other one out.
function override(): string {
    try {
        return localStorage.getItem("kumo-local-stream") ?? ""
    } catch {
        return ""
    }
}

export function prefersHls() {
    const o = override()
    if (o === "hls") return true
    if (o === "progressive") return false
    return safari
}

// The video formats this browser decodes, for the server to copy instead of
// converting them (HEVC on Macs and iPhones, for example). "mse" checks
// what hls.js can feed the browser, "element" what a <video> plays itself.
export function videoCaps(via: "mse" | "element"): string[] {
    const MS: any = typeof window === "undefined" ? undefined : ((window as any).ManagedMediaSource ?? window.MediaSource)
    const el = typeof document === "undefined" ? null : document.createElement("video")
    const can = (codecs: string) => {
        const type = `video/mp4; codecs="${codecs}"`
        try {
            return via === "mse" ? !!MS?.isTypeSupported?.(type) : !!el && el.canPlayType(type) !== ""
        } catch {
            return false
        }
    }
    const caps = ["h264"]
    const tests: [string, string][] = [
        ["h264-10", "avc1.6E0028"],
        ["hevc", "hvc1.1.6.L120.90"],
        ["hevc-10", "hvc1.2.4.L120.90"],
        ["av1", "av01.0.08M.08"],
        ["av1-10", "av01.0.08M.10"],
        ["vp9", "vp09.00.40.08"],
        ["vp9-10", "vp09.02.40.10"],
    ]
    for (const [cap, codecs] of tests) if (can(codecs)) caps.push(cap)
    // Chromium plays Matroska (.mkv) files as they are; Safari doesn't.
    if (chromium && via === "element") caps.push("mkv")
    return caps
}

// A random id; crypto.randomUUID only exists on https and localhost, and
// devices on the home network use plain http.
export function randomId() {
    try {
        if (typeof crypto !== "undefined" && crypto.getRandomValues) {
            return Array.from(crypto.getRandomValues(new Uint8Array(12)), b => b.toString(16).padStart(2, "0")).join("")
        }
    } catch {
        /* fall through */
    }
    return Math.random().toString(36).slice(2) + Date.now().toString(36)
}
