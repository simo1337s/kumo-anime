// Package config holds the user settings and resolves the XDG paths Kumo uses.
package config

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sync"

	"github.com/simo1337s/animetest/server/internal/db"
)

const (
	AppName = "Kumo"

	DefaultPort           = 43211
	DefaultAnilistClient  = "13985"
	DefaultMarketplaceURL = "https://raw.githubusercontent.com/Bas1874/Seanime-Marketplace/refs/heads/main/Marketplace/Main.json"

	// UpdateRepo is the GitHub repository Kumo looks for updates in
	// ($KUMO_UPDATE_REPO overrides it).
	UpdateRepo = "simo1337s/kumo-anime"
)

// Set when building, with -ldflags "-X <module>/internal/config.AppVersion=…
// -X <module>/internal/config.Commit=…": packaging/arch/PKGBUILD and the
// Windows release workflow do, from the commit they build.
var (
	// AppVersion is 1.0.<number of commits> in those builds.
	AppVersion = "1.0.0"
	// Commit is the git commit Kumo was built from; see BuildCommit.
	Commit = ""
)

// BuildCommit is the commit Kumo was built from: Commit, or else the one Go
// stamps into builds made inside a git checkout, or "" when unknown (e.g.
// built from a source zip).
func BuildCommit() string {
	if Commit != "" {
		return Commit
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return ""
}

// Settings is the whole user configuration. It is persisted as a single JSON
// document in the kv table and edited from the Settings pages of the UI.
type Settings struct {
	// SchemaVersion lets newer versions migrate settings saved by older ones.
	SchemaVersion int `json:"schemaVersion"`

	Library      LibrarySettings      `json:"library"`
	Playback     PlaybackSettings     `json:"playback"`
	Mpv          MpvSettings          `json:"mpv"`
	Transcode    TranscodeSettings    `json:"transcode"`
	AniCli       AniCliSettings       `json:"aniCli"`
	OnlineStream OnlineStreamSettings `json:"onlineStream"`
	Torrent      TorrentSettings      `json:"torrent"`
	Qbittorrent  TorrentClientConfig  `json:"qbittorrent"`
	Transmission TorrentClientConfig  `json:"transmission"`
	Manga        MangaSettings        `json:"manga"`
	Anilist      AnilistSettings      `json:"anilist"`
	Server       ServerSettings       `json:"server"`
	UI           UISettings           `json:"ui"`
	Discord      DiscordSettings      `json:"discord"`
	Extensions   ExtensionSettings    `json:"extensions"`
}

type LibrarySettings struct {
	Dir              string   `json:"dir"`
	ExtraDirs        []string `json:"extraDirs"`
	AutoRefresh      bool     `json:"autoRefresh"`
	RefreshOnStartup bool     `json:"refreshOnStartup"`
	// Minimum similarity (0-1) required to auto-match a title.
	MatchThreshold float64 `json:"matchThreshold"`
	// Glob patterns (matched against file/dir names) that the scanner skips.
	IgnorePatterns []string `json:"ignorePatterns"`
	// Also try to match against all of AniList, not only the user's list.
	MatchOutsideList bool `json:"matchOutsideList"`
}

type PlaybackSettings struct {
	// "mpv" (desktop media player) or "builtin" (in-app web player).
	DefaultPlayer       string  `json:"defaultPlayer"`
	AutoUpdateProgress  bool    `json:"autoUpdateProgress"`
	CompletionThreshold float64 `json:"completionThreshold"` // 0-1
	ResumePlayback      bool    `json:"resumePlayback"`
	AutoPlayNext        bool    `json:"autoPlayNext"`
	RememberTracks      bool    `json:"rememberTracks"`
	PreferredAudioLang  string  `json:"preferredAudioLang"` // e.g. "jpn,ja"
	PreferredSubLang    string  `json:"preferredSubLang"`   // e.g. "eng,en"
	SkipIntroAniSkip    bool    `json:"skipIntroAniSkip"`
}

type MpvSettings struct {
	Path       string `json:"path"`
	Socket     string `json:"socket"`
	ExtraArgs  string `json:"extraArgs"`
	Fullscreen bool   `json:"fullscreen"`
}

type TranscodeSettings struct {
	FfmpegPath  string `json:"ffmpegPath"`
	FfprobePath string `json:"ffprobePath"`
	// "auto" picks direct play when the browser can decode the file,
	// remux when only the container is the problem, transcode otherwise.
	Mode      string `json:"mode"`
	HwAccel   string `json:"hwAccel"` // "", "vaapi", "nvenc", "qsv"
	VaapiNode string `json:"vaapiNode"`
	Preset    string `json:"preset"`
}

type AniCliSettings struct {
	Enabled     bool   `json:"enabled"`
	Path        string `json:"path"`
	DefaultMode string `json:"defaultMode"` // sub | dub
	Quality     string `json:"quality"`     // best | worst | 1080 | 720 ...
	// Directory where ani-cli downloads are written. Empty = library directory.
	DownloadDir string `json:"downloadDir"`
	Downloader  string `json:"downloader"` // auto | yt-dlp | ffmpeg | aria2c
	// Where streams open: "mpv" or "builtin".
	Player string `json:"player"`
}

type OnlineStreamSettings struct {
	Enabled         bool   `json:"enabled"`
	DefaultProvider string `json:"defaultProvider"` // "ani-cli" or an extension id
	PreferDub       bool   `json:"preferDub"`
}

type TorrentSettings struct {
	DefaultClient   string `json:"defaultClient"`   // qbittorrent | transmission | none
	DefaultProvider string `json:"defaultProvider"` // nyaa | animetosho | extension id
	ShowActiveCount bool   `json:"showActiveCount"`
	// Create a sub folder per anime inside the library directory.
	CreateSubfolder     bool   `json:"createSubfolder"`
	PreferredResolution string `json:"preferredResolution"`
	AutoDownloader      bool   `json:"autoDownloader"`
	AutoDownloadMinutes int    `json:"autoDownloadMinutes"`
}

type TorrentClientConfig struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Executable string `json:"executable"`
	Tags       string `json:"tags"`
	Category   string `json:"category"`
	UseHTTPS   bool   `json:"useHttps"`
}

