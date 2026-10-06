// Shapes returned by the Kumo API (mirrors the Go structs).

export type FuzzyDate = { year?: number | null; month?: number | null; day?: number | null }

export type Media = {
    id: number
    idMal?: number | null
    type: "ANIME" | "MANGA"
    format: string
    status: string
    season?: string
    seasonYear?: number | null
    episodes?: number | null
    chapters?: number | null
    volumes?: number | null
    duration?: number | null
    isAdult: boolean
    title: { romaji?: string; english?: string; native?: string; userPreferred?: string }
    synonyms?: string[]
    coverImage: { extraLarge?: string; large?: string; medium?: string; color?: string }
    bannerImage?: string
    genres?: string[]
    averageScore?: number | null
    meanScore?: number | null
    popularity?: number
    startDate?: FuzzyDate
    endDate?: FuzzyDate
    nextAiringEpisode?: { airingAt: number; timeUntilAiring: number; episode: number } | null
    description?: string
    countryOfOrigin?: string
    source?: string
    siteUrl?: string
    trailer?: { id: string; site: string; thumbnail: string } | null
    studios?: { nodes: { id: number; name: string }[] }
    tags?: { name: string; rank: number; isMediaSpoiler: boolean }[]
    relations?: { edges: { relationType: string; node: Media }[] }
    recommendations?: { nodes: { mediaRecommendation: Media | null }[] }
    characters?: {
        edges: {
            role: string
            node: { id: number; name: { full: string }; image: { large: string } }
            voiceActors: { id: number; name: { full: string }; image: { large: string } }[]
        }[]
    }
    rankings?: { rank: number; type: string; allTime: boolean; year?: number; season?: string; context: string }[]
    mediaListEntry?: { id: number; status: string; progress: number; score: number; repeat: number } | null
}

export type ListEntry = {
    id: number
    mediaId: number
    status: string
    progress: number
    score: number
    repeat: number
    updatedAt: number
    media: Media
}

export type Viewer = {
    id: number
    name: string
    avatar: { large: string; medium: string }
    bannerImage?: string
}

export type Parsed = {
    title: string
    folderTitle: string
    season: number
    part: number
    episode: number
    episodeTitle: string
    releaseGroup: string
    resolution: string
    year: number
    kind: string
}

export type LocalFile = {
    path: string
    dir: string
    name: string
    size: number
    modTime: number
    parsed: Parsed
    mediaId: number
    episode: number
    airedEpisode: number
    kind: "main" | "special" | "nc"
    locked: boolean
    ignored: boolean
    matchScore: number
}

export type HistoryEntry = {
    mediaId: number
    episode: number
    position: number
    duration: number
    source: string
    updatedAt: number
}

export type EpisodeView = {
    number: number
    title: string
    image: string
    summary: string
    airDate: string
    runtime: number
    kind: string
    file: LocalFile | null
    watched: boolean
    aired: boolean
    history: HistoryEntry | null
    resumeAt: number
    hasFile: boolean
}

export type EntryView = {
    media: Media
    listEntry: ListEntry | null
    episodes: EpisodeView[]
    specials: EpisodeView[] | null
    others: EpisodeView[] | null
    nextEpisode: EpisodeView | null
    localCount: number
    images: { banner: string; poster: string; fanart: string; clearlogo: string }
    mappings: { malId: number; anidbId: number; tvdbId: number }
    metadataNote?: string
}

export type CollectionItem = {
    media: Media
    listEntry: ListEntry | null
    localFiles: number
    downloaded: number[] | null
    nextEpisode: number
    nextHasFile: boolean
    lastWatched: number
}

export type ContinueItem = {
    media: Media
    episode: number
    total: number
    title: string
    image: string
    runtime: number
    hasFile: boolean
    filePath?: string
    resumeAt: number
    duration: number
    lastWatched: number
    source: string
}

export type CollectionView = {
    lists: { status: string; name: string; items: CollectionItem[] }[] | null
    continueWatching: ContinueItem[] | null
    unmatchedCount: number
    ignoredCount: number
    localOnly: CollectionItem[] | null
    genres: string[] | null
}

