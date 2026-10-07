import { useQueryClient } from "@tanstack/react-query"
import { EyeOff, FolderOpen, Link2, Link2Off } from "lucide-react"
import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "@/lib/toast"
import { api } from "@/lib/api"
import { libraryFilesQuery, useLibraryFiles, useStatus } from "@/lib/queries"
import type { LocalFile, Media } from "@/lib/types"
import { mostCommon, plural, title } from "@/lib/utils"
import { DropdownItem, DropdownLabel, DropdownSeparator } from "../ui"
import { MatchDialog } from "./MatchDialog"

// The menu of an anime card that is in the library because of local files
// (Home's "In your library", the Local library page). It acts on those
// files, to get rid of a wrong match. Pass menu(media) to each MediaCard and
// render dialog once.
export function useLibraryCardMenu() {
    const [matching, setMatching] = useState<{ files: LocalFile[]; query: string } | null>(null)
    return {
        menu: (media: Media) => <LibraryCardMenu media={media} onMatch={(files, query) => setMatching({ files, query })} />,
        dialog: <MatchDialog open={!!matching} onOpenChange={v => !v && setMatching(null)} files={matching?.files ?? []} initialQuery={matching?.query} />,
    }
}

function LibraryCardMenu({ media, onMatch }: { media: Media; onMatch: (files: LocalFile[], query: string) => void }) {
    const { data: status } = useStatus()
    const { data: all } = useLibraryFiles() // refreshed as the menu opens
    const qc = useQueryClient()
    const navigate = useNavigate()
    const ofMedia = (fs?: LocalFile[] | null) => (fs ?? []).filter(f => f.mediaId === media.id && !f.ignored)
    const files = all === undefined ? null : ofMedia(all) // null while loading

    const post = async (path: string, body: object) => {
        await api.post(path, body)
        qc.invalidateQueries({ queryKey: ["library"] })
        qc.invalidateQueries({ queryKey: ["collection"] })
        qc.invalidateQueries({ queryKey: ["entry"] })
    }
    // An action acts on the files as they are when it's picked: picked before
    // they have loaded (first open), it waits for them.
    const act = (fn: (files: LocalFile[]) => unknown) => async () => {
        try {
            const fs = ofMedia(await qc.ensureQueryData(libraryFilesQuery))
            if (fs.length) await fn(fs)
        } catch (e: any) {
            toast.error(e.message)
        }
    }
    const ignore = act(async fs => {
        const paths = fs.map(f => f.path)
        await post("/api/library/ignore", { paths, ignored: true })
        toast.success(`Ignoring ${plural(fs.length, "file")} of ${title(media)}`, {
            description: "Restore them any time in Library tools › Ignored.",
            action: { label: "Undo", onClick: () => post("/api/library/ignore", { paths, ignored: false }).catch(e => toast.error(e.message)) },
        })
    })
    const unmatch = act(async fs => {
        await post("/api/library/unmatch", { paths: fs.map(f => f.path) })
        toast.success(`Unmatched ${plural(fs.length, "file")}`, {
            description: "Match them again in Library tools › Unmatched.",
            action: { label: "Show", onClick: () => navigate("/library?tab=unmatched") },
        })
    })
    // Searches AniList for the title the files carry, not the wrong match's.
    const match = act(fs => onMatch(fs, fs.find(f => f.parsed?.folderTitle)?.parsed.folderTitle || fs.find(f => f.parsed?.title)?.parsed.title || ""))
    const openFolder = act(fs => api.post("/api/open", { path: mostCommon(fs.map(f => f.dir)) }))
    const none = files?.length === 0

    return (
        <>
            <DropdownLabel>{files ? `${plural(files.length, "file")} on this computer` : "Local files…"}</DropdownLabel>
            <DropdownItem icon={<EyeOff />} disabled={none} onSelect={ignore}>
                Ignore its files
            </DropdownItem>
            <DropdownItem icon={<Link2Off />} disabled={none} onSelect={unmatch}>
                Unmatch files
            </DropdownItem>
            <DropdownItem icon={<Link2 />} disabled={none} onSelect={match}>
                Match to another anime…
            </DropdownItem>
            {status?.client !== "lan" && (
                <>
                    <DropdownSeparator />
                    <DropdownItem icon={<FolderOpen />} disabled={none} onSelect={openFolder}>
                        Open folder
                    </DropdownItem>
                </>
            )}
        </>
    )
}