type MangaSettings struct {
	Enabled         bool   `json:"enabled"`
	DefaultProvider string `json:"defaultProvider"`
	ReadingMode     string `json:"readingMode"` // long-strip | paged | double
	Direction       string `json:"direction"`   // ltr | rtl
}

type AnilistSettings struct {
	ClientID string `json:"clientId"`
	// Hide adult entries everywhere.
	HideAdult bool `json:"hideAdult"`
}

type ServerSettings struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// When true Kumo listens on every interface so other devices on the home
	// network can use it. Requests from public (non-private) addresses are
	// always refused.
	AllowLAN bool `json:"allowLan"`
	// Optional password required from LAN clients (localhost never needs it).
	Password string `json:"password"`
	// Serve the web UI to browsers (http://127.0.0.1:<port>). The desktop
	// window always works.
	WebUI bool `json:"webUi"`
}

type UISettings struct {
	AccentColor      string   `json:"accentColor"`
	ShowAdult        bool     `json:"showAdult"`
	BannerType       string   `json:"bannerType"` // episode | banner | none
	CardSize         string   `json:"cardSize"`   // sm | md | lg
	ReducedMotion    bool     `json:"reducedMotion"`
	ShowEpisodeTitle bool     `json:"showEpisodeTitle"`
	BlurUnwatched    bool     `json:"blurUnwatched"` // spoiler protection
	HomeSections     []string `json:"homeSections"`
}

type DiscordSettings struct {
	RichPresence bool   `json:"richPresence"`
	ClientID     string `json:"clientId"`
}

type ExtensionSettings struct {
	MarketplaceURL string `json:"marketplaceUrl"`
	AutoUpdate     bool   `json:"autoUpdate"`
}

