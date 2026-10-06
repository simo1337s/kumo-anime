import { useQueryClient } from "@tanstack/react-query"
import { ChevronDown, EyeOff, Folder, FolderOpen, Link2, Link2Off, Search } from "lucide-react"
import { useMemo, useState } from "react"
import { Link } from "react-router-dom"
import { toast } from "sonner"
import { api } from "@/lib/api"
import { useCollection, useLibraryFiles, useStatus } from "@/lib/queries"
import type { LocalFile, Media } from "@/lib/types"
import { cn, cover, formatBytes, title } from "@/lib/utils"
import { Badge, Button, EmptyState, IconButton, Input, Tooltip } from "../ui"
import { MatchDialog, naturalCompare } from "./MatchDialog"

export type LibraryFolder = {
    dir: string
    label: string // path below its library folder
    files: LocalFile[]
    size: number
    matches: { mediaId: number; count: number }[] // most files first
    unmatched: number
    query: string // best guess of the anime title, for searching AniList
}

function buildFolders(files: LocalFile[], roots: string[]): LibraryFolder[] {
    const byDir = new Map<string, LocalFile[]>()
    for (const f of files) {
        if (f.ignored) continue
        const list = byDir.get(f.dir)
        if (list) list.push(f)
        else byDir.set(f.dir, [f])
    }
    const out: LibraryFolder[] = []
    for (const [dir, fs] of byDir) {
        const root = roots.filter(r => dir === r || dir.startsWith(r + "/")).sort((a, b) => b.length - a.length)[0]
        const label = root ? dir.slice(root.length).replace(/^\/+/, "") || dir.split("/").pop() || dir : dir
        const counts = new Map<number, number>()
        let unmatched = 0
        for (const f of fs) {
            if (f.mediaId > 0) counts.set(f.mediaId, (counts.get(f.mediaId) ?? 0) + 1)
            else unmatched++
        }
        fs.sort((a, b) => naturalCompare(a.name, b.name))
        out.push({
            dir,
            label,
            files: fs,
            size: fs.reduce((n, f) => n + f.size, 0),
            matches: [...counts].map(([mediaId, count]) => ({ mediaId, count })).sort((a, b) => b.count - a.count),
            unmatched,
            query: fs.find(f => f.parsed?.folderTitle)?.parsed.folderTitle || fs[0]?.parsed?.title || label,
        })
    }
    return out
}

function words(s: string) {
    return s
        .toLowerCase()
        .replace(/[^\p{L}\p{N}]+/gu, " ")
        .split(" ")
        .filter(w => w.length > 1)
}

// How much a folder looks like an anime title (0-1), to list likely folders first.
function likeness(folder: LibraryFolder, media: Media) {
    const fw = new Set(words(`${folder.label} ${folder.query}`))
    let best = 0
    for (const t of [media.title.romaji, media.title.english, media.title.userPreferred, ...(media.synonyms ?? [])]) {
        const tw = words(t ?? "")
        if (!tw.length) continue
        best = Math.max(best, tw.filter(w => fw.has(w)).length / tw.length)
    }
    return best
}

function useMediaIndex() {
    const { data: coll } = useCollection()
    return useMemo(() => {
        const m = new Map<number, Media>()
        for (const l of coll?.lists ?? []) for (const i of l.items) m.set(i.media.id, i.media)
        for (const i of coll?.localOnly ?? []) m.set(i.media.id, i.media)
        return m
    }, [coll])
}

