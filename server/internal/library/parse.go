package library

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/5rahim/habari"
)

// VideoExtensions lists every container the scanner picks up. mpv can play
// all of them; the in-app player remuxes/transcodes the ones browsers can't.
var VideoExtensions = map[string]bool{
	".mkv": true, ".mk3d": true, ".mp4": true, ".m4v": true, ".avi": true, ".webm": true,
	".mov": true, ".qt": true, ".wmv": true, ".asf": true, ".flv": true, ".f4v": true,
	".ts": true, ".m2ts": true, ".mts": true, ".m2t": true, ".tp": true, ".trp": true,
	".ogv": true, ".ogm": true, ".mpg": true, ".mpeg": true, ".mpe": true, ".mpv": true,
	".m1v": true, ".m2v": true, ".vob": true, ".evo": true, ".3gp": true, ".3g2": true,
	".rm": true, ".rmvb": true, ".divx": true, ".xvid": true, ".dv": true, ".mxf": true,
	".nut": true, ".wtv": true, ".dvr-ms": true, ".amv": true, ".y4m": true, ".hevc": true,
	".h264": true, ".h265": true, ".264": true, ".265": true, ".av1": true, ".ivf": true,
	".bik": true, ".nsv": true, ".viv": true, ".flc": true, ".fli": true, ".gifv": true,
}

// SubtitleExtensions are external subtitle files picked up next to videos.
var SubtitleExtensions = map[string]bool{
	".ass": true, ".ssa": true, ".srt": true, ".vtt": true, ".sub": true, ".idx": true, ".sup": true,
}

func IsVideo(path string) bool {
	return VideoExtensions[strings.ToLower(filepath.Ext(path))]
}

type Parsed struct {
	Title        string `json:"title"`
	FolderTitle  string `json:"folderTitle"`
	Season       int    `json:"season"`
	Part         int    `json:"part"`
	Episode      int    `json:"episode"`    // -1 when unknown
	EpisodeEnd   int    `json:"episodeEnd"` // for multi-episode files
	EpisodeTitle string `json:"episodeTitle"`
	ReleaseGroup string `json:"releaseGroup"`
	Resolution   string `json:"resolution"`
	Year         int    `json:"year"`
	Kind         string `json:"kind"` // main | special | nc | extra
	Source       string `json:"source"`
}

var (
	reSeasonDir  = regexp.MustCompile(`(?i)^(?:season|s|series)[\s._-]*0*(\d{1,2})$|^(\d{1,2})(?:st|nd|rd|th)[\s._-]*season$`)
	reSeasonAny  = regexp.MustCompile(`(?i)\b(?:season|s)[\s._-]*0*(\d{1,2})\b|\b(\d{1,2})(?:st|nd|rd|th)\s+season\b`)
	reGenericDir = regexp.MustCompile(`(?i)^(?:season[\s._-]*\d+|s\d{1,2}|specials?|extras?|bonus|ova|ovas|movies?|featurettes?|nc|ncop|nced|creditless|batch|\d{3,4}p|subs?|subtitles?|fonts?|attachments?|disc\s*\d+|cd\s*\d+|vol(?:ume)?\.?\s*\d+|bd|bdmv|stream|video_ts)$`)
	reSpecialDir = regexp.MustCompile(`(?i)^(?:specials?|sp|ova|ovas|oad|bonus|extras?|featurettes?|omake)$`)
	reNCDir      = regexp.MustCompile(`(?i)^(?:nc|ncop|nced|creditless|op(?:ening)?s?|ed(?:ing)?s?|menus?|pv|cm|trailers?)$`)
	reNCFile     = regexp.MustCompile(`(?i)\b(?:NC[\s_-]?(?:OP|ED)|NCOP|NCED|creditless|clean\s+(?:op|ed)|opening|ending|menu|preview|trailer|teaser|PV\d*|CM\d*)\b`)
	reSpecialTag = regexp.MustCompile(`(?i)\b(?:OVA|OAD|ONA|special|SP\d+|S00E\d+)\b`)
)

// titleWords are words habari reads as metadata (NHK is a broadcaster
// "source") that are also part of real titles: "Welcome to the NHK",
// "NHK ni Youkoso!".
var titleWords = []string{"nhk"}

// restoreTitleWords puts such words back when habari cut them from the
// start or end of the title.
func restoreTitleWords(raw, title string) string {
	if title == "" {
		return title
	}
	norm := strings.ToLower(strings.NewReplacer("_", " ", ".", " ").Replace(raw))
	lt := strings.ToLower(title)
	idx := strings.Index(norm, lt)
	if idx < 0 {
		return title
	}
	isWordEnd := func(s string, i int) bool {
		return i >= len(s) || !unicode.IsLetter(rune(s[i])) && !unicode.IsDigit(rune(s[i]))
	}
	for _, w := range titleWords {
		if strings.Contains(lt, w) {
			continue
		}
		after := strings.TrimLeft(norm[idx+len(lt):], " ")
		before := strings.TrimRight(norm[:idx], " ")
		switch {
		case strings.HasPrefix(after, w) && isWordEnd(after, len(w)):
			title += " " + strings.ToUpper(w)
		case strings.HasSuffix(before, w) && (len(before) == len(w) || isWordEnd(before, len(before)-len(w)-1)):
			title = strings.ToUpper(w) + " " + title
		}
	}
	return title
}

