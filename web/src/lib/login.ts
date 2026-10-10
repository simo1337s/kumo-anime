// The AniList login started from Kumo's login button, which the callback page
// (AuthCallback) checks: another website could open that page with its own
// AniList token, and log Kumo into its account.

const key = "kumo:anilist-login"
// How long a login may take.
const maxAge = 30 * 60 * 1000

export function loginStarted() {
    try {
        localStorage.setItem(key, String(Date.now()))
    } catch {
        /* no storage: the callback refuses */
    }
}

// Whether a login was started from Kumo lately; it's used up.
export function takeLogin(): boolean {
    try {
        const at = Number(localStorage.getItem(key))
        localStorage.removeItem(key)
        return at > 0 && Date.now() - at < maxAge
    } catch {
        return false
    }
}
