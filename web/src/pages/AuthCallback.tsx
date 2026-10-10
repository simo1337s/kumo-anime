import { useQueryClient } from "@tanstack/react-query"
import { useEffect, useRef, useState } from "react"
import { useNavigate } from "react-router-dom"
import { toast } from "@/lib/toast"
import { Button, EmptyState, Spinner } from "@/components/ui"
import { api } from "@/lib/api"
import { takeLogin } from "@/lib/login"

// AniList redirects here (…/auth/callback#access_token=…) after "Log in with
// AniList" in a normal browser. The desktop window captures the token itself.
export default function AuthCallbackPage() {
    const navigate = useNavigate()
    const qc = useQueryClient()
    const [error, setError] = useState("")
    const done = useRef(false)

    useEffect(() => {
        if (done.current) return
        done.current = true
        const params = new URLSearchParams(window.location.hash.replace(/^#/, ""))
        const token = params.get("access_token")
        // Drop the token from the address bar and history.
        window.history.replaceState(null, "", window.location.pathname)
        if (!token) {
            setError(params.get("error_description") || params.get("error") || "AniList didn't send a login token.")
            return
        }
        if (!takeLogin()) {
            setError("This login wasn't started from Kumo: log in again with Kumo's own Log in button.")
            return
        }
        api.post<{ name: string }>("/api/auth/anilist", { token })
            .then(v => {
                qc.invalidateQueries()
                toast.success(`Logged in as ${v.name}`)
                navigate("/", { replace: true })
            })
            .catch(e => setError(e.message))
    }, [navigate, qc])

    return (
        <div className="grid min-h-full place-items-center p-10">
            {error ? (
                <EmptyState title="AniList login failed" action={<Button onClick={() => navigate("/", { replace: true })}>Back to Kumo</Button>}>
                    {error}
                </EmptyState>
            ) : (
                <div className="flex items-center gap-3 text-muted">
                    <Spinner className="size-5" /> Logging in with AniList…
                </div>
            )}
        </div>
    )
}