export type Settings = {
    library: {
        dir: string
        extraDirs: string[]
        autoRefresh: boolean
        refreshOnStartup: boolean
        matchThreshold: number
        ignorePatterns: string[]
        matchOutsideList: boolean
    }
    playback: {
        defaultPlayer: "mpv" | "builtin"
        autoUpdateProgress: boolean
        completionThreshold: number
        resumePlayback: boolean
        autoPlayNext: boolean
        rememberTracks: boolean
        preferredAudioLang: string
        preferredSubLang: string
        skipIntroAniSkip: boolean
    }
    mpv: { path: string; socket: string; extraArgs: string; fullscreen: boolean }
    transcode: { ffmpegPath: string; ffprobePath: string; mode: string; hwAccel: string; vaapiNode: string; preset: string }
    aniCli: {
        enabled: boolean
        path: string
        defaultMode: "sub" | "dub"
        quality: string
        downloadDir: string
        downloader: string
        player: "mpv" | "builtin"
    }
    onlineStream: { enabled: boolean; defaultProvider: string; preferDub: boolean }
    torrent: {
        defaultClient: string
        defaultProvider: string
        showActiveCount: boolean
        createSubfolder: boolean
        preferredResolution: string
        autoDownloader: boolean
        autoDownloadMinutes: number
    }
    qbittorrent: TorrentClientConfig
    transmission: TorrentClientConfig
    manga: { enabled: boolean; defaultProvider: string; readingMode: "long-strip" | "paged" | "double"; direction: string }
    anilist: { clientId: string; hideAdult: boolean }
    server: { host: string; port: number; allowLan: boolean; password: string; webUi: boolean }
    ui: {
        accentColor: string
        showAdult: boolean
        bannerType: string
        cardSize: string
        reducedMotion: boolean
        showEpisodeTitle: boolean
        blurUnwatched: boolean
        homeSections: string[]
    }
    discord: { richPresence: boolean; clientId: string }
    extensions: { marketplaceUrl: string; autoUpdate: boolean }
}

export type TorrentClientConfig = {
    host: string
    port: number
    username: string
    password: string
    executable: string
    tags: string
    category: string
    useHttps: boolean
}

export type Status = {
    version: string
    appName: string
    user: Viewer | null
    loggedIn: boolean
    anilistAuthUrl: string
    features: { mpv: boolean; ffmpeg: boolean; ffprobe: boolean; aniCli: boolean; ytDlp: boolean; xdgOpen: boolean }
    client: "desktop" | "local" | "lan"
    hostname: string
    platform?: string // "linux", "windows"…
    scanning: boolean
    dataDir: string
    listenAddr: string
    lanUrls?: string[] | null // where devices on the home network open Kumo
    webUiForced: boolean
    settings: Settings
}

export type PlaybackSession = {
    id: string
    mediaId: number
    episode: number
    title: string
    source: string
    player: string
    position: number
    duration: number
    paused: boolean
    active: boolean
    progressUpdated: boolean
}

export type TrackPrefs = {
    mediaId: number
    audioLang?: string
    audioTitle?: string
    audioIndex?: number
    subLang?: string
    subTitle?: string
    subIndex?: number
    subOff?: boolean
    streamMode?: string
}

export type ProbeStream = {
    index: number
    typeIndex: number
    type: string
    codec: string
    language: string
    title: string
    default: boolean
    forced: boolean
    channels?: number
    width?: number
    height?: number
    bitmap?: boolean
}

export type Probe = {
    path: string
    container: string
    duration: number
    size: number
    video: ProbeStream[] | null
    audio: ProbeStream[] | null
    subtitles: ProbeStream[] | null
    externalSubs: { name: string; path: string; language: string }[] | null
    method: "direct" | "remux" | "transcode"
    reason: string
}

export type StreamSource = {
    url: string
    originalUrl: string
    type: string
    quality: string
    label?: string
    server: string
    subtitles: { url: string; language: string; isDefault: boolean; original: string }[]
    headers: Record<string, string>
    referrer?: string
}

export type OnlineProvider = { id: string; name: string; icon: string; supportsDub: boolean; servers: string[]; builtin: boolean }

