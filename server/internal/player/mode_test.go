package player

import "testing"

func TestPickByMode(t *testing.T) {
	dual := []Track{
		{ID: 1, Type: "audio", Lang: "jpn", Title: "Japanese"},
		{ID: 2, Type: "audio", Lang: "eng", Title: "English Dub"},
		{ID: 1, Type: "sub", Lang: "eng", Title: "Signs & Songs"},
		{ID: 2, Type: "sub", Lang: "eng", Title: "Full Subtitles"},
	}
	if p := PickByMode(dual, "dub"); !p.HasAudio || p.Audio != 2 || !p.HasSub || p.Sub != 1 {
		t.Errorf("dub: %+v", p)
	}
	if p := PickByMode(dual, "sub"); !p.HasAudio || p.Audio != 1 || !p.HasSub || p.Sub != 2 {
		t.Errorf("sub: %+v", p)
	}

	// No language tags, only titles; no signs track -> subtitles off for dub.
	untagged := []Track{
		{ID: 1, Type: "audio", Title: "Japanese 2.0 FLAC"},
		{ID: 2, Type: "audio", Title: "English 5.1"},
		{ID: 1, Type: "sub", Title: "English"},
	}
	if p := PickByMode(untagged, "dub"); p.Audio != 2 || !p.SubOff {
		t.Errorf("dub by title: %+v", p)
	}
	if p := PickByMode(untagged, "sub"); p.Audio != 1 || p.Sub != 1 {
		t.Errorf("sub by title: %+v", p)
	}

	// A commentary track isn't a dub; a single audio track is left alone.
	commentary := []Track{{ID: 1, Type: "audio", Lang: "jpn"}, {ID: 2, Type: "audio", Lang: "eng", Title: "Commentary"}}
	if p := PickByMode(commentary, "dub"); p.HasAudio {
		t.Errorf("commentary picked as dub: %+v", p)
	}
	if p := PickByMode([]Track{{ID: 1, Type: "audio", Lang: "jpn"}}, "dub"); p.HasAudio || p.SubOff {
		t.Errorf("single audio: %+v", p)
	}
}
