import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { api, qs } from "./api"
import type {
    AutoRule,
    CollectionView,
    DownloadItem,
    EntryView,
    ExtensionInfo,
    ListEntry,
    LocalFile,
    MangaChapter,
    MarketEntry,
    Media,
    OnlineProvider,
    PluginState,
    ScheduleItem,
    Settings,
    Status,
    StreamEpisodes,
    Torrent,
    TorrentResult,
} from "./types"

export function useStatus() {
    return useQuery({ queryKey: ["status"], queryFn: () => api.get<Status>("/api/status"), staleTime: 30_000 })
}

export function useSettings() {
    const q = useStatus()
    return q.data?.settings
}

export function useSaveSettings() {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: (s: Settings) => api.put<Settings>("/api/settings", s),
        onSuccess: s => {
            qc.setQueryData<Status>(["status"], old => (old ? { ...old, settings: s } : old))
            qc.invalidateQueries({ queryKey: ["status"] })
            toast.success("Settings saved")
        },
        onError: (e: Error) => toast.error(e.message),
    })
}

export function useCollection() {
    return useQuery({ queryKey: ["collection"], queryFn: () => api.get<CollectionView>("/api/anime/collection"), staleTime: 60_000 })
}

export function useEntry(id: number) {
    return useQuery({
        queryKey: ["entry", id],
        queryFn: () => api.get<EntryView>(`/api/anime/${id}`),
        enabled: id > 0,
        staleTime: 30_000,
    })
}

