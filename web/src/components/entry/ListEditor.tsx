import { Check, ChevronDown, Minus, Plus, Star, Trash2 } from "lucide-react"
import { useEffect, useState } from "react"
import { useDeleteEntry, useUpdateEntry } from "@/lib/queries"
import type { Media } from "@/lib/types"
import { cn, LIST_STATUS, LIST_STATUS_MANGA, totalEpisodes } from "@/lib/utils"
import { Button, Dropdown, DropdownContent, DropdownItem, DropdownSeparator, DropdownTrigger } from "../ui"

type Entry = { status: string; progress: number; score: number } | null | undefined

export function ListStatusButton({ media, entry }: { media: Media; entry: Entry }) {
    const update = useUpdateEntry(media.id)
    const del = useDeleteEntry(media.id)
    const labels = media.type === "MANGA" ? LIST_STATUS_MANGA : LIST_STATUS
    return (
        <Dropdown>
            <DropdownTrigger asChild>
                <Button variant="subtle" size="lg" loading={update.isPending || del.isPending}>
                    {entry ? (
                        <>
                            <Check className="size-4 text-brand-strong" />
                            {labels[entry.status] ?? entry.status}
                        </>
                    ) : (
                        <>
                            <Plus className="size-4" />
                            Add to list
                        </>
                    )}
                    <ChevronDown className="size-4 opacity-60" />
                </Button>
            </DropdownTrigger>
            <DropdownContent align="start">
                {Object.entries(labels).map(([k, v]) => (
                    <DropdownItem key={k} onSelect={() => update.mutate({ status: k })}>
                        {entry?.status === k ? "● " : ""}
                        {v}
                    </DropdownItem>
                ))}
                {entry && (
                    <>
                        <DropdownSeparator />
                        <DropdownItem danger icon={<Trash2 />} onSelect={() => del.mutate()}>
                            Remove from list
                        </DropdownItem>
                    </>
                )}
            </DropdownContent>
        </Dropdown>
    )
}

export function ProgressEditor({ media, entry }: { media: Media; entry: Entry }) {
    const update = useUpdateEntry(media.id)
    const total = media.type === "MANGA" ? media.chapters ?? 0 : totalEpisodes(media)
    const [value, setValue] = useState(entry?.progress ?? 0)
    useEffect(() => setValue(entry?.progress ?? 0), [entry?.progress])
    const commit = (v: number) => {
        v = Math.max(0, total > 0 ? Math.min(total, v) : v)
        setValue(v)
        const body: { progress: number; status?: string } = { progress: v }
        if (!entry || entry.status === "PLANNING") body.status = "CURRENT"
        if (total > 0 && v >= total) body.status = "COMPLETED"
        update.mutate(body)
    }
    return (
        <div className="flex h-11 items-center gap-1 rounded-lg bg-white/[0.07] px-1">
            <button className="grid size-9 place-items-center rounded-md text-muted transition-colors hover:bg-white/10 hover:text-fg" onClick={() => commit(value - 1)} aria-label="Decrease progress">
                <Minus className="size-4" />
            </button>
            <div className="min-w-16 text-center text-sm tabular-nums">
                <input
                    value={value}
                    onChange={e => setValue(Number(e.target.value.replace(/\D/g, "")) || 0)}
                    onBlur={() => value !== (entry?.progress ?? 0) && commit(value)}
                    onKeyDown={e => e.key === "Enter" && commit(value)}
                    className="w-8 bg-transparent text-right font-semibold outline-none"
                />
                <span className="text-subtle"> / {total || "?"}</span>
            </div>
            <button className="grid size-9 place-items-center rounded-md text-muted transition-colors hover:bg-white/10 hover:text-fg" onClick={() => commit(value + 1)} aria-label="Increase progress">
                <Plus className="size-4" />
            </button>
        </div>
    )
}

export function ScoreEditor({ media, entry }: { media: Media; entry: Entry }) {
    const update = useUpdateEntry(media.id)
    const score = entry?.score ?? 0
    return (
        <Dropdown>
            <DropdownTrigger asChild>
                <button className="focus-ring flex h-11 items-center gap-2 rounded-lg bg-white/[0.07] px-4 text-[15px] font-medium transition-colors hover:bg-white/[0.11] data-[state=open]:bg-white/[0.11]">
                    <Star className={cn("size-4", score ? "fill-amber-300 text-amber-300" : "text-muted")} />
                    {score ? (score / 10).toFixed(score % 10 ? 1 : 0) : "Rate"}
                </button>
            </DropdownTrigger>
            {/* Menu items, not plain buttons: arrow keys move between them and picking one closes the menu. */}
            <DropdownContent align="start" className="grid grid-cols-5 gap-1 p-2">
                {[10, 9, 8, 7, 6, 5, 4, 3, 2, 1].map(s => (
                    <DropdownItem
                        key={s}
                        onSelect={() => update.mutate({ score: s * 10 })}
                        className={cn("justify-center px-0 font-semibold", score === s * 10 && "bg-brand text-white data-[highlighted]:bg-brand/80")}
                    >
                        {s}
                    </DropdownItem>
                ))}
                <DropdownItem onSelect={() => update.mutate({ score: 0 })} className="col-span-5 mt-1 h-8 justify-center text-xs text-muted">
                    Clear score
                </DropdownItem>
            </DropdownContent>
        </Dropdown>
    )
}