// Library tools › Folders: every folder with what it's matched to, and
// buttons to match it (or some of its files) to another anime by hand.
export function LibraryFolders() {
    const { data: status } = useStatus()
    const { data: files, isLoading } = useLibraryFiles()
    const media = useMediaIndex()
    const qc = useQueryClient()
    const [q, setQ] = useState("")
    const [matching, setMatching] = useState<{ files: LocalFile[]; query: string } | null>(null)
    const roots = useMemo(() => [status?.settings.library.dir ?? "", ...(status?.settings.library.extraDirs ?? [])].filter(Boolean), [status])
    const trusted = status?.client !== "lan"

    const folders = useMemo(() => {
        const all = buildFolders(files ?? [], roots)
        const needle = q.trim().toLowerCase()
        const shown = needle
            ? all.filter(f => [f.label, f.query, ...f.matches.map(m => title(media.get(m.mediaId)))].some(t => t?.toLowerCase().includes(needle)))
            : all
        // Unmatched and mixed folders first: they're the ones to fix.
        const rank = (f: LibraryFolder) => (f.matches.length === 0 ? 0 : f.matches.length > 1 || f.unmatched > 0 ? 1 : 2)
        return shown.sort((a, b) => rank(a) - rank(b) || naturalCompare(a.label, b.label))
    }, [files, roots, q, media])

    const post = async (path: string, body: object, done: string) => {
        try {
            await api.post(path, body)
            toast.success(done)
            qc.invalidateQueries({ queryKey: ["library"] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["entry"] })
        } catch (e: any) {
            toast.error(e.message)
        }
    }

    if (isLoading) return <div className="card h-40 shimmer" />
    if (!files?.length)
        return (
            <EmptyState icon={<Folder className="size-6" />} title="No files in your library yet">
                Set your library folder in Settings and run a scan.
            </EmptyState>
        )
    return (
        <div className="flex flex-col gap-4">
            <p className="text-sm text-muted">
                Wrong or missing match? Use <b>Match…</b> to pick the right anime. Manual matches are locked, so later scans keep them.
            </p>
            <div className="w-full max-w-md">
                <Input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter folders or anime…" icon={<Search className="size-4" />} />
            </div>
            <div className="flex flex-col gap-2">
                {folders.map(f => (
                    <FolderRow
                        key={f.dir}
                        folder={f}
                        media={media}
                        trusted={trusted}
                        onMatch={fs => setMatching({ files: fs, query: f.query })}
                        onUnmatch={fs => post("/api/library/unmatch", { paths: fs.map(x => x.path) }, `Unmatched ${fs.length} ${fs.length === 1 ? "file" : "files"}`)}
                        onIgnore={fs => post("/api/library/ignore", { paths: fs.map(x => x.path), ignored: true }, `Ignoring ${fs.length} ${fs.length === 1 ? "file" : "files"}`)}
                    />
                ))}
                {folders.length === 0 && <p className="py-10 text-center text-sm text-muted">No folder matches “{q}”.</p>}
            </div>
            <MatchDialog open={!!matching} onOpenChange={v => !v && setMatching(null)} files={matching?.files ?? []} initialQuery={matching?.query} />
        </div>
    )
}

