import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"
import type { Media } from "./types"

export function cn(...inputs: ClassValue[]) {
    return twMerge(clsx(inputs))
}

// The language of anime titles (Settings › Interface): English where AniList
// has an English title, or Japanese in Latin letters (romaji). AniList's own
// "preferred" title is romaji unless the account says otherwise.
let romajiTitles = false

export function setTitleLanguage(lang?: string) {
    romajiTitles = lang === "romaji"
}

export function title(m?: Media | null) {
    if (!m) return ""
    const t = m.title
    return (romajiTitles ? t.romaji || t.userPreferred || t.english : t.english || t.romaji || t.userPreferred) || t.native || ""
}

// The title in the other language, when it's another title.
export function otherTitle(m?: Media | null) {
    if (!m) return ""
    const other = romajiTitles ? m.title.english : m.title.romaji
    return other && other !== title(m) ? other : ""
}

export function englishTitle(m?: Media | null) {
    if (!m) return ""
    return m.title.english || m.title.userPreferred || m.title.romaji || ""
}

// Artwork is downloaded once by the server and served from its local cache
// (/api/img), so covers keep working offline.
export function img(url?: string | null) {
    if (!url) return ""
    if (url.startsWith("http://") || url.startsWith("https://")) return `/api/img?u=${encodeURIComponent(url)}`
    return url
}

export function cover(m?: Media | null) {
    return img(m?.coverImage?.extraLarge || m?.coverImage?.large || m?.coverImage?.medium || "")
}

export function banner(m?: Media | null) {
    return img(m?.bannerImage || m?.coverImage?.extraLarge || "")
}

export function totalEpisodes(m?: Media | null) {
    if (!m) return 0
    if (m.episodes) return m.episodes
    if (m.nextAiringEpisode) return m.nextAiringEpisode.episode - 1
    return 0
}

export function airedEpisodes(m?: Media | null) {
    if (!m) return 0
    if (m.nextAiringEpisode) return m.nextAiringEpisode.episode - 1
    if (m.status === "NOT_YET_RELEASED") return 0
    return m.episodes || 0
}

const FORMATS: Record<string, string> = {
    TV: "TV",
    TV_SHORT: "TV Short",
    MOVIE: "Movie",
    SPECIAL: "Special",
    OVA: "OVA",
    ONA: "ONA",
    MUSIC: "Music",
    MANGA: "Manga",
    NOVEL: "Light Novel",
    ONE_SHOT: "One Shot",
}
export const formatLabel = (f?: string) => (f ? FORMATS[f] ?? f : "")

const STATUSES: Record<string, string> = {
    FINISHED: "Finished",
    RELEASING: "Airing",
    NOT_YET_RELEASED: "Upcoming",
    CANCELLED: "Cancelled",
    HIATUS: "Hiatus",
}
export const statusLabel = (s?: string) => (s ? STATUSES[s] ?? s : "")

export const LIST_STATUS: Record<string, string> = {
    CURRENT: "Watching",
    REPEATING: "Rewatching",
    PLANNING: "Planning",
    PAUSED: "Paused",
    COMPLETED: "Completed",
    DROPPED: "Dropped",
}
export const LIST_STATUS_MANGA: Record<string, string> = {
    CURRENT: "Reading",
    REPEATING: "Rereading",
    PLANNING: "Planning",
    PAUSED: "Paused",
    COMPLETED: "Completed",
    DROPPED: "Dropped",
}

export const SEASONS = ["WINTER", "SPRING", "SUMMER", "FALL"]
export const GENRES = [
    "Action", "Adventure", "Comedy", "Drama", "Ecchi", "Fantasy", "Horror", "Mahou Shoujo", "Mecha", "Music",
    "Mystery", "Psychological", "Romance", "Sci-Fi", "Slice of Life", "Sports", "Supernatural", "Thriller",
]

export function seasonLabel(s?: string, y?: number | null) {
    if (!s) return y ? String(y) : ""
    return `${s.charAt(0)}${s.slice(1).toLowerCase()}${y ? " " + y : ""}`
}

