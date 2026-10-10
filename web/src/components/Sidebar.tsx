import {
    Bell,
    BellOff,
    BookOpen,
    CalendarDays,
    Compass,
    Download,
    FolderSearch,
    HardDrive,
    Home,
    ListChecks,
    LogIn,
    LogOut,
    Magnet,
    Puzzle,
    Rss,
    Search,
    Settings as SettingsIcon,
    UserRound,
} from "lucide-react"
import { useState } from "react"
import { NavLink, useLocation, useNavigate } from "react-router-dom"
import { api } from "@/lib/api"
import { useMediaQuery } from "@/lib/hooks"
import { useCollection, useDownloads, useStatus } from "@/lib/queries"
import { notificationsMutedStore, searchOpenStore, torrentCountStore, useStore } from "@/lib/store"
import { tv } from "@/lib/tv"
import { cn, img } from "@/lib/utils"
import { LoginDialog } from "./LoginDialog"
import { PluginTrays } from "./plugins/PluginTrays"
import { Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger, Tooltip } from "./ui"
import { useQueryClient } from "@tanstack/react-query"
import { toast as sonner } from "sonner"
import { toast } from "@/lib/toast"

// Kumo's logo: a rounded k on an off-white tile. The same drawing is the
// app icon (packaging/icons/kumo.svg) and the favicon (public/kumo.svg).
export function KumoLogo({ className }: { className?: string }) {
    return (
        <svg viewBox="0 0 64 64" className={className} aria-hidden>
            <path d="M59.86 59.86C59.27 60.45 58.63 60.95 57.87 61.4C57.12 61.85 56.31 62.23 55.33 62.56C54.36 62.88 53.35 63.15 52.01 63.36C50.66 63.58 50.57 63.74 47.24 63.84C43.9 63.95 37.08 64 32 64C26.92 64 20.1 63.95 16.76 63.84C13.43 63.74 13.34 63.58 11.99 63.36C10.65 63.15 9.64 62.88 8.67 62.56C7.69 62.23 6.88 61.85 6.13 61.4C5.37 60.95 4.73 60.45 4.14 59.86C3.55 59.27 3.05 58.63 2.6 57.87C2.15 57.12 1.77 56.31 1.44 55.33C1.12 54.36 0.85 53.35 0.64 52.01C0.42 50.66 0.26 50.57 0.16 47.24C0.05 43.9 0 37.08 0 32C0 26.92 0.05 20.1 0.16 16.76C0.26 13.43 0.42 13.34 0.64 11.99C0.85 10.65 1.12 9.64 1.44 8.67C1.77 7.69 2.15 6.88 2.6 6.13C3.05 5.37 3.55 4.73 4.14 4.14C4.73 3.55 5.37 3.05 6.13 2.6C6.88 2.15 7.69 1.77 8.67 1.44C9.64 1.12 10.65 0.85 11.99 0.64C13.34 0.42 13.43 0.26 16.76 0.16C20.1 0.05 26.92 0 32 0C37.08 0 43.9 0.05 47.24 0.16C50.57 0.26 50.66 0.42 52.01 0.64C53.35 0.85 54.36 1.12 55.33 1.44C56.31 1.77 57.12 2.15 57.87 2.6C58.63 3.05 59.27 3.55 59.86 4.14C60.45 4.73 60.95 5.37 61.4 6.13C61.85 6.88 62.23 7.69 62.56 8.67C62.88 9.64 63.15 10.65 63.36 11.99C63.58 13.34 63.74 13.43 63.84 16.76C63.95 20.1 64 26.92 64 32C64 37.08 63.95 43.9 63.84 47.24C63.74 50.57 63.58 50.66 63.36 52.01C63.15 53.35 62.88 54.36 62.56 55.33C62.23 56.31 61.85 57.12 61.4 57.87C60.95 58.63 60.45 59.27 59.86 59.86Z" fill="#e2e2e6" />
            <g fill="none" stroke="#141416" strokeWidth="8" strokeLinecap="round" strokeLinejoin="round">
                <path d="M24.5 15.5V48.5M41 28 24.5 42M31.5 36.2 41.5 48.5" />
            </g>
        </svg>
    )
}

// "kumo", drawn to match the logo's k; in the text color.
export function KumoWordmark({ className }: { className?: string }) {
    return (
        <svg viewBox="0 0 116 40" className={className} aria-hidden>
            <g fill="none" stroke="currentColor" strokeWidth="6.5" strokeLinecap="round" strokeLinejoin="round">
                <path d="M3.5 3.5V36M18 14 3.5 26.5M9.5 21.3 18.5 36M27 14v13a9 9 0 0 0 18 0V14m0 0v22M54.5 36V14m0 7a7 7 0 0 1 14 0v15m0-15a7 7 0 0 1 14 0v15" />
                <circle cx="101" cy="25" r="11" />
            </g>
        </svg>
    )
}