// Parse extracts metadata from a file path. roots are the library roots so
// folder names above the library aren't used as titles.
func Parse(path string, roots []string) Parsed {
	name := filepath.Base(path)
	md := habari.Parse(name)
	p := Parsed{
		Title:        cleanTitle(restoreTitleWords(name, firstNonEmpty(md.Title, md.FormattedTitle))),
		EpisodeTitle: md.EpisodeTitle,
		ReleaseGroup: md.ReleaseGroup,
		Resolution:   md.VideoResolution,
		Episode:      -1,
		Kind:         "main",
	}
	for _, t := range md.AnimeType {
		switch strings.ToUpper(t) {
		case "OVA", "OAD", "ONA", "SP", "SPECIAL", "SPECIALS":
			p.Kind = "special"
		case "NCOP", "NCED", "OP", "ED", "OPENING", "ENDING", "PV", "CM", "PREVIEW":
			p.Kind = "nc"
		}
	}
	if len(md.Source) > 0 {
		p.Source = md.Source[0]
	}
	if y, err := strconv.Atoi(md.Year); err == nil {
		p.Year = y
	}
	if len(md.SeasonNumber) > 0 {
		p.Season = atoiFloor(md.SeasonNumber[0])
	}
	if len(md.PartNumber) > 0 {
		p.Part = atoiFloor(md.PartNumber[0])
	}
	if len(md.EpisodeNumber) > 0 {
		p.Episode = atoiFloor(md.EpisodeNumber[0])
		if len(md.EpisodeNumber) > 1 {
			p.EpisodeEnd = atoiFloor(md.EpisodeNumber[len(md.EpisodeNumber)-1])
		}
	} else if len(md.EpisodeNumberAlt) > 0 {
		p.Episode = atoiFloor(md.EpisodeNumberAlt[0])
	}

	// Look at the folders between the library root and the file.
	dir := filepath.Dir(path)
	var rel string
	for _, r := range roots {
		if strings.HasPrefix(dir+string(filepath.Separator), r+string(filepath.Separator)) {
			rel, _ = filepath.Rel(r, dir)
			break
		}
	}
	if rel != "" && rel != "." {
		parts := strings.Split(rel, string(filepath.Separator))
		for i := len(parts) - 1; i >= 0; i-- {
			d := parts[i]
			if m := reSeasonDir.FindStringSubmatch(strings.TrimSpace(d)); m != nil && p.Season == 0 {
				p.Season = atoiFloor(firstNonEmpty(m[1], m[2]))
			}
			if reSpecialDir.MatchString(strings.TrimSpace(d)) && p.Kind == "main" {
				p.Kind = "special"
			}
			if reNCDir.MatchString(strings.TrimSpace(d)) {
				p.Kind = "nc"
			}
			if p.FolderTitle == "" && !reGenericDir.MatchString(strings.TrimSpace(d)) {
				fmd := habari.Parse(d)
				ft := cleanTitle(restoreTitleWords(d, firstNonEmpty(fmd.Title, fmd.FormattedTitle)))
				if ft != "" {
					p.FolderTitle = ft
					if p.Season == 0 && len(fmd.SeasonNumber) > 0 {
						p.Season = atoiFloor(fmd.SeasonNumber[0])
					}
					if p.Year == 0 {
						p.Year, _ = strconv.Atoi(fmd.Year)
					}
				}
			}
		}
	}
	if p.Season == 0 {
		if m := reSeasonAny.FindStringSubmatch(p.Title); m != nil {
			p.Season = atoiFloor(firstNonEmpty(m[1], m[2]))
		}
	}
	if reNCFile.MatchString(name) {
		p.Kind = "nc"
	} else if p.Kind == "main" && (reSpecialTag.MatchString(name) || p.Season == 0 && strings.Contains(strings.ToLower(name), "s00")) {
		p.Kind = "special"
	}
	if p.Title == "" {
		p.Title = p.FolderTitle
	}
	return p
}

var (
	reTrailingJunk = regexp.MustCompile(`(?i)[\s._-]+(?:-|–)?\s*$`)
	reTrailingType = regexp.MustCompile(`(?i)[\s._-]*(?:-|–)?\s*\b(?:OVA|OAD|ONA|specials?|movie|the movie|NCOP|NCED|creditless)\s*$`)
)

func cleanTitle(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.TrimSpace(s)
	s = reTrailingType.ReplaceAllString(s, "")
	s = reTrailingJunk.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

func atoiFloor(s string) int {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".,"); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