function FolderRow({
    folder: f,
    media,
    trusted,
    onMatch,
    onUnmatch,
    onIgnore,
}: {
    folder: LibraryFolder
    media: Map<number, Media>
    trusted: boolean
    onMatch: (files: LocalFile[]) => void
    onUnmatch: (files: LocalFile[]) => void
    onIgnore: (files: LocalFile[]) => void
}) {
    const [open, setOpen] = useState(false)
    const [sel, setSel] = useState<Set<string>>(new Set())
    const selected = f.files.filter(x => sel.has(x.path))
    const primary = f.matches.length === 1 ? media.get(f.matches[0].mediaId) : undefined

    return (
        <div className="card overflow-hidden">
            <div className="flex flex-wrap items-center gap-3 p-3 pr-4">
                <button onClick={() => setOpen(!open)} className="flex min-w-0 flex-1 items-center gap-3 text-left">
                    <span className="grid size-10 shrink-0 place-items-center rounded-xl bg-white/[0.05] text-muted">
                        <Folder className="size-5" />
                    </span>
                    <span className="min-w-0">
                        <span className="block truncate font-semibold" title={f.dir}>
                            {f.label}
                        </span>
                        <span className="text-xs text-subtle">
                            {f.files.length} {f.files.length === 1 ? "file" : "files"} · {formatBytes(f.size)}
                        </span>
                    </span>
                </button>

                <div className="flex min-w-0 items-center gap-2">
                    {f.matches.length === 0 ? (
                        <Badge tone="amber">Unmatched</Badge>
                    ) : primary || f.matches.length === 1 ? (
                        <Link to={`/entry?id=${f.matches[0].mediaId}`} className="flex min-w-0 items-center gap-2 rounded-lg px-1.5 py-1 hover:bg-white/[0.05]">
                            {primary && <img src={cover(primary)} alt="" className="h-9 w-6 rounded object-cover" />}
                            <span className="max-w-64 truncate text-sm text-brand-strong">{primary ? title(primary) : `AniList #${f.matches[0].mediaId}`}</span>
                        </Link>
                    ) : (
                        <div className="flex flex-wrap gap-1.5">
                            {f.matches.map(m => (
                                <Link key={m.mediaId} to={`/entry?id=${m.mediaId}`}>
                                    <Badge tone="blue">
                                        {title(media.get(m.mediaId)) || `#${m.mediaId}`} · {m.count}
                                    </Badge>
                                </Link>
                            ))}
                        </div>
                    )}
                    {f.matches.length > 0 && f.unmatched > 0 && <Badge tone="amber">{f.unmatched} unmatched</Badge>}
                </div>

                <div className="flex shrink-0 items-center gap-1.5">
                    <Button size="sm" variant={f.matches.length === 1 && f.unmatched === 0 ? "subtle" : "primary"} icon={<Link2 className="size-4" />} onClick={() => onMatch(f.files)}>
                        {f.matches.length === 0 ? "Match…" : "Change match…"}
                    </Button>
                    {f.matches.length > 0 && (
                        <IconButton size="sm" label="Unmatch folder" onClick={() => onUnmatch(f.files)}>
                            <Link2Off className="size-4" />
                        </IconButton>
                    )}
                    <IconButton size="sm" label="Ignore folder (never match or show it)" onClick={() => onIgnore(f.files)}>
                        <EyeOff className="size-4" />
                    </IconButton>
                    {trusted && (
                        <IconButton size="sm" label="Open folder" onClick={() => api.post("/api/open", { path: f.dir }).catch(e => toast.error(e.message))}>
                            <FolderOpen className="size-4" />
                        </IconButton>
                    )}
                    <IconButton size="sm" label={open ? "Hide files" : "Show files"} onClick={() => setOpen(!open)}>
                        <ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} />
                    </IconButton>
                </div>
            </div>

            {open && (
                <div className="border-t border-line bg-surface-1/50">
                    {selected.length > 0 && (
                        <div className="flex items-center gap-2 border-b border-line px-4 py-2">
                            <span className="flex-1 text-xs text-muted">{selected.length} selected</span>
                            <Button size="xs" variant="primary" icon={<Link2 className="size-3.5" />} onClick={() => onMatch(selected)}>
                                Match selected…
                            </Button>
                            <Button size="xs" icon={<Link2Off className="size-3.5" />} onClick={() => onUnmatch(selected)}>
                                Unmatch
                            </Button>
                            <Button size="xs" icon={<EyeOff className="size-3.5" />} onClick={() => onIgnore(selected)}>
                                Ignore
                            </Button>
                        </div>
                    )}
                    {f.files.map(x => {
                        const m = x.mediaId > 0 ? media.get(x.mediaId) : undefined
                        return (
                            <label key={x.path} className="flex cursor-pointer items-center gap-3 px-4 py-2 text-sm transition hover:bg-white/[0.03]">
                                <input
                                    type="checkbox"
                                    checked={sel.has(x.path)}
                                    onChange={() =>
                                        setSel(s => {
                                            const n = new Set(s)
                                            n.has(x.path) ? n.delete(x.path) : n.add(x.path)
                                            return n
                                        })
                                    }
                                    className="size-4 accent-[var(--brand)]"
                                />
                                <span className="min-w-0 flex-1 truncate text-fg/85" title={x.path}>
                                    {x.name}
                                </span>
                                <span className="hidden max-w-56 truncate text-xs text-subtle sm:inline">{x.mediaId > 0 ? title(m) || `#${x.mediaId}` : "Unmatched"}</span>
                                <Tooltip content={x.locked ? "Matched by hand — scans keep it" : "Matched automatically"}>
                                    <span className="w-20 shrink-0 text-right text-xs font-semibold tabular-nums">{x.kind === "nc" ? "OP/ED" : x.kind === "special" ? "Special" : x.episode ? `Ep ${x.episode}` : "—"}</span>
                                </Tooltip>
                            </label>
                        )
                    })}
                </div>
            )}
        </div>
    )
}