// Defaults returns the settings used on first launch.
func Defaults() Settings {
	home, _ := os.UserHomeDir()
	videos := filepath.Join(home, "Videos", "Anime")
	return Settings{
		SchemaVersion: schemaVersion,
		Library: LibrarySettings{
			Dir:              videos,
			ExtraDirs:        []string{},
			AutoRefresh:      true,
			RefreshOnStartup: true,
			MatchThreshold:   0.5,
			IgnorePatterns:   []string{"*sample*", "*.part", "*NCOP*", "*NCED*"},
			MatchOutsideList: true,
		},
		Playback: PlaybackSettings{
			DefaultPlayer:       "builtin",
			AutoUpdateProgress:  true,
			CompletionThreshold: 0.85,
			ResumePlayback:      true,
			AutoPlayNext:        false,
			RememberTracks:      true,
			PreferredAudioLang:  "jpn,ja",
			PreferredSubLang:    "eng,en",
		},
		Mpv: MpvSettings{Path: "mpv"},
		Transcode: TranscodeSettings{
			FfmpegPath:  "ffmpeg",
			FfprobePath: "ffprobe",
			Mode:        "auto",
			Preset:      "veryfast",
			VaapiNode:   "/dev/dri/renderD128",
		},
		AniCli: AniCliSettings{
			Enabled:     true,
			Path:        "ani-cli",
			DefaultMode: "sub",
			Quality:     "best",
			Downloader:  "auto",
			Player:      "builtin",
		},
		OnlineStream: OnlineStreamSettings{Enabled: true, DefaultProvider: "ani-cli"},
		Torrent: TorrentSettings{
			DefaultClient:       "qbittorrent",
			DefaultProvider:     "nyaa",
			ShowActiveCount:     true,
			CreateSubfolder:     true,
			PreferredResolution: "1080",
			AutoDownloadMinutes: 20,
		},
		Qbittorrent:  TorrentClientConfig{Host: "127.0.0.1", Port: 8081, Username: "admin", Executable: findQbittorrent(home)},
		Transmission: TorrentClientConfig{Host: "127.0.0.1", Port: 9091, Executable: defaultTransmission()},
		Manga:        MangaSettings{Enabled: true, ReadingMode: "long-strip", Direction: "ltr"},
		Anilist:      AnilistSettings{ClientID: DefaultAnilistClient, HideAdult: true},
		Server:       ServerSettings{Host: "127.0.0.1", Port: DefaultPort, WebUI: true},
		UI: UISettings{
			AccentColor:      "#7c6cf2",
			BannerType:       "episode",
			CardSize:         "md",
			ShowEpisodeTitle: true,
			HomeSections:     []string{"continue", "genres", "watching", "airing", "planning", "completed"},
		},
		Discord:    DiscordSettings{RichPresence: false},
		Extensions: ExtensionSettings{MarketplaceURL: DefaultMarketplaceURL},
	}
}

// Store loads and saves settings, keeping a cached copy in memory.
type Store struct {
	db       *db.DB
	mu       sync.RWMutex
	current  Settings
	onChange []func(old, new Settings)
}

const settingsKey = "settings"

