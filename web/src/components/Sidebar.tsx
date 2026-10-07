import {
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
import { searchOpenStore, torrentCountStore, useStore } from "@/lib/store"
import { cn, img } from "@/lib/utils"
import { LoginDialog } from "./LoginDialog"
import { PluginTrays } from "./plugins/PluginTrays"
import { Dropdown, DropdownContent, DropdownItem, DropdownLabel, DropdownSeparator, DropdownTrigger, Tooltip } from "./ui"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

export function KumoLogo({ className }: { className?: string }) {
    return (
        <svg viewBox="0 0 64 64" className={className} aria-hidden>
            <rect width="64" height="64" rx="16" fill="var(--brand)" />
            <path d="M19 43h26a9.5 9.5 0 0 0 1.5-18.9A12.5 12.5 0 0 0 22.4 27 8 8 0 0 0 19 43z" fill="#fff" />
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
                {wide && it.hint && !badge && <kbd className="ml-auto font-sans text-[11px] text-subtle">{it.hint}</kbd>}
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
            <button onClick={() => navigate("/")} className={cn("focus-ring mb-4 flex h-10 items-center gap-2.5 rounded-md", wide ? "px-2" : "justify-center")} aria-label="Kumo home">
                <KumoLogo className="size-7" />
                {wide && <span className="text-[15px] font-semibold tracking-tight">Kumo</span>}
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
                {link({ to: "/settings", label: "Settings", icon: <SettingsIcon /> })}
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