export function formatDuration(sec: number) {
    if (!isFinite(sec) || sec < 0) sec = 0
    const h = Math.floor(sec / 3600)
    const m = Math.floor((sec % 3600) / 60)
    const s = Math.floor(sec % 60)
    if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`
    return `${m}:${String(s).padStart(2, "0")}`
}

export function formatBytes(b: number) {
    if (!b) return "0 B"
    const u = ["B", "KB", "MB", "GB", "TB"]
    const i = Math.min(Math.floor(Math.log(b) / Math.log(1024)), u.length - 1)
    return `${(b / Math.pow(1024, i)).toFixed(i ? 1 : 0)} ${u[i]}`
}

export function formatSpeed(b: number) {
    return b > 0 ? `${formatBytes(b)}/s` : "—"
}

export function timeUntil(seconds: number) {
    if (seconds <= 0) return "now"
    const d = Math.floor(seconds / 86400)
    const h = Math.floor((seconds % 86400) / 3600)
    const m = Math.floor((seconds % 3600) / 60)
    if (d > 0) return `${d}d ${h}h`
    if (h > 0) return `${h}h ${m}m`
    return `${m}m`
}

export function relativeTime(unix: number) {
    const diff = Date.now() / 1000 - unix
    if (diff < 60) return "just now"
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
    if (diff < 86400 * 30) return `${Math.floor(diff / 86400)}d ago`
    return new Date(unix * 1000).toLocaleDateString()
}

// AniList descriptions contain a little HTML (<br>, <i>, <b>).
export function cleanDescription(html?: string) {
    if (!html) return ""
    return html
        .replace(/<br\s*\/?>(\s*<br\s*\/?>)*/gi, "\n")
        .replace(/<\/?(i|b|em|strong)>/gi, "")
        .replace(/<[^>]+>/g, "")
        .replace(/&quot;/g, '"')
        .replace(/&amp;/g, "&")
        .replace(/&#039;/g, "'")
        .replace(/&lt;/g, "<")
        .replace(/&gt;/g, ">")
        .replace(/\(Source:[^)]*\)/gi, "")
        .trim()
}

export function scoreColor(score?: number | null) {
    if (!score) return "text-muted"
    if (score >= 80) return "text-emerald-400"
    if (score >= 65) return "text-lime-300"
    if (score >= 50) return "text-amber-300"
    return "text-rose-400"
}

export function hexToRgb(hex: string) {
    const m = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex)
    return m ? `${parseInt(m[1], 16)} ${parseInt(m[2], 16)} ${parseInt(m[3], 16)}` : "124 108 242"
}

// Copies text to the clipboard. The Clipboard API only exists on secure pages
// (https, localhost), so plain-http LAN addresses fall back to a hidden
// textarea. Resolves false when nothing worked.
export async function copyText(text: string) {
    try {
        if (navigator.clipboard?.writeText) {
            await navigator.clipboard.writeText(text)
            return true
        }
    } catch {
        /* blocked: try the fallback */
    }
    const prev = document.activeElement as HTMLElement | null
    const ta = document.createElement("textarea")
    ta.value = text
    ta.setAttribute("readonly", "")
    ta.style.position = "fixed"
    ta.style.top = "0"
    ta.style.opacity = "0"
    document.body.appendChild(ta)
    ta.focus({ preventScroll: true })
    ta.select()
    ta.setSelectionRange(0, text.length) // iOS ignores select()
    try {
        return document.execCommand("copy")
    } catch {
        return false
    } finally {
        ta.remove()
        prev?.focus({ preventScroll: true })
    }
}

// "1 file", "2 files".
export const plural = (n: number, one: string, many = one + "s") => `${n} ${n === 1 ? one : many}`

// The value that occurs most often (the first one on a tie), or "".
export function mostCommon(xs: string[]) {
    const counts = new Map<string, number>()
    let best = ""
    for (const x of xs) {
        const n = (counts.get(x) ?? 0) + 1
        counts.set(x, n)
        if (n > (counts.get(best) ?? 0)) best = x
    }
    return best
}

export function entryUrl(m: { id: number; type?: string }) {
    return m.type === "MANGA" ? `/manga/entry?id=${m.id}` : `/entry?id=${m.id}`
}
