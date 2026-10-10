import { useQueryClient } from "@tanstack/react-query"
import { Lock } from "lucide-react"
import { lazy, Suspense, useEffect, useState } from "react"
import { Route, Routes, useLocation, useNavigate } from "react-router-dom"
import { Toaster } from "sonner"
import { NowPlaying, ScanIndicator } from "./components/NowPlaying"
import { PlayerOverlay } from "./components/player/Player"
import { QuickSearch } from "./components/QuickSearch"
import { KumoLogo, Sidebar } from "./components/Sidebar"
import { Button, Input, Spinner, TooltipProvider } from "./components/ui"
import { api, onPasswordRequired } from "./lib/api"
import { connectEvents, setNavigate } from "./lib/events"
import { useStatus } from "./lib/queries"
import { accentPreviewStore, passwordStore, useStore } from "./lib/store"
import { initTV } from "./lib/tv"
import { setTitleLanguage } from "./lib/utils"
import HomePage from "./pages/Home"

const EntryPage = lazy(() => import("./pages/Entry"))
const DiscoverPage = lazy(() => import("./pages/Discover"))
const SearchPage = lazy(() => import("./pages/Search"))
const SchedulePage = lazy(() => import("./pages/Schedule"))
const ListsPage = lazy(() => import("./pages/Lists"))
const TorrentsPage = lazy(() => import("./pages/Torrents"))
const DownloadsPage = lazy(() => import("./pages/Downloads"))
const AutoDownloaderPage = lazy(() => import("./pages/AutoDownloader"))
const LibraryPage = lazy(() => import("./pages/Library"))
const LocalLibraryPage = lazy(() => import("./pages/LocalLibrary"))
const AuthCallbackPage = lazy(() => import("./pages/AuthCallback"))
const ExtensionsPage = lazy(() => import("./pages/Extensions"))
const SettingsPage = lazy(() => import("./pages/Settings"))
const MangaPage = lazy(() => import("./pages/Manga"))
const MangaEntryPage = lazy(() => import("./pages/MangaEntry"))
const MangaReaderPage = lazy(() => import("./pages/MangaReader"))
const WebviewPage = lazy(() => import("./pages/Webview"))

export default function App() {
    const qc = useQueryClient()
    const navigate = useNavigate()
    const location = useLocation()
    const { data: status } = useStatus()
    const needsPassword = useStore(passwordStore)

    useEffect(() => connectEvents(qc), [qc])
    useEffect(() => setNavigate(navigate), [navigate])
    // The remote's Back (TV mode): the page before, then Home, then out.
    useEffect(
        () =>
            initTV(() => {
                if (((window.history.state as { idx?: number } | null)?.idx ?? 0) > 0) navigate(-1)
                else if (window.location.pathname !== "/") navigate("/", { replace: true })
                else return false
                return true
            }),
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [],
    )
    useEffect(() => {
        const off = onPasswordRequired(() => passwordStore.set(true))
        return () => {
            off()
        }
    }, [])

    // Apply UI preferences. Settings previews an accent color before it is
    // saved; when that preview ends (Discard, leaving the page) the saved one
    // comes back.
    const ui = status?.settings.ui
    const accentPreview = useStore(accentPreviewStore)
    useEffect(() => {
        if (!ui) return
        document.documentElement.style.setProperty("--brand", accentPreview || ui.accentColor || "#7c6cf2")
        document.documentElement.classList.toggle("reduce-motion", !!ui.reducedMotion)
    }, [ui, accentPreview])

    // Anime titles in the language chosen in Settings, set before the pages
    // render: they're drawn again when it changes (keyed by it).
    const titleLanguage = ui?.titleLanguage === "romaji" ? "romaji" : "english"
    setTitleLanguage(titleLanguage)

    // Tell plugins where the user is (ctx.screen.onNavigate).
    useEffect(() => {
        const sp = Object.fromEntries(new URLSearchParams(location.search))
        api.post("/api/plugins/navigate", { pathname: location.pathname, searchParams: sp }).catch(() => {})
    }, [location.pathname, location.search])

    if (needsPassword) return <PasswordGate />

    return (
        <TooltipProvider>
            <div className="flex h-full bg-frame">
                <Sidebar />
                {/* The pages: a panel inset in the window, next to the sidebar. */}
                <div className="min-w-0 flex-1 py-2 pr-2">
                    <main id="main-scroll" className="relative h-full overflow-x-hidden overflow-y-auto rounded-xl border border-line bg-bg">
                        <Suspense
                            fallback={
                                <div className="grid h-full place-items-center">
                                    <Spinner className="size-7" />
                                </div>
                            }
                        >
                            <Routes key={titleLanguage}>
                                <Route path="/" element={<HomePage />} />
                                <Route path="/entry" element={<EntryPage />} />
                                <Route path="/discover" element={<DiscoverPage />} />
                                <Route path="/search" element={<SearchPage />} />
                                <Route path="/schedule" element={<SchedulePage />} />
                                <Route path="/lists" element={<ListsPage />} />
                                <Route path="/torrent-list" element={<TorrentsPage />} />
                                <Route path="/downloads" element={<DownloadsPage />} />
                                <Route path="/auto-downloader" element={<AutoDownloaderPage />} />
                                <Route path="/library" element={<LibraryPage />} />
                                <Route path="/local" element={<LocalLibraryPage />} />
                                <Route path="/auth/callback" element={<AuthCallbackPage />} />
                                <Route path="/extensions" element={<ExtensionsPage />} />
                                <Route path="/settings" element={<SettingsPage />} />
                                <Route path="/manga" element={<MangaPage />} />
                                <Route path="/manga/entry" element={<MangaEntryPage />} />
                                <Route path="/manga/read" element={<MangaReaderPage />} />
                                <Route path="/webview" element={<WebviewPage />} />
                                <Route path="*" element={<HomePage />} />
                            </Routes>
                        </Suspense>
                    </main>
                </div>
            </div>
            <NowPlaying />
            <ScanIndicator />
            <QuickSearch />
            <PlayerOverlay />
            <Toaster
                theme="dark"
                position="top-right"
                richColors
                closeButton
                toastOptions={{ style: { background: "var(--surface-2)", border: "1px solid var(--line-strong)", borderRadius: 10 } }}
            />
        </TooltipProvider>
    )
}

function PasswordGate() {
    const [pw, setPw] = useState("")
    const [busy, setBusy] = useState(false)
    const [err, setErr] = useState("")
    const qc = useQueryClient()
    const submit = async () => {
        setBusy(true)
        setErr("")
        try {
            await api.post("/api/auth/server-login", { password: pw })
            passwordStore.set(false)
            qc.invalidateQueries()
        } catch (e: any) {
            setErr(e.message)
        } finally {
            setBusy(false)
        }
    }
    return (
        <div className="grid h-full place-items-center bg-bg p-6">
            <div className="card w-full max-w-sm p-8 text-center rise-in">
                <KumoLogo className="mx-auto size-11" />
                <h1 className="mt-4 text-xl font-semibold">Kumo</h1>
                <p className="mt-1 text-sm text-muted">This server is password protected.</p>
                <div className="mt-6 flex flex-col gap-3">
                    <Input type="password" autoFocus icon={<Lock className="size-4" />} placeholder="Password" value={pw} onChange={e => setPw(e.target.value)} onKeyDown={e => e.key === "Enter" && submit()} />
                    {err && <p className="text-sm text-rose-300">{err}</p>}
                    <Button variant="primary" loading={busy} onClick={submit}>
                        Unlock
                    </Button>
                </div>
            </div>
        </div>
    )
}
