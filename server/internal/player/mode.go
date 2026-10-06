package player

import "strings"

// Sub/dub for local files with several audio tracks (dual-audio releases):
// "dub" plays the English track, found by its language tag or a title like
// "English" / "English Dub", with only signs & songs subtitles; "sub" plays
// the original audio with full subtitles.

// ModePick is what PickByMode chose; the Has* fields say whether a track
// was found (otherwise the player's own defaults stay).
type ModePick struct {
	Audio    int
	HasAudio bool
	Sub      int
	HasSub   bool
	SubOff   bool
}

func hasWord(s, w string) bool {
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return !('a' <= r && r <= 'z' || '0' <= r && r <= '9') }) {
		if f == w {
			return true
		}
	}
	return false
}

// IsEnglishTrack reports an English (dub) track by language tag or title.
func IsEnglishTrack(lang, title string) bool {
	l, t := strings.ToLower(strings.TrimSpace(lang)), strings.ToLower(title)
	if strings.Contains(t, "commentary") {
		return false
	}
	return l == "eng" || l == "en" || strings.Contains(t, "english") || strings.Contains(t, "dub") || hasWord(t, "eng")
}

// IsJapaneseTrack reports a Japanese (original) track by language tag or title.
func IsJapaneseTrack(lang, title string) bool {
	l, t := strings.ToLower(strings.TrimSpace(lang)), strings.ToLower(title)
	return l == "jpn" || l == "ja" || l == "jp" || strings.Contains(t, "japanese") || hasWord(t, "jpn") || hasWord(t, "jap")
}

// PickByMode chooses the audio and subtitle tracks for a sub/dub mode.
// Files with a single audio track are left alone.
func PickByMode(tracks []Track, mode string) ModePick {
	var audio, subs []Track
	for _, t := range tracks {
		switch t.Type {
		case "audio":
			audio = append(audio, t)
		case "sub":
			subs = append(subs, t)
		}
	}
	var p ModePick
	if len(audio) < 2 || (mode != "sub" && mode != "dub") {
		return p
	}
	if mode == "dub" {
		for _, a := range audio {
			if IsEnglishTrack(a.Lang, a.Title) {
				p.Audio, p.HasAudio = a.ID, true
				break
			}
		}
		if !p.HasAudio {
			return p // no dub in this file
		}
		// Only signs & songs (on-screen text) with a dub.
		for _, s := range subs {
			if (isSigns(s.Title) || s.Forced) && (s.Lang == "" || IsEnglishTrack(s.Lang, s.Title) || langMatch(s.Lang, "eng")) {
				p.Sub, p.HasSub = s.ID, true
				return p
			}
		}
		p.SubOff = true
		return p
	}
	// sub: the original audio (Japanese, else the first non-English track).
	for _, a := range audio {
		if IsJapaneseTrack(a.Lang, a.Title) {
			p.Audio, p.HasAudio = a.ID, true
			break
		}
	}
	if !p.HasAudio {
		for _, a := range audio {
			if !IsEnglishTrack(a.Lang, a.Title) {
				p.Audio, p.HasAudio = a.ID, true
				break
			}
		}
	}
	// Full English subtitles, not signs & songs.
	for _, s := range subs {
		if (langMatch(s.Lang, "eng") || strings.Contains(strings.ToLower(s.Title), "english")) && !isSigns(s.Title) && !s.Forced {
			p.Sub, p.HasSub = s.ID, true
			return p
		}
	}
	return p
}
