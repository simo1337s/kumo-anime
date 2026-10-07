import { Download, Wrench, X } from "lucide-react"
import { Button, IconButton } from "@/components/ui"
import { usePersisted } from "@/lib/hooks"
import { canInstallPrograms, missingPrograms, useInstallPrograms, useWatchSetup } from "@/lib/programs"
import { useStatus } from "@/lib/queries"

const list = (names: string[]) => (names.length < 2 ? names.join("") : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`)

// On Windows, when the in-app player's ffmpeg or ani-cli is missing: a
// button that installs what Kumo uses. Hidden for good per set of missing
// programs.
export function ProgramsNotice() {
    const { data: status } = useStatus()
    const [hidden, setHidden] = usePersisted("kumo-programs-notice-hidden", "")
    const install = useInstallPrograms()
    useWatchSetup(status)
    const missing = missingPrograms(status)
    const key = missing.join(",")
    const running = !!status?.setupRunning
    if (!canInstallPrograms(status) || !(missing.includes("ffmpeg") || missing.includes("ani-cli")) || hidden === key) return null
    return (
        <div className="flex flex-wrap items-center gap-4 rounded-2xl border border-amber-500/25 bg-amber-500/10 px-5 py-4">
            <Wrench className="size-5 shrink-0 text-amber-300" />
            <div className="min-w-0 flex-1 text-sm">
                <p className="font-semibold text-amber-200">{running ? "Installing the programs Kumo uses…" : `Kumo needs ${list(missing)}`}</p>
                <p className="mt-0.5 text-muted">
                    {running
                        ? "Follow the PowerShell window. Kumo picks the programs up as soon as they're installed."
                        : "Kumo can install them for you with Scoop, in a PowerShell window, for your Windows user (no administrator rights)."}
                </p>
            </div>
            {!running && (
                <Button variant="primary" size="sm" icon={<Download className="size-4" />} loading={install.isPending} onClick={() => install.mutate()}>
                    Install
                </Button>
            )}
            <IconButton label="Hide" variant="ghost" size="sm" onClick={() => setHidden(key)}>
                <X className="size-4" />
            </IconButton>
        </div>
    )
}
