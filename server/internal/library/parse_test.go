package library

import "testing"

func TestParse(t *testing.T) {
	roots := []string{"/lib"}
	cases := []struct {
		path    string
		title   string
		season  int
		episode int
		kind    string
	}{
		{"/lib/Code Geass/[SubsPlease] Code Geass - Hangyaku no Lelouch - 07 (1080p) [ABCD1234].mkv", "Code Geass - Hangyaku no Lelouch", 0, 7, "main"},
		{"/lib/Attack on Titan/Season 2/Attack on Titan S02E05 1080p.mkv", "Attack on Titan", 2, 5, "main"},
		{"/lib/Frieren/[Erai-raws] Sousou no Frieren - 12 [1080p][Multiple Subtitle].mkv", "Sousou no Frieren", 0, 12, "main"},
		{"/lib/[Judas] Jujutsu Kaisen (Season 2) [1080p][HEVC x265 10bit][Eng-Subs]/[Judas] Jujutsu Kaisen - S02E03.mkv", "Jujutsu Kaisen", 2, 3, "main"},
		{"/lib/Your Name (2016)/Your Name (2016) [BD 1080p].mp4", "Your Name", 0, -1, "main"},
		{"/lib/Show/NC/[Group] Show - NCOP 01 [1080p].mkv", "Show", 0, 1, "nc"},
		{"/lib/One Piece/One Piece - 1050 [720p].avi", "One Piece", 0, 1050, "main"},
		{"/lib/Bocchi/Extras/Bocchi the Rock! - OVA.mkv", "Bocchi the Rock!", 0, -1, "special"},
	}
	for _, c := range cases {
		p := Parse(c.path, roots)
		if p.Title != c.title || p.Season != c.season || p.Episode != c.episode || p.Kind != c.kind {
			t.Errorf("%s\n  got title=%q season=%d ep=%d kind=%s folder=%q\n want title=%q season=%d ep=%d kind=%s", c.path, p.Title, p.Season, p.Episode, p.Kind, p.FolderTitle, c.title, c.season, c.episode, c.kind)
		}
	}
}

func TestIsVideo(t *testing.T) {
	for _, n := range []string{"a.mkv", "b.MP4", "c.avi", "d.webm", "e.m2ts", "f.rmvb", "g.ogm", "h.wmv", "i.flv", "j.ts", "k.vob", "l.mov"} {
		if !IsVideo(n) {
			t.Errorf("%s should be a video", n)
		}
	}
	for _, n := range []string{"a.srt", "b.ass", "c.nfo", "d.jpg", "e.!qB", "f.part"} {
		if IsVideo(n) {
			t.Errorf("%s should not be a video", n)
		}
	}
}
