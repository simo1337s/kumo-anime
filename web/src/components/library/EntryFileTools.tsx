import { useQueryClient } from "@tanstack/react-query"
import { FolderInput, FolderSearch, Link2, Link2Off, Settings2 } from "lucide-react"
import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "sonner"
import { api } from "@/lib/api"
import type { EntryView, LocalFile, Media } from "@/lib/types"
import { title } from "@/lib/utils"
import { Button, Dialog, Dropdown, DropdownContent, DropdownItem, DropdownSeparator, DropdownTrigger } from "../ui"
import { FolderPicker } from "./LibraryFolders"
import { MatchDialog } from "./MatchDialog"

// "Add files from your library": pick folders, then confirm the match (with
// episode numbering) to this anime.
export function AddFromLibraryDialog({ open, onOpenChange, media }: { open: boolean; onOpenChange: (v: boolean) => void; media: Media }) {
    const [files, setFiles] = useState<LocalFile[] | null>(null)
    return (
        <>
            <Dialog
                open={open && !files}
                onOpenChange={v => {
                    if (!v) onOpenChange(false)
                }}
                title={`Add files to ${title(media)}`}
                description="Pick the folders that hold this anime. Likely ones are listed first."
                className="w-[min(94vw,720px)]"
            >
                {open && <FolderPicker target={media} onPicked={setFiles} />}
            </Dialog>
            <MatchDialog
                open={open && !!files}
                onOpenChange={v => {
                    if (!v) {
                        setFiles(null)
                        onOpenChange(false)
                    }
                }}
                files={files ?? []}
                target={media}
            />
        </>
    )
}

// The files matched to an anime, for moving them elsewhere.
export function entryFiles(entry: EntryView): LocalFile[] {
    const out = new Map<string, LocalFile>()
    for (const e of [...entry.episodes, ...(entry.specials ?? []), ...(entry.others ?? [])]) if (e.file) out.set(e.file.path, e.file)
    return [...out.values()]
}

// "Manage files" menu on an anime's Library tab.
export function EntryFilesMenu({ entry }: { entry: EntryView }) {
    const qc = useQueryClient()
    const navigate = useNavigate()
    const [adding, setAdding] = useState(false)
    const [moving, setMoving] = useState(false)
    const files = entryFiles(entry)
    const unmatchAll = async () => {
        if (!confirm(`Unmatch all ${files.length} files of ${title(entry.media)}? They'll show up under Library tools › Unmatched.`)) return
        try {
            await api.post("/api/library/unmatch", { paths: files.map(f => f.path) })
            toast.success(`Unmatched ${files.length} files`)
            qc.invalidateQueries({ queryKey: ["library"] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["entry"] })
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    return (
        <>
            <Dropdown>
                <DropdownTrigger asChild>
                    <Button size="sm" variant="ghost" icon={<Settings2 className="size-4" />}>
                        Manage files
                    </Button>
                </DropdownTrigger>
                <DropdownContent>
                    <DropdownItem icon={<FolderInput />} onSelect={() => setAdding(true)}>
                        Add files from library…
                    </DropdownItem>
                    <DropdownItem icon={<Link2 />} onSelect={() => setMoving(true)} disabled={files.length === 0}>
                        Wrong anime? Match these files to another…
                    </DropdownItem>
                    <DropdownItem icon={<Link2Off />} onSelect={unmatchAll} disabled={files.length === 0} danger>
                        Unmatch all files
                    </DropdownItem>
                    <DropdownSeparator />
                    <DropdownItem icon={<FolderSearch />} onSelect={() => navigate("/library?tab=folders")}>
                        Library tools
                    </DropdownItem>
                </DropdownContent>
            </Dropdown>
            <AddFromLibraryDialog open={adding} onOpenChange={setAdding} media={entry.media} />
            <MatchDialog open={moving} onOpenChange={setMoving} files={files} initialQuery={files[0]?.parsed.folderTitle || files[0]?.parsed.title || ""} />
        </>
    )
}
