import { useQueryClient } from "@tanstack/react-query"
import { ArrowRight, Link2 } from "lucide-react"
import { useEffect, useMemo, useRef, useState } from "react"
import { toast } from "@/lib/toast"
import { api } from "@/lib/api"
import type { LocalFile, Media } from "@/lib/types"
import { cn, cover, formatLabel, title } from "@/lib/utils"
import { MediaPicker } from "../MediaPicker"
import { Button, Dialog, Input } from "../ui"

type Numbering = "names" | "offset" | "order"

// Natural order for file names ("Ep 2" before "Ep 10"), like the server.
export function naturalCompare(a: string, b: string) {
    return a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" })
}

// What episode each file gets with the chosen numbering (mirrors the server).
function episodesFor(files: LocalFile[], target: Media | null, numbering: Numbering, offset: number) {
    const order = new Map<string, number>()
    if (numbering === "order") {
        files
            .filter(f => f.kind === "main")
            .slice()
            .sort((a, b) => naturalCompare(a.name, b.name))
            .forEach((f, i) => order.set(f.path, i + 1))
    }
    const shift = numbering === "offset" ? offset : 0
    return new Map(
        files.map(f => {
            if (f.kind === "nc") return [f.path, 0]
            let ep = (order.get(f.path) ?? f.parsed.episode) + shift
            if (numbering !== "order" && f.parsed.episode < 0 && target && (target.format === "MOVIE" || target.episodes === 1)) ep = 1
            return [f.path, ep > 0 ? ep : 0]
        }),
    )
}