func NewStore(d *db.DB) (*Store, error) {
	s := &Store{db: d, current: Defaults()}
	// Decode on top of the defaults so fields added in newer versions get
	// sane values.
	merged := Defaults()
	merged.SchemaVersion = 0 // missing in settings saved by older versions
	ok, err := d.GetKV(settingsKey, &merged)
	if err != nil {
		return nil, err
	}
	if ok {
		migrated := migrate(&merged)
		s.current = sanitize(merged)
		if migrated {
			if err := d.SetKV(settingsKey, s.current); err != nil {
				return nil, err
			}
		}
	} else {
		if err := d.SetKV(settingsKey, s.current); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Get returns a copy of the settings. Slices are copied too: callers decode
// JSON into the result, which would otherwise write into the live settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current.clone()
}

func (c Settings) clone() Settings {
	c.Library.ExtraDirs = slices.Clone(c.Library.ExtraDirs)
	c.Library.IgnorePatterns = slices.Clone(c.Library.IgnorePatterns)
	c.UI.HomeSections = slices.Clone(c.UI.HomeSections)
	return c
}

func (s *Store) Save(next Settings) (Settings, error) {
	next = sanitize(next.clone())
	if err := s.db.SetKV(settingsKey, next); err != nil {
		return s.Get(), err
	}
	s.mu.Lock()
	old := s.current
	s.current = next
	listeners := append([]func(old, new Settings){}, s.onChange...)
	s.mu.Unlock()
	// Listeners get their own copies; none of them may touch s.current.
	for _, fn := range listeners {
		go fn(old.clone(), next.clone())
	}
	return next.clone(), nil
}

// OnChange registers a callback invoked (in a goroutine) after every save.
func (s *Store) OnChange(fn func(old, new Settings)) {
	s.mu.Lock()
	s.onChange = append(s.onChange, fn)
	s.mu.Unlock()
}

// schemaVersion is the current settings version; add a step to migrate when
// changing a default that existing installs should pick up too.
const schemaVersion = 2

func migrate(s *Settings) bool {
	if s.SchemaVersion >= schemaVersion {
		return false
	}
	if s.SchemaVersion < 2 {
		// v2: the in-app player is the default for files and streams.
		s.Playback.DefaultPlayer = "builtin"
		s.AniCli.Player = "builtin"
	}
	s.SchemaVersion = schemaVersion
	return true
}

func sanitize(s Settings) Settings {
	d := Defaults()
	s.SchemaVersion = schemaVersion
	if s.Server.Port <= 0 || s.Server.Port > 65535 {
		s.Server.Port = d.Server.Port
	}
	if s.Server.Host == "" {
		s.Server.Host = d.Server.Host
	}
	if s.Playback.CompletionThreshold <= 0 || s.Playback.CompletionThreshold > 1 {
		s.Playback.CompletionThreshold = d.Playback.CompletionThreshold
	}
	if s.Library.MatchThreshold <= 0 || s.Library.MatchThreshold > 1 {
		s.Library.MatchThreshold = d.Library.MatchThreshold
	}
	if s.Library.ExtraDirs == nil {
		s.Library.ExtraDirs = []string{}
	}
	if s.Library.IgnorePatterns == nil {
		s.Library.IgnorePatterns = []string{}
	}
	if s.Mpv.Path == "" {
		s.Mpv.Path = "mpv"
	}
	if s.Transcode.FfmpegPath == "" {
		s.Transcode.FfmpegPath = "ffmpeg"
	}
	if s.Transcode.FfprobePath == "" {
		s.Transcode.FfprobePath = "ffprobe"
	}
	if s.Transcode.Mode == "" {
		s.Transcode.Mode = "auto"
	}
	if s.Transcode.Preset == "" {
		s.Transcode.Preset = "veryfast"
	}
	if s.AniCli.Path == "" {
		s.AniCli.Path = "ani-cli"
	}
	if s.AniCli.DefaultMode != "dub" {
		s.AniCli.DefaultMode = "sub"
	}
	if s.AniCli.Quality == "" {
		s.AniCli.Quality = "best"
	}
	if s.AniCli.Player != "mpv" {
		s.AniCli.Player = "builtin"
	}
	if s.Anilist.ClientID == "" {
		s.Anilist.ClientID = DefaultAnilistClient
	}
	if s.Extensions.MarketplaceURL == "" {
		s.Extensions.MarketplaceURL = DefaultMarketplaceURL
	}
	if s.Torrent.AutoDownloadMinutes < 5 {
		s.Torrent.AutoDownloadMinutes = 20
	}
	if s.UI.AccentColor == "" {
		s.UI.AccentColor = d.UI.AccentColor
	}
	if len(s.UI.HomeSections) == 0 {
		s.UI.HomeSections = d.UI.HomeSections
	}
	if s.Playback.DefaultPlayer != "mpv" {
		s.Playback.DefaultPlayer = "builtin"
	}
	return s
}

// LibraryDirs returns the main library dir followed by the extra ones.
func (s Settings) LibraryDirs() []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range append([]string{s.Library.Dir}, s.Library.ExtraDirs...) {
		d = filepath.Clean(expandHome(d))
		if d == "." || d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || len(p) > 1 && p[0] == '~' && (p[1] == '/' || p[1] == filepath.Separator) {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[1:])
	}
	return p
}

// ExpandHome is exported for other packages.
func ExpandHome(p string) string { return expandHome(p) }

// Paths -----------------------------------------------------------------

// DataDir is ~/.local/share/kumo on Linux and %APPDATA%\Kumo on Windows,
// or $KUMO_DATA_DIR.
func DataDir() string {
	if d := os.Getenv("KUMO_DATA_DIR"); d != "" {
		return d
	}
	return defaultDataDir()
}

// RuntimeDir holds the server's address, the desktop window's token and
// other temporary files: $XDG_RUNTIME_DIR/kumo on Linux,
// %LOCALAPPDATA%\Kumo\run on Windows.
func RuntimeDir() string {
	dir := runtimeDir()
	_ = os.MkdirAll(dir, 0o700)
	return dir
}
