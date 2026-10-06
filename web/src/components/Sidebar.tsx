import {
    BookOpen,
    CalendarDays,
    Compass,
    Download,
    FolderSearch,
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
            <defs>
                <linearGradient id="kumo-g" x1="0" y1="0" x2="1" y2="1">
                    <stop offset="0" stopColor="color-mix(in oklab, var(--brand) 70%, white)" />
                    <stop offset="1" stopColor="var(--brand)" />
                </linearGradient>
            </defs>
            <rect width="64" height="64" rx="18" fill="url(#kumo-g)" />
            <path d="M19 43h26a9.5 9.5 0 0 0 1.5-18.9A12.5 12.5 0 0 0 22.4 27 8 8 0 0 0 19 43z" fill="#fff" />
        </svg>
    )
}

type Item = { to: string; label: string; icon: React.ReactNode; badge?: number; match?: (p: string) => boolean }

export function Sidebar() {
    const { data: status } = useStatus()
    const { data: coll } = useCollection()
    const { data: downloads } = useDownloads()
    const torrentCount = useStore(torrentCountStore)
    const [loginOpen, setLoginOpen] = useState(false)
    const location = useLocation()
    const navigate = useNavigate()
    const qc = useQueryClient()
    const settings = status?.settings

    const activeDownloads = (downloads ?? []).filter(d => ["queued", "resolving", "downloading"].includes(d.status)).length

    const items: Item[] = [
        { to: "/", label: "Home", icon: <Home />, match: p => p === "/" },
        { to: "/schedule", label: "Schedule", icon: <CalendarDays /> },
        ...(settings?.manga.enabled !== false ? [{ to: "/manga", label: "Manga", icon: <BookOpen />, match: (p: string) => p.startsWith("/manga") }] : []),
        { to: "/lists", label: "My lists", icon: <ListChecks /> },
        { to: "/discover", label: "Discover", icon: <Compass /> },
        { to: "/search", label: "Search", icon: <Search /> },
        { to: "/torrent-list", label: "Torrents", icon: <Magnet />, badge: settings?.torrent.showActiveCount ? torrentCount : 0 },
        { to: "/downloads", label: "Downloads", icon: <Download />, badge: activeDownloads },
        { to: "/auto-downloader", label: "Auto downloader", icon: <Rss /> },
        { to: "/library", label: "Library tools", icon: <FolderSearch />, badge: coll?.unmatchedCount ?? 0 },
        { to: "/extensions", label: "Extensions", icon: <Puzzle /> },
    ]

    const logout = async () => {
        await api.post("/api/auth/logout")
        qc.invalidateQueries()
        toast.success("Logged out of AniList")
    }

    return (
        <aside className="relative z-40 flex h-full w-[72px] shrink-0 flex-col items-center border-r border-line bg-surface-1/80 py-4 backdrop-blur-xl">
            <button onClick={() => navigate("/")} className="focus-ring mb-5 rounded-2xl transition-transform hover:scale-105" aria-label="Kumo home">
                <KumoLogo className="size-11 drop-shadow-[0_8px_20px_color-mix(in_oklab,var(--brand)_45%,transparent)]" />
            </button>

            <nav className="no-scrollbar flex w-full flex-1 flex-col items-center gap-1 overflow-y-auto px-3">
                {items.map(it => {
                    const active = it.match ? it.match(location.pathname) : location.pathname.startsWith(it.to)
                    return (
                        <Tooltip key={it.to} content={it.label} side="right">
                            <NavLink
                                to={it.to}
                                onClick={e => {
                                    if (it.to === "/search" && !e.metaKey && !e.ctrlKey) {
                                        e.preventDefault()
                                        searchOpenStore.set(true)
                                    }
                                }}
                                className={cn(
                                    "focus-ring group relative grid size-11 place-items-center rounded-xl transition-all duration-200 [&>svg]:size-[21px] [&>svg]:stroke-[1.75]",
                                    active ? "bg-brand-soft text-fg" : "text-muted hover:bg-white/[0.06] hover:text-fg",
                                )}
                            >
                                {active && <span className="absolute top-1/2 -left-3 h-5 w-1 -translate-y-1/2 rounded-r-full bg-brand" />}
                                {it.icon}
                                {!!it.badge && it.badge > 0 && (
                                    <span className="absolute -top-0.5 -right-0.5 grid h-[18px] min-w-[18px] place-items-center rounded-full bg-brand px-1 text-[10px] font-bold text-white ring-2 ring-surface-1">
                                        {it.badge > 99 ? "99+" : it.badge}
                                    </span>
                                )}
                            </NavLink>
                        </Tooltip>
                    )
                })}
            </nav>

            <div className="flex flex-col items-center gap-1.5 px-3 pt-3">
                <PluginTrays />
                <Tooltip content="Settings" side="right">
                    <NavLink
                        to="/settings"
                        className={({ isActive }) =>
                            cn(
                                "focus-ring grid size-11 place-items-center rounded-xl transition-all [&>svg]:size-[21px] [&>svg]:stroke-[1.75]",
                                isActive ? "bg-brand-soft text-fg" : "text-muted hover:bg-white/[0.06] hover:text-fg",
                            )
                        }
                    >
                        <SettingsIcon />
                    </NavLink>
                </Tooltip>
                <Dropdown>
                    <DropdownTrigger asChild>
                        <button className="focus-ring mt-1 size-10 overflow-hidden rounded-full ring-2 ring-line-strong transition hover:ring-brand/60">
                            {status?.user?.avatar?.large ? (
                                <img src={img(status.user.avatar.large)} alt="" className="size-full object-cover" />
                            ) : (
                                <span className="grid size-full place-items-center bg-surface-3 text-muted">
                                    <UserRound className="size-5" />
                                </span>
                            )}
                        </button>
                    </DropdownTrigger>
                    <DropdownContent align="start" className="ml-2">
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
