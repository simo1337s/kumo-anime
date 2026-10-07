import { useQueryClient } from "@tanstack/react-query"
import { ExternalLink, KeyRound } from "lucide-react"
import { useEffect, useState } from "react"
import { toast } from "sonner"
import { api, desktop } from "@/lib/api"
import { useStatus } from "@/lib/queries"
import { Button, Dialog, Field, Input } from "./ui"

// AniList login. The client id (13985) uses AniList's PIN page as redirect,
// so the token is either captured automatically by the desktop window or
// pasted by the user.
export function LoginDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
    const { data: status } = useStatus()
    const [token, setToken] = useState("")
    const [busy, setBusy] = useState(false)
    const qc = useQueryClient()
    // Don't keep a typed token around once the dialog is closed.
    useEffect(() => {
        if (!open) setToken("")
    }, [open])
    const url = status?.anilistAuthUrl ?? "https://anilist.co/api/v2/oauth/authorize?client_id=13985&response_type=token"

    const submit = async (t: string) => {
        if (!t.trim()) return
        setBusy(true)
        try {
            const v = await api.post<{ name: string }>("/api/auth/anilist", { token: t })
            toast.success(`Welcome, ${v.name}!`)
            setToken("")
            onOpenChange(false)
            qc.invalidateQueries()
        } catch (e: any) {
            toast.error(e.message)
        } finally {
            setBusy(false)
        }
    }

    const openAnilist = async () => {
        const d = desktop()
        if (d) {
            const t = await d.anilistLogin(url)
            if (t) await submit(t)
            return
        }
        window.open(url, "_blank", "noopener")
    }

    return (
        <Dialog
            open={open}
            onOpenChange={onOpenChange}
            title="Log in with AniList"
            description="Sync your lists, progress and scores with your AniList account."
        >
            <div className="flex flex-col gap-5">
                <div className="rounded-lg bg-white/[0.03] p-5">
                    <p className="text-sm text-muted">
                        {desktop()
                            ? "A login window will open. After you authorize Kumo, the token is captured automatically."
                            : "Authorize Kumo on AniList, then copy the token shown on the page and paste it below."}
                    </p>
                    <Button variant="primary" className="mt-4 w-full" icon={<ExternalLink className="size-4" />} onClick={openAnilist}>
                        Open AniList
                    </Button>
                </div>
                <Field label="Access token" help="You can also paste the whole URL you were redirected to.">
                    <Input
                        icon={<KeyRound className="size-4" />}
                        placeholder="eyJ0eXAiOiJKV1QiLCJhbGciOiJSUzI1NiJ9…"
                        value={token}
                        onChange={e => setToken(e.target.value)}
                        onKeyDown={e => e.key === "Enter" && submit(token)}
                    />
                </Field>
                <Button variant="white" loading={busy} disabled={!token.trim()} onClick={() => submit(token)}>
                    Log in
                </Button>
            </div>
        </Dialog>
    )
}