type Item = { to: string; label: string; icon: React.ReactNode; badge?: number; hint?: string; match?: (p: string) => boolean }

// Labels from 1024px wide; narrower, only the icons, named by tooltips.
const WIDE = "(min-width: 1024px)"

export function Sidebar() {
    const { data: status } = useStatus()
    const { data: coll } = useCollection()
    const { data: downloads } = useDownloads()
    const torrentCount = useStore(torrentCountStore)
    const [loginOpen, setLoginOpen] = useState(false)
    const location = useLocation()
    const navigate = useNavigate()
    const qc = useQueryClient()
    const wide = useMediaQuery(WIDE)
    const settings = status?.settings

    const activeDownloads = (downloads ?? []).filter(d => ["queued", "resolving", "downloading"].includes(d.status)).length

    const groups: Item[][] = [
        [
            { to: "/", label: "Home", icon: <Home />, match: p => p === "/" },
            { to: "/local", label: "Local library", icon: <HardDrive /> },
            { to: "/schedule", label: "Schedule", icon: <CalendarDays /> },
            ...(settings?.manga.enabled !== false ? [{ to: "/manga", label: "Manga", icon: <BookOpen />, match: (p: string) => p.startsWith("/manga") }] : []),
            { to: "/lists", label: "My lists", icon: <ListChecks /> },
        ],
        [
            { to: "/discover", label: "Discover", icon: <Compass /> },
            { to: "/search", label: "Search", icon: <Search />, hint: "Ctrl K" },
        ],
        [
            { to: "/torrent-list", label: "Torrents", icon: <Magnet />, badge: settings?.torrent.showActiveCount ? torrentCount : 0 },
            { to: "/downloads", label: "Downloads", icon: <Download />, badge: activeDownloads },
            { to: "/auto-downloader", label: "Auto downloader", icon: <Rss /> },
        ],
        [
            { to: "/library", label: "Library tools", icon: <FolderSearch />, badge: coll?.unmatchedCount ?? 0 },
            { to: "/extensions", label: "Extensions", icon: <Puzzle /> },
        ],
    ]

    const logout = async () => {
        await api.post("/api/auth/logout")
        qc.invalidateQueries()
        toast.success("Logged out of AniList")
    }

    const link = (it: Item) => {
        const active = it.match ? it.match(location.pathname) : location.pathname.startsWith(it.to)
        const badge = it.badge && it.badge > 0 ? (it.badge > 99 ? "99+" : String(it.badge)) : ""
        const el = (
            <NavLink
                key={it.to}
                to={it.to}
                onClick={e => {
                    if (it.to === "/search" && !e.metaKey && !e.ctrlKey) {
                        e.preventDefault()
                        searchOpenStore.set(true)
                    }
                }}
                className={cn(
                    "focus-ring relative flex h-9 shrink-0 items-center rounded-md text-[13.5px] font-medium transition-colors [&>svg]:size-[18px] [&>svg]:shrink-0 [&>svg]:stroke-[1.75]",
                    wide ? "gap-3 px-2.5" : "w-10 justify-center",
                    active ? "bg-white/[0.08] text-fg" : "text-muted hover:bg-white/[0.04] hover:text-fg",
                )}
            >
                {it.icon}
                {wide && <span className="truncate">{it.label}</span>}
                {wide && it.hint && !badge && !tv && <kbd className="ml-auto font-sans text-[11px] text-subtle">{it.hint}</kbd>}
                {badge &&
                    (wide ? (
                        <span className="ml-auto rounded bg-white/[0.08] px-1.5 text-[11px] leading-[18px] text-fg/80 tabular-nums">{badge}</span>
                    ) : (
                        <span className="absolute top-0.5 right-0.5 grid h-4 min-w-4 place-items-center rounded-full bg-brand px-1 text-[10px] font-semibold text-white tabular-nums">{badge}</span>
                    ))}
            </NavLink>
        )
        return wide ? (
            el
        ) : (
            <Tooltip key={it.to} content={it.label} side="right">
                {el}
            </Tooltip>
        )
    }

    return (
        <aside className={cn("relative z-40 flex h-full shrink-0 flex-col py-3", wide ? "w-56 px-3" : "w-16 items-center px-2")}>
            {/* Home is the first link below: the remote skips this one. */}
            <button onClick={() => navigate("/")} data-tv-skip-nav className={cn("focus-ring mb-4 flex h-10 items-center gap-2.5 rounded-md", wide ? "px-2" : "justify-center")} aria-label="Kumo home">
                <KumoLogo className="size-7" />
                {wide && <KumoWordmark className="h-[18px] w-auto text-fg" />}
            </button>

            <nav className={cn("no-scrollbar flex flex-1 flex-col overflow-y-auto", wide ? "gap-4" : "items-center gap-3")}>
                {groups.map((g, i) => (
                    <div key={i} className={cn("flex flex-col gap-0.5", !wide && "items-center", !wide && i > 0 && "border-t border-line pt-3")}>
                        {g.map(link)}
                    </div>
                ))}
            </nav>

            <div className={cn("flex flex-col gap-0.5 pt-3", !wide && "items-center")}>
                <PluginTrays />
                <div className={cn("flex gap-0.5", wide ? "items-center" : "flex-col-reverse items-center")}>
                    <div className={wide ? "min-w-0 flex-1" : undefined}>{link({ to: "/settings", label: "Settings", icon: <SettingsIcon /> })}</div>
                    <MuteButton side={wide ? "top" : "right"} />
                </div>
                <Dropdown>
                    <DropdownTrigger asChild>
                        <button
                            className={cn(
                                "focus-ring mt-1 flex h-10 items-center gap-2.5 rounded-md text-left transition-colors hover:bg-white/[0.04] data-[state=open]:bg-white/[0.06]",
                                wide ? "px-2" : "w-10 justify-center",
                            )}
                            aria-label="Account"
                        >
                            {status?.user?.avatar?.large ? (
                                <img src={img(status.user.avatar.large)} alt="" className="size-7 shrink-0 rounded-full object-cover" />
                            ) : (
                                <span className="grid size-7 shrink-0 place-items-center rounded-full bg-surface-3 text-muted">
                                    <UserRound className="size-4" />
                                </span>
                            )}
                            {wide && (
                                <span className="min-w-0 flex-1">
                                    <span className="block truncate text-[13px] font-medium">{status?.user ? status.user.name : "Not logged in"}</span>
                                    <span className="block truncate text-[11px] text-subtle">{status?.user ? "AniList" : "Log in to sync"}</span>
                                </span>
                            )}
                        </button>
                    </DropdownTrigger>
                    <DropdownContent align="start" side="top" className="w-52">
                        <DropdownLabel>{status?.user ? status.user.name : "Not logged in"}</DropdownLabel>
                        {status?.user ? (
                            <>
                                <DropdownItem icon={<UserRound />} onSelect={() => window.open(`https://anilist.co/user/${status.user!.name}`, "_blank", "noopener")}>
                                    AniList profile
                                </DropdownItem>
                                <DropdownSeparator />
                                <DropdownItem icon={<LogOut />} danger onSelect={logout}>
                                    Log out
                                </DropdownItem>
                            </>
                        ) : (
                            <DropdownItem icon={<LogIn />} onSelect={() => setLoginOpen(true)}>
                                Log in with AniList
                            </DropdownItem>
                        )}
                    </DropdownContent>
                </Dropdown>
            </div>
            <LoginDialog open={loginOpen} onOpenChange={setLoginOpen} />
        </aside>
    )
}

// Mutes notifications (toasts), all but errors (lib/toast.ts), or turns them
// back on.
function MuteButton({ side }: { side: "top" | "right" }) {
    const muted = useStore(notificationsMutedStore)
    const label = muted ? "Unmute notifications" : "Mute notifications"
    const toggle = () => {
        if (!muted) sonner.dismiss() // what's on screen goes too
        notificationsMutedStore.set(!muted)
        // Shown past the mute: it says what just happened.
        sonner.message(muted ? "Notifications on" : "Notifications muted. Errors still show.", { duration: 2500 })
    }
    return (
        <Tooltip content={label} side={side}>
            <button
                onClick={toggle}
                aria-label={label}
                aria-pressed={muted}
                className={cn(
                    "focus-ring grid size-9 shrink-0 place-items-center rounded-md transition-colors hover:bg-white/[0.04] [&>svg]:size-[18px] [&>svg]:stroke-[1.75]",
                    muted ? "text-amber-300" : "text-muted hover:text-fg",
                )}
            >
                {muted ? <BellOff /> : <Bell />}
            </button>
        </Tooltip>
    )
}