// Matches local files to an anime by hand (the match is locked, so later
// scans keep it). Without a target the anime is searched on AniList first.
export function MatchDialog({
    open,
    onOpenChange,
    files,
    target: fixedTarget,
    initialQuery = "",
    onDone,
}: {
    open: boolean
    onOpenChange: (v: boolean) => void
    files: LocalFile[]
    target?: Media | null
    initialQuery?: string
    onDone?: () => void
}) {
    const qc = useQueryClient()
    const [picked, setPicked] = useState<Media | null>(null)
    const [numbering, setNumbering] = useState<Numbering>("names")
    const [offset, setOffset] = useState(0)
    const [saving, setSaving] = useState(false)
    const justPicked = useRef(false)
    const target = fixedTarget ?? picked

    const main = files.filter(f => f.kind === "main")
    const parsedEps = main.map(f => f.parsed.episode).filter(e => e > 0)
    const minEp = parsedEps.length ? Math.min(...parsedEps) : 0
    const unnumbered = main.length > 0 && parsedEps.length < main.length

    // Reset when opened for another set of files, and suggest a numbering:
    // files without numbers -> file order; absolute numbers past the end of
    // the anime (26-50 for a 25-episode season) -> shift down.
    useEffect(() => {
        if (!open) return
        setPicked(null)
        setSaving(false)
        setNumbering(unnumbered ? "order" : "names")
        setOffset(0)
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open, files])
    useEffect(() => {
        if (!target || unnumbered) return
        const total = target.episodes ?? 0
        if (total > 0 && minEp > total) {
            setNumbering("offset")
            setOffset(-(minEp - 1))
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [target?.id])

    const eps = useMemo(() => episodesFor(files, target, numbering, offset), [files, target, numbering, offset])
    const total = target?.episodes ?? 0
    const outOfRange = total > 0 ? main.filter(f => (eps.get(f.path) ?? 0) > total).length : 0

    const save = async () => {
        if (!target) return
        setSaving(true)
        try {
            await api.post("/api/library/match", {
                paths: files.map(f => f.path),
                mediaId: target.id,
                episodeOffset: numbering === "offset" ? offset : 0,
                renumber: numbering === "order",
            })
            toast.success(`Matched ${files.length} ${files.length === 1 ? "file" : "files"} to ${title(target)}`)
            qc.invalidateQueries({ queryKey: ["library"] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["entry"] })
            onOpenChange(false)
            onDone?.()
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setSaving(false)
        }
    }

    // Step 1: choose the anime.
    if (open && !target) {
        return (
            <MediaPicker
                open
                // The picker closes itself right after a pick: that isn't a cancel.
                onOpenChange={v => {
                    if (!v && !justPicked.current) onOpenChange(false)
                    justPicked.current = false
                }}
                onPick={m => {
                    justPicked.current = true
                    setPicked(m)
                }}
                initialQuery={initialQuery}
                heading={`Match ${files.length} ${files.length === 1 ? "file" : "files"} to…`}
                description="Search AniList for the right anime. The match is locked, so later scans keep it."
            />
        )
    }

    // Step 2: episode numbering and confirm.
    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title="Confirm match"
            description="Check the episode numbers before saving."
            className="w-[min(94vw,720px)]"
            footer={
                <>
                    {!fixedTarget && (
                        <Button variant="ghost" onClick={() => setPicked(null)}>
                            Pick another anime
                        </Button>
                    )}
                    <Button variant="primary" loading={saving} icon={<Link2 className="size-4" />} onClick={save}>
                        Match {files.length} {files.length === 1 ? "file" : "files"}
                    </Button>
                </>
            }
        >
            {target && (
                <div className="flex items-center gap-4 rounded-xl border border-line bg-surface-2/60 p-3">
                    <img src={cover(target)} alt="" className="h-20 w-14 rounded-lg object-cover" />
                    <div className="min-w-0">
                        <p className="truncate font-semibold">{title(target)}</p>
                        <p className="text-sm text-muted">{[formatLabel(target.format), target.seasonYear, total ? `${total} episodes` : ""].filter(Boolean).join(" · ")}</p>
                    </div>
                </div>
            )}

            <div className="mt-5 flex flex-col gap-2">
                <p className="text-sm font-medium">Episode numbers</p>
                {(
                    [
                        ["names", "From the file names"],
                        ["offset", "Shift the numbers"],
                        ["order", "1, 2, 3… in file-name order"],
                    ] as [Numbering, string][]
                ).map(([v, label]) => (
                    <label key={v} className={cn("flex cursor-pointer items-center gap-3 rounded-xl border px-3 py-2.5 text-sm transition", numbering === v ? "border-brand/50 bg-brand-soft" : "border-line hover:bg-white/[0.03]")}>
                        <input type="radio" name="numbering" checked={numbering === v} onChange={() => setNumbering(v)} className="accent-[var(--brand)]" />
                        <span className="flex-1">{label}</span>
                        {v === "offset" && numbering === "offset" && (
                            <span className="flex items-center gap-2 text-xs text-muted">
                                by
                                <div className="w-20">
                                    <Input type="number" value={offset} onChange={e => setOffset(Number(e.target.value) || 0)} />
                                </div>
                            </span>
                        )}
                    </label>
                ))}
                {outOfRange > 0 && (
                    <p className="text-xs text-amber-300">
                        {outOfRange} {outOfRange === 1 ? "file gets" : "files get"} an episode number above {total}. If the folder uses absolute numbering, shift the numbers down.
                    </p>
                )}
            </div>

            <div className="mt-5 max-h-64 overflow-y-auto rounded-xl border border-line">
                {files
                    .slice()
                    .sort((a, b) => naturalCompare(a.name, b.name))
                    .map(f => (
                        <div key={f.path} className="flex items-center gap-3 border-b border-line px-3 py-2 text-sm last:border-b-0">
                            <span className="min-w-0 flex-1 truncate text-fg/85" title={f.path}>
                                {f.name}
                            </span>
                            <ArrowRight className="size-3.5 shrink-0 text-subtle" />
                            <span className={cn("w-24 shrink-0 text-right font-semibold tabular-nums", total > 0 && (eps.get(f.path) ?? 0) > total && "text-amber-300")}>
                                {f.kind === "nc" ? "OP/ED" : f.kind === "special" ? "Special" : eps.get(f.path) ? `Episode ${eps.get(f.path)}` : "No number"}
                            </span>
                        </div>
                    ))}
            </div>
        </Dialog>
    )
}