export function useUpdateEntry(mediaId: number) {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: (body: { status?: string; progress?: number; score?: number; repeat?: number }) => api.post(`/api/anime/${mediaId}/entry`, body),
        onSuccess: () => {
            qc.invalidateQueries({ queryKey: ["entry", mediaId] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["list"] })
            qc.invalidateQueries({ queryKey: ["manga"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
}

// Marks single episodes as watched or unwatched. AniList only stores how far
// you got, so marking episode 7 watched raises progress to 7 (1-6 count as
// watched too) and unwatched lowers it to 6. The server works it out from the
// current list entry: watched never lowers progress, unwatched never raises
// it; the last episode completes the show and an earlier one reopens it.
export function useEpisodeMarker(media: Media | undefined, _listEntry?: Pick<ListEntry, "status" | "progress" | "repeat"> | null) {
    const qc = useQueryClient()
    const m = useMutation({
        mutationFn: (v: { episode: number; watched: boolean }) => api.post<{ changed: boolean; progress: number; status: string }>(`/api/anime/${media?.id ?? 0}/episode`, v),
        onSuccess: (res, v) => {
            qc.invalidateQueries({ queryKey: ["entry", media?.id] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["list"] })
            const what = media?.format === "MOVIE" ? "Movie" : `Episode ${v.episode}`
            if (!res.changed) toast.info(`${what} is already ${v.watched ? "watched" : "unwatched"}`)
            else toast.success(`${what} marked as ${v.watched ? "watched" : "unwatched"}${res.status === "COMPLETED" ? " — completed!" : ""}`)
        },
        onError: (e: Error) => toast.error(e.message),
    })
    const mark = (episode: number, watched: boolean) => {
        if (media) m.mutate({ episode, watched })
    }
    return { mark, pending: m.isPending }
}

// The sub/dub choice remembered for an anime (server-side, so every device
// and every way of starting an episode uses it).
export type LanguageMode = { mode: "sub" | "dub"; saved: boolean }

export function useLanguageMode(mediaId: number) {
    const qc = useQueryClient()
    const query = useQuery({
        queryKey: ["language", mediaId],
        queryFn: () => api.get<LanguageMode>(`/api/anime/${mediaId}/language`),
        enabled: mediaId > 0,
        staleTime: 60_000,
    })
    const set = useMutation({
        mutationFn: (mode: "sub" | "dub") => api.put<LanguageMode>(`/api/anime/${mediaId}/language`, { mode }),
        onMutate: mode => qc.setQueryData<LanguageMode>(["language", mediaId], { mode, saved: true }),
        onSettled: () => qc.invalidateQueries({ queryKey: ["language", mediaId] }),
        onError: (e: Error) => toast.error(e.message),
    })
    return { mode: query.data?.mode, saved: query.data?.saved ?? false, loaded: query.isSuccess || query.isError, set: set.mutate }
}

// For click handlers outside a component that has the hook.
export async function fetchLanguageMode(mediaId: number, fallbackDub: boolean): Promise<boolean> {
    try {
        return (await api.get<LanguageMode>(`/api/anime/${mediaId}/language`)).mode === "dub"
    } catch {
        return fallbackDub
    }
}

export function useDeleteEntry(mediaId: number) {
    const qc = useQueryClient()
    return useMutation({
        mutationFn: () => api.del(`/api/anime/${mediaId}/entry`),
        onSuccess: () => {
            toast.success("Removed from your list")
            qc.invalidateQueries({ queryKey: ["entry", mediaId] })
            qc.invalidateQueries({ queryKey: ["collection"] })
            qc.invalidateQueries({ queryKey: ["list"] })
        },
        onError: (e: Error) => toast.error(e.message),
    })
}

export type SearchParams = {
    page?: number
    perPage?: number
    search?: string
    type?: string
    genres?: string[]
    season?: string
    year?: number
    formats?: string[]
    status?: string
    sort?: string[]
}

export function useSearch(params: SearchParams, enabled = true) {
    return useQuery({
        queryKey: ["search", params],
        queryFn: () => api.post<{ pageInfo: { hasNextPage: boolean; total: number; currentPage: number }; media: Media[] | null }>("/api/anilist/search", params),
        enabled,
        placeholderData: keepPreviousData,
        staleTime: 5 * 60_000,
    })
}

export function useDiscover() {
    return useQuery({
        queryKey: ["discover"],
        queryFn: () =>
            api.get<{ trending: Media[]; thisSeason: Media[]; nextSeason: Media[]; popular: Media[]; topRated: Media[]; season: string; year: number }>(
                "/api/anilist/discover",
            ),
        staleTime: 10 * 60_000,
    })
}

export function useSchedule(days = 7) {
    return useQuery({ queryKey: ["schedule", days], queryFn: () => api.get<ScheduleItem[]>(`/api/anilist/schedule?days=${days}`), staleTime: 10 * 60_000 })
}

export function useRawList(type: "ANIME" | "MANGA") {
    return useQuery({
        queryKey: ["list", type],
        queryFn: () => api.get<{ lists: { name: string; status: string; isCustomList: boolean; entries: any[] }[] | null }>(`/api/anilist/list?type=${type}`),
        staleTime: 60_000,
    })
}

export function useScan() {
    return useMutation({
        mutationFn: (full: boolean) => api.post("/api/library/scan", { full }),
        onSuccess: () => toast.info("Scanning your library…"),
        onError: (e: Error) => toast.error(e.message),
    })
}

export function useUnmatched() {
    return useQuery({
        queryKey: ["library", "unmatched"],
        queryFn: () => api.get<{ dir: string; title: string; files: LocalFile[] }[]>("/api/library/unmatched"),
    })
}

export function useLibraryFiles() {
    return useQuery({ queryKey: ["library", "files"], queryFn: () => api.get<LocalFile[] | null>("/api/library/files") })
}

export function useOnlineProviders() {
    return useQuery({ queryKey: ["os-providers"], queryFn: () => api.get<OnlineProvider[]>("/api/onlinestream/providers"), staleTime: 5 * 60_000 })
}

export function useStreamEpisodes(provider: string, mediaId: number, dub: boolean, enabled = true) {
    return useQuery({
        queryKey: ["os-episodes", provider, mediaId, dub],
        queryFn: () => api.get<StreamEpisodes>(`/api/onlinestream/episodes${qs({ provider, mediaId, dub })}`),
        enabled: enabled && mediaId > 0 && !!provider,
        retry: false,
        staleTime: 10 * 60_000,
    })
}

export function useDownloads() {
    return useQuery({ queryKey: ["downloads"], queryFn: () => api.get<DownloadItem[]>("/api/downloads") })
}

export function useTorrentList(enabled = true) {
    return useQuery({
        queryKey: ["torrents"],
        queryFn: () => api.get<Torrent[]>("/api/torrent-client/list"),
        refetchInterval: 3000,
        retry: false,
        enabled,
    })
}

export function useTorrentSearch() {
    return useMutation({
        mutationFn: (body: { provider: string; mediaId?: number; query?: string; episode?: number; batch?: boolean; resolution?: string }) =>
            api.post<TorrentResult[]>("/api/torrents/search", body),
        onError: (e: Error) => toast.error(e.message),
    })
}

export function useExtensions() {
    return useQuery({ queryKey: ["extensions"], queryFn: () => api.get<ExtensionInfo[]>("/api/extensions") })
}

export function useMarketplace(url: string) {
    return useQuery({
        queryKey: ["marketplace", url],
        queryFn: () => api.get<MarketEntry[]>(`/api/extensions/marketplace${qs({ url })}`),
        staleTime: 30 * 60_000,
        retry: false,
    })
}

export function usePluginStates() {
    return useQuery({ queryKey: ["plugins-ui"], queryFn: () => api.get<PluginState[]>("/api/plugins/ui") })
}

export function useAutoRules() {
    return useQuery({ queryKey: ["auto-rules"], queryFn: () => api.get<AutoRule[]>("/api/autodownloader/rules") })
}

export function useMangaChapters(mediaId: number, provider: string) {
    return useQuery({
        queryKey: ["manga", "chapters", mediaId, provider],
        queryFn: () =>
            api.get<{ provider: string; mapping: { id: string; title: string } | null; chapters: MangaChapter[] | null }>(
                `/api/manga/${mediaId}/chapters${qs({ provider })}`,
            ),
        enabled: mediaId > 0,
        retry: false,
        staleTime: 10 * 60_000,
    })
}