// Picks library folders (or files) to match to a given anime, likeliest
// first. Used from an anime's page when its files weren't found.
export function FolderPicker({ target, onPicked }: { target: Media; onPicked: (files: LocalFile[]) => void }) {
    const { data: status } = useStatus()
    const { data: files, isLoading } = useLibraryFiles()
    const media = useMediaIndex()
    const [q, setQ] = useState("")
    const [sel, setSel] = useState<Set<string>>(new Set())
    const roots = useMemo(() => [status?.settings.library.dir ?? "", ...(status?.settings.library.extraDirs ?? [])].filter(Boolean), [status])
    const folders = useMemo(() => {
        const all = buildFolders(files ?? [], roots).filter(f => !(f.matches.length === 1 && f.matches[0].mediaId === target.id && f.unmatched === 0))
        const needle = q.trim().toLowerCase()
        const shown = needle ? all.filter(f => [f.label, f.query].some(t => t.toLowerCase().includes(needle))) : all
        return shown
            .map(f => ({ f, score: likeness(f, target) }))
            .sort((a, b) => b.score - a.score || naturalCompare(a.f.label, b.f.label))
            .map(x => x.f)
    }, [files, roots, q, target])
    const picked = folders.filter(f => sel.has(f.dir)).flatMap(f => f.files)

    if (isLoading) return <div className="h-40 shimmer rounded-xl" />
    return (
        <div className="flex flex-col gap-3">
            <Input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter folders…" icon={<Search className="size-4" />} />
            <div className="max-h-[50vh] overflow-y-auto rounded-xl border border-line">
                {folders.map(f => (
                    <label key={f.dir} className="flex cursor-pointer items-center gap-3 border-b border-line px-3 py-2.5 text-sm last:border-b-0 hover:bg-white/[0.03]">
                        <input
                            type="checkbox"
                            checked={sel.has(f.dir)}
                            onChange={() =>
                                setSel(s => {
                                    const n = new Set(s)
                                    n.has(f.dir) ? n.delete(f.dir) : n.add(f.dir)
                                    return n
                                })
                            }
                            className="size-4 accent-[var(--brand)]"
                        />
                        <Folder className="size-4 shrink-0 text-subtle" />
                        <span className="min-w-0 flex-1">
                            <span className="block truncate font-medium" title={f.dir}>
                                {f.label}
                            </span>
                            <span className="text-xs text-subtle">
                                {f.files.length} files ·{" "}
                                {f.matches.length === 0 ? "unmatched" : f.matches.map(m => title(media.get(m.mediaId)) || `#${m.mediaId}`).join(", ")}
                            </span>
                        </span>
                    </label>
                ))}
                {folders.length === 0 && <p className="py-8 text-center text-sm text-muted">No other folders in your library.</p>}
            </div>
            <div className="flex justify-end">
                <Button variant="primary" disabled={picked.length === 0} icon={<Link2 className="size-4" />} onClick={() => onPicked(picked)}>
                    Continue with {picked.length} {picked.length === 1 ? "file" : "files"}
                </Button>
            </div>
        </div>
    )
}