export type StreamEpisodes = {
    provider: string
    mapping: { id: string; title: string; score: number; query?: string; index?: number } | null
    episodes: { number: number; id: string; title: string }[] | null
    dub: boolean
}

export type DownloadItem = {
    id: string
    mediaId: number
    episode: number
    animeTitle: string
    image: string
    mode: string
    source: string
    provider: string
    status: "queued" | "resolving" | "downloading" | "completed" | "failed" | "canceled"
    progress: number
    speed: string
    eta: string
    error?: string
    output?: string
    createdAt: number
}

export type TorrentResult = {
    provider: string
    name: string
    link: string
    downloadUrl: string
    magnetLink: string
    infoHash: string
    size: number
    formattedSize: string
    seeders: number
    leechers: number
    downloadCount: number
    date: string
    resolution: string
    releaseGroup: string
    episodeNumber: number
    isBatch: boolean
    isBestRelease: boolean
    confirmed: boolean
    trusted: boolean
    remake: boolean
    dub: boolean
}

export type Torrent = {
    hash: string
    name: string
    size: number
    progress: number
    downSpeed: number
    upSpeed: number
    eta: number
    state: string
    seeds: number
    peers: number
    savePath: string
    contentPath: string
    addedOn: number
    ratio: number
}

export type ExtensionManifest = {
    id: string
    name: string
    version: string
    manifestURI: string
    language: string
    type: string
    description: string
    author: string
    icon: string
    website: string
    lang: string
    notes?: string
    userConfig?: {
        version: number
        requiresConfig: boolean
        fields: {
            type: "text" | "switch" | "select"
            name: string
            label: string
            options?: { value: string; label: string }[]
            default?: string
            description?: string
        }[]
    } | null
    plugin?: {
        version: string
        permissions: {
            scopes: string[] | null
            allow: {
                networkAccess: { allowedDomains: string[] | null; reasoning: string }
                unsafeFlags: { flag: string; reason: string }[] | null
                readPaths: string[] | null
                writePaths: string[] | null
            }
        }
    } | null
}

export type ExtensionInfo = {
    manifest: ExtensionManifest
    enabled: boolean
    userConfig: { version: number; values: Record<string, string> }
    error?: string
    configError?: string
    running: boolean
    installedAt: number
    updatedAt: number
    supported: boolean
    granted: boolean
}

export type MarketEntry = {
    id: string
    name: string
    version: string
    author: string
    description: string
    type: string
    language: string
    lang: string
    icon: string
    manifestURI: string
    website: string
    flags: string
    permalink: string
    workingTag: boolean
    brokenTag: boolean
    deprecatedTag: boolean
    official: boolean
    stars: number
    updatedAt: string
    installed: boolean
    installedVersion?: string
    hasUpdate: boolean
    supported: boolean
}

export type UINode = { id: string; type: string; props: Record<string, any> }

export type PluginState = {
    id: string
    name: string
    icon: string
    state: {
        trays?: {
            id: string
            iconUrl: string
            tooltipText: string
            withContent: boolean
            width: string
            minHeight: string
            badge?: { number?: number; intent?: string } | null
            tree: UINode | null
        }[]
        actions?: { id: string; kind: string; props: Record<string, any> }[]
        webviews?: { id: string; options: Record<string, any>; hidden: boolean; html: string }[]
    }
}

export type ScheduleItem = {
    id: number
    airingAt: number
    timeUntilAiring: number
    episode: number
    media: Media
    listStatus: string
}

export type AutoRule = {
    id?: number
    enabled: boolean
    mediaId: number
    title: string
    releaseGroups: string[]
    resolutions: string[]
    additionalTerms: string[]
    episodeType: "recent" | "all"
    minSeeders: number
    provider: string
}

export type MangaChapter = {
    id: string
    url: string
    title: string
    chapter: string
    index: number
    scanlator?: string
    language?: string
    updatedAt?: string
}

// A library folder as found on disk (Library tools, manual matching).
export type FolderInfo = {
    dir: string
    label: string // path below its library folder
    videos: number // video files directly inside
    notIndexed: number // of those, not in the library index yet
    problem?: string // why it has nothing to match (still downloading, unreadable…)
}
